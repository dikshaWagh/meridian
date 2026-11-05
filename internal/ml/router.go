package ml

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Weights are the configurable scoring constants. Lower score wins.
type Weights struct {
	Latency float64 `yaml:"latency" json:"latency"`
	Queue   float64 `yaml:"queue" json:"queue"`
	GPU     float64 `yaml:"gpu" json:"gpu"`
	CPU     float64 `yaml:"cpu" json:"cpu"`
}

// DefaultWeights is the specified ML scoring profile:
// score = 0.40*latency + 0.25*queue + 0.20*gpu + 0.15*cpu
var DefaultWeights = Weights{Latency: 0.40, Queue: 0.25, GPU: 0.20, CPU: 0.15}

func (w Weights) Validate() error {
	for name, v := range map[string]float64{"latency": w.Latency, "queue": w.Queue, "gpu": w.GPU, "cpu": w.CPU} {
		if v < 0 {
			return fmt.Errorf("%s weight must be >= 0", name)
		}
	}
	if w.Latency+w.Queue+w.GPU+w.CPU <= 0 {
		return fmt.Errorf("weights must not all be zero")
	}
	return nil
}

// Score combines normalized metrics into the final routing score.
func (w Weights) Score(latency, queue, gpu, cpu float64) float64 {
	return w.Latency*latency + w.Queue*queue + w.GPU*gpu + w.CPU*cpu
}

// Router performs two-stage routing: model match, then best healthy instance.
type Router struct {
	pool    *Pool
	weights Weights
}

func NewRouter(pool *Pool, weights Weights) *Router {
	if weights.Validate() != nil {
		weights = DefaultWeights
	}
	return &Router{pool: pool, weights: weights}
}

func (r *Router) Weights() Weights { return r.weights }
func (r *Router) Pool() *Pool      { return r.pool }

