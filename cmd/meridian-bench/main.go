// Command meridian-bench measures a running Meridian leader end to end.
//
// It is built for the cluster (leader + LAN worker) where the routing policy is
// a system setting: for every policy it switches the default with the admin API,
// runs the same workload, and records client-side latency plus leader-side
// status snapshots. It then writes a JSON/CSV/HTML report.
//
// Standalone Meridian is supported too. There the per-request header
// X-Meridian-Routing-Policy is honoured, so the policy can be compared without
// an admin token (the tool falls back to the header if the PUT fails).
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

const version = "0.1.0"

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "\nerror:", err)
		os.Exit(1)
	}
}

// benchConfig is the resolved command line.
type benchConfig struct {
	Addr             string
	WorkerAddr       string
	AdminToken       string
	ModelsCSV        string
	PoliciesCSV      string
	ConcurrenciesCSV string
	Requests         int
	Warmup           int
	MaxTokens        int
	Profile          string
	Stream           bool
	StreamUsage      bool
	Cooldown         time.Duration
	Timeout          time.Duration
	StatusInterval   time.Duration
	OutDir           string
	RestorePolicy    bool
	Seed             int64
	Label            string
	Quiet            bool

	// Resolved after the environment is read.
	Models        []string
	Policies      []string
	Concurrencies []int
}

func parseFlags(args []string) (*benchConfig, error) {
	cfg := &benchConfig{}
	fs := flag.NewFlagSet("meridian-bench", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.Addr, "addr", "http://127.0.0.1:8080", "leader base URL (the only public API)")
	fs.StringVar(&cfg.WorkerAddr, "worker-addr", "", "optional worker HTTP base URL for a reachability check, e.g. http://192.168.1.103:8080")
	fs.StringVar(&cfg.AdminToken, "admin-token", os.Getenv("MERIDIAN_ADMIN_TOKEN"), "leader admin token; required to switch policies in cluster mode (default $MERIDIAN_ADMIN_TOKEN)")
	fs.StringVar(&cfg.ModelsCSV, "models", "", "comma-separated model IDs to test (empty = every healthy model)")
	fs.StringVar(&cfg.PoliciesCSV, "policies", "", "comma-separated policies to compare (empty = every policy the leader lists)")
	fs.StringVar(&cfg.ConcurrenciesCSV, "concurrency", "2,4", "comma-separated concurrency levels")
	fs.IntVar(&cfg.Requests, "requests", 40, "measured requests per policy x concurrency run")
	fs.IntVar(&cfg.Warmup, "warmup", 3, "warm-up requests per run (0 = skip)")
	fs.IntVar(&cfg.MaxTokens, "max-tokens", 0, "completion cap for every request (0 = per-profile default)")
	fs.StringVar(&cfg.Profile, "profile", "mixed", "prompt/output mix: short, medium, long or mixed")
	fs.BoolVar(&cfg.Stream, "stream", true, "stream responses to measure time to first token")
	fs.BoolVar(&cfg.StreamUsage, "stream-usage", true, "ask for the final usage chunk in streams (llama.cpp include_usage)")
	fs.DurationVar(&cfg.Cooldown, "cooldown", 3*time.Second, "pause between runs")
	fs.DurationVar(&cfg.Timeout, "timeout", 10*time.Minute, "per-request timeout")
	fs.DurationVar(&cfg.StatusInterval, "status-interval", 1*time.Second, "leader /v1/status polling interval")
	fs.StringVar(&cfg.OutDir, "out", "", "output directory (default results/bench-<timestamp>)")
	fs.BoolVar(&cfg.RestorePolicy, "restore-policy", true, "restore the leader's original default policy when done")
	fs.Int64Var(&cfg.Seed, "seed", 1, "seed for the deterministic prompt mix")
	fs.StringVar(&cfg.Label, "label", "", "label stored in the report")
	fs.BoolVar(&cfg.Quiet, "quiet", false, "only print the final table")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if cfg.Requests < 1 {
		return nil, errors.New("--requests must be at least 1")
	}
	if cfg.Warmup < 0 {
		return nil, errors.New("--warmup cannot be negative")
	}
	if cfg.MaxTokens < 0 {
		return nil, errors.New("--max-tokens cannot be negative")
	}
	profile := strings.ToLower(strings.TrimSpace(cfg.Profile))
	if profile != "short" && profile != "medium" && profile != "long" && profile != "mixed" {
		return nil, fmt.Errorf("--profile %q: use short, medium, long or mixed", cfg.Profile)
	}
	cfg.Profile = profile
	if cfg.StatusInterval < 100*time.Millisecond {
		return nil, errors.New("--status-interval must be at least 100ms")
	}
	if cfg.Timeout <= 0 {
		return nil, errors.New("--timeout must be positive")
	}
	if cfg.Cooldown < 0 {
		return nil, errors.New("--cooldown cannot be negative")
	}
	cfg.Addr = strings.TrimRight(strings.TrimSpace(cfg.Addr), "/")
	if _, err := url.ParseRequestURI(cfg.Addr); err != nil {
		return nil, fmt.Errorf("--addr %q: %w", cfg.Addr, err)
	}
	if cfg.WorkerAddr != "" {
		cfg.WorkerAddr = strings.TrimRight(strings.TrimSpace(cfg.WorkerAddr), "/")
		if _, err := url.ParseRequestURI(cfg.WorkerAddr); err != nil {
			return nil, fmt.Errorf("--worker-addr %q: %w", cfg.WorkerAddr, err)
		}
	}
	return cfg, nil
}

