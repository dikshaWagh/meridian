package main

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type reportData struct {
	GeneratedAt time.Time
	Addr        string
	WorkerCheck *workerCheck
	Config      benchConfig
	Env         envInfo
	Warnings    []string
	Runs        []runSummary
	RunResults  []runResult
}

// ---- warnings and observations -------------------------------------------

func buildWarnings(env envInfo, models, policies []string, levels []int, cfg *benchConfig, wc *workerCheck, switchMode string) []string {
	var out []string

	// Does any model have replicas on more than one node?
	span := map[string]map[string]bool{}
	healthyByModel := map[string][]replicaInfo{}
	for _, r := range env.Replicas {
		if !r.Healthy {
			continue
		}
		if span[r.Model] == nil {
			span[r.Model] = map[string]bool{}
		}
		span[r.Model][r.NodeID] = true
		healthyByModel[r.Model] = append(healthyByModel[r.Model], r)
	}
	shared := 0
	var single []string
	for _, m := range models {
		if len(span[m]) > 1 {
			shared++
		}
		if len(healthyByModel[m]) <= 1 {
			single = append(single, m)
		}
	}
	if shared == 0 && env.Mode == "cluster" {
		out = append(out, "No tested model has replicas on more than one node. Routing policies can only choose among replicas of the same model, so this run does NOT measure cross-node routing. Add a shared model to both nodes (same .gguf filename) to exercise node-to-node balancing.")
	}
	if len(single) > 0 {
		out = append(out, fmt.Sprintf("%d of %d tested model(s) have a single healthy replica (%s). Requests to those models cannot be routed differently by any policy.", len(single), len(models), strings.Join(single, ", ")))
	}

	// Capacity: the gateway returns 503 when every replica for a model is full;
	// it does not queue. Warn when the per-model share of concurrency exceeds
	// the model's total parallel slots.
	for _, conc := range levels {
		for _, m := range models {
			reps := healthyByModel[m]
			if len(reps) == 0 {
				continue
			}
			slots := 0
			for _, r := range reps {
				s := r.MaxConcurrent
				if s <= 0 {
					s = 1
				}
				slots += s
			}
			perModel := int(math.Ceil(float64(conc) / float64(len(models))))
			if perModel > slots {
				out = append(out, fmt.Sprintf("At concurrency %d, model %q may see about %d simultaneous requests but has %d slot(s); expect 503 no_healthy_worker errors. Lower --concurrency or raise --parallel.", conc, m, perModel, slots))
				break
			}
		}
	}

	if len(policies) > 1 && switchMode == "none" {
		out = append(out, "Only the leader's current default policy was measured. Pass --admin-token (or set MERIDIAN_ADMIN_TOKEN) to compare policies.")
	}

	// A flat server-side tok/s signal makes token-aware behave like
	// least-connections. Report the raw values so the report is honest.
	if containsString(policies, "token-aware") {
		var rates []float64
		for _, m := range models {
			for _, r := range healthyByModel[m] {
				rates = append(rates, r.TokPerSec)
			}
		}
		if len(rates) >= 2 {
			lo, hi := minMax(rates)
			if hi > 0 && lo > 0 && hi/lo < 1.05 {
				out = append(out, fmt.Sprintf("The leader's routing signal tok_per_sec is flat (min %.2f, max %.2f). token-aware therefore falls back toward least-connections; see the per-replica comparison after the run.", lo, hi))
			}
		}
	}
	if wc != nil && !wc.Reached {
		out = append(out, fmt.Sprintf("Worker %s did not answer /healthz (%s). The leader may still route to it; check firewall and --advertise-ip.", wc.URL, wc.Error))
	}
	if cfg.Requests < 10 {
		out = append(out, fmt.Sprintf("Only %d requests per run: p95/p99 are noisy. Use --requests 30 or more for a stable comparison.", cfg.Requests))
	}
	return out
}

func dynamicWarnings(summaries []runSummary, runs []runResult, env envInfo) []string {
	var out []string
	mismatch, failed, streamErr := 0, 0, 0
	for _, s := range summaries {
		mismatch += s.PolicyMismatch
		failed += s.Failed
		streamErr += s.StreamErrors
	}
	if mismatch > 0 {
		out = append(out, fmt.Sprintf("%d response(s) reported a routing policy different from the one requested. The admin policy switch may not have taken effect; re-run with a valid --admin-token.", mismatch))
	}
	if failed > 0 {
		reason := ""
		if streamErr > 0 {
			reason = fmt.Sprintf(" (%d HTTP 200 replies ended with a stream error)", streamErr)
		}
		out = append(out, fmt.Sprintf("%d request(s) failed%s. Check failure_by_status in summary.json before comparing latency percentiles.", failed, reason))
	}

	hasTokenAware := false
	for _, s := range summaries {
		if s.Policy == "token-aware" {
			hasTokenAware = true
			break
		}
	}
	if hasTokenAware {
		var server, observed []float64
		tokSeen := false
		for _, s := range summaries {
			if s.Policy != "token-aware" {
				continue
			}
			for _, r := range s.PerReplica {
				if r.ServerTokensPerSecEnd > 0 {
					server = append(server, r.ServerTokensPerSecEnd)
				}
				if r.ObservedTokensPerSec > 0 {
					observed = append(observed, r.ObservedTokensPerSec)
					tokSeen = true
				}
			}
		}
		if tokSeen && len(server) >= 2 {
			sl, sh := minMax(server)
			ol, oh := minMax(observed)
			if sh > 0 && sl > 0 && sh/sl < 1.1 && oh > 0 && ol > 0 && oh/ol > 1.25 {
				out = append(out, fmt.Sprintf("token-aware had a flat server-side speed signal (tok_per_sec %.1f..%.1f) while client-observed generation rates differed (%.1f..%.1f tok/s). In cluster mode the gateway does not feed measured tok/s back into the registry, so token-aware currently behaves like least-connections. This is a server limitation, not a benchmark error; the client-observed column in replicas.csv shows the real speed gap.", sl, sh, ol, oh))
			}
		}
	}

	for _, s := range summaries {
		if (s.Policy == "round-robin" || s.Policy == "least-connections") && s.Concurrency >= 2 && s.FairnessCV > 0.35 {
			out = append(out, fmt.Sprintf("%s at concurrency %d was skewed (fairness CV %.2f, busiest replica %.0f%%). Concurrent picks can herd before the reservation is taken; this is a real scheduling effect worth explaining.", s.Policy, s.Concurrency, s.FairnessCV, s.BusiestReplicaPct))
		}
	}
	_ = env
	return out
}

