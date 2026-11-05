package ml

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// gpuSampler reads GPU utilisation through the NVIDIA management CLI.
//
// It is entirely optional and adds no build dependency: when nvidia-smi is not
// present (CPU-only hosts, non-NVIDIA GPUs) the sampler reports unavailable and
// gpu_score is 0, flagged "n/a". CPU-only operation is never broken.
type gpuSampler struct {
	mu        sync.Mutex
	bin       string
	looked    bool
	available bool
	gpus      []GPUStats
	lastRun   time.Time
	minPeriod time.Duration
}

func newGPUSampler() *gpuSampler {
	return &gpuSampler{minPeriod: 2 * time.Second}
}

// Available reports whether any GPU telemetry source was found.
func (g *gpuSampler) Available() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.available
}

// Last returns the most recent GPU readings without resampling.
func (g *gpuSampler) Last() ([]GPUStats, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]GPUStats(nil), g.gpus...), g.available
}

// Sample refreshes GPU readings, rate limited to minPeriod.
func (g *gpuSampler) Sample(ctx context.Context) ([]GPUStats, bool) {
	g.mu.Lock()
	if !g.looked {
		g.looked = true
		if path, err := exec.LookPath("nvidia-smi"); err == nil {
			g.bin = path
		}
	}
	bin := g.bin
	if bin == "" {
		g.available = false
		g.mu.Unlock()
		return nil, false
	}
	if time.Since(g.lastRun) < g.minPeriod {
		gpus, ok := append([]GPUStats(nil), g.gpus...), g.available
		g.mu.Unlock()
		return gpus, ok
	}
	g.lastRun = time.Now()
	g.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin,
		"--query-gpu=index,name,utilization.gpu,memory.used,memory.total",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()

	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil {
		g.available = false
		return nil, false
	}
	gpus := parseNvidiaSMI(string(out))
	if len(gpus) == 0 {
		g.available = false
		return nil, false
	}
	g.gpus, g.available = gpus, true
	return append([]GPUStats(nil), gpus...), true
}

func parseNvidiaSMI(out string) []GPUStats {
	var gpus []GPUStats
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) < 5 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		util, err := strconv.ParseFloat(f[2], 64)
		if err != nil {
			continue
		}
		memPct := 0.0
		used, uErr := strconv.ParseFloat(f[3], 64)
		total, tErr := strconv.ParseFloat(f[4], 64)
		if uErr == nil && tErr == nil && total > 0 {
			memPct = clampPercent(used / total * 100)
		}
		gpus = append(gpus, GPUStats{Index: idx, Name: f[1], UtilPercent: clampPercent(util), MemPercent: memPct})
	}
	return gpus
}