// Route selects a worker for model. The decision is returned even on error so
// the TUI can show why nothing was selected.
func (r *Router) Route(model string) (*Worker, *RoutingDecision, error) {
	d := &RoutingDecision{
		Model:            model,
		CandidateWorkers: []string{},
		HealthyWorkers:   []string{},
		Candidates:       []CandidateScore{},
		Timestamp:        time.Now(),
	}

	// Stage 1: model routing. Workers of other models are never considered.
	candidates := r.pool.ForModel(model)
	if len(candidates) == 0 {
		d.Reason = "no worker configured for model " + model
		return nil, d, ErrModelNotFound
	}
	for _, w := range candidates {
		d.CandidateWorkers = append(d.CandidateWorkers, w.Spec.ID)
	}

	// Stage 2: worker routing. The health filter always runs before scoring.
	type sample struct {
		worker    *Worker
		latencyMS float64
		inFlight  int64
		tel       WorkerTelemetry
	}
	var healthy []sample
	unhealthy := map[string]CandidateScore{}
	for _, w := range candidates {
		if !w.IsHealthy() {
			unhealthy[w.Spec.ID] = CandidateScore{
				WorkerID: w.Spec.ID,
				State:    w.State().String(),
				Healthy:  false,
				Scored:   false,
				InFlight: w.InFlight(),
			}
			continue
		}
		d.HealthyWorkers = append(d.HealthyWorkers, w.Spec.ID)
		healthy = append(healthy, sample{
			worker:    w,
			latencyMS: w.Latency().AvgMS,
			inFlight:  w.InFlight(),
			tel:       w.Telemetry(),
		})
	}
	if len(healthy) == 0 {
		for _, w := range candidates {
			d.Candidates = append(d.Candidates, unhealthy[w.Spec.ID])
		}
		d.Reason = fmt.Sprintf("all %d worker(s) for %s are unavailable", len(candidates), model)
		return nil, d, ErrNoHealthyWorker
	}

	// Normalize against the current candidate set; guard zero denominators.
	maxLatency, maxInFlight := 0.0, int64(0)
	for _, s := range healthy {
		if s.latencyMS > maxLatency {
			maxLatency = s.latencyMS
		}
		if s.inFlight > maxInFlight {
			maxInFlight = s.inFlight
		}
	}

	scored := make([]CandidateScore, 0, len(healthy))
	for _, s := range healthy {
		c := CandidateScore{
			WorkerID:      s.worker.Spec.ID,
			State:         s.worker.State().String(),
			Healthy:       true,
			Scored:        true,
			LatencyMS:     s.latencyMS,
			InFlight:      s.inFlight,
			CPUPercent:    s.tel.CPUPercent,
			CPUAvailable:  s.tel.CPUAvailable,
			GPUPercent:    s.tel.GPUPercent,
			GPUMemPercent: s.tel.GPUMemPercent,
			GPUAvailable:  s.tel.GPUAvailable,
		}
		c.LatencyScore = ratio(s.latencyMS, maxLatency)
		c.QueueScore = ratio(float64(s.inFlight), float64(maxInFlight))
		if s.tel.CPUAvailable {
			c.CPUScore = clamp01(s.tel.CPUPercent / 100)
		}
		if s.tel.GPUAvailable {
			c.GPUScore = clamp01(s.tel.GPUPercent / 100)
		}
		c.FinalScore = r.weights.Score(c.LatencyScore, c.QueueScore, c.GPUScore, c.CPUScore)
		scored = append(scored, c)
	}

	// Lowest score wins; ties break on in-flight then worker id for determinism.
	best := 0
	for i := 1; i < len(scored); i++ {
		if lessCandidate(scored[i], scored[best]) {
			best = i
		}
	}
	selected := healthy[best].worker // scored is built in healthy order
	scored[best].Selected = true

	byID := map[string]CandidateScore{}
	for _, c := range scored {
		byID[c.WorkerID] = c
	}
	for id, c := range unhealthy {
		byID[id] = c
	}
	for _, w := range candidates {
		d.Candidates = append(d.Candidates, byID[w.Spec.ID])
	}

	b := scored[best]
	d.SelectedWorker = b.WorkerID
	d.LatencyScore, d.QueueScore, d.GPUScore, d.CPUScore, d.FinalScore = b.LatencyScore, b.QueueScore, b.GPUScore, b.CPUScore, b.FinalScore
	d.Reason = describe(b, len(d.HealthyWorkers), len(candidates))
	return selected, d, nil
}

func lessCandidate(a, b CandidateScore) bool {
	if a.FinalScore != b.FinalScore {
		return a.FinalScore < b.FinalScore
	}
	if a.InFlight != b.InFlight {
		return a.InFlight < b.InFlight
	}
	return a.WorkerID < b.WorkerID
}

func describe(c CandidateScore, healthy, total int) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("latency=%.3f", c.LatencyScore))
	parts = append(parts, fmt.Sprintf("queue=%.3f", c.QueueScore))
	if c.GPUAvailable {
		parts = append(parts, fmt.Sprintf("gpu=%.3f", c.GPUScore))
	} else {
		parts = append(parts, "gpu=n/a")
	}
	if c.CPUAvailable {
		parts = append(parts, fmt.Sprintf("cpu=%.3f", c.CPUScore))
	} else {
		parts = append(parts, "cpu=n/a")
	}
	return fmt.Sprintf("lowest score %.3f of %d healthy/%d candidates (%s)", c.FinalScore, healthy, total, strings.Join(parts, " "))
}

// ratio normalizes v against the candidate maximum, guarding zero denominators.
func ratio(v, max float64) float64 {
	if max <= 0 {
		return 0
	}
	return clamp01(v / max)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// SortedCandidates returns a decision's candidates ordered best score first,
// unscored workers last. Used by the TUI.
func SortedCandidates(d *RoutingDecision) []CandidateScore {
	out := append([]CandidateScore(nil), d.Candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scored != out[j].Scored {
			return out[i].Scored
		}
		if !out[i].Scored {
			return out[i].WorkerID < out[j].WorkerID
		}
		return lessCandidate(out[i], out[j])
	})
	return out
}
