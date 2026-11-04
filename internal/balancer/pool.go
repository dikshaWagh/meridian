package balancer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

var (
	ErrModelNotFound   = errors.New("model not found")
	ErrNoHealthyWorker = errors.New("no healthy worker")
)

const (
	StateStarting int32 = iota
	StateHealthy
	StateUnhealthy
)

type probeResult int

const (
	probeSuccess probeResult = iota
	probeNotReady
	probeFailure
)

type Worker struct {
	Spec                WorkerSpec
	URL                 *url.URL
	Proxy               *httputil.ReverseProxy
	PID                 atomic.Int64
	state               atomic.Int32
	InFlight            atomic.Int64
	EstTokensInFlight   atomic.Int64
	Served              atomic.Int64
	consecutiveFailures atomic.Int32
	exited              atomic.Bool
	startedAt           time.Time
	grace               time.Duration
	mu                  sync.RWMutex
	lastCheck           time.Time
	lastError           string
}

type Pool struct {
	workers []*Worker
	mu      sync.Mutex
	rr      map[string]int
}

func NewPool(specs []WorkerSpec) *Pool {
	p := &Pool{rr: map[string]int{}}
	for _, s := range specs {
		u := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", s.Port)}
		proxy := httputil.NewSingleHostReverseProxy(u)
		proxy.Transport = &http.Transport{ResponseHeaderTimeout: 5 * time.Minute}
		worker := &Worker{Spec: s, URL: u, Proxy: proxy, startedAt: time.Now(), grace: 180 * time.Second}
		proxy.ModifyResponse = func(resp *http.Response) error {
			if usage, ok := resp.Request.Context().Value(usageKey{}).(*requestUsage); ok {
				return captureUsage(resp, usage)
			}
			return nil
		}
		proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
			worker.recordFailure(err)
			api.WriteError(rw, 502, "upstream_error", err.Error())
		}
		p.workers = append(p.workers, worker)
	}
	return p
}

func (w *Worker) IsHealthy() bool { return w.state.Load() == StateHealthy }
func (w *Worker) State() string {
	switch w.state.Load() {
	case StateHealthy:
		return "healthy"
	case StateUnhealthy:
		return "unhealthy"
	default:
		return "starting"
	}
}
func (w *Worker) setHealthy(v bool) (changed bool) {
	if v {
		return w.applyProbe(probeSuccess, time.Now())
	}
	old := w.state.Swap(StateUnhealthy)
	return old != StateUnhealthy
}

// applyProbe is isolated so the state machine can be tested without HTTP or sleeps.
func (w *Worker) applyProbe(result probeResult, now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	old := w.state.Load()
	w.lastCheck = now
	if w.exited.Load() {
		w.state.Store(StateUnhealthy)
		w.lastError = "process exited"
		return old != StateUnhealthy
	}
	switch result {
	case probeSuccess:
		w.consecutiveFailures.Store(0)
		w.lastError = ""
		if old == StateStarting || old == StateUnhealthy {
			w.state.Store(StateHealthy)
		}
	case probeNotReady:
		if old == StateStarting && now.Sub(w.startedAt) >= w.grace {
			w.state.Store(StateUnhealthy)
			w.lastError = "readiness grace period expired"
		} else if old == StateHealthy {
			n := w.consecutiveFailures.Add(1)
			if n >= 3 {
				w.state.Store(StateUnhealthy)
				w.lastError = "consecutive health check failures"
			}
		}
	case probeFailure:
		if old == StateStarting {
			if now.Sub(w.startedAt) >= w.grace {
				w.state.Store(StateUnhealthy)
				w.lastError = "readiness grace period expired"
			}
		} else if old == StateHealthy {
			n := w.consecutiveFailures.Add(1)
			if n >= 3 {
				w.state.Store(StateUnhealthy)
				w.lastError = "consecutive health check failures"
			}
		}
	}
	return old != w.state.Load()
}

func (w *Worker) recordFailure(err error) {
	w.mu.Lock()
	w.lastError = err.Error()
	w.mu.Unlock()
	w.applyProbe(probeFailure, time.Now())
}
func (w *Worker) markExited() bool {
	was := w.state.Load()
	w.exited.Store(true)
	w.mu.Lock()
	w.lastError = "process exited"
	w.mu.Unlock()
	w.state.Store(StateUnhealthy)
	return was != StateUnhealthy
}

func (p *Pool) Workers() []*Worker { return p.workers }

func (p *Pool) Pick(model string) (*Worker, error) {
	var known, healthy []*Worker
	for _, w := range p.workers {
		if w.Spec.Model == model {
			known = append(known, w)
			if w.IsHealthy() {
				healthy = append(healthy, w)
			}
		}
	}
	if len(known) == 0 {
		return nil, ErrModelNotFound
	}
	if len(healthy) == 0 {
		return nil, ErrNoHealthyWorker
	}
	p.mu.Lock()
	idx := p.rr[model] % len(healthy)
	p.rr[model]++
	p.mu.Unlock()
	return healthy[idx], nil
}

func (p *Pool) ModelStarting(model string) bool {
	for _, w := range p.workers {
		if w.Spec.Model == model && w.State() == "starting" {
			return true
		}
	}
	return false
}
func (p *Pool) Models() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, w := range p.workers {
		if w.IsHealthy() && !seen[w.Spec.Model] {
			seen[w.Spec.Model] = true
			out = append(out, w.Spec.Model)
		}
	}
	sort.Strings(out)
	return out
}

func (p *Pool) Snapshot() []api.WorkerStatus {
	out := make([]api.WorkerStatus, 0, len(p.workers))
	for _, w := range p.workers {
		w.mu.RLock()
		check, lastErr := w.lastCheck, w.lastError
		w.mu.RUnlock()
		lastCheck := ""
		if !check.IsZero() {
			lastCheck = check.UTC().Format(time.RFC3339)
		}
		out = append(out, api.WorkerStatus{ID: w.Spec.ID, Model: w.Spec.Model, Port: w.Spec.Port, PID: int(w.PID.Load()), State: w.State(), Healthy: w.IsHealthy(), InFlight: w.InFlight.Load(), Served: w.Served.Load(), EstTokensInFlight: w.EstTokensInFlight.Load(), ConsecutiveFailures: int(w.consecutiveFailures.Load()), LastCheck: lastCheck, LastError: lastErr})
	}
	return out
}

func (p *Pool) StartHealthChecks(ctx context.Context) {
	client := &http.Client{Timeout: 2 * time.Second}
	go func() {
		p.checkAll(client)
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.checkAll(client)
			}
		}
	}()
}

func (p *Pool) checkAll(client *http.Client) {
	for _, w := range p.workers {
		result := probeFailure
		resp, err := client.Get(w.URL.String() + "/health")
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				result = probeSuccess
			} else if resp.StatusCode == http.StatusServiceUnavailable {
				result = probeNotReady
			}
			resp.Body.Close()
		}
		if w.applyProbe(result, time.Now()) {
			log.Printf("worker %s (%s) is now %s", w.Spec.ID, w.Spec.Model, w.State())
		}
	}
}
