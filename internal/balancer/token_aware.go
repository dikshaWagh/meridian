package balancer

import (
	"math"

	"github.com/dikshaWagh/meridian/internal/tokens"
)

// TokenAwarePolicy is Token-Weighted Least Expected Latency (TW-LEL). For each
// eligible worker it estimates when the new request would finish:
//
//	C_w = (F_w*(I+Obar) + I + O) / R_w
//
// F_w is the worker's outstanding requests, I and O the request's estimated
// input and output tokens, Obar the model's historical mean output length, and
// R_w the worker's measured tokens per second. The lowest C_w wins.
//
// This is a heuristic. Each queued request is assumed to be as large as the new
// one, and input and output tokens are assumed to cost the same. Continuous
// batching means throughput does not fall linearly with load.
type TokenAwarePolicy struct{}

func (p *TokenAwarePolicy) Name() string { return "token-aware" }
func (p *TokenAwarePolicy) Description() string {
	return "lowest estimated completion time from tok/s, in-flight load and request size"
}

// Pick is used when the request size is unknown. Every request is treated as
// having the default output length and no measured input.
func (p *TokenAwarePolicy) Pick(model string, healthy []*Worker) *Worker {
	return p.PickSized(model, healthy, RouteRequest{
		OutputTokens:    tokens.DefaultOutputTokens,
		AvgOutputTokens: tokens.DefaultOutputTokens,
	})
}

// PredictMs returns the TW-LEL estimate C_w in milliseconds for a worker, or
// -1 when the worker has no valid throughput. Callers use it for reporting.
func PredictMs(w *Worker, req RouteRequest) float64 {
	rate := w.GetTokPerSec()
	if !(rate > 0) || math.IsInf(rate, 0) {
		return -1
	}
	in := float64(req.InputTokens)
	out := float64(req.OutputTokens)
	f := float64(w.Load())
	return (f*(in+req.AvgOutputTokens) + in + out) / rate * 1000
}

func (p *TokenAwarePolicy) PickSized(model string, healthy []*Worker, req RouteRequest) *Worker {
	in := float64(req.InputTokens)
	out := float64(req.OutputTokens)
	avg := req.AvgOutputTokens

	var best *Worker
	bestScore := math.Inf(1)
	for _, w := range healthy {
		rate := w.GetTokPerSec()
		// Skip zero, negative, NaN and infinite throughput: no valid estimate.
		if !(rate > 0) || math.IsInf(rate, 0) {
			continue
		}
		f := float64(w.Load())
		score := (f*(in+avg) + in + out) / rate
		if score < bestScore {
			best, bestScore = w, score
		}
	}
	return best
}
