package ml

import (
	"context"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTelemetryInterval is how often host CPU/GPU are resampled.
const DefaultTelemetryInterval = time.Second

// recentEvents is how many routing events the TUI log keeps.
const recentEvents = 64

// Telemetry collects host resource usage for ML workers and keeps a rolling log
// of routing decisions for the ML TUI. Every value it publishes is measured;
// unmeasurable signals are flagged unavailable.
type Telemetry struct {
	pool     *Pool
	cpu      *cpuSampler
	gpu      *gpuSampler
	interval time.Duration

	mu     sync.RWMutex
	events []RequestEvent
	seq    atomic.Int64
	once   sync.Once
}

func NewTelemetry(pool *Pool, interval time.Duration) *Telemetry {
	if interval <= 0 {
		interval = DefaultTelemetryInterval
	}
	return &Telemetry{
		pool:     pool,
		cpu:      &cpuSampler{},
		gpu:      newGPUSampler(),
		interval: interval,
		events:   make([]RequestEvent, 0, recentEvents),
	}
}

// Start samples host telemetry until ctx is cancelled.
func (t *Telemetry) Start(ctx context.Context) {
	t.once.Do(func() {
		go func() {
			t.Sample(ctx)
			tk := time.NewTicker(t.interval)
			defer tk.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
					t.Sample(ctx)
				}
			}
		}()
	})
}

// Sample refreshes CPU/GPU readings and attributes them to workers.
//
// Meridian does not spawn ML workers, so there is no per-process accounting:
// host telemetry is attributed only to workers running on this host (loopback
// URLs). Remote workers report CPU/GPU as unavailable instead of a guess.
func (t *Telemetry) Sample(ctx context.Context) {
	cpuPct, cpuOK := t.cpu.Sample()
	gpus, gpuOK := t.gpu.Sample(ctx)
	for _, w := range t.pool.Workers() {
		tel := WorkerTelemetry{}
		local := isLocalURL(w.URL)
		switch {
		case !local:
			tel.CPUSource = "n/a (remote worker)"
		case cpuOK:
			tel.CPUPercent, tel.CPUAvailable, tel.CPUSource = cpuPct, true, "host"
		default:
			tel.CPUSource = "n/a (no host cpu telemetry)"
		}
		switch {
		case !w.Spec.GPUEnabled:
			tel.GPUSource = "n/a (cpu-only worker)"
		case !local:
			tel.GPUSource = "n/a (remote worker)"
		case !gpuOK:
			tel.GPUSource = "n/a (no gpu detected)"
		default:
			if g, ok := gpuAt(gpus, w.Spec.GPUIndex); ok {
				tel.GPUPercent, tel.GPUMemPercent, tel.GPUAvailable = g.UtilPercent, g.MemPercent, true
				tel.GPUSource = "nvidia-smi:" + g.Name
			} else {
				tel.GPUSource = "n/a (gpu index out of range)"
			}
		}
		w.setTelemetry(tel)
	}
}

func gpuAt(gpus []GPUStats, index int) (GPUStats, bool) {
	for _, g := range gpus {
		if g.Index == index {
			return g, true
		}
	}
	return GPUStats{}, false
}

// Record appends one completed (or rejected) request to the routing log.
func (t *Telemetry) Record(d *RoutingDecision, status int, latency, routing time.Duration, errMsg string) RequestEvent {
	ev := RequestEvent{
		Seq:       t.seq.Add(1),
		Time:      time.Now().Format("15:04:05"),
		Status:    status,
		LatencyMS: float64(latency) / float64(time.Millisecond),
		RoutingMS: float64(routing) / float64(time.Millisecond),
		Error:     errMsg,
	}
	if d != nil {
		ev.Model = d.Model
		ev.Candidates = SortedCandidates(d)
		ev.Selected = d.SelectedWorker
		ev.Reason = d.Reason
	}
	t.mu.Lock()
	t.events = append(t.events, ev)
	if len(t.events) > recentEvents {
		t.events = append(t.events[:0], t.events[len(t.events)-recentEvents:]...)
	}
	t.mu.Unlock()
	return ev
}

// Events returns the recent routing log, newest last.
func (t *Telemetry) Events() []RequestEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]RequestEvent(nil), t.events...)
}

// Snapshot is the full ML state served to the TUI.
func (t *Telemetry) Snapshot(weights Weights) StatusResponse {
	cpuPct, cpuOK := t.cpu.Last()
	gpus, gpuOK := t.gpu.Last()
	return StatusResponse{
		Time:    time.Now().Format(time.RFC3339),
		Weights: weights,
		Models:  t.pool.ModelStatus(),
		Workers: t.pool.Status(),
		Totals:  t.pool.Totals(),
		Recent:  t.Events(),
		Host: HostStatus{
			CPUPercent:   cpuPct,
			CPUAvailable: cpuOK,
			GPUAvailable: gpuOK,
			GPUs:         gpus,
		},
	}
}

func isLocalURL(u *url.URL) bool {
	if u == nil {
		return false
	}
	host := u.Hostname()
	if host == "" || host == "localhost" {
		return host == "localhost"
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
