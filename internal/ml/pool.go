package ml

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"sync"
	"time"
)

var (
	// ErrModelNotFound means no ML worker is configured for the model.
	ErrModelNotFound = errors.New("ml model not found")
	// ErrNoHealthyWorker means the model exists but every worker is filtered out.
	ErrNoHealthyWorker = errors.New("no healthy ml worker")
)

// Pool is the thread-safe ML worker set plus the explicit model -> workers map
// that drives stage one of routing.
type Pool struct {
	mu      sync.RWMutex
	workers []*Worker
	byModel map[string][]*Worker
}

// NewPool builds a pool from specs. grace is the startup readiness window and
// threshold the consecutive failures tolerated before a worker leaves the
// candidate set.
func NewPool(specs []WorkerSpec, grace time.Duration, threshold int) (*Pool, error) {
	p := &Pool{byModel: map[string][]*Worker{}}
	seen := map[string]bool{}
	for _, s := range specs {
		if s.ID == "" || s.Model == "" {
			return nil, fmt.Errorf("ml worker needs both id and model")
		}
		if seen[s.ID] {
			return nil, fmt.Errorf("duplicate ml worker id %q", s.ID)
		}
		seen[s.ID] = true
		u, err := url.Parse(s.URL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("ml worker %s: invalid url %q", s.ID, s.URL)
		}
		if s.HealthPath == "" {
			s.HealthPath = DefaultHealthPath
		}
		if s.PredictPath == "" {
			s.PredictPath = DefaultPredictPath
		}
		w := newWorker(s, u, grace, threshold)
		p.workers = append(p.workers, w)
		p.byModel[s.Model] = append(p.byModel[s.Model], w)
	}
	return p, nil
}

// Workers returns every worker in configuration order.
func (p *Pool) Workers() []*Worker {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]*Worker(nil), p.workers...)
}

// ForModel is stage one of routing: only workers serving model are candidates.
func (p *Pool) ForModel(model string) []*Worker {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]*Worker(nil), p.byModel[model]...)
}

// HasModel reports whether any worker is configured for the model.
func (p *Pool) HasModel(model string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.byModel[model]) > 0
}

// Models lists every configured model, sorted.
func (p *Pool) Models() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.byModel))
	for m := range p.byModel {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// ModelStatus summarises the model -> workers map.
func (p *Pool) ModelStatus() []ModelStatus {
	out := []ModelStatus{}
	for _, m := range p.Models() {
		ms := ModelStatus{Model: m, WorkerID: []string{}}
		for _, w := range p.ForModel(m) {
			ms.Workers++
			ms.WorkerID = append(ms.WorkerID, w.Spec.ID)
			if w.IsHealthy() {
				ms.Healthy++
			}
		}
		out = append(out, ms)
	}
	return out
}

// Status snapshots every worker.
func (p *Pool) Status() []WorkerStatus {
	workers := p.Workers()
	out := make([]WorkerStatus, 0, len(workers))
	for _, w := range workers {
		out = append(out, w.Status())
	}
	return out
}

// Totals aggregates request metrics over the whole ML pool.
func (p *Pool) Totals() Totals {
	var t Totals
	var weighted float64
	var samples int
	for _, w := range p.Workers() {
		t.Served += w.Served()
		t.Success += w.Success()
		t.Failures += w.Failures()
		t.InFlight += w.InFlight()
		if lat := w.Latency(); lat.Samples > 0 {
			weighted += lat.AvgMS * float64(lat.Samples)
			samples += lat.Samples
		}
	}
	if t.Served > 0 {
		t.SuccessRate = float64(t.Success) / float64(t.Served)
	}
	if samples > 0 {
		t.AvgLatencyMS = weighted / float64(samples)
	}
	return t
}

// StopAll marks every worker STOPPED, used on shutdown.
func (p *Pool) StopAll(reason string) {
	for _, w := range p.Workers() {
		w.Stop(reason)
	}
}