func observations(data reportData) []string {
	var out []string
	if len(data.Runs) == 0 {
		return []string{"No runs completed."}
	}
	byConc := map[int][]runSummary{}
	for _, s := range data.Runs {
		byConc[s.Concurrency] = append(byConc[s.Concurrency], s)
	}
	var concs []int
	for c := range byConc {
		concs = append(concs, c)
	}
	sort.Ints(concs)
	for _, c := range concs {
		best := byConc[c][0]
		throughput := byConc[c][0]
		for _, s := range byConc[c] {
			if s.Succeeded == 0 {
				continue
			}
			if best.Succeeded == 0 || s.E2E.P95 < best.E2E.P95 {
				best = s
			}
			if s.RequestsPerSec > throughput.RequestsPerSec {
				throughput = s
			}
		}
		if best.Succeeded > 0 {
			out = append(out, fmt.Sprintf("Concurrency %d: lowest p95 end-to-end latency was %s (%.0f ms, p50 %.0f ms); highest goodput was %s (%.2f req/s, %.0f completion tok/s).",
				c, best.Policy, best.E2E.P95, best.E2E.P50, throughput.Policy, throughput.RequestsPerSec, throughput.CompletionTokensPerSec))
		}
	}

	// Direct token-aware vs least-connections comparison at the highest level.
	if len(concs) > 0 {
		high := concs[len(concs)-1]
		var ta, lc *runSummary
		for i := range byConc[high] {
			switch byConc[high][i].Policy {
			case "token-aware":
				ta = &byConc[high][i]
			case "least-connections":
				lc = &byConc[high][i]
			}
		}
		if ta != nil && lc != nil && ta.Succeeded > 0 && lc.Succeeded > 0 && lc.E2E.P95 > 0 {
			diff := math.Abs(ta.E2E.P95-lc.E2E.P95) / lc.E2E.P95 * 100
			if diff < 5 {
				out = append(out, fmt.Sprintf("At concurrency %d, token-aware and least-connections were within %.1f%% on p95 (%.0f vs %.0f ms). That is the expected result when the tok/s signal is flat; it is not evidence that token-aware is a bad idea.", high, diff, ta.E2E.P95, lc.E2E.P95))
			} else if ta.E2E.P95 < lc.E2E.P95 {
				out = append(out, fmt.Sprintf("At concurrency %d, token-aware beat least-connections on p95 by %.1f%% (%.0f vs %.0f ms).", high, diff, ta.E2E.P95, lc.E2E.P95))
			} else {
				out = append(out, fmt.Sprintf("At concurrency %d, least-connections beat token-aware on p95 by %.1f%% (%.0f vs %.0f ms).", high, diff, lc.E2E.P95, ta.E2E.P95))
			}
		}
	}

	total, ok := 0, 0
	for _, s := range data.Runs {
		total += s.Requests
		ok += s.Succeeded
	}
	if total > 0 {
		out = append(out, fmt.Sprintf("Overall: %d/%d requests succeeded (%.1f%%).", ok, total, float64(ok)/float64(total)*100))
	}
	ttftSeen := false
	for _, s := range data.Runs {
		if s.TTFT.Count > 0 {
			ttftSeen = true
			break
		}
	}
	if !ttftSeen {
		out = append(out, "No time-to-first-token data: run with --stream=true (the default) and a llama.cpp build that emits SSE.")
	}
	return out
}

