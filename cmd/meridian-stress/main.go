package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

var fallbackModels = []string{"qwen2.5-3b", "llama-3.2-3b", "qwen2.5-0.5b"}

// scripts are fixed multi-turn conversations, reused (not regenerated) across
// conversation IDs so runs stay comparable.
var scripts = [][]string{
	{
		"Hi, can you help me understand how load balancers work?",
		"What's the difference between round robin and least connections?",
		"Summarize that in one sentence.",
	},
	{
		"What is a context window in an LLM?",
		"Why does it matter for conversation history?",
	},
	{
		"Explain what a health check does in a distributed system.",
		"What happens if a health check keeps failing?",
		"And if the process exits entirely?",
	},
	{
		"Give me a short definition of throughput.",
		"How is that different from latency?",
	},
}

type conversation struct {
	Model  string
	ConvID string
	Script []string
}

type turnSample struct {
	ConversationID         string `json:"conversation_id"`
	Model                  string `json:"model"`
	TurnIndex              int    `json:"turn_index"`
	StatusCode             int    `json:"status_code"`
	LatencyMS              int64  `json:"latency_ms"`
	WorkerID               string `json:"worker_id"`
	RoutingPolicy          string `json:"routing_policy"`
	ActualPromptTokens     int    `json:"actual_prompt_tokens"`
	ActualCompletionTokens int    `json:"actual_completion_tokens"`
}

type latencySummary struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	P99 int64 `json:"p99"`
}

type summary struct {
	Policy                string                 `json:"policy"` // requested policy; "" = server default
	PoliciesObserved      map[string]int         `json:"policies_observed"`
	ModelsCount           int                    `json:"models_count"`
	ConversationsPerModel int                    `json:"conversations_per_model"`
	ConversationsTotal    int                    `json:"conversations_total"`
	Concurrency           int                    `json:"concurrency"`
	RequestsSent          int                    `json:"requests_sent"`
	Succeeded             int                    `json:"succeeded"`
	Failed                int                    `json:"failed"`
	SuccessPercent        float64                `json:"success_percent"`
	FailurePercent        float64                `json:"failure_percent"`
	FailuresByStatus      map[int]int            `json:"failures_by_status"`
	LatencySuccessful     latencySummary         `json:"latency_successful_ms"`
	LatencyByTurnIndex    map[int]latencySummary `json:"latency_by_turn_index_ms"`
	RequestsByWorker      map[string]int         `json:"requests_by_worker"`
	RequestsPerSecond     float64                `json:"requests_per_second"`
	TokensPerSecond       float64                `json:"tokens_per_second"`
	RunDurationSeconds    float64                `json:"run_duration_seconds"`
}

func main() {
	addr := flag.String("addr", "http://localhost:8080", "Meridian server address")
	concurrency := flag.Int("concurrency", 6, "conversations running in parallel (used when --concurrencies is empty)")
	concurrencies := flag.String("concurrencies", "", "comma-separated concurrency levels to sweep, e.g. 2,4,8,12")
	policies := flag.String("policies", "", "comma-separated routing policies to compare, e.g. round-robin,token-aware (empty = server default)")
	convPerModel := flag.Int("conversations-per-model", 200, "unique conversations to run per model")
	cooldown := flag.Duration("cooldown", 5*time.Second, "pause between runs in a sweep")
	flag.Parse()

	if *convPerModel <= 0 {
		fmt.Fprintln(os.Stderr, "error: --conversations-per-model must be > 0")
		os.Exit(2)
	}
	levels := []int{*concurrency}
	if *concurrencies != "" {
		parsed, err := parseInts(*concurrencies)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: --concurrencies:", err)
			os.Exit(2)
		}
		levels = parsed
	}
	for _, c := range levels {
		if c <= 0 {
			fmt.Fprintln(os.Stderr, "error: concurrency must be > 0")
			os.Exit(2)
		}
	}
	policyList := parseList(*policies)
	if len(policyList) == 0 {
		policyList = []string{""}
	}

	base := strings.TrimRight(*addr, "/")
	models := fetchModels(base)
	fmt.Printf("models: %s\n", strings.Join(models, ", "))

	var runs []summary
	for _, p := range policyList {
		for _, c := range levels {
			if len(runs) > 0 {
				time.Sleep(*cooldown)
			}
			n := len(runs) + 1
			fmt.Printf("\n=== run %d: policy=%s concurrency=%d ===\n", n, policyLabel(p), c)
			tag := fmt.Sprintf("r%d-%d", time.Now().Unix(), n)
			s := runOnce(base, models, p, c, *convPerModel, tag)
			printSummary(s)
			runs = append(runs, s)
		}
	}

	if len(runs) == 1 {
		if err := writeJSON("stress-results.json", runs[0]); err != nil {
			fmt.Fprintln(os.Stderr, "error writing stress-results.json:", err)
			os.Exit(1)
		}
		return
	}
	printMatrix(runs)
	if err := writeJSON("stress-results-matrix.json", runs); err != nil {
		fmt.Fprintln(os.Stderr, "error writing stress-results-matrix.json:", err)
		os.Exit(1)
	}
	fmt.Println("\nwrote stress-results-matrix.json")
}

