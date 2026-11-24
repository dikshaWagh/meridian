package main

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// sample is one measured chat request.
type sample struct {
	Index            int       `json:"index"`
	Model            string    `json:"model"`
	Profile          string    `json:"profile"`
	Policy           string    `json:"policy"`
	Replica          string    `json:"replica,omitempty"`
	PolicyHeader     string    `json:"policy_header,omitempty"`
	Status           int       `json:"status"`
	OK               bool      `json:"ok"`
	Error            string    `json:"error,omitempty"`
	StartedAt        time.Time `json:"started_at"`
	TTFTms           float64   `json:"ttft_ms"`
	E2Ems            float64   `json:"e2e_ms"`
	TTFTKnown        bool      `json:"ttft_known"`
	Streamed         bool      `json:"streamed"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	Failover         string    `json:"failover,omitempty"`
	QueueWaitMs      float64   `json:"queue_wait_ms"` // time in leader before dispatch
	PredictedMs      float64   `json:"predicted_ms"`  // TW-LEL estimate, -1 when not applicable
}

type latencyStats struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Mean  float64 `json:"mean"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
}

func summarizeLatencies(values []float64) latencyStats {
	n := len(values)
	if n == 0 {
		return latencyStats{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	return latencyStats{
		Count: n,
		P50:   percentile(sorted, 50),
		P90:   percentile(sorted, 90),
		P95:   percentile(sorted, 95),
		P99:   percentile(sorted, 99),
		Mean:  sum / float64(n),
		Min:   sorted[0],
		Max:   sorted[n-1],
	}
}

// percentile uses linear interpolation between the two nearest ranks.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	rank := p / 100 * float64(n-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo < 0 {
		lo = 0
	}
	if hi >= n {
		hi = n - 1
	}
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

type modelSummary struct {
	Model            string         `json:"model"`
	Requests         int            `json:"requests"`
	Succeeded        int            `json:"succeeded"`
	Failed           int            `json:"failed"`
	TTFT             latencyStats   `json:"ttft_ms"`
	E2E              latencyStats   `json:"e2e_ms"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	Replicas         map[string]int `json:"replicas"`
}

type replicaSummary struct {
	Replica                 string  `json:"replica"`
	Node                    string  `json:"node"`
	Model                   string  `json:"model"`
	Requests                int     `json:"requests"`
	SharePct                float64 `json:"share_pct"`
	CompletionTokens        int     `json:"completion_tokens"`
	ObservedTokensPerSec    float64 `json:"observed_tok_per_sec"`
	ServerTokensPerSecStart float64 `json:"server_tok_per_sec_start"`
	ServerTokensPerSecEnd   float64 `json:"server_tok_per_sec_end"`
	ServedDelta             int64   `json:"served_delta"`
}

type runSummary struct {
	Policy                 string                   `json:"policy"`
	Concurrency            int                      `json:"concurrency"`
	Requests               int                      `json:"requests"`
	Succeeded              int                      `json:"succeeded"`
	Failed                 int                      `json:"failed"`
	StreamErrors           int                      `json:"stream_errors"`
	SuccessPct             float64                  `json:"success_pct"`
	FailureByStatus        map[int]int              `json:"failure_by_status"`
	PolicyMismatch         int                      `json:"policy_mismatch"`
	ObservedPolicies       map[string]int           `json:"observed_policies"`
	DurationSec            float64                  `json:"duration_sec"`
	RequestsPerSec         float64                  `json:"requests_per_sec"`
	CompletionTokensPerSec float64                  `json:"completion_tokens_per_sec"`
	TotalPromptTokens      int                      `json:"total_prompt_tokens"`
	TotalCompletionTokens  int                      `json:"total_completion_tokens"`
	TTFT                   latencyStats             `json:"ttft_ms"`
	E2E                    latencyStats             `json:"e2e_ms"`
	GenerationTokPerSec    latencyStats             `json:"generation_tok_per_sec"`
	PredictedLatency       latencyStats             `json:"predicted_ms"`
	PerModel               map[string]*modelSummary `json:"per_model"`
	PerProfile             map[string]latencyStats  `json:"per_profile"`
	PerReplica             []replicaSummary         `json:"per_replica"`
	PerNode                map[string]int           `json:"per_node"`
	NodeSharePct           map[string]float64       `json:"node_share_pct"`
	FairnessCV             float64                  `json:"fairness_cv"`
	BusiestReplicaPct      float64                  `json:"busiest_replica_pct"`
	StartedAt              time.Time                `json:"started_at"`
}

type replicaAgg struct {
	Count            int
	CompletionTokens int
	GenSec           float64
	Model            string
	Node             string
}

// computeSummary turns raw samples into the table the report uses.
func computeSummary(r *runResult, env envInfo) runSummary {
	s := runSummary{
		Policy:           r.Policy,
		Concurrency:      r.Concurrency,
		Requests:         len(r.Samples),
		FailureByStatus:  map[int]int{},
		ObservedPolicies: map[string]int{},
		PerModel:         map[string]*modelSummary{},
		PerProfile:       map[string]latencyStats{},
		PerNode:          map[string]int{},
		NodeSharePct:     map[string]float64{},
		StartedAt:        r.StartedAt,
	}
	if r.Duration > 0 {
		s.DurationSec = r.Duration.Seconds()
	}

	var ttfts, e2es, genRates, preds []float64
	perProfile := map[string][]float64{}
	replicaAggs := map[string]*replicaAgg{}
	modelReplicas := map[string]map[string]int{}

	for _, x := range r.Samples {
		if x.PolicyHeader != "" {
			s.ObservedPolicies[x.PolicyHeader]++
			if x.PolicyHeader != r.Policy {
				s.PolicyMismatch++
			}
		} else if x.Policy != "" {
			s.ObservedPolicies[x.Policy]++
		}

		replica := x.Replica
		if replica == "" {
			replica = "(unknown)"
		}
		if x.Replica != "" {
			s.PerNode[nodeOf(x.Replica, env)]++
			if modelReplicas[x.Model] == nil {
				modelReplicas[x.Model] = map[string]int{}
			}
			modelReplicas[x.Model][replica]++
			agg := replicaAggs[replica]
			if agg == nil {
				agg = &replicaAgg{Model: x.Model, Node: nodeOf(x.Replica, env)}
				replicaAggs[replica] = agg
			}
			agg.Count++
		}

		if !x.OK {
			s.Failed++
			s.FailureByStatus[x.Status]++
			if x.Status == 200 {
				s.StreamErrors++
			}
			continue
		}
		s.Succeeded++
		s.TotalPromptTokens += x.PromptTokens
		s.TotalCompletionTokens += x.CompletionTokens

		preds = append(preds, x.PredictedMs)
		e2es = append(e2es, x.E2Ems)
		if x.TTFTKnown {
			ttfts = append(ttfts, x.TTFTms)
		}
		perProfile[x.Profile] = append(perProfile[x.Profile], x.E2Ems)

		genSec := generationSeconds(x)
		if genSec > 0 && x.CompletionTokens > 0 {
			genRates = append(genRates, float64(x.CompletionTokens)/genSec)
		}
		if agg := replicaAggs[replica]; agg != nil && genSec > 0 && x.CompletionTokens > 0 {
			agg.CompletionTokens += x.CompletionTokens
			agg.GenSec += genSec
		}

		ms := s.PerModel[x.Model]
		if ms == nil {
			ms = &modelSummary{Model: x.Model, Replicas: map[string]int{}}
			s.PerModel[x.Model] = ms
		}
		ms.Requests++
		ms.Succeeded++
		ms.PromptTokens += x.PromptTokens
		ms.CompletionTokens += x.CompletionTokens
		ms.Replicas[replica]++
	}

	for model, ms := range s.PerModel {
		var modelTTFT, modelE2E []float64
		for _, x := range r.Samples {
			if x.Model != model || !x.OK {
				continue
			}
			modelE2E = append(modelE2E, x.E2Ems)
			if x.TTFTKnown {
				modelTTFT = append(modelTTFT, x.TTFTms)
			}
		}
		ms.TTFT = summarizeLatencies(modelTTFT)
		ms.E2E = summarizeLatencies(modelE2E)
		ms.Failed = ms.Requests - ms.Succeeded
	}

	for profile, vals := range perProfile {
		s.PerProfile[profile] = summarizeLatencies(vals)
	}
	s.TTFT = summarizeLatencies(ttfts)
	s.E2E = summarizeLatencies(e2es)
	s.GenerationTokPerSec = summarizeLatencies(genRates)
	s.PredictedLatency = summarizeLatencies(preds)

	if s.DurationSec > 0 {
		s.RequestsPerSec = float64(s.Succeeded) / s.DurationSec
		s.CompletionTokensPerSec = float64(s.TotalCompletionTokens) / s.DurationSec
	}
	if s.Requests > 0 {
		s.SuccessPct = float64(s.Succeeded) / float64(s.Requests) * 100
	}

	// Per-replica rows include healthy replicas that received zero requests, so
	// starvation is visible.
	totalReplicaRequests := 0
	for _, n := range replicaAggs {
		totalReplicaRequests += n.Count
	}
	seen := map[string]bool{}
	addReplica := func(id, node, model string, agg *replicaAgg) {
		if seen[id] {
			return
		}
		seen[id] = true
		row := replicaSummary{Replica: id, Node: node, Model: model}
		if agg != nil {
			row.Requests = agg.Count
			row.CompletionTokens = agg.CompletionTokens
			if agg.GenSec > 0 {
				row.ObservedTokensPerSec = float64(agg.CompletionTokens) / agg.GenSec
			}
		}
		if totalReplicaRequests > 0 {
			row.SharePct = float64(row.Requests) / float64(totalReplicaRequests) * 100
		}
		for _, snap := range r.Snapshots {
			for _, rep := range snap.Replicas {
				if rep.ID != id {
					continue
				}
				if row.ServerTokensPerSecStart == 0 {
					row.ServerTokensPerSecStart = rep.TokPerSec
				}
				row.ServerTokensPerSecEnd = rep.TokPerSec
			}
		}
		if len(r.Snapshots) >= 2 {
			startServed := servedOf(r.Snapshots[0], id)
			endServed := servedOf(r.Snapshots[len(r.Snapshots)-1], id)
			if startServed >= 0 && endServed >= 0 {
				row.ServedDelta = endServed - startServed
			}
		}
		s.PerReplica = append(s.PerReplica, row)
	}
	for _, rep := range env.Replicas {
		if !containsString(envModelsForRun(r.Samples), rep.Model) {
			continue
		}
		addReplica(rep.ID, rep.NodeID, rep.Model, replicaAggs[rep.ID])
	}
	for id, agg := range replicaAggs {
		addReplica(id, agg.Node, agg.Model, agg)
	}
	sort.Slice(s.PerReplica, func(i, j int) bool {
		if s.PerReplica[i].Model != s.PerReplica[j].Model {
			return s.PerReplica[i].Model < s.PerReplica[j].Model
		}
		return s.PerReplica[i].Replica < s.PerReplica[j].Replica
	})

	for node, n := range s.PerNode {
		if totalReplicaRequests > 0 {
			s.NodeSharePct[node] = float64(n) / float64(totalReplicaRequests) * 100
		}
	}
	// Fairness: coefficient of variation of request counts across healthy
	// replicas of each model, averaged over models with more than one replica.
	// Busiest is the worst within-model replica share, so a model mix does not
	// make a single busy model look like a load-balancing failure.
	weightedCV, weight := 0.0, 0
	for model, counts := range modelReplicas {
		reps := healthyReplicasForModel(env, model)
		if len(reps) < 2 {
			continue
		}
		total, max := 0, 0
		for _, r := range reps {
			n := counts[r.ID]
			total += n
			if n > max {
				max = n
			}
		}
		if total == 0 {
			continue
		}
		mean := float64(total) / float64(len(reps))
		variance := 0.0
		for _, r := range reps {
			d := float64(counts[r.ID]) - mean
			variance += d * d
		}
		cv := math.Sqrt(variance/float64(len(reps))) / mean
		weightedCV += cv * float64(total)
		weight += total
		if share := float64(max) / float64(total) * 100; share > s.BusiestReplicaPct {
			s.BusiestReplicaPct = share
		}
	}
	if weight > 0 {
		s.FairnessCV = weightedCV / float64(weight)
	}
	return s
}

func generationSeconds(x sample) float64 {
	if x.TTFTKnown && x.E2Ems > x.TTFTms {
		return (x.E2Ems - x.TTFTms) / 1000
	}
	return x.E2Ems / 1000
}

func nodeOf(replica string, env envInfo) string {
	for _, r := range env.Replicas {
		if r.ID == replica {
			return r.NodeID
		}
	}
	return "(unknown)"
}

func servedOf(snap snapshot, replica string) int64 {
	for _, r := range snap.Replicas {
		if r.ID == replica {
			return r.Served
		}
	}
	return -1
}

func healthyReplicasForModel(env envInfo, model string) []replicaInfo {
	var out []replicaInfo
	for _, r := range env.Replicas {
		if r.Model == model && r.Healthy {
			out = append(out, r)
		}
	}
	return out
}

func envModelsForRun(samples []sample) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range samples {
		if !seen[s.Model] {
			seen[s.Model] = true
			out = append(out, s.Model)
		}
	}
	return out
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// ---- CSV ------------------------------------------------------------------

func writeCSV(path string, header []string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return w.Error()
}

func writeSummaryCSV(path string, runs []runSummary) error {
	header := []string{
		"policy", "concurrency", "requests", "succeeded", "failed", "stream_errors", "success_pct",
		"duration_s", "req_per_s", "completion_tok_per_s", "total_prompt_tokens", "total_completion_tokens",
		"ttft_p50_ms", "ttft_p95_ms", "ttft_p99_ms",
		"e2e_p50_ms", "e2e_p90_ms", "e2e_p95_ms", "e2e_p99_ms", "e2e_mean_ms",
		"gen_tok_per_s_p50", "gen_tok_per_s_p95",
		"fairness_cv", "busiest_replica_pct", "policy_mismatch", "node_share_pct",
	}
	var rows [][]string
	for _, s := range runs {
		rows = append(rows, []string{
			s.Policy, itoa(s.Concurrency), itoa(s.Requests), itoa(s.Succeeded), itoa(s.Failed), itoa(s.StreamErrors), ftoa(s.SuccessPct, 2),
			ftoa(s.DurationSec, 3), ftoa(s.RequestsPerSec, 3), ftoa(s.CompletionTokensPerSec, 1), itoa(s.TotalPromptTokens), itoa(s.TotalCompletionTokens),
			ftoa(s.TTFT.P50, 1), ftoa(s.TTFT.P95, 1), ftoa(s.TTFT.P99, 1),
			ftoa(s.E2E.P50, 1), ftoa(s.E2E.P90, 1), ftoa(s.E2E.P95, 1), ftoa(s.E2E.P99, 1), ftoa(s.E2E.Mean, 1),
			ftoa(s.GenerationTokPerSec.P50, 2), ftoa(s.GenerationTokPerSec.P95, 2),
			ftoa(s.FairnessCV, 3), ftoa(s.BusiestReplicaPct, 1), itoa(s.PolicyMismatch), formatNodeShare(s.NodeSharePct),
		})
	}
	return writeCSV(path, header, rows)
}

func writeRequestsCSV(path string, runs []runResult, env envInfo) error {
	header := []string{
		"run_id", "policy", "concurrency", "index", "model", "profile", "replica", "node",
		"status", "ok", "ttft_ms", "e2e_ms", "ttft_known", "streamed", "prompt_tokens", "completion_tokens", "error",
	}
	var rows [][]string
	for _, r := range runs {
		for _, x := range r.Samples {
			rows = append(rows, []string{
				itoa(r.ID), r.Policy, itoa(r.Concurrency), itoa(x.Index), x.Model, x.Profile, x.Replica, nodeOf(x.Replica, env),
				itoa(x.Status), boolStr(x.OK), ftoa(x.TTFTms, 2), ftoa(x.E2Ems, 2), boolStr(x.TTFTKnown), boolStr(x.Streamed),
				itoa(x.PromptTokens), itoa(x.CompletionTokens), x.Error,
			})
		}
	}
	return writeCSV(path, header, rows)
}

func writeTimeseriesCSV(path string, runs []runResult) error {
	header := []string{"run_id", "policy", "concurrency", "elapsed_s", "kind", "id", "node", "model", "cpu_pct", "ram_used_mb", "in_flight", "tok_per_sec", "served", "error"}
	var rows [][]string
	for _, r := range runs {
		for _, snap := range r.Snapshots {
			elapsed := ftoa(float64(snap.ElapsedMS)/1000, 3)
			if snap.Error != "" {
				rows = append(rows, []string{itoa(r.ID), r.Policy, itoa(r.Concurrency), elapsed, "snapshot", "", "", "", "", "", "", "", "", snap.Error})
			}
			for _, n := range snap.Nodes {
				rows = append(rows, []string{
					itoa(r.ID), r.Policy, itoa(r.Concurrency), elapsed, "node", n.NodeID, n.NodeID, "", ftoa(n.CPUPercent, 2), i64toa(n.RAMUsedMB), "", "", "", "",
				})
			}
			for _, rep := range snap.Replicas {
				rows = append(rows, []string{
					itoa(r.ID), r.Policy, itoa(r.Concurrency), elapsed, "replica", rep.ID, rep.NodeID, rep.Model, "", "", i64toa(rep.InFlight), ftoa(rep.TokPerSec, 3), i64toa(rep.Served), "",
				})
			}
		}
	}
	return writeCSV(path, header, rows)
}

func writeReplicasCSV(path string, runs []runSummary) error {
	header := []string{
		"policy", "concurrency", "replica", "node", "model", "requests", "share_pct",
		"completion_tokens", "observed_tok_per_sec", "server_tok_per_sec_start", "server_tok_per_sec_end", "served_delta",
	}
	var rows [][]string
	for _, s := range runs {
		for _, r := range s.PerReplica {
			rows = append(rows, []string{
				s.Policy, itoa(s.Concurrency), r.Replica, r.Node, r.Model, itoa(r.Requests), ftoa(r.SharePct, 2),
				itoa(r.CompletionTokens), ftoa(r.ObservedTokensPerSec, 3), ftoa(r.ServerTokensPerSecStart, 3), ftoa(r.ServerTokensPerSecEnd, 3), i64toa(r.ServedDelta),
			})
		}
	}
	return writeCSV(path, header, rows)
}

func formatNodeShare(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%.1f%%", k, m[k]))
	}
	return strings.Join(parts, ";")
}

func itoa(n int) string     { return strconv.Itoa(n) }
func i64toa(n int64) string { return strconv.FormatInt(n, 10) }
func ftoa(v float64, digits int) string {
	return strconv.FormatFloat(v, 'f', digits, 64)
}
func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
