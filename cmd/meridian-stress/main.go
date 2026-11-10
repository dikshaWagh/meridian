package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

var fallbackModels = []string{"qwen2.5-3b", "llama-3.2-3b", "qwen2.5-0.5b"}

type sample struct {
	StatusCode             int    `json:"status_code"`
	LatencyMS              int64  `json:"latency_ms"`
	WorkerID               string `json:"worker_id"`
	ActualPromptTokens     int    `json:"actual_prompt_tokens"`
	ActualCompletionTokens int    `json:"actual_completion_tokens"`
}

type summary struct {
	RequestsSent       int            `json:"requests_sent"`
	Succeeded          int            `json:"succeeded"`
	Failed             int            `json:"failed"`
	SuccessPercent     float64        `json:"success_percent"`
	FailurePercent     float64        `json:"failure_percent"`
	FailuresByStatus   map[int]int    `json:"failures_by_status"`
	LatencySuccessful  latencySummary `json:"latency_successful_ms"`
	RequestsPerSecond  float64        `json:"requests_per_second"`
	TokensPerSecond    float64        `json:"tokens_per_second"`
	RunDurationSeconds float64        `json:"run_duration_seconds"`
}

type latencySummary struct {
	P50 int64 `json:"p50"`
	P95 int64 `json:"p95"`
	P99 int64 `json:"p99"`
}

func main() {
	addr := flag.String("addr", "http://localhost:8080", "Meridian server address")
	concurrency := flag.Int("concurrency", 10, "number of parallel request loops")
	duration := flag.Duration("duration", 60*time.Second, "how long to run")
	model := flag.String("model", "", "pin to one model, or omit to spread across all")
	flag.Parse()

	if *concurrency <= 0 {
		fmt.Fprintln(os.Stderr, "error: --concurrency must be > 0")
		os.Exit(2)
	}
	if *duration <= 0 {
		fmt.Fprintln(os.Stderr, "error: --duration must be > 0")
		os.Exit(2)
	}

	base := strings.TrimRight(*addr, "/")
	models := []string{strings.TrimSpace(*model)}
	if models[0] == "" {
		models = fetchModels(base)
	}

	client := &http.Client{Timeout: 300 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()

	results := make(chan sample, *concurrency*4)
	var collected []sample
	var collectWG sync.WaitGroup
	collectWG.Add(1)
	go func() {
		defer collectWG.Done()
		for s := range results {
			collected = append(collected, s)
		}
	}()

	start := time.Now()
	var workers sync.WaitGroup
	prompts := []string{
		"Give a short explanation of local inference.",
		"Summarize why load balancing matters for LLM workers.",
		"Reply with one concise paragraph about systems programming.",
		"Explain request latency in simple terms.",
	}

	for i := 0; i < *concurrency; i++ {
		workers.Add(1)
		go func(id int) {
			defer workers.Done()
			for n := 0; ; n++ {
				select {
				case <-ctx.Done():
					return
				default:
				}
				m := models[(id+n)%len(models)]
				prompt := prompts[(id+n)%len(prompts)]
				results <- sendOne(client, base, m, prompt)
			}
		}(i)
	}

	workers.Wait()
	actualDuration := time.Since(start)
	close(results)
	collectWG.Wait()

	s := summarize(collected, actualDuration)
	printSummary(s)
	if err := writeSummary(s); err != nil {
		fmt.Fprintln(os.Stderr, "error writing stress-results.json:", err)
		os.Exit(1)
	}
}

func fetchModels(addr string) []string {
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

func sendOne(client *http.Client, addr, model, prompt string) sample {
	body, _ := json.Marshal(api.ChatRequest{
		Model:     model,
		Messages:  []api.Message{{Role: "user", Content: prompt}},
		MaxTokens: 64,
	})
	start := time.Now()
	req, err := http.NewRequest(http.MethodPost, addr+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return sample{StatusCode: 0, LatencyMS: time.Since(start).Milliseconds()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return sample{StatusCode: 0, LatencyMS: latency}
	}
	defer resp.Body.Close()

	out := sample{
		StatusCode: resp.StatusCode,
		LatencyMS:  latency,
		WorkerID:   resp.Header.Get("X-Meridian-Worker"),
	}
	if resp.StatusCode == http.StatusOK {
		var chat api.ChatResponse
		if err := json.NewDecoder(resp.Body).Decode(&chat); err == nil {
			out.ActualPromptTokens = chat.Usage.PromptTokens
			out.ActualCompletionTokens = chat.Usage.CompletionTokens
			return out
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return out
}

func summarize(samples []sample, runDuration time.Duration) summary {
	total := len(samples)
	succeeded := 0
	failed := 0
	completionTokens := 0
	latencies := []int64{}
	byStatus := map[int]int{}

	for _, s := range samples {
		if s.StatusCode == http.StatusOK {
			succeeded++
			completionTokens += s.ActualCompletionTokens
			latencies = append(latencies, s.LatencyMS)
		} else {
			failed++
			byStatus[s.StatusCode]++
		}
	}

	seconds := runDuration.Seconds()
	if seconds <= 0 {
		seconds = 1
	}
	out := summary{
		RequestsSent:       total,
		Succeeded:          succeeded,
		Failed:             failed,
		FailuresByStatus:   byStatus,
		LatencySuccessful:  percentiles(latencies),
		RequestsPerSecond:  float64(succeeded) / seconds,
		TokensPerSecond:    float64(completionTokens) / seconds,
		RunDurationSeconds: seconds,
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
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return latencySummary{P50: percentile(samples, 50), P95: percentile(samples, 95), P99: percentile(samples, 99)}
}

func percentile(samples []int64, k int) int64 {
	idx := int(math.Ceil(float64(k)/100*float64(len(samples)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(samples) {
		idx = len(samples) - 1
	}
	return samples[idx]
}

func printSummary(s summary) {
	fmt.Printf("Requests sent:     %d\n", s.RequestsSent)
	fmt.Printf("Succeeded:         %d   (%.1f%%)\n", s.Succeeded, s.SuccessPercent)
	fmt.Printf("Failed:            %d   (%.1f%%)\n", s.Failed, s.FailurePercent)
	printFailureBreakdown(s.FailuresByStatus)
	fmt.Println()
	fmt.Println("Latency (successful requests, ms):")
	fmt.Printf("  p50: %d\n", s.LatencySuccessful.P50)
	fmt.Printf("  p95: %d\n", s.LatencySuccessful.P95)
	fmt.Printf("  p99: %d\n", s.LatencySuccessful.P99)
	fmt.Println()
	fmt.Println("Throughput:")
	fmt.Printf("  Requests/sec:  %.1f\n", s.RequestsPerSecond)
	fmt.Printf("  Tokens/sec:    %.0f   (sum of actual_completion_tokens / actual run duration)\n", s.TokensPerSecond)
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

func writeSummary(s summary) error {
	f, err := os.Create("stress-results.json")
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}
