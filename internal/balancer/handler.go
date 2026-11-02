package balancer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

func NewHandler(pool *Pool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { handleChat(pool, w, r) })
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) { handleModels(pool, w) })
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { handleStatus(pool, w) })
	return mux
}

// statusRecorder captures the status code written by the proxy.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func handleChat(pool *Pool, w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "could not read body (max 1 MiB)")
		return
	}
	var req api.ChatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "body is not valid JSON")
		return
	}
	if req.Model == "" {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}

	// The balancer must read `model` to route, but the worker still needs the
	// full body, so we buffer it and put it back.
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))

	worker, err := pool.Pick(req.Model)
	switch {
	case errors.Is(err, ErrModelNotFound):
		api.WriteError(w, http.StatusNotFound, "model_not_found", "unknown model "+strconv.Quote(req.Model))
		return
	case errors.Is(err, ErrNoHealthyWorker):
		api.WriteError(w, http.StatusServiceUnavailable, "no_healthy_worker", "no healthy worker for model "+strconv.Quote(req.Model))
		return
	case err != nil:
		api.WriteError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	worker.InFlight.Add(1)
	defer worker.InFlight.Add(-1)

	w.Header().Set("X-Meridian-Worker", worker.Spec.ID)
	w.Header().Set("X-Meridian-Model", req.Model)
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	worker.Proxy.ServeHTTP(rec, r)
	if rec.status < 500 {
		worker.Served.Add(1)
	}
	log.Printf("model=%s worker=%s status=%d time=%dms",
		req.Model, worker.Spec.ID, rec.status, time.Since(start).Milliseconds())
}

func handleModels(pool *Pool, w http.ResponseWriter) {
	list := api.ModelList{Object: "list", Data: []api.ModelInfo{}}
	for _, m := range pool.Models() {
		list.Data = append(list.Data, api.ModelInfo{ID: m, Object: "model"})
	}
	api.WriteJSON(w, http.StatusOK, list)
}

func handleStatus(pool *Pool, w http.ResponseWriter) {
	api.WriteJSON(w, http.StatusOK, api.StatusResponse{Workers: pool.Snapshot()})
}