// envInfo is the leader's live view of the cluster, read once before the runs.
type envInfo struct {
	Mode          string        `json:"mode"`
	LeaderID      string        `json:"leader_id"`
	Nodes         []nodeInfo    `json:"nodes"`
	Replicas      []replicaInfo `json:"replicas"`
	Models        []string      `json:"models"`
	Policies      []policyInfo  `json:"policies"`
	DefaultPolicy string        `json:"default_policy"`
}

type nodeInfo struct {
	NodeID     string  `json:"node_id"`
	Address    string  `json:"address"`
	Local      bool    `json:"local"`
	State      string  `json:"state"`
	CPUCores   int     `json:"cpu_cores"`
	RAMTotalMB int64   `json:"ram_total_mb"`
	CPUPercent float64 `json:"cpu_percent"`
	RAMUsedMB  int64   `json:"ram_used_mb"`
}

type replicaInfo struct {
	ID            string  `json:"id"`
	NodeID        string  `json:"node_id"`
	Model         string  `json:"model"`
	Healthy       bool    `json:"healthy"`
	State         string  `json:"state"`
	ContextWindow int     `json:"context_window"`
	MaxConcurrent int     `json:"max_concurrent"`
	TokPerSec     float64 `json:"tok_per_sec"`
	InFlight      int64   `json:"in_flight"`
	Served        int64   `json:"served"`
}

type policyInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type workerCheck struct {
	URL     string `json:"url"`
	Reached bool   `json:"reached"`
	Error   string `json:"error,omitempty"`
}

// apiClient talks to the leader. It has no overall timeout: a generation can be
// long. Each request uses a context deadline instead.
type apiClient struct {
	base        string
	adminToken  string
	stream      bool
	streamUsage bool
	timeout     time.Duration
	http        *http.Client
}

func newAPIClient(base, adminToken string, stream, streamUsage bool, timeout time.Duration) *apiClient {
	tr := &http.Transport{
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 128,
		IdleConnTimeout:     90 * time.Second,
	}
	return &apiClient{
		base:        base,
		adminToken:  adminToken,
		stream:      stream,
		streamUsage: streamUsage,
		timeout:     timeout,
		http:        &http.Client{Transport: tr},
	}
}

