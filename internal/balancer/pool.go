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
	ErrModelNotFound        = errors.New("model not found")
	ErrNoHealthyWorker      = errors.New("no healthy worker")
	ErrModelMismatch        = errors.New("model mismatch for session")
	ErrUnknownRoutingPolicy = errors.New("unknown routing policy")
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
	Reserved            atomic.Int64
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

// Load is the number of requests assigned to the worker. It includes requests
// that have been selected but have not started proxying yet.
func (w *Worker) Load() int64 {
	return w.InFlight.Load() + w.Reserved.Load()
}

// WorkerReservation holds a worker slot between selection and proxying.
// Call Commit when the request starts proxying, or Release if it never does.
type WorkerReservation struct {
	worker *Worker
	once   sync.Once
}

func (r *WorkerReservation) Commit() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.worker.Reserved.Add(-1)
		r.worker.InFlight.Add(1)
	})
}

func (r *WorkerReservation) Release() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		r.worker.Reserved.Add(-1)
	})
}

func reserveWorker(w *Worker) *WorkerReservation {
	w.Reserved.Add(1)
	return &WorkerReservation{worker: w}
}

type Pool struct {
	workers       []*Worker
	mu            sync.Mutex
	pickMu        sync.Mutex
	policies      map[string]RoutingPolicy
	defaultPolicy string
	store         *SessionStore
	ctxSize       int
	parallel      int
}

func NewPool(specs []WorkerSpec) *Pool {
	p := &Pool{
		policies: map[string]RoutingPolicy{},
		store:    NewSessionStore(),
		ctxSize:  8192,
		parallel: 2,
	}
	for _, policy := range newRoutingPolicies() {
		p.policies[policy.Name()] = policy
		if p.defaultPolicy == "" {
			p.defaultPolicy = policy.Name()
		}
	}
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

// markStarting resets a worker for a fresh process generation. It clears the
// exited flag and renews the readiness grace window so a restart is not
// immediately judged against its previous lifetime.
func (w *Worker) markStarting() {
	w.exited.Store(false)
	w.consecutiveFailures.Store(0)
	w.mu.Lock()
	w.startedAt = time.Now()
	w.lastError = ""
	w.lastCheck = time.Time{}
	w.mu.Unlock()
	w.state.Store(StateStarting)
}

func (p *Pool) Workers() []*Worker { return p.workers }

// Pick uses the pool's default routing policy.
func (p *Pool) Pick(model string) (*Worker, error) {
	return p.PickWithPolicy(model, "")
}

// PickWithPolicy uses the named routing policy. An empty name means the
// pool's default policy.
func (p *Pool) PickWithPolicy(model, policyName string) (*Worker, error) {
	policy, err := p.routingPolicy(policyName)
	if err != nil {
		return nil, err
	}
	return p.pickWith(policy, model)
}

func (p *Pool) pickWith(policy RoutingPolicy, model string) (*Worker, error) {
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
	w := policy.Pick(model, healthy)
	if w == nil {
		return nil, ErrNoHealthyWorker
	}
	return w, nil
}

// reservePick selects a worker and reserves it in one critical section, so
// concurrent selections cannot all choose the same least-loaded worker.
func (p *Pool) reservePick(policy RoutingPolicy, model string) (*Worker, *WorkerReservation, error) {
	p.pickMu.Lock()
	defer p.pickMu.Unlock()
	w, err := p.pickWith(policy, model)
	if err != nil {
		return nil, nil, err
	}
	return w, reserveWorker(w), nil
}

func (p *Pool) routingPolicy(name string) (RoutingPolicy, error) {
	if name == "" {
		p.mu.Lock()
		name = p.defaultPolicy
		p.mu.Unlock()
	}
	policy, ok := p.policies[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownRoutingPolicy, name)
	}
	return policy, nil
}

