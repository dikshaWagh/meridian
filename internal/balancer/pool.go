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

type Worker struct {
	Spec  WorkerSpec
	URL   *url.URL
	Proxy *httputil.ReverseProxy
	PID   atomic.Int64 // set by the supervisor

	healthy  atomic.Bool
	InFlight atomic.Int64
	Served   atomic.Int64
	// V1: telemetry fields go here
}

type Pool struct {
	workers []*Worker
	mu      sync.Mutex
	rr      map[string]int // per-model round-robin counters
}

func (w *Worker) IsHealthy() bool { return w.healthy.Load() }

// setHealthy stores the state and reports whether it changed.
func (w *Worker) setHealthy(v bool) (changed bool) {
	return w.healthy.Swap(v) != v
}

func NewPool(specs []WorkerSpec) *Pool {
	p := &Pool{rr: map[string]int{}}
	for _, s := range specs {
		u := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", s.Port)}
		proxy := httputil.NewSingleHostReverseProxy(u)
		proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, err error) {
			api.WriteError(rw, http.StatusBadGateway, "upstream_error", err.Error())
		}
		p.workers = append(p.workers, &Worker{Spec: s, URL: u, Proxy: proxy})
	}
	return p
}

func (p *Pool) Workers() []*Worker { return p.workers }

// Pick is the only place routing logic lives.
func (p *Pool) Pick(model string) (*Worker, error) {
	var known, healthy []*Worker
	for _, w := range p.workers {
		if w.Spec.Model != model {
			continue
		}
		known = append(known, w)
		if w.IsHealthy() {
			healthy = append(healthy, w)
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

// Models returns sorted, distinct models with at least one healthy worker.
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
		out = append(out, api.WorkerStatus{
			ID: w.Spec.ID, Model: w.Spec.Model, Port: w.Spec.Port,
			PID: int(w.PID.Load()), Healthy: w.IsHealthy(),
			InFlight: w.InFlight.Load(), Served: w.Served.Load(),
		})
	}
	return out
}

// StartHealthChecks checks once now, then every second until ctx ends.
func (p *Pool) StartHealthChecks(ctx context.Context) {
	client := &http.Client{Timeout: time.Second}
	go func() {
		p.checkAll(client)
		t := time.NewTicker(time.Second)
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
		ok := false
		resp, err := client.Get(w.URL.String() + "/health")
		if err == nil {
			ok = resp.StatusCode == http.StatusOK
			resp.Body.Close()
		}
		if w.setHealthy(ok) {
			state := "down"
			if ok {
				state = "up"
			}
			log.Printf("worker %s (%s) is now %s", w.Spec.ID, w.Spec.Model, state)
		}
	}
}