func (c *apiClient) getJSON(ctx context.Context, path string, v any) (int, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("GET %s: HTTP %d: %s", path, resp.StatusCode, truncate(string(body), 200))
	}
	if v != nil {
		if err := json.Unmarshal(body, v); err != nil {
			return resp.StatusCode, fmt.Errorf("GET %s: decode: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

func (c *apiClient) ping(ctx context.Context) error {
	var out map[string]string
	code, err := c.getJSON(ctx, "/healthz", &out)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("/healthz returned %d", code)
	}
	return nil
}

// setPolicy changes the leader's system default policy. Works without a token
// on standalone Meridian; cluster mode needs the admin token.
func (c *apiClient) setPolicy(ctx context.Context, name string) error {
	body, _ := json.Marshal(api.PolicyUpdateRequest{Name: name})
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, c.base+"/v1/policies/default", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.adminToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.adminToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PUT /v1/policies/default: HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var got api.RoutingPolicyList
	if err := json.Unmarshal(raw, &got); err != nil {
		return fmt.Errorf("PUT /v1/policies/default: decode: %w", err)
	}
	if got.Default != name {
		return fmt.Errorf("PUT /v1/policies/default: asked for %q, server reports %q", name, got.Default)
	}
	return nil
}

// loadEnvironment reads models, policies and node/replica status once.
func (c *apiClient) loadEnvironment(ctx context.Context) (envInfo, error) {
	var env envInfo
	var models api.ModelList
	if _, err := c.getJSON(ctx, "/v1/models", &models); err != nil {
		return env, fmt.Errorf("read models: %w", err)
	}
	for _, m := range models.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			env.Models = append(env.Models, id)
		}
	}
	sort.Strings(env.Models)

	var pol api.RoutingPolicyList
	if _, err := c.getJSON(ctx, "/policies", &pol); err != nil {
		return env, fmt.Errorf("read policies: %w", err)
	}
	env.DefaultPolicy = pol.Default
	for _, p := range pol.Policies {
		env.Policies = append(env.Policies, policyInfo{Name: p.Name, Description: p.Description})
	}

	st, err := c.readStatus(ctx)
	if err != nil {
		return env, err
	}
	env.Mode = st.Mode
	env.LeaderID = st.LeaderID
	env.Nodes = st.Nodes
	env.Replicas = st.Replicas
	if len(env.Models) == 0 {
		seen := map[string]bool{}
		for _, r := range env.Replicas {
			if r.Healthy && !seen[r.Model] {
				seen[r.Model] = true
				env.Models = append(env.Models, r.Model)
			}
		}
		sort.Strings(env.Models)
	}
	return env, nil
}

type statusData struct {
	Mode     string
	LeaderID string
	Nodes    []nodeInfo
	Replicas []replicaInfo
}

// readStatus tries the cluster endpoint first, then the standalone one. The two
// roles expose different shapes on different paths.
func (c *apiClient) readStatus(ctx context.Context) (statusData, error) {
	var cs api.ClusterStatusResponse
	code, clusterErr := c.getJSON(ctx, "/v1/status", &cs)
	if clusterErr == nil && code == http.StatusOK && (cs.LeaderID != "" || len(cs.Nodes) > 0 || len(cs.Replicas) > 0) {
		out := statusData{Mode: "cluster", LeaderID: cs.LeaderID}
		for _, n := range cs.Nodes {
			out.Nodes = append(out.Nodes, nodeInfo{
				NodeID: n.NodeID, Address: n.Address, Local: n.Local, State: n.State,
				CPUCores: n.CPUCores, RAMTotalMB: n.RAMTotalMB, CPUPercent: n.CPUPercent, RAMUsedMB: n.RAMUsedMB,
			})
		}
		for _, r := range cs.Replicas {
			out.Replicas = append(out.Replicas, replicaInfo{
				ID: r.ID, NodeID: r.NodeID, Model: r.Model, Healthy: r.Healthy, State: r.State,
				ContextWindow: r.ContextWindow, MaxConcurrent: r.MaxConcurrent,
				TokPerSec: r.TokPerSec, InFlight: r.InFlight, Served: r.Served,
			})
		}
		return out, nil
	}

	var ss api.StatusResponse
	if _, err := c.getJSON(ctx, "/status", &ss); err != nil {
		return statusData{}, fmt.Errorf("read status: cluster /v1/status failed (%v) and standalone /status failed (%v)", clusterErr, err)
	}
	out := statusData{Mode: "standalone", LeaderID: "standalone"}
	out.Nodes = []nodeInfo{{NodeID: "standalone", State: "online", Local: true}}
	for _, w := range ss.Workers {
		out.Replicas = append(out.Replicas, replicaInfo{
			ID: w.ID, NodeID: "standalone", Model: w.Model, Healthy: w.Healthy, State: w.State,
			TokPerSec: w.TokPerSec, InFlight: w.InFlight, Served: w.Served,
		})
	}
	return out, nil
}