// runOnce runs every conversation once at the given concurrency. The tag makes
// conversation IDs unique per run, so sessions from an earlier run are not reused.
func runOnce(base string, models []string, policy string, concurrency, perModel int, tag string) summary {
	convs := buildConversations(models, perModel, tag)
	total := len(convs)
	fmt.Printf("running %d conversations (%d models x %d/model) at concurrency %d\n", total, len(models), perModel, concurrency)

	// This creates a buffered channel named jobs that can hold total items of type
	// conversation. It is a thread-safe FIFO queue.
	jobs := make(chan conversation, total)
	for _, c := range convs {
		jobs <- c
	}
	close(jobs)

	client := &http.Client{Timeout: 300 * time.Second}
	results := make(chan turnSample, concurrency*4)
	var collected []turnSample
	var collectWG sync.WaitGroup
	collectWG.Add(1)
	go func() {
		defer collectWG.Done()
		for s := range results {
			collected = append(collected, s)
		}
	}()

	var done atomic.Int64
	start := time.Now()
	var workers sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for c := range jobs {
				runConversation(client, base, c, policy, results)
				if n := done.Add(1); n%25 == 0 {
					fmt.Printf("progress: %d/%d conversations completed\n", n, total)
				}
			}
		}()
	}

	workers.Wait()
	runDuration := time.Since(start)
	close(results)
	collectWG.Wait()

	s := summarize(collected, runDuration, len(models), perModel, total, concurrency)
	s.Policy = policy
	return s
}

func fetchModels(addr string) []string {
	// make a conn. with the Server and do a GET Request to fetch the number+types of models being served.
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(addr + "/v1/models")
	if err != nil {
		return fallbackModels
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fallbackModels
	}
	var list api.ModelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return fallbackModels
	}
	models := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		if strings.TrimSpace(m.ID) != "" {
			models = append(models, m.ID)
		}
	}
	if len(models) == 0 {
		return fallbackModels
	}
	return models
}

// buildConversations creates one entry per (model, index) pair; conversation i
// for a given model reuses scripts[i % len(scripts)]. tag prefixes every ID.
func buildConversations(models []string, perModel int, tag string) []conversation {
	convs := make([]conversation, 0, len(models)*perModel)
	for i := 0; i < perModel; i++ { // outer loop: conversation index
		for _, m := range models { // inner loop: model
			convs = append(convs, conversation{
				Model:  m,
				ConvID: fmt.Sprintf("%s-%s-c%d", tag, m, i),
				Script: scripts[i%len(scripts)],
			})
		}
	}
	return convs
}

// runConversation sends each turn sequentially, waiting for the response
// before sending the next (Meridian returns 409 on concurrent same-ID use).
func runConversation(client *http.Client, addr string, c conversation, policy string, results chan<- turnSample) {
	for idx, turn := range c.Script {
		results <- sendTurn(client, addr, c.Model, c.ConvID, idx+1, turn, policy)
	}
}

