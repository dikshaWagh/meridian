package ml

import (
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// WorkerSpec is static ML worker configuration. It is intentionally separate
// from balancer.WorkerSpec: ML workers are addressed by URL and are not spawned
// by Meridian.
type WorkerSpec struct {
	ID          string
	Model       string
	URL         string
	HealthPath  string
	ReadyPath   string
	PredictPath string
	// GPUEnabled is false for CPU-only workers: no GPU data is reported and
	// gpu_score is 0, flagged "n/a".
	GPUEnabled bool
	GPUIndex   int
	Enabled    bool
}

// Worker is one ML inference instance. Counters are atomics; compound state is
// guarded by mu.
type Worker struct {
	Spec WorkerSpec
	URL  *url.URL

	state    atomic.Int32
	inFlight atomic.Int64
	served   atomic.Int64
	success  atomic.Int64
	failures atomic.Int64

	consecutiveFailures atomic.Int32

	mu        sync.RWMutex
	latency   latencyWindow
	telemetry WorkerTelemetry
	lastCheck time.Time
	lastError string

	startedAt time.Time
	grace     time.Duration
	threshold int32
}

func newWorker(spec WorkerSpec, u *url.URL, grace time.Duration, threshold int) *Worker {
	w := &Worker{Spec: spec, URL: u, startedAt: time.Now(), grace: grace, threshold: int32(threshold)}
	w.latency = newLatencyWindow(latencyWindowSize)
	if !spec.Enabled {
		w.state.Store(int32(StateStopped))
		w.lastError = "disabled by configuration"
	}
	if !spec.GPUEnabled {
		w.telemetry.GPUSource = "n/a (cpu-only worker)"
	}
	return w
}

func (w *Worker) State() State    { return State(w.state.Load()) }
func (w *Worker) IsHealthy() bool { return w.State() == StateHealthy }
func (w *Worker) InFlight() int64 { return w.inFlight.Load() }
func (w *Worker) Served() int64   { return w.served.Load() }
func (w *Worker) Success() int64  { return w.success.Load() }
func (w *Worker) Failures() int64 { return w.failures.Load() }

// Latency returns the rolling latency statistics used by the router.
func (w *Worker) Latency() LatencyStats {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.latency.stats()
}

// Telemetry returns the last sampled CPU/GPU data for this worker.
func (w *Worker) Telemetry() WorkerTelemetry {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.telemetry
}

func (w *Worker) setTelemetry(t WorkerTelemetry) {
	w.mu.Lock()
	w.telemetry = t
	w.mu.Unlock()
}

// acquire marks the start of a proxied request; release must be deferred.
func (w *Worker) acquire() { w.inFlight.Add(1) }
func (w *Worker) release() { w.inFlight.Add(-1) }

// recordSuccess counts a 2xx response. A completed request proves reachability,
// so the health failure streak is cleared.
func (w *Worker) recordSuccess(latency time.Duration) {
	w.served.Add(1)
	w.success.Add(1)
	w.consecutiveFailures.Store(0)
	w.observeLatency(latency)
}

// recordRequestFailure counts a 4xx/5xx worker response. Failure rate is not
// health: the state machine is untouched here.
func (w *Worker) recordRequestFailure(latency time.Duration, reason string) {
	w.served.Add(1)
	w.failures.Add(1)
	w.observeLatency(latency)
	w.mu.Lock()
	w.lastError = reason
	w.mu.Unlock()
}

// recordTransportFailure counts an unreachable worker. Reachability *is* health,
// so this feeds the consecutive-failure streak and may flip the state.
func (w *Worker) recordTransportFailure(latency time.Duration, reason string) (changed bool) {
	w.served.Add(1)
	w.failures.Add(1)
	w.observeLatency(latency)
	return w.applyProbe(probeFailure, reason, time.Now())
}

func (w *Worker) observeLatency(latency time.Duration) {
	w.mu.Lock()
	w.latency.add(latency)
	w.mu.Unlock()
}

// Stop moves a worker to STOPPED; it is then never a routing candidate.
func (w *Worker) Stop(reason string) (changed bool) {
	old := State(w.state.Swap(int32(StateStopped)))
	w.mu.Lock()
	w.lastError = reason
	w.mu.Unlock()
	return old != StateStopped
}

// Status is the JSON snapshot of this worker.
func (w *Worker) Status() WorkerStatus {
	w.mu.RLock()
	lat, tel, check, lastErr := w.latency.stats(), w.telemetry, w.lastCheck, w.lastError
	w.mu.RUnlock()
	lastCheck := ""
	if !check.IsZero() {
		lastCheck = check.UTC().Format(time.RFC3339)
	}
	served, success := w.served.Load(), w.success.Load()
	rate := 0.0
	if served > 0 {
		rate = float64(success) / float64(served)
	}
	return WorkerStatus{
		ID:                  w.Spec.ID,
		Model:               w.Spec.Model,
		URL:                 w.Spec.URL,
		State:               w.State().String(),
		Healthy:             w.IsHealthy(),
		InFlight:            w.inFlight.Load(),
		Served:              served,
		Success:             success,
		Failures:            w.failures.Load(),
		SuccessRate:         rate,
		Latency:             lat,
		Telemetry:           tel,
		ConsecutiveFailures: int(w.consecutiveFailures.Load()),
		LastCheck:           lastCheck,
		LastError:           lastErr,
	}
}