func checkWorker(ctx context.Context, base string) *workerCheck {
	wc := &workerCheck{URL: base}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		wc.Error = err.Error()
		return wc
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		wc.Error = err.Error()
		return wc
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode == http.StatusOK {
		wc.Reached = true
	} else {
		wc.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return wc
}

// ---- workload -------------------------------------------------------------

type job struct {
	Index     int
	Model     string
	Profile   string
	Prompt    string
	MaxTokens int
	ConvID    string
}

type profileSpec struct {
	Name        string
	Prompt      string
	MaxTokens   int
	Description string
}

const background = "A small office runs a private AI cluster on commodity machines. One machine has a GPU and several CPU desktops join over a local network. The gateway sends each chat request to one model replica. Replicas differ in speed, memory, and current load. Some machines sleep or lose Wi-Fi. The operator wants low latency for interactive chat, good throughput for batch work, and no request failures when a node disappears. "

func profileFor(name string) profileSpec {
	switch name {
	case "short":
		return profileSpec{
			Name:        "short",
			Prompt:      "In one sentence, explain what a load balancer does.",
			MaxTokens:   48,
			Description: "one-line question, short answer",
		}
	case "medium":
		return profileSpec{
			Name:        "medium",
			Prompt:      "Explain the difference between latency and throughput in a distributed LLM cluster. Cover queueing, continuous batching, and why p95 latency matters more than the average. Use three short paragraphs.",
			MaxTokens:   128,
			Description: "explanatory answer, a few paragraphs",
		}
	case "long":
		return profileSpec{
			Name:        "long",
			Prompt:      strings.Repeat(background, 2) + "\n\nList the three biggest risks in this design and one mitigation for each. Be concise.",
			MaxTokens:   256,
			Description: "large prompt, longer answer",
		}
	}
	return profileSpec{Name: "mixed", MaxTokens: 128}
}

func buildJobs(models []string, n int, profile string, seed int64, tag string) []job {
	if n <= 0 || len(models) == 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(seed))
	profiles := []string{"short", "medium", "long"}
	if profile != "mixed" {
		profiles = []string{profile}
	}
	jobs := make([]job, 0, n)
	for i := 0; i < n; i++ {
		p := profiles[i%len(profiles)]
		spec := profileFor(p)
		// A unique prefix defeats llama.cpp prefix caching, so every request
		// pays prompt processing instead of reusing the previous prompt.
		nonce := fmt.Sprintf("%08x", rng.Uint32())
		prompt := fmt.Sprintf("Request %d [%s]. %s", i, nonce, spec.Prompt)
		jobs = append(jobs, job{
			Index:     i,
			Model:     models[i%len(models)],
			Profile:   p,
			Prompt:    prompt,
			MaxTokens: spec.MaxTokens,
			ConvID:    fmt.Sprintf("bench-%s-%d-%s", tag, i, nonce),
		})
	}
	return jobs
}

func (c *apiClient) selectMaxTokens(j job, override int) int {
	if override > 0 {
		return override
	}
	return j.MaxTokens
}

// ---- chat -----------------------------------------------------------------

type sseChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *api.Usage `json:"usage"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []api.Message  `json:"messages"`
	MaxTokens     int            `json:"max_tokens,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type chatResult struct {
	Status           int
	OK               bool
	Error            string
	Replica          string
	PolicyHeader     string
	Failover         string
	QueueWaitMs      float64 // time in leader before dispatch (ms)
	PredictedMs      float64 // TW-LEL estimate, -1 when not applicable
	StartedAt        time.Time
	TTFTms           float64
	E2Ems            float64
	TTFTKnown        bool
	Streamed         bool
	PromptTokens     int
	CompletionTokens int
}

