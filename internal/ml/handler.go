package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

// MaxPredictBody caps the /v1/predict body. ML inputs can be images, so this is
// larger than the chat limit.
const MaxPredictBody = 16 << 20

// DefaultRequestTimeout bounds one proxied inference call.
const DefaultRequestTimeout = 120 * time.Second

// attempt carries the outcome of a single proxy call out of the ReverseProxy
// error handler, which runs inside ServeHTTP.
type attempt struct {
	transportErr error
	status       int
	clientAbort  bool
}

type attemptKey struct{}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status, s.written = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.written = true
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Handler serves the ML HTTP surface. It never touches LLM routes.
type Handler struct {
	router    *Router
	telemetry *Telemetry
	proxies   map[string]*httputil.ReverseProxy
	timeout   time.Duration
	logger    *log.Logger
}

// NewHandler builds the ML mux: POST /v1/predict plus read-only ML status.
func NewHandler(router *Router, telemetry *Telemetry, timeout time.Duration, logger *log.Logger) http.Handler {
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	if logger == nil {
		logger = log.Default()
	}
	h := &Handler{
		router:    router,
		telemetry: telemetry,
		proxies:   map[string]*httputil.ReverseProxy{},
		timeout:   timeout,
		logger:    logger,
	}
	for _, w := range router.Pool().Workers() {
		h.proxies[w.Spec.ID] = newWorkerProxy(w)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/predict", h.handlePredict)
	mux.HandleFunc("GET /ml/status", h.handleStatus)
	mux.HandleFunc("GET /ml/models", h.handleModels)
	mux.HandleFunc("GET /ml/decisions", h.handleDecisions)
	mux.HandleFunc("GET /ml/healthz", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok", "subsystem": "ml"})
	})
	return mux
}

func newWorkerProxy(w *Worker) *httputil.ReverseProxy {
	target := w.URL
	predictPath := joinPath(target.Path, w.Spec.PredictPath)
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = predictPath
			req.URL.RawPath = ""
			req.Host = target.Host
			if _, ok := req.Header["User-Agent"]; !ok {
				req.Header.Set("User-Agent", "meridian-ml")
			}
		},
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConnsPerHost: 32,
			IdleConnTimeout:     90 * time.Second,
		},
		ErrorHandler: func(rw http.ResponseWriter, req *http.Request, err error) {
			a, _ := req.Context().Value(attemptKey{}).(*attempt)
			if errors.Is(err, context.Canceled) {
				// The client went away; this is not a worker failure.
				if a != nil {
					a.clientAbort, a.status = true, 0
				}
				return
			}
			status, errType := http.StatusBadGateway, "worker_unreachable"
			if isTimeout(err) {
				status, errType = http.StatusGatewayTimeout, "worker_timeout"
			}
			if a != nil {
				a.transportErr, a.status = err, status
			}
			api.WriteError(rw, status, errType, err.Error())
		},
	}
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func joinPath(base, path string) string {
	base = strings.TrimRight(base, "/")
	if path == "" {
		path = DefaultPredictPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func (h *Handler) handlePredict(w http.ResponseWriter, r *http.Request) {
	t0 := time.Now()

	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxPredictBody))
	if err != nil {
		h.reject(w, nil, http.StatusBadRequest, "invalid_request", fmt.Sprintf("could not read body (max %d bytes)", MaxPredictBody), t0)
		return
	}
	var req PredictRequest
	if json.Unmarshal(raw, &req) != nil {
		h.reject(w, nil, http.StatusBadRequest, "invalid_request", "body is not valid JSON", t0)
		return
	}
	if req.Model == "" {
		h.reject(w, nil, http.StatusBadRequest, "invalid_request", "model is required", t0)
		return
	}
	if len(req.Input) == 0 {
		h.reject(w, &RoutingDecision{Model: req.Model}, http.StatusBadRequest, "invalid_request", "input is required", t0)
		return
	}

	worker, decision, err := h.router.Route(req.Model)
	routing := time.Since(t0)
	switch {
	case errors.Is(err, ErrModelNotFound):
		h.reject(w, decision, http.StatusNotFound, "model_not_found", "unknown ml model "+strconv.Quote(req.Model), t0)
		return
	case errors.Is(err, ErrNoHealthyWorker):
		h.reject(w, decision, http.StatusServiceUnavailable, "no_healthy_worker", "no healthy ml worker for model "+strconv.Quote(req.Model), t0)
		return
	case err != nil:
		h.reject(w, decision, http.StatusInternalServerError, "internal_error", err.Error(), t0)
		return
	}

	proxy := h.proxies[worker.Spec.ID]
	if proxy == nil {
		h.reject(w, decision, http.StatusInternalServerError, "internal_error", "no proxy for worker "+worker.Spec.ID, t0)
		return
	}

	w.Header().Set("X-Meridian-ML-Worker", worker.Spec.ID)
	w.Header().Set("X-Meridian-ML-Model", req.Model)
	w.Header().Set("X-Meridian-ML-Score", strconv.FormatFloat(decision.FinalScore, 'f', 4, 64))
	w.Header().Set("X-Meridian-ML-Candidates", strconv.Itoa(len(decision.CandidateWorkers)))

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()
	a := &attempt{}
	req2 := r.WithContext(context.WithValue(ctx, attemptKey{}, a))
	req2.Body = io.NopCloser(bytes.NewReader(raw))
	req2.ContentLength = int64(len(raw))

	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	worker.acquire()
	start := time.Now()
	proxy.ServeHTTP(rec, req2)
	latency := time.Since(start)
	worker.release()

	status := rec.status
	errMsg := ""
	switch {
	case a.clientAbort:
		status = 499
		errMsg = "client closed request"
	case a.transportErr != nil:
		status = a.status
		errMsg = a.transportErr.Error()
		if worker.recordTransportFailure(latency, errMsg) {
			h.logger.Printf("ml worker %s (%s) is now %s: %s", worker.Spec.ID, worker.Spec.Model, worker.State(), errMsg)
		}
	case status >= 200 && status < 300:
		worker.recordSuccess(latency)
	default:
		errMsg = fmt.Sprintf("worker returned %d", status)
		worker.recordRequestFailure(latency, errMsg)
	}

	ev := h.telemetry.Record(decision, status, latency, routing, errMsg)
	h.logger.Printf("ml model=%s worker=%s status=%d score=%.3f latency=%.0fms routing=%.1fms candidates=%d healthy=%d",
		req.Model, worker.Spec.ID, status, decision.FinalScore, ev.LatencyMS, ev.RoutingMS,
		len(decision.CandidateWorkers), len(decision.HealthyWorkers))
}

// reject answers a request that never reached a worker. It is still recorded so
// the TUI shows 400/404/503 outcomes.
func (h *Handler) reject(w http.ResponseWriter, d *RoutingDecision, status int, errType, msg string, t0 time.Time) {
	api.WriteError(w, status, errType, msg)
	if h.telemetry != nil {
		h.telemetry.Record(d, status, 0, time.Since(t0), msg)
	}
	model := ""
	if d != nil {
		model = d.Model
	}
	h.logger.Printf("ml model=%s status=%d error=%s", model, status, msg)
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, h.telemetry.Snapshot(h.router.Weights()))
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, map[string]any{"models": h.router.Pool().ModelStatus()})
}

func (h *Handler) handleDecisions(w http.ResponseWriter, r *http.Request) {
	api.WriteJSON(w, http.StatusOK, map[string]any{"recent": h.telemetry.Events()})
}
