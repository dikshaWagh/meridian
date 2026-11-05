package ml

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// probeResult is the health verdict for one check. Health answers "can this
// worker serve requests?" only: load and failure rate are not inputs.
type probeResult int

const (
	probeSuccess probeResult = iota
	probeNotReady
	probeFailure
)

const (
	// DefaultStartupGrace is how long a worker may stay STARTING while it loads
	// its model before it is considered UNHEALTHY.
	DefaultStartupGrace = 60 * time.Second
	// DefaultFailureThreshold is the consecutive failures tolerated before a
	// HEALTHY worker is removed from the candidate set.
	DefaultFailureThreshold = 3
	// DefaultHealthInterval is the health check period.
	DefaultHealthInterval = 2 * time.Second
)

// healthBody is the optional JSON an ML worker may return from /health.
type healthBody struct {
	Status      string `json:"status"`
	Model       string `json:"model"`
	ModelLoaded *bool  `json:"model_loaded"`
}

// applyProbe is the health state machine. It is isolated from HTTP so the
// transitions can be tested without sockets or sleeps.
func (w *Worker) applyProbe(result probeResult, detail string, now time.Time) (changed bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	old := State(w.state.Load())
	w.lastCheck = now
	if old == StateStopped {
		return false
	}
	threshold := w.threshold
	if threshold <= 0 {
		threshold = DefaultFailureThreshold
	}
	switch result {
	case probeSuccess:
		w.consecutiveFailures.Store(0)
		w.lastError = ""
		w.state.Store(int32(StateHealthy))
	case probeNotReady, probeFailure:
		if detail != "" {
			w.lastError = detail
		}
		switch old {
		case StateStarting:
			if now.Sub(w.startedAt) >= w.grace {
				w.state.Store(int32(StateUnhealthy))
				w.lastError = "startup grace period expired: " + detail
			}
		case StateHealthy:
			if w.consecutiveFailures.Add(1) >= threshold {
				w.state.Store(int32(StateUnhealthy))
			}
		case StateUnhealthy:
			w.consecutiveFailures.Add(1)
		}
	}
	return old != State(w.state.Load())
}

// HealthChecker periodically probes every ML worker.
type HealthChecker struct {
	pool     *Pool
	client   *http.Client
	interval time.Duration
	logger   *log.Logger
	once     sync.Once
}

func NewHealthChecker(pool *Pool, interval time.Duration, logger *log.Logger) *HealthChecker {
	if interval <= 0 {
		interval = DefaultHealthInterval
	}
	if logger == nil {
		logger = log.Default()
	}
	return &HealthChecker{
		pool:     pool,
		client:   &http.Client{Timeout: 2 * time.Second},
		interval: interval,
		logger:   logger,
	}
}

// Start runs checks until ctx is cancelled.
func (h *HealthChecker) Start(ctx context.Context) {
	h.once.Do(func() {
		go func() {
			h.CheckAll(ctx)
			t := time.NewTicker(h.interval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					h.CheckAll(ctx)
				}
			}
		}()
	})
}

// CheckAll probes every worker in parallel.
func (h *HealthChecker) CheckAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, w := range h.pool.Workers() {
		if w.State() == StateStopped {
			continue
		}
		wg.Add(1)
		go func(w *Worker) {
			defer wg.Done()
			result, detail := h.Probe(ctx, w)
			if w.applyProbe(result, detail, time.Now()) {
				h.logger.Printf("ml worker %s (%s) is now %s: %s", w.Spec.ID, w.Spec.Model, w.State(), detailOr(detail, "ok"))
			}
		}(w)
	}
	wg.Wait()
}

func detailOr(detail, fallback string) string {
	if detail == "" {
		return fallback
	}
	return detail
}

// Probe checks reachability, the health endpoint, optional readiness and
// whether the worker reports the expected model as loaded.
func (h *HealthChecker) Probe(ctx context.Context, w *Worker) (probeResult, string) {
	result, detail := h.get(ctx, w, w.Spec.HealthPath, true)
	if result != probeSuccess || w.Spec.ReadyPath == "" {
		return result, detail
	}
	return h.get(ctx, w, w.Spec.ReadyPath, false)
}

func (h *HealthChecker) get(ctx context.Context, w *Worker, path string, inspectBody bool) (probeResult, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.Spec.URL+path, nil)
	if err != nil {
		return probeFailure, err.Error()
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return probeFailure, "unreachable: " + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		return probeNotReady, path + " reports not ready"
	case resp.StatusCode != http.StatusOK:
		return probeFailure, fmt.Sprintf("%s returned %d", path, resp.StatusCode)
	}
	if !inspectBody {
		return probeSuccess, ""
	}
	var hb healthBody
	if json.Unmarshal(body, &hb) != nil {
		return probeSuccess, "" // body is optional and free-form
	}
	if hb.ModelLoaded != nil && !*hb.ModelLoaded {
		return probeNotReady, "model not loaded"
	}
	if hb.Model != "" && hb.Model != w.Spec.Model {
		return probeFailure, fmt.Sprintf("model mismatch: worker serves %q, expected %q", hb.Model, w.Spec.Model)
	}
	if hb.Status != "" && !okStatus(hb.Status) {
		return probeNotReady, "status " + hb.Status
	}
	return probeSuccess, ""
}

func okStatus(s string) bool {
	switch strings.ToLower(s) {
	case "ok", "healthy", "ready", "up", "serving":
		return true
	}
	return false
}
