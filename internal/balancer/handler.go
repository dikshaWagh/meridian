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
	"github.com/dikshaWagh/meridian/internal/metrics"
	"github.com/dikshaWagh/meridian/internal/tokens"
)

type requestUsage struct {
	in, out      int
	ok           bool
	assistantMsg *api.Message
}
type usageKey struct{}

func NewHandler(pool *Pool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { handleChat(pool, w, r) })
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) { handleModels(pool, w) })
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { handleStatus(pool, w) })
	mux.HandleFunc("GET /policies", func(w http.ResponseWriter, r *http.Request) { handlePolicies(pool, w) })
	mux.HandleFunc("PUT /v1/policies/default", func(w http.ResponseWriter, r *http.Request) {
		handleSetDefaultPolicy(pool, w, r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.status = code
	s.wrote = true
	s.ResponseWriter.WriteHeader(code)
}
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func handleChat(pool *Pool, w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: 200}
	w = rec
	requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
	if requestID == "" {
		requestID = metrics.NewRequestID()
	}
	var req api.ChatRequest
	convID := strings.TrimSpace(r.Header.Get("X-Conversation-ID"))
	policyName := strings.TrimSpace(r.Header.Get("X-Meridian-Routing-Policy"))
	workerID := ""
	est := tokens.Estimate{}
	usage := &requestUsage{}
	defer func() {
		entry := metrics.RequestLog{
			Timestamp:       time.Now().UTC().Format(time.RFC3339),
			RequestID:       requestID,
			Model:           req.Model,
			WorkerID:        workerID,
			ConversationID:  convID,
			StatusCode:      rec.status,
			LatencyMS:       time.Since(start).Milliseconds(),
			EstInputTokens:  est.Input,
			EstOutputTokens: est.Output,
			EstTotalTokens:  est.Total,
		}
		if usage.ok {
			entry.ActualPromptTokens = usage.in
			entry.ActualCompletionTokens = usage.out
		}
		metrics.AppendRequest(entry)
	}()

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		api.WriteError(w, 400, "invalid_request", "could not read body (max 1 MiB)")
		return
	}
	if json.Unmarshal(raw, &req) != nil {
		api.WriteError(w, 400, "invalid_request", "body is not valid JSON")
		return
	}
	if req.Model == "" {
		api.WriteError(w, 400, "invalid_request", "model is required")
		return
	}

	worker, sess, reservation, err := pool.PickForSessionWithPolicyReserved(req.Model, convID, policyName)
	if errors.Is(err, ErrUnknownRoutingPolicy) {
		api.WriteError(w, 400, "unknown_routing_policy", err.Error())
		return
	}
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
	if errors.Is(err, ErrModelMismatch) {
		api.WriteError(w, 400, "model_mismatch", "conversation is bound to a different model")
		return
	}
	if err != nil {
		api.WriteError(w, 500, "internal_error", err.Error())
		return
	}
	// Release the reservation if the request fails before proxying.
	defer reservation.Release()
	workerID = worker.Spec.ID

	usedPolicy := policyName
	if usedPolicy == "" {
		usedPolicy = pool.DefaultRoutingPolicy()
	}

	upstreamReq := req
	if convID != "" && sess != nil {
		sess.mu.Lock()
		if sess.inFlight {
			sess.mu.Unlock()
			api.WriteError(w, 409, "conversation_busy", "conversation is busy")
			return
		}
		sess.inFlight = true
		sess.LastUsed = time.Now()

		history := make([]api.Message, len(sess.Messages))
		copy(history, sess.Messages)
		sess.mu.Unlock()

		defer func() {
			sess.mu.Lock()
			sess.inFlight = false
			sess.LastUsed = time.Now()
			sess.mu.Unlock()
		}()

		upstreamReq.Messages = append(history, req.Messages...)
	}

	est = tokens.ForRequest(upstreamReq)

	if convID != "" && sess != nil {
		perConvCtx := pool.PerConvContext()
		if perConvCtx > 0 && (est.Input > perConvCtx || est.Total > perConvCtx) {
			api.WriteError(w, 413, "context_too_long", "prompt exceeds per-conversation context limit")
			return
		}

		upstreamRaw, err := json.Marshal(upstreamReq)
		if err != nil {
			api.WriteError(w, 500, "internal_error", "failed to marshal request")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(upstreamRaw))
		r.ContentLength = int64(len(upstreamRaw))
	} else {
		r.Body = io.NopCloser(bytes.NewReader(raw))
		r.ContentLength = int64(len(raw))
	}

	w.Header().Set("X-Meridian-Worker", worker.Spec.ID)
	w.Header().Set("X-Meridian-Model", req.Model)
	w.Header().Set("X-Meridian-Routing-Policy", usedPolicy)
	w.Header().Set("X-Meridian-Est-Input-Tokens", strconv.Itoa(est.Input))
	w.Header().Set("X-Meridian-Est-Output-Tokens", strconv.Itoa(est.Output))
	w.Header().Set("X-Meridian-Est-Total-Tokens", strconv.Itoa(est.Total))

	reservation.Commit()
	defer worker.InFlight.Add(-1)
	worker.EstTokensInFlight.Add(int64(est.Total))
	defer worker.EstTokensInFlight.Add(-int64(est.Total))

	r = r.WithContext(context.WithValue(r.Context(), usageKey{}, usage))

	worker.Proxy.ServeHTTP(w, r)

	if rec.status < 500 {
		worker.Served.Add(1)
	}

	if convID != "" && sess != nil && rec.status == 200 && r.Context().Err() == nil && usage.assistantMsg != nil {
		sess.mu.Lock()
		sess.Messages = append(sess.Messages, req.Messages...)
		sess.Messages = append(sess.Messages, *usage.assistantMsg)
		sess.mu.Unlock()
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
		if len(result.Choices) > 0 {
			msg := result.Choices[0].Message
			usage.assistantMsg = &msg
		}
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
	api.WriteJSON(w, 200, api.StatusResponse{
		Workers:      pool.Snapshot(),
		SessionCount: pool.SessionStore().TotalCount(),
	})
}
func handlePolicies(pool *Pool, w http.ResponseWriter) {
	api.WriteJSON(w, 200, api.RoutingPolicyList{
		Default:  pool.DefaultRoutingPolicy(),
		Policies: pool.RoutingPolicies(),
	})
}
