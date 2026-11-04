package balancer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/tokens"
)

type requestUsage struct {
	in, out int
	ok      bool
}
type usageKey struct{}

func NewHandler(pool *Pool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { handleChat(pool, w, r) })
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) { handleModels(pool, w) })
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { handleStatus(pool, w) })
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int)        { s.status = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func handleChat(pool *Pool, w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		api.WriteError(w, 400, "invalid_request", "could not read body (max 1 MiB)")
		return
	}
	var req api.ChatRequest
	if json.Unmarshal(raw, &req) != nil {
		api.WriteError(w, 400, "invalid_request", "body is not valid JSON")
		return
	}
	if req.Model == "" {
		api.WriteError(w, 400, "invalid_request", "model is required")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	r.ContentLength = int64(len(raw))
	est := tokens.ForRequest(req)
	worker, err := pool.Pick(req.Model)
	if errors.Is(err, ErrModelNotFound) {
		api.WriteError(w, 404, "model_not_found", "unknown model "+strconv.Quote(req.Model))
		return
	}
	if errors.Is(err, ErrNoHealthyWorker) {
		msg := "no healthy worker for model " + strconv.Quote(req.Model)
		if pool.ModelStarting(req.Model) {
			msg = "workers for " + strconv.Quote(req.Model) + " are still starting"
		}
		api.WriteError(w, 503, "no_healthy_worker", msg)
		return
	}
	if err != nil {
		api.WriteError(w, 500, "internal_error", err.Error())
		return
	}
	w.Header().Set("X-Meridian-Worker", worker.Spec.ID)
	w.Header().Set("X-Meridian-Model", req.Model)
	w.Header().Set("X-Meridian-Est-Input-Tokens", strconv.Itoa(est.Input))
	w.Header().Set("X-Meridian-Est-Output-Tokens", strconv.Itoa(est.Output))
	w.Header().Set("X-Meridian-Est-Total-Tokens", strconv.Itoa(est.Total))
	worker.InFlight.Add(1)
	worker.EstTokensInFlight.Add(int64(est.Total))
	defer worker.InFlight.Add(-1)
	defer worker.EstTokensInFlight.Add(-int64(est.Total))
	usage := &requestUsage{}
	r = r.WithContext(context.WithValue(r.Context(), usageKey{}, usage))
	rec := &statusRecorder{ResponseWriter: w, status: 200}
	worker.Proxy.ServeHTTP(rec, r)
	if rec.status < 500 {
		worker.Served.Add(1)
	}
	actIn, actOut := "?", "?"
	if usage.ok {
		actIn = strconv.Itoa(usage.in)
		actOut = strconv.Itoa(usage.out)
	}
	log.Printf("model=%s worker=%s status=%d time=%dms est_in=%d est_out<=%d act_in=%s act_out=%s", req.Model, worker.Spec.ID, rec.status, time.Since(start).Milliseconds(), est.Input, est.Output, actIn, actOut)
}

func captureUsage(resp *http.Response, usage *requestUsage) error {
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return nil
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	resp.ContentLength = int64(len(raw))
	var result api.ChatResponse
	if json.Unmarshal(raw, &result) == nil {
		usage.in = result.Usage.PromptTokens
		usage.out = result.Usage.CompletionTokens
		usage.ok = true
	}
	return nil
}

func handleModels(pool *Pool, w http.ResponseWriter) {
	list := api.ModelList{Object: "list", Data: []api.ModelInfo{}}
	for _, m := range pool.Models() {
		list.Data = append(list.Data, api.ModelInfo{ID: m, Object: "model"})
	}
	api.WriteJSON(w, 200, list)
}
func handleStatus(pool *Pool, w http.ResponseWriter) {
	api.WriteJSON(w, 200, api.StatusResponse{Workers: pool.Snapshot()})
}