func minMax(v []float64) (float64, float64) {
	if len(v) == 0 {
		return 0, 0
	}
	lo, hi := v[0], v[0]
	for _, x := range v[1:] {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	return lo, hi
}

// ---- printing -------------------------------------------------------------

func printEnvironment(cfg *benchConfig, env envInfo, wc *workerCheck, warnings []string) {
	if cfg.Label != "" {
		fmt.Printf("label:          %s\n", cfg.Label)
	}
	fmt.Printf("leader:         %s\n", cfg.Addr)
	fmt.Printf("mode:           %s\n", env.Mode)
	fmt.Printf("leader id:      %s\n", env.LeaderID)
	if wc != nil {
		if wc.Reached {
			fmt.Printf("worker:         %s reachable\n", wc.URL)
		} else {
			fmt.Printf("worker:         %s NOT reachable (%s)\n", wc.URL, wc.Error)
		}
	}
	fmt.Printf("models:         %s\n", strings.Join(env.Models, ", "))
	var names []string
	for _, p := range env.Policies {
		names = append(names, p.Name)
	}
	fmt.Printf("policies:       %s\n", strings.Join(names, ", "))
	fmt.Printf("default policy: %s\n", env.DefaultPolicy)
	fmt.Println("nodes:")
	for _, n := range env.Nodes {
		where := "remote"
		if n.Local {
			where = "local"
		}
		ram := ""
		if n.RAMTotalMB > 0 {
			ram = fmt.Sprintf("  ram=%d/%d MB", n.RAMUsedMB, n.RAMTotalMB)
		}
		cpu := "n/a"
		if env.Mode == "cluster" {
			cpu = fmt.Sprintf("%.0f%%", n.CPUPercent)
		}
		fmt.Printf("  %-20s %-6s %-8s cpu=%s%s\n", n.NodeID, where, n.State, cpu, ram)
	}
	fmt.Println("replicas:")
	for _, r := range env.Replicas {
		state := "down"
		if r.Healthy {
			state = "healthy"
		}
		slots := "n/a"
		if r.MaxConcurrent > 0 {
			slots = fmt.Sprintf("%d", r.MaxConcurrent)
		}
		fmt.Printf("  %-42s %-10s %-32s %-7s tok/s=%.2f slots=%s\n",
			r.ID, r.NodeID, r.Model, state, r.TokPerSec, slots)
	}
	if len(warnings) > 0 {
		fmt.Println("warnings:")
		for _, w := range warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
}

func printRunSummary(s runSummary) {
	fmt.Printf("  result: ok=%d/%d (%.1f%%)  ttft p50/p95=%.0f/%.0f ms  e2e p50/p95/p99=%.0f/%.0f/%.0f ms  req/s=%.2f  tok/s=%.0f  fairness=%.2f  busiest=%.0f%%\n",
		s.Succeeded, s.Requests, s.SuccessPct,
		s.TTFT.P50, s.TTFT.P95,
		s.E2E.P50, s.E2E.P95, s.E2E.P99,
		s.RequestsPerSec, s.CompletionTokensPerSec, s.FairnessCV, s.BusiestReplicaPct)
}

func printFinalTable(runs []runSummary) {
	fmt.Println()
	fmt.Println("Comparison (successful requests):")
	fmt.Printf("%-20s %5s %7s %10s %10s %10s %10s %8s %8s %6s %8s\n",
		"policy", "conc", "ok%", "ttft p50", "e2e p50", "e2e p95", "e2e p99", "req/s", "tok/s", "fair", "busiest")
	for _, s := range runs {
		fmt.Printf("%-20s %5d %7.1f %10.0f %10.0f %10.0f %10.0f %8.2f %8.0f %6.2f %7.0f%%\n",
			s.Policy, s.Concurrency, s.SuccessPct,
			s.TTFT.P50, s.E2E.P50, s.E2E.P95, s.E2E.P99,
			s.RequestsPerSec, s.CompletionTokensPerSec, s.FairnessCV, s.BusiestReplicaPct)
	}
}

// ---- file output ----------------------------------------------------------

type configView struct {
	Addr           string  `json:"addr"`
	WorkerAddr     string  `json:"worker_addr,omitempty"`
	Models         string  `json:"models,omitempty"`
	Policies       string  `json:"policies,omitempty"`
	Concurrency    string  `json:"concurrency"`
	Requests       int     `json:"requests"`
	Warmup         int     `json:"warmup"`
	MaxTokens      int     `json:"max_tokens,omitempty"`
	Profile        string  `json:"profile"`
	Stream         bool    `json:"stream"`
	StreamUsage    bool    `json:"stream_usage"`
	CooldownSec    float64 `json:"cooldown_sec"`
	TimeoutSec     float64 `json:"timeout_sec"`
	StatusInterval string  `json:"status_interval"`
	Seed           int64   `json:"seed"`
	Label          string  `json:"label,omitempty"`
}

func configOf(cfg benchConfig) configView {
	return configView{
		Addr: cfg.Addr, WorkerAddr: cfg.WorkerAddr,
		Models: strings.Join(cfg.Models, ","), Policies: strings.Join(cfg.Policies, ","),
		Concurrency: intsToString(cfg.Concurrencies),
		Requests:    cfg.Requests, Warmup: cfg.Warmup, MaxTokens: cfg.MaxTokens,
		Profile: cfg.Profile, Stream: cfg.Stream, StreamUsage: cfg.StreamUsage,
		CooldownSec: cfg.Cooldown.Seconds(), TimeoutSec: cfg.Timeout.Seconds(),
		StatusInterval: cfg.StatusInterval.String(), Seed: cfg.Seed, Label: cfg.Label,
	}
}

func intsToString(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ",")
}

func writeReport(dir string, data reportData) error {
	obs := observations(data)
	cfg := configOf(data.Config)

	type reportJSON struct {
		GeneratedAt  time.Time    `json:"generated_at"`
		Version      string       `json:"version"`
		Addr         string       `json:"addr"`
		Mode         string       `json:"mode"`
		WorkerCheck  *workerCheck `json:"worker_check,omitempty"`
		Config       configView   `json:"config"`
		Environment  envInfo      `json:"environment"`
		Warnings     []string     `json:"warnings"`
		Observations []string     `json:"observations"`
		Runs         []runSummary `json:"runs"`
	}
	payload := reportJSON{
		GeneratedAt: data.GeneratedAt.UTC(), Version: version, Addr: data.Addr,
		Mode: data.Env.Mode, WorkerCheck: data.WorkerCheck, Config: cfg,
		Environment: data.Env, Warnings: data.Warnings, Observations: obs, Runs: data.Runs,
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), raw, 0o644); err != nil {
		return err
	}
	if err := writeSummaryCSV(filepath.Join(dir, "summary.csv"), data.Runs); err != nil {
		return err
	}
	if err := writeRequestsCSV(filepath.Join(dir, "requests.csv"), data.RunResults, data.Env); err != nil {
		return err
	}
	if err := writeTimeseriesCSV(filepath.Join(dir, "timeseries.csv"), data.RunResults); err != nil {
		return err
	}
	if err := writeReplicasCSV(filepath.Join(dir, "replicas.csv"), data.Runs); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte(markdownReport(data, obs)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.html"), []byte(htmlReport(data, obs)), 0o644); err != nil {
		return err
	}
	return nil
}

// ---- markdown -------------------------------------------------------------

