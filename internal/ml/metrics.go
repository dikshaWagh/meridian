package ml

import (
	"sort"
	"sync"
	"time"
)

// latencyWindowSize is the number of recent request latencies kept per worker.
const latencyWindowSize = 256

// latencyWindow is a fixed-size ring of measured request latencies. Every
// sample comes from time.Since around a real proxy call; nothing is synthesised.
type latencyWindow struct {
	samples []time.Duration
	next    int
	filled  bool
	last    time.Duration
}

func newLatencyWindow(size int) latencyWindow {
	return latencyWindow{samples: make([]time.Duration, size)}
}

func (l *latencyWindow) add(d time.Duration) {
	if len(l.samples) == 0 {
		l.samples = make([]time.Duration, latencyWindowSize)
	}
	l.samples[l.next] = d
	l.next = (l.next + 1) % len(l.samples)
	if l.next == 0 {
		l.filled = true
	}
	l.last = d
}

func (l *latencyWindow) view() []time.Duration {
	n := l.next
	if l.filled {
		n = len(l.samples)
	}
	out := make([]time.Duration, n)
	copy(out, l.samples[:n])
	return out
}

func (l *latencyWindow) stats() LatencyStats {
	v := l.view()
	if len(v) == 0 {
		return LatencyStats{}
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	var total time.Duration
	for _, d := range v {
		total += d
	}
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	return LatencyStats{
		Samples: len(v),
		AvgMS:   ms(total) / float64(len(v)),
		P50MS:   ms(percentile(v, 0.50)),
		P95MS:   ms(percentile(v, 0.95)),
		P99MS:   ms(percentile(v, 0.99)),
		LastMS:  ms(l.last),
	}
}

// percentile expects sorted input.
func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(q * float64(len(sorted)-1))
	if i < 0 {
		i = 0
	}
	if i >= len(sorted) {
		i = len(sorted) - 1
	}
	return sorted[i]
}

// cpuSampler turns host CPU counters into a utilisation percentage. The first
// sample only establishes a baseline and reports nothing, because a single
// reading cannot measure utilisation.
type cpuSampler struct {
	mu        sync.Mutex
	lastIdle  uint64
	lastTotal uint64
	primed    bool
	percent   float64
	available bool
}

// Sample returns host CPU utilisation in percent and whether it is measurable.
func (c *cpuSampler) Sample() (float64, bool) {
	idle, total, ok := readCPUTimes()
	if !ok {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.available = false
		return 0, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.primed {
		c.lastIdle, c.lastTotal, c.primed = idle, total, true
		return 0, false
	}
	dIdle := float64(idle - c.lastIdle)
	dTotal := float64(total - c.lastTotal)
	c.lastIdle, c.lastTotal = idle, total
	if dTotal <= 0 {
		return c.percent, c.available
	}
	pct := (1 - dIdle/dTotal) * 100
	c.percent = clampPercent(pct)
	c.available = true
	return c.percent, true
}

// Last returns the most recent measurement without resampling.
func (c *cpuSampler) Last() (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.percent, c.available
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