// RoutingPolicies lists the policies available to the server and TUI.
func (p *Pool) RoutingPolicies() []api.RoutingPolicyInfo {
	out := make([]api.RoutingPolicyInfo, 0, len(p.policies))
	for _, policy := range p.policies {
		out = append(out, api.RoutingPolicyInfo{
			Name:        policy.Name(),
			Description: policy.Description(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (p *Pool) DefaultRoutingPolicy() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.defaultPolicy
}

// SetDefaultRoutingPolicy changes the policy used when a request does not
// specify one. It is normally called once at startup.
func (p *Pool) SetDefaultRoutingPolicy(name string) error {
	if _, ok := p.policies[name]; !ok {
		return fmt.Errorf("%w: %s", ErrUnknownRoutingPolicy, name)
	}
	p.mu.Lock()
	p.defaultPolicy = name
	p.mu.Unlock()
	return nil
}

func (p *Pool) ModelStarting(model string) bool {
	for _, w := range p.workers {
		if w.Spec.Model == model && w.State() == "starting" {
			return true
		}
	}
	return false
}
// Models returns model ids with at least one healthy worker.
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

// ModelCatalog lists every configured LLM model, healthy or not. The React UI
// needs the full list while the LLM subsystem is stopped so the user can pick
// a model before switching back.
func (p *Pool) ModelCatalog() []api.CatalogModel {
	if p == nil {
		return []api.CatalogModel{}
	}
	type agg struct {
		model           string
		workers, healthy, starting int
	}
	order := []string{}
	byModel := map[string]*agg{}
	for _, w := range p.workers {
		e, ok := byModel[w.Spec.Model]
		if !ok {
			e = &agg{model: w.Spec.Model}
			byModel[w.Spec.Model] = e
			order = append(order, w.Spec.Model)
		}
		e.workers++
		switch w.State() {
		case "healthy":
			e.healthy++
		case "starting":
			e.starting++
		}
	}
	sort.Strings(order)
	out := make([]api.CatalogModel, 0, len(order))
	for _, model := range order {
		e := byModel[model]
		state := "unhealthy"
		switch {
		case e.starting > 0:
			state = "starting"
		case e.healthy > 0:
			state = "ready"
		}
		out = append(out, api.CatalogModel{
			ID:             model,
			Object:         "model",
			Kind:           "llm",
			State:          state,
			Healthy:        e.healthy > 0,
			Workers:        e.workers,
			HealthyWorkers: e.healthy,
			ContextWindow:  p.PerConvContext(),
		})
	}
	return out
}

func (p *Pool) SessionStore() *SessionStore { return p.store }

func (p *Pool) SetContextAndParallel(ctxSize, parallel int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctxSize > 0 {
		p.ctxSize = ctxSize
	}
	if parallel > 0 {
		p.parallel = parallel
	}
}

func (p *Pool) PerConvContext() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.parallel <= 0 {
		return p.ctxSize
	}
	return p.ctxSize / p.parallel
}

func (p *Pool) WorkerByID(id string) *Worker {
	for _, w := range p.workers {
		if w.Spec.ID == id {
			return w
		}
	}
	return nil
}

// PickForSession uses the pool's default routing policy.
func (p *Pool) PickForSession(model, convID string) (*Worker, *Session, error) {
	return p.PickForSessionWithPolicy(model, convID, "")
}

// PickForSessionWithPolicy is like PickForSession but uses the named routing
// policy when a worker has to be chosen (new conversation or worker move).
func (p *Pool) PickForSessionWithPolicy(model, convID, policyName string) (*Worker, *Session, error) {
	w, sess, _, err := p.pickForSession(model, convID, policyName, false)
	return w, sess, err
}

// PickForSessionWithPolicyReserved is like PickForSessionWithPolicy, but also
// reserves the selected worker until Commit or Release is called. This makes
// policies such as least-connections safe under concurrent requests.
func (p *Pool) PickForSessionWithPolicyReserved(model, convID, policyName string) (*Worker, *Session, *WorkerReservation, error) {
	return p.pickForSession(model, convID, policyName, true)
}

func (p *Pool) pickForSession(model, convID, policyName string, reserve bool) (*Worker, *Session, *WorkerReservation, error) {
	policy, err := p.routingPolicy(policyName)
	if err != nil {
		return nil, nil, nil, err
	}

	pick := func() (*Worker, *WorkerReservation, error) {
		if reserve {
			return p.reservePick(policy, model)
		}
		w, err := p.pickWith(policy, model)
		return w, nil, err
	}

	if convID == "" {
		w, r, err := pick()
		return w, nil, r, err
	}

	sess, ok := p.store.Get(convID)
	if !ok {
		w, r, err := pick()
		if err != nil {
			return nil, nil, nil, err
		}

		// Create atomically: two concurrent first requests for the same ID must
		// share one Session so one of them can be rejected with 409.
		var created bool
		sess, created = p.store.CreateIfAbsent(convID, model, w.Spec.ID)
		if created {
			return w, sess, r, nil
		}
		// Lost the race; discard the reservation and re-evaluate the session.
		r.Release()
	}

	sess.mu.Lock()
	sessModel := sess.Model
	pinnedWorkerID := sess.WorkerID
	sess.mu.Unlock()

	if sessModel != model {
		return nil, nil, nil, ErrModelMismatch
	}

	w := p.WorkerByID(pinnedWorkerID)
	if w != nil && w.IsHealthy() {
		var r *WorkerReservation
		if reserve {
			r = reserveWorker(w)
		}
		return w, sess, r, nil
	}

	// Pinned worker is starting/unhealthy/exited -> move
	w, r, err := pick()
	if err != nil {
		return nil, sess, nil, err
	}

	sess.mu.Lock()
	oldWorkerID := sess.WorkerID
	sess.WorkerID = w.Spec.ID
	sess.mu.Unlock()

	reason := "worker unhealthy"
	if wOld := p.WorkerByID(oldWorkerID); wOld != nil {
		if wOld.exited.Load() {
			reason = "worker process exited"
		} else if wOld.State() == "starting" {
			reason = "worker starting"
		}
	}

	log.Printf("session %s moved %s -> %s (reason: %s)", convID, oldWorkerID, w.Spec.ID, reason)
	return w, sess, r, nil
}

func (p *Pool) Snapshot() []api.WorkerStatus {
	out := make([]api.WorkerStatus, 0, len(p.workers))
	counts := p.store.SessionCountPerWorker()
	for _, w := range p.workers {
		w.mu.RLock()
		check, lastErr := w.lastCheck, w.lastError
		w.mu.RUnlock()
		lastCheck := ""
		if !check.IsZero() {
			lastCheck = check.UTC().Format(time.RFC3339)
		}
		out = append(out, api.WorkerStatus{
			ID:                  w.Spec.ID,
			Model:               w.Spec.Model,
			Port:                w.Spec.Port,
			PID:                 int(w.PID.Load()),
			State:               w.State(),
			Healthy:             w.IsHealthy(),
			InFlight:            w.InFlight.Load(),
			Served:              w.Served.Load(),
			EstTokensInFlight:   w.EstTokensInFlight.Load(),
			ConsecutiveFailures: int(w.consecutiveFailures.Load()),
			LastCheck:           lastCheck,
			LastError:           lastErr,
			Sessions:            counts[w.Spec.ID],
		})
	}
	return out
}

func (p *Pool) StartSessionSweeper(ctx context.Context) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.store.Sweep()
			}
		}
	}()
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