func markdownReport(data reportData, obs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Meridian routing benchmark\n\n")
	fmt.Fprintf(&b, "Generated %s", data.GeneratedAt.Format(time.RFC3339))
	if data.Config.Label != "" {
		fmt.Fprintf(&b, " · %s", data.Config.Label)
	}
	fmt.Fprintf(&b, "\n\n")
	fmt.Fprintf(&b, "- Leader: `%s`\n- Mode: `%s`\n- Models: `%s`\n- Policies: `%s`\n- Concurrency: `%s`\n- Requests per run: %d\n- Workload profile: `%s`\n- Streaming: %v\n\n",
		data.Addr, data.Env.Mode, strings.Join(data.Config.Models, ", "), strings.Join(data.Config.Policies, ", "),
		intsToString(data.Config.Concurrencies), data.Config.Requests, data.Config.Profile, data.Config.Stream)

	fmt.Fprintf(&b, "## What this run says\n\n")
	for _, o := range obs {
		fmt.Fprintf(&b, "- %s\n", o)
	}
	b.WriteString("\n")

	if len(data.Warnings) > 0 {
		fmt.Fprintf(&b, "## Warnings\n\n")
		for _, w := range data.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Results\n\n")
	b.WriteString("| policy | conc | ok% | ttft p50 | ttft p95 | e2e p50 | e2e p95 | e2e p99 | req/s | tok/s | fairness CV | busiest |\n")
	b.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, s := range data.Runs {
		fmt.Fprintf(&b, "| %s | %d | %.1f | %.0f | %.0f | %.0f | %.0f | %.0f | %.2f | %.0f | %.2f | %.0f%% |\n",
			s.Policy, s.Concurrency, s.SuccessPct, s.TTFT.P50, s.TTFT.P95,
			s.E2E.P50, s.E2E.P95, s.E2E.P99, s.RequestsPerSec, s.CompletionTokensPerSec,
			s.FairnessCV, s.BusiestReplicaPct)
	}
	b.WriteString("\n")

	if len(data.Env.Models) > 1 {
		fmt.Fprintf(&b, "## Per-model p95 end-to-end latency (ms)\n\n")
		b.WriteString("| run |")
		for _, m := range data.Env.Models {
			fmt.Fprintf(&b, " %s |", m)
		}
		b.WriteString("\n|---|")
		for range data.Env.Models {
			b.WriteString("---:|")
		}
		b.WriteString("\n")
		for _, s := range data.Runs {
			fmt.Fprintf(&b, "| %s @ %d |", s.Policy, s.Concurrency)
			for _, m := range data.Env.Models {
				if ms, ok := s.PerModel[m]; ok {
					fmt.Fprintf(&b, " %.0f |", ms.E2E.P95)
				} else {
					b.WriteString(" - |")
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "## Per-replica behaviour\n\n")
	b.WriteString("| policy | conc | replica | node | model | reqs | share | observed tok/s | server tok/s start→end | served Δ |\n")
	b.WriteString("|---|---:|---|---|---|---:|---:|---:|---|---:|\n")
	for _, s := range data.Runs {
		for _, r := range s.PerReplica {
			fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %d | %.1f%% | %.1f | %.1f → %.1f | %d |\n",
				s.Policy, s.Concurrency, r.Replica, r.Node, r.Model, r.Requests, r.SharePct,
				r.ObservedTokensPerSec, r.ServerTokensPerSecStart, r.ServerTokensPerSecEnd, r.ServedDelta)
		}
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "## How to read the numbers\n\n")
	b.WriteString("- **e2e**: wall-clock time from sending the request until the last response byte. This is what a user feels.\n")
	b.WriteString("- **ttft**: time to the first content token on a streamed reply. Prefill and queueing dominate it. `n/a` when `--stream=false`.\n")
	b.WriteString("- **p50/p95/p99**: percentiles over successful requests. p95 is the useful planning number; p99 shows tail pain.\n")
	b.WriteString("- **req/s** and **tok/s**: successful requests and completion tokens divided by the measured wall-clock run duration (goodput, not raw load).\n")
	b.WriteString("- **fairness CV**: coefficient of variation of request counts across healthy replicas of the same model. 0 = perfectly even; higher = more skew. token-aware may skew on purpose toward faster replicas.\n")
	b.WriteString("- **busiest**: share of requests handled by the single busiest replica. For N equally fast replicas, an even split is 100/N%.\n")
	b.WriteString("- **observed tok/s**: client-side completion tokens divided by generation time (e2e − ttft for streams). Compare it with the server's `tok_per_sec` to see whether the router had a real speed signal.\n\n")

	fmt.Fprintf(&b, "## Files\n\n")
	b.WriteString("- `report.html` — charts and tables (open this)\n")
	b.WriteString("- `summary.json` — machine-readable run summaries\n")
	b.WriteString("- `summary.csv` — one row per policy × concurrency\n")
	b.WriteString("- `requests.csv` — every request with replica, latency and tokens\n")
	b.WriteString("- `timeseries.csv` — leader `/v1/status` snapshots (in-flight, tok/s, CPU, RAM)\n")
	b.WriteString("- `replicas.csv` — per-replica distribution and speed signal\n")
	return b.String()
}

// ---- HTML -----------------------------------------------------------------

var palette = []string{
	"#2563eb", "#16a34a", "#dc2626", "#d97706", "#7c3aed",
	"#0891b2", "#be185d", "#4d7c0f", "#9333ea", "#0d9488",
	"#b45309", "#1d4ed8",
}

type chartSeries struct {
	Label  string
	Color  string
	Values []float64
}

type chartPoint struct {
	X float64
	Y float64
}

type lineSeries struct {
	Label  string
	Color  string
	Points []chartPoint
}

func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	exp := math.Floor(math.Log10(v))
	base := math.Pow(10, exp)
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*base {
			return m * base
		}
	}
	return 10 * base
}

func fmtAxis(v float64) string {
	switch {
	case v >= 1000:
		return fmt.Sprintf("%.0f", v)
	case v >= 100:
		return fmt.Sprintf("%.0f", v)
	case v >= 10:
		return fmt.Sprintf("%.0f", v)
	case v >= 1:
		return fmt.Sprintf("%.1f", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

func esc(s string) string { return html.EscapeString(s) }

func svgGroupedBars(title string, categories []string, series []chartSeries, yLabel string) string {
	if len(categories) == 0 || len(series) == 0 {
		return "<p class=\"muted\">no data</p>"
	}
	const W, H = 960, 360
	const left, right, top, bottom = 74, 24, 64, 112
	plotW := float64(W - left - right)
	plotH := float64(H - top - bottom)

	maxV := 1.0
	for _, s := range series {
		for _, v := range s.Values {
			if v > maxV {
				maxV = v
			}
		}
	}
	scale := niceMax(maxV)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" class="chart">`, W, H, esc(title))
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" fill="#ffffff"/>`, W, H)
	fmt.Fprintf(&b, `<text x="%d" y="24" font-size="15" font-weight="600" fill="#0f172a">%s</text>`, left, esc(title))
	fmt.Fprintf(&b, `<text x="%d" y="41" font-size="11" fill="#64748b">%s</text>`, left, esc(yLabel))

	const ticks = 5
	for i := 0; i <= ticks; i++ {
		v := scale * float64(i) / ticks
		y := float64(top) + plotH - v/scale*plotH
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#e2e8f0" stroke-width="1"/>`, left, y, W-right, y)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" font-size="10" fill="#64748b" text-anchor="end">%s</text>`, left-8, y+4, fmtAxis(v))
	}
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#94a3b8" stroke-width="1"/>`, left, top+int(plotH), W-right, top+int(plotH))

	groupW := plotW / float64(len(categories))
	barW := groupW * 0.72 / float64(len(series))
	for ci, cat := range categories {
		gx := float64(left) + float64(ci)*groupW
		for si, s := range series {
			v := 0.0
			if ci < len(s.Values) {
				v = s.Values[ci]
			}
			h := v / scale * plotH
			x := gx + groupW*0.14 + float64(si)*barW
			y := float64(top) + plotH - h
			fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s" rx="2"/>`, x, y, math.Max(barW-2, 1), h, s.Color)
			if len(categories)*len(series) <= 24 {
				fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="9" fill="#334155" text-anchor="middle">%s</text>`, x+(barW-2)/2, y-4, fmtAxis(v))
			}
		}
		lx := gx + groupW/2
		ly := float64(top + int(plotH) + 18)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="10" fill="#334155" text-anchor="end" transform="rotate(-22 %.1f %.1f)">%s</text>`, lx, ly, lx, ly, esc(cat))
	}

	// Legend.
	lx := float64(left)
	for _, s := range series {
		fmt.Fprintf(&b, `<rect x="%.1f" y="44" width="10" height="10" fill="%s" rx="2"/>`, lx, s.Color)
		fmt.Fprintf(&b, `<text x="%.1f" y="53" font-size="11" fill="#334155">%s</text>`, lx+14, esc(s.Label))
		lx += 34 + float64(len(s.Label))*6.2
	}
	fmt.Fprintf(&b, `</svg>`)
	return b.String()
}

func svgLines(title string, series []lineSeries, xLabel, yLabel string) string {
	var maxX, maxY float64
	for _, s := range series {
		for _, p := range s.Points {
			if p.X > maxX {
				maxX = p.X
			}
			if p.Y > maxY {
				maxY = p.Y
			}
		}
	}
	if maxX <= 0 || len(series) == 0 {
		return "<p class=\"muted\">no data</p>"
	}
	const W, H = 960, 320
	const left, right, top, bottom = 74, 24, 64, 64
	plotW := float64(W - left - right)
	plotH := float64(H - top - bottom)
	scaleX := niceMax(maxX)
	scaleY := niceMax(maxY)

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" role="img" aria-label="%s" class="chart">`, W, H, esc(title))
	fmt.Fprintf(&b, `<rect x="0" y="0" width="%d" height="%d" fill="#ffffff"/>`, W, H)
	fmt.Fprintf(&b, `<text x="%d" y="24" font-size="15" font-weight="600" fill="#0f172a">%s</text>`, left, esc(title))

	const ticks = 5
	for i := 0; i <= ticks; i++ {
		v := scaleY * float64(i) / ticks
		y := float64(top) + plotH - v/scaleY*plotH
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#e2e8f0"/>`, left, y, W-right, y)
		fmt.Fprintf(&b, `<text x="%d" y="%.1f" font-size="10" fill="#64748b" text-anchor="end">%s</text>`, left-8, y+4, fmtAxis(v))
	}
	for i := 0; i <= ticks; i++ {
		v := scaleX * float64(i) / ticks
		x := float64(left) + v/scaleX*plotW
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%d" x2="%.1f" y2="%d" stroke="#f1f5f9"/>`, x, top, x, top+int(plotH))
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" font-size="10" fill="#64748b" text-anchor="middle">%s</text>`, x, top+int(plotH)+18, fmtAxis(v))
	}
	fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#94a3b8"/>`, left, top+int(plotH), W-right, top+int(plotH))
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="#64748b" text-anchor="middle">%s</text>`, left+int(plotW/2), H-12, esc(xLabel))
	fmt.Fprintf(&b, `<text x="%d" y="42" font-size="11" fill="#64748b">%s</text>`, left, esc(yLabel))

	for _, s := range series {
		if len(s.Points) == 0 {
			continue
		}
		var pts strings.Builder
		for i, p := range s.Points {
			x := float64(left) + p.X/scaleX*plotW
			y := float64(top) + plotH - p.Y/scaleY*plotH
			if i > 0 {
				pts.WriteByte(' ')
			}
			fmt.Fprintf(&pts, "%.1f,%.1f", x, y)
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round"/>`, pts.String(), s.Color)
	}

	lx := float64(left)
	for _, s := range series {
		fmt.Fprintf(&b, `<rect x="%.1f" y="44" width="10" height="10" fill="%s" rx="2"/>`, lx, s.Color)
		fmt.Fprintf(&b, `<text x="%.1f" y="53" font-size="11" fill="#334155">%s</text>`, lx+14, esc(s.Label))
		lx += 34 + float64(len(s.Label))*6.2
	}
	fmt.Fprintf(&b, `</svg>`)
	return b.String()
}

func cdfSeries(label string, values []float64, color string) lineSeries {
	if len(values) == 0 {
		return lineSeries{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	points := []chartPoint{{X: 0, Y: 0}}
	const maxPoints = 300
	if len(sorted) <= maxPoints {
		for i, v := range sorted {
			points = append(points, chartPoint{X: v, Y: float64(i+1) / float64(len(sorted)) * 100})
		}
	} else {
		for i := 0; i <= maxPoints; i++ {
			idx := int(math.Round(float64(i) / maxPoints * float64(len(sorted)-1)))
			points = append(points, chartPoint{X: sorted[idx], Y: float64(idx+1) / float64(len(sorted)) * 100})
		}
	}
	return lineSeries{Label: label, Color: color, Points: points}
}

func inFlightSeries(run runResult) []lineSeries {
	order := []string{}
	seen := map[string]bool{}
	for _, snap := range run.Snapshots {
		for _, r := range snap.Replicas {
			if !seen[r.ID] {
				seen[r.ID] = true
				order = append(order, r.ID)
			}
		}
	}
	var out []lineSeries
	for i, id := range order {
		var pts []chartPoint
		for _, snap := range run.Snapshots {
			for _, r := range snap.Replicas {
				if r.ID == id {
					pts = append(pts, chartPoint{X: float64(snap.ElapsedMS) / 1000, Y: float64(r.InFlight)})
					break
				}
			}
		}
		if len(pts) > 0 {
			out = append(out, lineSeries{Label: id, Color: palette[i%len(palette)], Points: pts})
		}
	}
	return out
}

func cpuSeries(run runResult) []lineSeries {
	order := []string{}
	seen := map[string]bool{}
	for _, snap := range run.Snapshots {
		for _, n := range snap.Nodes {
			if !seen[n.NodeID] {
				seen[n.NodeID] = true
				order = append(order, n.NodeID)
			}
		}
	}
	var out []lineSeries
	for i, id := range order {
		var pts []chartPoint
		for _, snap := range run.Snapshots {
			for _, n := range snap.Nodes {
				if n.NodeID == id {
					pts = append(pts, chartPoint{X: float64(snap.ElapsedMS) / 1000, Y: n.CPUPercent})
					break
				}
			}
		}
		if len(pts) > 0 {
			out = append(out, lineSeries{Label: id, Color: palette[i%len(palette)], Points: pts})
		}
	}
	return out
}

func svgShareBar(run runSummary, colors map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="share-row"><span class="share-label">%s @ %d</span><div class="share-bar">`, esc(run.Policy), run.Concurrency)
	for _, r := range run.PerReplica {
		if r.Requests == 0 {
			continue
		}
		color := colors[r.Replica]
		if color == "" {
			color = "#94a3b8"
		}
		fmt.Fprintf(&b, `<span class="share-seg" style="width:%.2f%%;background:%s" title="%s (%s): %d requests, %.1f%%"></span>`,
			r.SharePct, color, esc(r.Replica), esc(r.Node), r.Requests, r.SharePct)
	}
	b.WriteString(`</div></div>`)
	return b.String()
}

func replicaColors(runs []runSummary) map[string]string {
	ids := map[string]bool{}
	for _, s := range runs {
		for _, r := range s.PerReplica {
			ids[r.Replica] = true
		}
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	out := map[string]string{}
	for i, id := range list {
		out[id] = palette[i%len(palette)]
	}
	return out
}

func htmlTable(headers []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<table class="metrics"><thead><tr>`)
	for _, h := range headers {
		fmt.Fprintf(&b, `<th>%s</th>`, esc(h))
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, row := range rows {
		b.WriteString(`<tr>`)
		for _, cell := range row {
			fmt.Fprintf(&b, `<td>%s</td>`, esc(cell))
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

func metricRows(runs []runSummary) [][]string {
	var rows [][]string
	for _, s := range runs {
		rows = append(rows, []string{
			s.Policy, itoa(s.Concurrency), ftoa(s.SuccessPct, 1),
			ftoa(s.TTFT.P50, 0), ftoa(s.TTFT.P95, 0),
			ftoa(s.E2E.P50, 0), ftoa(s.E2E.P95, 0), ftoa(s.E2E.P99, 0),
			ftoa(s.RequestsPerSec, 2), ftoa(s.CompletionTokensPerSec, 0),
			ftoa(s.FairnessCV, 2), ftoa(s.BusiestReplicaPct, 0) + "%",
		})
	}
	return rows
}

func htmlReport(data reportData, obs []string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">`)
	b.WriteString(`<title>Meridian routing benchmark</title><style>`)
	b.WriteString(`
:root{--bg:#f8fafc;--card:#fff;--ink:#0f172a;--muted:#64748b;--line:#e2e8f0;--accent:#2563eb;--warnbg:#fff7ed;--warnline:#fed7aa}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font-family:system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;line-height:1.5}
main{max-width:1120px;margin:0 auto;padding:32px 20px 80px}
h1{font-size:30px;margin:0 0 4px;letter-spacing:-.02em}
h2{font-size:20px;margin:0 0 12px}
h3{font-size:16px;margin:20px 0 8px}
p{margin:8px 0}
.muted{color:var(--muted)}
.card{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:20px 22px;margin:16px 0;box-shadow:0 1px 2px rgba(15,23,42,.04)}
.card.warn{background:var(--warnbg);border-color:var(--warnline)}
.card.good{background:#f0fdf4;border-color:#bbf7d0}
ul{margin:8px 0 8px 20px;padding:0}
li{margin:6px 0}
table.metrics{border-collapse:collapse;width:100%;font-size:12.5px;font-variant-numeric:tabular-nums}
table.metrics th,table.metrics td{padding:7px 8px;border-bottom:1px solid var(--line);text-align:right;white-space:nowrap}
table.metrics th{color:var(--muted);font-weight:600;font-size:11px;text-transform:uppercase;letter-spacing:.04em}
table.metrics th:first-child,table.metrics td:first-child{text-align:left}
table.metrics tbody tr:hover{background:#f8fafc}
.chart{width:100%;height:auto;display:block;margin:8px 0 4px}
.share-row{display:flex;align-items:center;gap:10px;margin:7px 0}
.share-label{width:210px;flex:0 0 210px;font-size:12px;color:#334155;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.share-bar{display:flex;height:20px;width:100%;background:#f1f5f9;border-radius:6px;overflow:hidden;border:1px solid var(--line)}
.share-seg{display:block;height:100%}
.legend{display:flex;flex-wrap:wrap;gap:12px;margin:8px 0}
.legend span{font-size:11px;color:#334155;display:inline-flex;align-items:center;gap:5px}
.legend i{width:10px;height:10px;border-radius:2px;display:inline-block}
details{border:1px solid var(--line);border-radius:10px;padding:10px 14px;margin:10px 0;background:#fff}
summary{cursor:pointer;font-size:13px;font-weight:600;color:#334155}
code{background:#f1f5f9;padding:1px 5px;border-radius:4px;font-size:.9em}
.kv{display:grid;grid-template-columns:200px 1fr;gap:4px 14px;font-size:13px}
.kv dt{color:var(--muted)}
.kv dd{margin:0}
.two{display:grid;grid-template-columns:1fr 1fr;gap:18px}
@media(max-width:800px){.two{grid-template-columns:1fr}.share-label{width:140px;flex-basis:140px}}
`)
	b.WriteString(`</style></head><body><main>`)

	fmt.Fprintf(&b, `<h1>Meridian routing benchmark</h1><p class="muted">Generated %s · leader <code>%s</code> · mode <code>%s</code>`, esc(data.GeneratedAt.Format("2006-01-02 15:04:05")), esc(data.Addr), esc(data.Env.Mode))
	if data.Config.Label != "" {
		fmt.Fprintf(&b, ` · %s`, esc(data.Config.Label))
	}
	b.WriteString(`</p>`)

	// TL;DR.
	b.WriteString(`<div class="card"><h2>What this run says</h2><ul>`)
	for _, o := range obs {
		fmt.Fprintf(&b, `<li>%s</li>`, esc(o))
	}
	b.WriteString(`</ul></div>`)

	if len(data.Warnings) > 0 {
		b.WriteString(`<div class="card warn"><h2>Warnings</h2><ul>`)
		for _, w := range data.Warnings {
			fmt.Fprintf(&b, `<li>%s</li>`, esc(w))
		}
		b.WriteString(`</ul></div>`)
	}

	// Environment.
	b.WriteString(`<div class="card"><h2>Environment</h2><dl class="kv">`)
	fmt.Fprintf(&b, `<dt>Models</dt><dd><code>%s</code></dd>`, esc(strings.Join(data.Config.Models, ", ")))
	fmt.Fprintf(&b, `<dt>Policies</dt><dd><code>%s</code></dd>`, esc(strings.Join(data.Config.Policies, ", ")))
	fmt.Fprintf(&b, `<dt>Concurrency</dt><dd><code>%s</code></dd>`, esc(intsToString(data.Config.Concurrencies)))
	fmt.Fprintf(&b, `<dt>Requests per run</dt><dd>%d</dd>`, data.Config.Requests)
	fmt.Fprintf(&b, `<dt>Workload</dt><dd><code>%s</code> (per-profile max tokens: short 48, medium 128, long 256)</dd>`, esc(data.Config.Profile))
	fmt.Fprintf(&b, `<dt>Streaming</dt><dd>%v (TTFT measured from SSE content)</dd>`, data.Config.Stream)
	if data.WorkerCheck != nil {
		status := "not reachable"
		if data.WorkerCheck.Reached {
			status = "reachable"
		}
		fmt.Fprintf(&b, `<dt>Worker check</dt><dd>%s — <code>%s</code> %s</dd>`, esc(status), esc(data.WorkerCheck.URL), esc(data.WorkerCheck.Error))
	}
	b.WriteString(`</dl>`)
	if len(data.Env.Nodes) > 0 {
		b.WriteString(`<h3>Nodes</h3><table class="metrics"><thead><tr><th>node</th><th>role</th><th>state</th><th>cpu %</th><th>ram used MB</th><th>ram total MB</th></tr></thead><tbody>`)
		for _, n := range data.Env.Nodes {
			role := "remote"
			if n.Local {
				role = "local"
			}
			fmt.Fprintf(&b, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%.0f</td><td>%d</td><td>%d</td></tr>`,
				esc(n.NodeID), role, esc(n.State), n.CPUPercent, n.RAMUsedMB, n.RAMTotalMB)
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`</div>`)

	// Main table.
	b.WriteString(`<div class="card"><h2>Results</h2>`)
	b.WriteString(htmlTable(
		[]string{"policy", "conc", "ok %", "ttft p50", "ttft p95", "e2e p50", "e2e p95", "e2e p99", "req/s", "tok/s", "fairness", "busiest"},
		metricRows(data.Runs)))
	b.WriteString(`<p class="muted">All latency values are milliseconds over successful requests. goodput = successful work ÷ run duration.</p></div>`)

	// Charts per concurrency.
	b.WriteString(`<div class="card"><h2>Latency, throughput and distribution</h2>`)
	concs := uniqueConcs(data.Runs)
	for _, c := range concs {
		runs := filterConc(data.Runs, c)
		if len(runs) == 0 {
			continue
		}
		fmt.Fprintf(&b, `<h3>Concurrency %d</h3>`, c)
		cats, e2eSeries, ttftSeries := latencySeries(runs)
		b.WriteString(svgGroupedBars("End-to-end latency by policy", cats, e2eSeries, "milliseconds (lower is better)"))
		if hasTTFT(runs) {
			b.WriteString(svgGroupedBars("Time to first token by policy", cats, ttftSeries, "milliseconds (lower is better)"))
		}
		b.WriteString(`<div class="two">`)
		b.WriteString(svgGroupedBars("Goodput", cats, []chartSeries{{
			Label: "successful req/s", Color: palette[0], Values: valuesOf(runs, func(s runSummary) float64 { return s.RequestsPerSec }),
		}}, "requests per second (higher is better)"))
		b.WriteString(svgGroupedBars("Completion throughput", cats, []chartSeries{{
			Label: "completion tok/s", Color: palette[1], Values: valuesOf(runs, func(s runSummary) float64 { return s.CompletionTokensPerSec }),
		}}, "tokens per second (higher is better)"))
		b.WriteString(`</div>`)

		// CDF from raw samples.
		var lines []lineSeries
		for i, rr := range data.RunResults {
			if rr.Concurrency != c {
				continue
			}
			var vals []float64
			for _, x := range rr.Samples {
				if x.OK {
					vals = append(vals, x.E2Ems)
				}
			}
			lines = append(lines, cdfSeries(data.Runs[i].Policy, vals, palette[len(lines)%len(palette)]))
		}
		if len(lines) > 0 {
			b.WriteString(svgLines("End-to-end latency CDF", lines, "latency (ms)", "percent of successful requests"))
		}
	}
	b.WriteString(`</div>`)

	// Request distribution.
	b.WriteString(`<div class="card"><h2>Request distribution</h2>`)
	colors := replicaColors(data.Runs)
	b.WriteString(`<div class="legend">`)
	ids := make([]string, 0, len(colors))
	for id := range colors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, `<span><i style="background:%s"></i>%s</span>`, colors[id], esc(id))
	}
	b.WriteString(`</div>`)
	for _, s := range data.Runs {
		b.WriteString(svgShareBar(s, colors))
	}
	b.WriteString(`</div>`)

	// Per-model.
	if len(data.Env.Models) > 1 {
		b.WriteString(`<div class="card"><h2>Per-model p95 end-to-end latency (ms)</h2>`)
		headers := []string{"run"}
		headers = append(headers, data.Env.Models...)
		var rows [][]string
		for _, s := range data.Runs {
			row := []string{fmt.Sprintf("%s @ %d", s.Policy, s.Concurrency)}
			for _, m := range data.Env.Models {
				if ms, ok := s.PerModel[m]; ok {
					row = append(row, ftoa(ms.E2E.P95, 0))
				} else {
					row = append(row, "-")
				}
			}
			rows = append(rows, row)
		}
		b.WriteString(htmlTable(headers, rows))
		b.WriteString(`</div>`)
	}

	// Per-replica.
	b.WriteString(`<div class="card"><h2>Per-replica behaviour</h2>`)
	var rows [][]string
	for _, s := range data.Runs {
		for _, r := range s.PerReplica {
			rows = append(rows, []string{
				s.Policy, itoa(s.Concurrency), r.Replica, r.Node, r.Model,
				itoa(r.Requests), ftoa(r.SharePct, 1) + "%",
				ftoa(r.ObservedTokensPerSec, 1),
				ftoa(r.ServerTokensPerSecStart, 1) + " → " + ftoa(r.ServerTokensPerSecEnd, 1),
				i64toa(r.ServedDelta),
			})
		}
	}
	b.WriteString(htmlTable(
		[]string{"policy", "conc", "replica", "node", "model", "reqs", "share", "observed tok/s", "server tok/s start → end", "served Δ"},
		rows))
	b.WriteString(`</div>`)

	// Server-side time series.
	b.WriteString(`<div class="card"><h2>Server-side time series</h2><p class="muted">Polled from the leader's <code>/v1/status</code>. In-flight is what the router sees; CPU and RAM come from each node's heartbeat.</p>`)
	for _, rr := range data.RunResults {
		label := fmt.Sprintf("run %d · %s @ concurrency %d", rr.ID, rr.Policy, rr.Concurrency)
		fmt.Fprintf(&b, `<details><summary>%s</summary>`, esc(label))
		if len(rr.Snapshots) == 0 {
			b.WriteString(`<p class="muted">No status snapshots.</p>`)
		} else {
			b.WriteString(svgLines("In-flight requests per replica", inFlightSeries(rr), "seconds into run", "in-flight"))
			b.WriteString(svgLines("Node CPU", cpuSeries(rr), "seconds into run", "CPU %"))
		}
		b.WriteString(`</details>`)
	}
	b.WriteString(`</div>`)

	// How to read.
	b.WriteString(`<div class="card"><h2>How to read the numbers</h2><ul>`)
	b.WriteString(`<li><b>e2e</b>: wall-clock time from sending the request until the last byte. This is what a user feels.</li>`)
	b.WriteString(`<li><b>ttft</b>: time to the first content token on a streamed reply. Prefill and queueing dominate it. It is blank when streaming is off.</li>`)
	b.WriteString(`<li><b>p95</b>: 95% of successful requests were at or below this value. Use p95, not the mean, for planning.</li>`)
	b.WriteString(`<li><b>req/s</b> and <b>tok/s</b>: successful requests and completion tokens divided by the measured run duration. These are goodput, so a policy that fails requests does not look fast.</li>`)
	b.WriteString(`<li><b>fairness CV</b>: coefficient of variation of request counts across healthy replicas of the same model. 0 is perfectly even. token-aware may skew on purpose toward faster replicas.</li>`)
	b.WriteString(`<li><b>busiest</b>: share handled by the single busiest replica. An even split across N replicas is 100/N%.</li>`)
	b.WriteString(`<li><b>observed tok/s</b>: client-side completion tokens ÷ generation time (e2e − ttft for streams). Compare it with the server's <code>tok_per_sec</code> to see whether the router had a real speed signal.</li>`)
	b.WriteString(`</ul></div>`)

	// Files.
	b.WriteString(`<div class="card"><h2>Raw data</h2><ul>`)
	b.WriteString(`<li><code>summary.json</code> — machine-readable run summaries</li>`)
	b.WriteString(`<li><code>summary.csv</code> — one row per policy × concurrency</li>`)
	b.WriteString(`<li><code>requests.csv</code> — every request with replica, latency and tokens</li>`)
	b.WriteString(`<li><code>timeseries.csv</code> — status snapshots (in-flight, tok/s, CPU, RAM)</li>`)
	b.WriteString(`<li><code>replicas.csv</code> — per-replica distribution and speed signal</li>`)
	b.WriteString(`</ul></div>`)

	b.WriteString(`</main></body></html>`)
	return b.String()
}

func uniqueConcs(runs []runSummary) []int {
	seen := map[int]bool{}
	var out []int
	for _, s := range runs {
		if !seen[s.Concurrency] {
			seen[s.Concurrency] = true
			out = append(out, s.Concurrency)
		}
	}
	sort.Ints(out)
	return out
}

func filterConc(runs []runSummary, c int) []runSummary {
	var out []runSummary
	for _, s := range runs {
		if s.Concurrency == c {
			out = append(out, s)
		}
	}
	return out
}

func latencySeries(runs []runSummary) ([]string, []chartSeries, []chartSeries) {
	var cats []string
	p50 := make([]float64, 0, len(runs))
	p95 := make([]float64, 0, len(runs))
	p99 := make([]float64, 0, len(runs))
	t50 := make([]float64, 0, len(runs))
	t95 := make([]float64, 0, len(runs))
	for _, s := range runs {
		cats = append(cats, s.Policy)
		p50 = append(p50, s.E2E.P50)
		p95 = append(p95, s.E2E.P95)
		p99 = append(p99, s.E2E.P99)
		t50 = append(t50, s.TTFT.P50)
		t95 = append(t95, s.TTFT.P95)
	}
	e2e := []chartSeries{
		{Label: "p50", Color: palette[0], Values: p50},
		{Label: "p95", Color: palette[1], Values: p95},
		{Label: "p99", Color: palette[2], Values: p99},
	}
	ttft := []chartSeries{
		{Label: "p50", Color: palette[0], Values: t50},
		{Label: "p95", Color: palette[1], Values: t95},
	}
	return cats, e2e, ttft
}

func hasTTFT(runs []runSummary) bool {
	for _, s := range runs {
		if s.TTFT.Count > 0 {
			return true
		}
	}
	return false
}

func valuesOf(runs []runSummary, f func(runSummary) float64) []float64 {
	out := make([]float64, len(runs))
	for i, s := range runs {
		out[i] = f(s)
	}
	return out
}