func (c *apiClient) chat(ctx context.Context, j job, policy string, maxTokens int) chatResult {
	out := chatResult{StartedAt: time.Now()}
	req := chatRequest{
		Model:     j.Model,
		Messages:  []api.Message{{Role: "user", Content: j.Prompt}},
		MaxTokens: maxTokens,
		Stream:    c.stream,
	}
	if c.stream && c.streamUsage {
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	body, _ := json.Marshal(req)
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Conversation-ID", j.ConvID)
	if policy != "" {
		// Honoured by standalone Meridian; ignored by the cluster leader, which
		// uses its system default (set with the admin API).
		httpReq.Header.Set("X-Meridian-Routing-Policy", policy)
	}

	start := time.Now()
	resp, err := c.http.Do(httpReq)
	if err != nil {
		out.E2Ems = msSince(start)
		out.Error = err.Error()
		return out
	}
	defer resp.Body.Close()

	out.Status = resp.StatusCode
	out.Replica = resp.Header.Get("X-Meridian-Worker")
	out.PolicyHeader = resp.Header.Get("X-Meridian-Routing-Policy")
	out.Failover = resp.Header.Get("X-Meridian-Failover")
	out.QueueWaitMs, _ = strconv.ParseFloat(resp.Header.Get("X-Meridian-Queue-Ms"), 64)
	out.PredictedMs, _ = strconv.ParseFloat(resp.Header.Get("X-Meridian-Predicted-Ms"), 64)

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		out.E2Ems = msSince(start)
		out.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
		return out
	}

	ctype := resp.Header.Get("Content-Type")
	if c.stream && strings.Contains(ctype, "text/event-stream") {
		out.Streamed = true
		readSSE(resp.Body, start, &out)
	} else {
		var cr api.ChatResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&cr); err != nil {
			out.E2Ems = msSince(start)
			out.Error = "decode response: " + err.Error()
			return out
		}
		out.E2Ems = msSince(start)
		out.PromptTokens = cr.Usage.PromptTokens
		out.CompletionTokens = cr.Usage.CompletionTokens
	}
	out.OK = out.Error == ""
	return out
}

func readSSE(r io.Reader, start time.Time, out *chatResult) {
	br := bufio.NewReaderSize(r, 64<<10)
	var text, reasoning strings.Builder
	chunks := 0
	usageKnown := false
	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data != "" && data != "[DONE]" {
					var chunk sseChunk
					if json.Unmarshal([]byte(data), &chunk) == nil {
						for _, ch := range chunk.Choices {
							piece := ch.Delta.Content
							if piece == "" {
								piece = ch.Delta.ReasoningContent
							}
							if piece != "" {
								if !out.TTFTKnown {
									out.TTFTms = msSince(start)
									out.TTFTKnown = true
								}
								text.WriteString(ch.Delta.Content)
								reasoning.WriteString(ch.Delta.ReasoningContent)
								chunks++
							}
						}
						if chunk.Usage != nil {
							out.PromptTokens = chunk.Usage.PromptTokens
							out.CompletionTokens = chunk.Usage.CompletionTokens
							usageKnown = true
						}
					}
				}
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				out.Error = "stream: " + err.Error()
			}
			break
		}
	}
	out.E2Ems = msSince(start)
	if !usageKnown {
		// llama.cpp normally sends one token per content chunk. Fall back to
		// that count, then to a cruder ~4 chars/token estimate.
		if chunks > 0 {
			out.CompletionTokens = chunks
		} else if n := len(text.String()) + len(reasoning.String()); n > 0 {
			out.CompletionTokens = n / 4
		}
	}
}

func msSince(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000.0 }

// ---- status polling -------------------------------------------------------

type snapshot struct {
	At        time.Time     `json:"at"`
	ElapsedMS int64         `json:"elapsed_ms"`
	Error     string        `json:"error,omitempty"`
	Nodes     []nodeSnap    `json:"nodes,omitempty"`
	Replicas  []replicaSnap `json:"replicas,omitempty"`
}

type nodeSnap struct {
	NodeID     string  `json:"node_id"`
	State      string  `json:"state"`
	CPUPercent float64 `json:"cpu_percent"`
	RAMUsedMB  int64   `json:"ram_used_mb"`
	RAMTotalMB int64   `json:"ram_total_mb"`
}

