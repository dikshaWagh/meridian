package arbiter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

// Register mounts the exclusive-mode control plane on mux.
//
//	GET  /v1/system/state   current mode, phase and progress
//	POST /v1/system/mode    request a mode switch {"mode":"llm"|"ml"}
//	GET  /v1/system/events  SSE stream of state changes
func (a *Arbiter) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/system/state", a.handleState)
	mux.HandleFunc("POST /v1/system/mode", a.handleMode)
	mux.HandleFunc("GET /v1/system/events", a.handleEvents)
}

// Middleware rejects work for the inactive subsystem and tracks in-flight
// requests so a switch can drain cleanly. Unrelated paths pass through.
func (a *Arbiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode, guarded := classify(r)
		if !guarded {
			next.ServeHTTP(w, r)
			return
		}
		release, st, status, errType, msg := a.acquire(mode)
		if status != 0 {
			w.Header().Set("X-Meridian-Mode", string(st.Mode))
			w.Header().Set("X-Meridian-Phase", st.Phase)
			w.Header().Set("Cache-Control", "no-store")
			if status == http.StatusServiceUnavailable {
				w.Header().Set("Retry-After", "2")
			}
			api.WriteError(w, status, errType, msg)
			return
		}
		defer release()
		next.ServeHTTP(w, r)
	})
}

// classify maps the work-carrying endpoints to their subsystem. Status, model
// listing, artifacts and control-plane calls are intentionally unguarded: they
// are cheap and the UI needs them while the other mode is active.
func classify(r *http.Request) (Mode, bool) {
	if r.Method != http.MethodPost {
		return ModeIdle, false
	}
	switch r.URL.Path {
	case "/v1/chat/completions":
		return ModeLLM, true
	case "/v1/ocr", "/v1/images/generations":
		return ModeML, true
	}
	return ModeIdle, false
}

func (a *Arbiter) handleState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	api.WriteJSON(w, http.StatusOK, a.State())
}

func (a *Arbiter) handleMode(w http.ResponseWriter, r *http.Request) {
	raw, err := readLimited(r, 4<<10)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "could not read body")
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal(raw, &req) != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "body is not valid JSON")
		return
	}
	mode, ok := ParseMode(strings.TrimSpace(req.Mode))
	if !ok {
		api.WriteError(w, http.StatusBadRequest, "invalid_mode", "mode must be \"llm\" or \"ml\"")
		return
	}

	st, started, err := a.Activate(mode)
	switch {
	case err == nil:
	case errors.Is(err, ErrModeUnavailable):
		api.WriteError(w, http.StatusConflict, "mode_unavailable", err.Error())
		return
	case errors.Is(err, ErrSwitchInProgress):
		api.WriteError(w, http.StatusConflict, "switch_in_progress", err.Error())
		return
	case errors.Is(err, ErrClosed):
		api.WriteError(w, http.StatusServiceUnavailable, "service_disabled", err.Error())
		return
	case errors.Is(err, ErrUnknownMode):
		api.WriteError(w, http.StatusBadRequest, "invalid_mode", err.Error())
		return
	default:
		api.WriteError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	status := http.StatusOK
	if started {
		status = http.StatusAccepted
	}
	api.WriteJSON(w, status, st)
}

func (a *Arbiter) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		api.WriteError(w, http.StatusInternalServerError, "streaming_unsupported", "response writer cannot stream")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	ch, unsubscribe := a.Subscribe()
	defer unsubscribe()

	writeEvent := func(ev Event) bool {
		raw, err := json.Marshal(ev)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, raw); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !writeEvent(Event{Type: "state", State: a.State()}) {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !writeEvent(ev) {
				return
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func readLimited(r *http.Request, limit int64) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, limit))
}