func sendTurn(client *http.Client, addr, model, convID string, turnIndex int, turn, policy string) turnSample {
	out := turnSample{ConversationID: convID, Model: model, TurnIndex: turnIndex}

	body, _ := json.Marshal(api.ChatRequest{
		Model:    model,
		Messages: []api.Message{{Role: "user", Content: turn}},
	})
	start := time.Now()
	req, err := http.NewRequest(http.MethodPost, addr+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		out.LatencyMS = time.Since(start).Milliseconds()
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Conversation-ID", convID)
	if policy != "" {
		req.Header.Set("X-Meridian-Routing-Policy", policy)
	}

	resp, err := client.Do(req)
	out.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		return out
	}
	defer resp.Body.Close()

	out.StatusCode = resp.StatusCode
	out.WorkerID = resp.Header.Get("X-Meridian-Worker")
	out.RoutingPolicy = resp.Header.Get("X-Meridian-Routing-Policy")
	if resp.StatusCode == http.StatusOK {
		var chat api.ChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&chat); err == nil {
			out.ActualPromptTokens = chat.Usage.PromptTokens
			out.ActualCompletionTokens = chat.Usage.CompletionTokens
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return out
}

func summarize(samples []turnSample, runDuration time.Duration, modelsCount, perModel, convTotal, concurrency int) summary {
	total := len(samples)
	succeeded := 0
	failed := 0
	completionTokens := 0
	byStatus := map[int]int{}
	allLatencies := []int64{}
	byTurn := map[int][]int64{}
	observed := map[string]int{}
	byWorker := map[string]int{}

	for _, s := range samples {
		if s.RoutingPolicy != "" {
			observed[s.RoutingPolicy]++
		}
		if s.WorkerID != "" {
			byWorker[s.WorkerID]++
		}
		if s.StatusCode == http.StatusOK {
			succeeded++
			completionTokens += s.ActualCompletionTokens
			allLatencies = append(allLatencies, s.LatencyMS)
			byTurn[s.TurnIndex] = append(byTurn[s.TurnIndex], s.LatencyMS)
		} else {
			failed++
			byStatus[s.StatusCode]++
		}
	}

	seconds := runDuration.Seconds()
	if seconds <= 0 {
		seconds = 1
	}

	turnSummaries := map[int]latencySummary{}
	for idx, lat := range byTurn {
		turnSummaries[idx] = percentiles(lat)
	}

	out := summary{
		PoliciesObserved:      observed,
		RequestsByWorker:      byWorker,
		ModelsCount:           modelsCount,
		ConversationsPerModel: perModel,
		ConversationsTotal:    convTotal,
		Concurrency:           concurrency,
		RequestsSent:          total,
		Succeeded:             succeeded,
		Failed:                failed,
		FailuresByStatus:      byStatus,
		LatencySuccessful:     percentiles(allLatencies),
		LatencyByTurnIndex:    turnSummaries,
		RequestsPerSecond:     float64(succeeded) / seconds,
		TokensPerSecond:       float64(completionTokens) / seconds,
		RunDurationSeconds:    seconds,
	}
	if total > 0 {
		out.SuccessPercent = float64(succeeded) / float64(total) * 100
		out.FailurePercent = float64(failed) / float64(total) * 100
	}
	return out
}

func percentiles(samples []int64) latencySummary {
	if len(samples) == 0 {
		return latencySummary{}
	}
	sorted := append([]int64(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return latencySummary{P50: percentile(sorted, 50), P95: percentile(sorted, 95), P99: percentile(sorted, 99)}
}

func percentile(sorted []int64, k int) int64 {
	idx := int(math.Ceil(float64(k)/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func printSummary(s summary) {
	fmt.Println()
	fmt.Printf("Policy:            %s\n", policyLabel(s.Policy))
	if len(s.PoliciesObserved) > 0 {
		fmt.Printf("Policy observed:   %s\n", formatCounts(s.PoliciesObserved))
	}
	fmt.Printf("Conversations:     %d (%d models x %d/model, concurrency %d)\n", s.ConversationsTotal, s.ModelsCount, s.ConversationsPerModel, s.Concurrency)
	fmt.Printf("Requests sent:     %d\n", s.RequestsSent)
	fmt.Printf("Succeeded:         %d   (%.1f%%)\n", s.Succeeded, s.SuccessPercent)
	fmt.Printf("Failed:            %d   (%.1f%%)\n", s.Failed, s.FailurePercent)
	printFailureBreakdown(s.FailuresByStatus)
	if len(s.RequestsByWorker) > 0 {
		fmt.Printf("Requests/worker:   %s\n", formatCounts(s.RequestsByWorker))
	}
	fmt.Println()
	fmt.Println("Latency (successful requests, ms):")
	fmt.Printf("  p50: %d\n", s.LatencySuccessful.P50)
	fmt.Printf("  p95: %d\n", s.LatencySuccessful.P95)
	fmt.Printf("  p99: %d\n", s.LatencySuccessful.P99)
	fmt.Println()
	fmt.Println("Latency by turn index (successful requests, ms):")
	indices := make([]int, 0, len(s.LatencyByTurnIndex))
	for idx := range s.LatencyByTurnIndex {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	for _, idx := range indices {
		l := s.LatencyByTurnIndex[idx]
		fmt.Printf("  turn %d: p50=%d p95=%d p99=%d\n", idx, l.P50, l.P95, l.P99)
	}
	fmt.Println()
	fmt.Println("Throughput:")
	fmt.Printf("  Requests/sec:  %.1f\n", s.RequestsPerSecond)
	fmt.Printf("  Tokens/sec:    %.0f   (sum of actual_completion_tokens / actual run duration)\n", s.TokensPerSecond)
}

// printMatrix prints one row per run so policies and concurrency levels can be
// compared side by side.
func printMatrix(runs []summary) {
	fmt.Println()
	fmt.Println("Comparison (successful requests):")
	fmt.Printf("%-16s %5s %9s %9s %8s %8s %7s %12s\n", "policy", "conc", "p50 ms", "p95 ms", "req/s", "tok/s", "fail%", "busiest wkr")
	for _, s := range runs {
		fmt.Printf("%-16s %5d %9d %9d %8.2f %8.0f %7.1f %11.0f%%\n",
			policyLabel(s.Policy), s.Concurrency,
			s.LatencySuccessful.P50, s.LatencySuccessful.P95,
			s.RequestsPerSecond, s.TokensPerSecond, s.FailurePercent,
			busiestShare(s.RequestsByWorker))
	}
}

// busiestShare is the percentage of requests served by the most-used worker.
// An even split across N workers gives 100/N.
func busiestShare(byWorker map[string]int) float64 {
	total, max := 0, 0
	for _, n := range byWorker {
		total += n
		if n > max {
			max = n
		}
	}
	if total == 0 {
		return 0
	}
	return float64(max) / float64(total) * 100
}

func printFailureBreakdown(byStatus map[int]int) {
	if len(byStatus) == 0 {
		return
	}
	codes := make([]int, 0, len(byStatus))
	for code := range byStatus {
		codes = append(codes, code)
	}
	sort.Ints(codes)
	fmt.Print(" ")
	for i, code := range codes {
		if i > 0 {
			fmt.Print("   ")
		}
		fmt.Printf("%d: %d", code, byStatus[code])
	}
	fmt.Println()
}

func formatCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}
	return strings.Join(parts, "  ")
}

func policyLabel(p string) string {
	if p == "" {
		return "server-default"
	}
	return p
}

// parseList splits a comma-separated flag and drops empty entries.
func parseList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseInts(s string) ([]int, error) {
	var out []int
	for _, part := range parseList(s) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", part)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no values given")
	}
	return out, nil
}

func writeJSON(path string, v any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