type replicaSnap struct {
	ID        string  `json:"id"`
	NodeID    string  `json:"node_id"`
	Model     string  `json:"model"`
	InFlight  int64   `json:"in_flight"`
	TokPerSec float64 `json:"tok_per_sec"`
	Served    int64   `json:"served"`
}

type statusPoller struct {
	client    *apiClient
	interval  time.Duration
	startedAt time.Time
	mu        sync.Mutex
	snaps     []snapshot
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func newStatusPoller(c *apiClient, interval time.Duration) *statusPoller {
	return &statusPoller{
		client: c, interval: interval, startedAt: time.Now(),
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
}

// start takes the t=0 snapshot synchronously, then polls in the background.
func (p *statusPoller) start(ctx context.Context) {
	p.sample(ctx)
	go p.loop(ctx)
}

func (p *statusPoller) loop(ctx context.Context) {
	defer close(p.doneCh)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			// Final snapshot: use a fresh context so a cancelled parent does
			// not lose the end-of-run counters.
			finalCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			p.sample(finalCtx)
			cancel()
			return
		case <-ticker.C:
			p.sample(ctx)
		}
	}
}

func (p *statusPoller) stop() []snapshot {
	select {
	case <-p.doneCh:
	default:
		close(p.stopCh)
		<-p.doneCh
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]snapshot(nil), p.snaps...)
}

func (p *statusPoller) sample(ctx context.Context) {
	snap := snapshot{At: time.Now(), ElapsedMS: time.Since(p.startedAt).Milliseconds()}
	st, err := p.client.readStatus(ctx)
	if err != nil {
		if ctx.Err() == nil {
			snap.Error = err.Error()
		}
	} else {
		for _, n := range st.Nodes {
			snap.Nodes = append(snap.Nodes, nodeSnap{
				NodeID: n.NodeID, State: n.State, CPUPercent: n.CPUPercent,
				RAMUsedMB: n.RAMUsedMB, RAMTotalMB: n.RAMTotalMB,
			})
		}
		for _, r := range st.Replicas {
			snap.Replicas = append(snap.Replicas, replicaSnap{
				ID: r.ID, NodeID: r.NodeID, Model: r.Model,
				InFlight: r.InFlight, TokPerSec: r.TokPerSec, Served: r.Served,
			})
		}
	}
	p.mu.Lock()
	p.snaps = append(p.snaps, snap)
	p.mu.Unlock()
}

// ---- run orchestration ----------------------------------------------------

type runResult struct {
	ID          int
	Policy      string
	Concurrency int
	StartedAt   time.Time
	EndedAt     time.Time
	Duration    time.Duration
	Samples     []sample
	Snapshots   []snapshot
}

func run(ctx context.Context, cfg *benchConfig) error {
	client := newAPIClient(cfg.Addr, cfg.AdminToken, cfg.Stream, cfg.StreamUsage, cfg.Timeout)
	if err := client.ping(ctx); err != nil {
		return fmt.Errorf("cannot reach leader at %s: %w", cfg.Addr, err)
	}
	env, err := client.loadEnvironment(ctx)
	if err != nil {
		return err
	}
	if len(env.Policies) == 0 {
		return errors.New("leader lists no routing policies")
	}

	models, err := selectModels(cfg.ModelsCSV, env)
	if err != nil {
		return err
	}
	policies, err := selectPolicies(cfg.PoliciesCSV, env)
	if err != nil {
		return err
	}
	levels, err := parseInts(cfg.ConcurrenciesCSV)
	if err != nil {
		return fmt.Errorf("--concurrency: %w", err)
	}
	cfg.Models, cfg.Policies, cfg.Concurrencies = models, policies, levels
	env.Models = models

	if cfg.OutDir == "" {
		cfg.OutDir = filepath.Join("results", "bench-"+time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	var wc *workerCheck
	if cfg.WorkerAddr != "" {
		wc = checkWorker(ctx, cfg.WorkerAddr)
	}

	// Decide how policy changes will be applied. Keep the original default so
	// the leader is left as we found it.
	originalDefault := env.DefaultPolicy
	switchMode := "global"
	if env.Mode == "cluster" && cfg.AdminToken == "" {
		if len(policies) > 1 || policies[0] != env.DefaultPolicy {
			return fmt.Errorf("cluster mode: switching policies needs the leader's admin token.\n"+
				"Start the leader with --admin-token <token> (or set MERIDIAN_ADMIN_TOKEN) and pass --admin-token,\n"+
				"or test only the current policy with --policies %s", env.DefaultPolicy)
		}
		switchMode = "none"
	} else {
		// Probe once so a bad token fails before a long run, and so standalone
		// can fall back to the per-request header.
		if err := client.setPolicy(ctx, policies[0]); err != nil {
			if env.Mode == "standalone" {
				switchMode = "header"
			} else {
				return fmt.Errorf("cannot switch routing policy: %w\n"+
					"Check that --admin-token matches the leader's --admin-token / $MERIDIAN_ADMIN_TOKEN", err)
			}
		}
	}

	if switchMode == "global" && cfg.RestorePolicy {
		defer func() {
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := client.setPolicy(rctx, originalDefault); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not restore default policy %q: %v\n", originalDefault, err)
			}
		}()
	}

	warnings := buildWarnings(env, models, policies, levels, cfg, wc, switchMode)
	printEnvironment(cfg, env, wc, warnings)

	// The prompt set is identical for every policy and concurrency so the
	// comparison is like-for-like.
	baseJobs := buildJobs(models, cfg.Requests, cfg.Profile, cfg.Seed, "run")
	warmJobs := buildJobs(models, cfg.Warmup, cfg.Profile, cfg.Seed+1, "warm")
	if cfg.Warmup > 0 && len(warmJobs) < len(models) {
		warmJobs = buildJobs(models, len(models), cfg.Profile, cfg.Seed+1, "warm")
	}

	var runs []runResult
	runID := 0
	for _, policy := range policies {
		if switchMode == "global" {
			if err := client.setPolicy(ctx, policy); err != nil {
				return fmt.Errorf("switch to policy %q: %w", policy, err)
			}
		}
		for _, conc := range levels {
			if runID > 0 && cfg.Cooldown > 0 {
				if !cfg.Quiet {
					fmt.Printf("  cooling down for %s...\n", cfg.Cooldown)
				}
				if err := sleepCtx(ctx, cfg.Cooldown); err != nil {
					return err
				}
			}
			runID++
			tag := fmt.Sprintf("r%d-%s-%d", runID, policy, conc)
			jobs := make([]job, len(baseJobs))
			copy(jobs, baseJobs)
			for i := range jobs {
				jobs[i].ConvID = fmt.Sprintf("%s-%d", tag, i)
			}

			if !cfg.Quiet {
				fmt.Printf("\n=== run %d: policy=%s concurrency=%d requests=%d ===\n", runID, policy, conc, len(jobs))
			}
			if len(warmJobs) > 0 {
				if !cfg.Quiet {
					fmt.Printf("  warm-up: %d request(s)\n", len(warmJobs))
				}
				warm := runJobs(ctx, client, warmJobs, conc, policy, cfg.MaxTokens, true, 0)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if allFailed(warm) {
					fmt.Fprintf(os.Stderr, "warning: every warm-up request failed; trying the measured run anyway\n")
				}
			}

			poller := newStatusPoller(client, cfg.StatusInterval)
			poller.start(ctx)
			start := time.Now()
			samples := runJobs(ctx, client, jobs, conc, policy, cfg.MaxTokens, !cfg.Quiet, len(jobs))
			end := time.Now()
			snaps := poller.stop()
			if ctx.Err() != nil {
				return ctx.Err()
			}

			rr := runResult{
				ID: runID, Policy: policy, Concurrency: conc,
				StartedAt: start, EndedAt: end, Duration: end.Sub(start),
				Samples: samples, Snapshots: snaps,
			}
			runs = append(runs, rr)
			s := computeSummary(&rr, env)
			printRunSummary(s)
		}
	}

	summaries := make([]runSummary, len(runs))
	for i := range runs {
		summaries[i] = computeSummary(&runs[i], env)
	}
	data := reportData{
		GeneratedAt: time.Now(),
		Addr:        cfg.Addr,
		WorkerCheck: wc,
		Config:      *cfg,
		Env:         env,
		Warnings:    append(warnings, dynamicWarnings(summaries, runs, env)...),
		Runs:        summaries,
		RunResults:  runs,
	}
	if err := writeReport(cfg.OutDir, data); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	printFinalTable(summaries)
	fmt.Printf("\nreport: %s\n", filepath.Join(cfg.OutDir, "report.html"))
	fmt.Printf("data:   %s\n", filepath.Join(cfg.OutDir, "summary.json"))
	return nil
}

func selectModels(csv string, env envInfo) ([]string, error) {
	available := map[string]bool{}
	for _, m := range env.Models {
		available[m] = true
	}
	requested := parseList(csv)
	if len(requested) == 0 {
		if len(env.Models) == 0 {
			return nil, errors.New("leader reports no healthy models")
		}
		return env.Models, nil
	}
	var out []string
	for _, m := range requested {
		if !available[m] {
			return nil, fmt.Errorf("model %q is not healthy on the leader; available: %s", m, strings.Join(env.Models, ", "))
		}
		out = append(out, m)
	}
	return out, nil
}

func selectPolicies(csv string, env envInfo) ([]string, error) {
	available := map[string]bool{}
	for _, p := range env.Policies {
		available[p.Name] = true
	}
	requested := parseList(csv)
	if len(requested) == 0 {
		for _, p := range env.Policies {
			requested = append(requested, p.Name)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range requested {
		if !available[p] {
			names := make([]string, 0, len(env.Policies))
			for _, x := range env.Policies {
				names = append(names, x.Name)
			}
			return nil, fmt.Errorf("unknown policy %q; available: %s", p, strings.Join(names, ", "))
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no policies selected")
	}
	return out, nil
}

func parseInts(s string) ([]int, error) {
	parts := parseList(s)
	if len(parts) == 0 {
		return nil, errors.New("no values given")
	}
	var out []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%q is not a positive integer", p)
		}
		out = append(out, n)
	}
	return out, nil
}

func parseList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func runJobs(ctx context.Context, c *apiClient, jobs []job, conc int, policy string, maxTokens int, progress bool, total int) []sample {
	if len(jobs) == 0 {
		return nil
	}
	if conc < 1 {
		conc = 1
	}
	jobCh := make(chan job)
	out := make(chan sample, conc*2)
	var wg sync.WaitGroup
	var done atomic.Int64
	step := total / 5
	if step < 5 {
		step = 5
	}
	for i := 0; i < conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				res := c.chat(ctx, j, policy, maxTokens)
				out <- sample{
					Index: j.Index, Model: j.Model, Profile: j.Profile,
					Policy: policy, Replica: res.Replica, PolicyHeader: res.PolicyHeader,
					Status: res.Status, OK: res.OK, Error: res.Error,
					StartedAt: res.StartedAt, TTFTms: res.TTFTms, E2Ems: res.E2Ems,
					TTFTKnown: res.TTFTKnown, Streamed: res.Streamed,
					PromptTokens: res.PromptTokens, CompletionTokens: res.CompletionTokens,
					Failover: res.Failover,
				}
				if progress {
					n := done.Add(1)
					if n == int64(total) || n%int64(step) == 0 {
						fmt.Printf("\r  progress: %d/%d", n, total)
					}
				}
			}
		}()
	}
	go func() {
		defer close(jobCh)
		for _, j := range jobs {
			select {
			case jobCh <- j:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(out)
	}()
	samples := make([]sample, 0, len(jobs))
	for s := range out {
		samples = append(samples, s)
	}
	if progress && total > 0 {
		fmt.Println()
	}
	sort.SliceStable(samples, func(i, j int) bool { return samples[i].Index < samples[j].Index })
	return samples
}

func allFailed(samples []sample) bool {
	if len(samples) == 0 {
		return false
	}
	for _, s := range samples {
		if s.OK {
			return false
		}
	}
	return true
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
