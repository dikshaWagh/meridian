package ml_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/ml"
)

// llmWorker is a minimal llama-server stand-in on a concrete port, because the
// LLM pool addresses workers by port.
func llmWorker(t *testing.T, id, model string) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, api.ChatResponse{
			ID: "llm-" + id, Object: "chat.completion", Model: model,
			Choices: []api.Choice{{Message: api.Message{Role: "assistant", Content: id}, FinishReason: "stop"}},
			Usage:   api.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2},
		})
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	return port, func() {
		sctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}
}

// mlWorker is a minimal ML inference worker with a fixed inference delay.
func mlWorker(t *testing.T, id, model string, delay time.Duration, hits *int64, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "model": model, "model_loaded": true})
	})
	mux.HandleFunc("POST /predict", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*hits++
		mu.Unlock()
		time.Sleep(delay)
		conf := 0.99
		api.WriteJSON(w, http.StatusOK, ml.PredictResponse{Prediction: "cat", Confidence: &conf, Model: model, Worker: id})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func waitHealthy(t *testing.T, pool *balancer.Pool, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		n := 0
		for _, w := range pool.Snapshot() {
			if w.Healthy {
				n++
			}
		}
		if n >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("only %v became healthy", pool.Snapshot())
}

// TestLLMAndMLRunSideBySide is the §13 integration case: both subsystems serve
// at the same time and share no routing state. The LLM balancer keeps its round
// robin; the ML balancer keeps its lowest-score selection.
func TestLLMAndMLRunSideBySide(t *testing.T) {
	// --- LLM subsystem (unchanged code path) ---
	p1, stop1 := llmWorker(t, "w1", "qwen2.5-3b")
	defer stop1()
	p2, stop2 := llmWorker(t, "w2", "qwen2.5-3b")
	defer stop2()
	llmPool := balancer.NewPool([]balancer.WorkerSpec{
		{ID: "w1", Model: "qwen2.5-3b", Port: p1},
		{ID: "w2", Model: "qwen2.5-3b", Port: p2},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	llmPool.StartHealthChecks(ctx)
	llmSrv := httptest.NewServer(balancer.NewHandler(llmPool))
	defer llmSrv.Close()
	waitHealthy(t, llmPool, 2)

	// --- ML subsystem (new, isolated) ---
	var mu sync.Mutex
	var fastHits, slowHits int64
	fast := mlWorker(t, "M1", "resnet50", 2*time.Millisecond, &fastHits, &mu)
	slow := mlWorker(t, "M2", "resnet50", 60*time.Millisecond, &slowHits, &mu)
	sub, err := ml.New(ml.Options{
		Workers: []ml.WorkerSpec{
			{ID: "M1", Model: "resnet50", URL: fast.URL, Enabled: true, GPUEnabled: true},
			{ID: "M2", Model: "resnet50", URL: slow.URL, Enabled: true, GPUEnabled: true},
		},
		Logger: log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("ml.New: %v", err)
	}
	sub.Health.CheckAll(ctx)
	mlSrv := httptest.NewServer(sub.Handler)
	defer mlSrv.Close()

	// --- drive both at once ---
	var wg sync.WaitGroup
	llmHits := map[string]int{}
	var llmMu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			body, _ := json.Marshal(api.ChatRequest{Model: "qwen2.5-3b", Messages: []api.Message{{Role: "user", Content: "hi"}}})
			resp, err := http.Post(llmSrv.URL+"/v1/chat/completions", "application/json", strings.NewReader(string(body)))
			if err != nil {
				t.Error(err)
				return
			}
			worker := resp.Header.Get("X-Meridian-Worker")
			resp.Body.Close()
			llmMu.Lock()
			llmHits[worker]++
			llmMu.Unlock()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			resp, err := http.Post(mlSrv.URL+"/v1/predict", "application/json",
				strings.NewReader(`{"model":"resnet50","input":{"image":"x"}}`))
			if err != nil {
				t.Error(err)
				return
			}
			if resp.StatusCode != http.StatusOK {
				t.Errorf("ml status = %d", resp.StatusCode)
			}
			resp.Body.Close()
		}
	}()
	wg.Wait()

	// LLM behaviour is unchanged: round robin spread evenly across w1/w2.
	if llmHits["w1"] != 10 || llmHits["w2"] != 10 {
		t.Fatalf("LLM round robin changed: %v", llmHits)
	}

	// ML behaviour is score based: the fast instance gets most of the traffic.
	mu.Lock()
	gotFast, gotSlow := fastHits, slowHits
	mu.Unlock()
	if gotFast+gotSlow != 20 {
		t.Fatalf("ml requests = %d fast + %d slow", gotFast, gotSlow)
	}
	if gotFast <= gotSlow {
		t.Fatalf("scoring did not favour the fast worker: fast=%d slow=%d", gotFast, gotSlow)
	}

	// No shared state: counters live in their own pools.
	var llmServed int64
	for _, w := range llmPool.Snapshot() {
		llmServed += w.Served
	}
	if llmServed != 20 {
		t.Fatalf("llm served = %d", llmServed)
	}
	if got := sub.Pool.Totals().Served; got != 20 {
		t.Fatalf("ml served = %d", got)
	}
	for _, w := range llmPool.Snapshot() {
		if w.ID == "M1" || w.ID == "M2" {
			t.Fatal("ML workers leaked into the LLM pool")
		}
	}
	for _, w := range sub.Pool.Status() {
		if w.ID == "w1" || w.ID == "w2" {
			t.Fatal("LLM workers leaked into the ML pool")
		}
	}
}

// TestMLFailureDoesNotAffectLLM covers the §15 demo: stopping an ML worker
// removes it from the ML candidate set and leaves the LLM balancer untouched.
func TestMLFailureDoesNotAffectLLM(t *testing.T) {
	port, stop := llmWorker(t, "w1", "qwen2.5-3b")
	defer stop()
	llmPool := balancer.NewPool([]balancer.WorkerSpec{{ID: "w1", Model: "qwen2.5-3b", Port: port}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	llmPool.StartHealthChecks(ctx)
	waitHealthy(t, llmPool, 1)

	var mu sync.Mutex
	var aHits, bHits int64
	a := mlWorker(t, "M1", "resnet50", time.Millisecond, &aHits, &mu)
	b := mlWorker(t, "M2", "resnet50", time.Millisecond, &bHits, &mu)
	sub, err := ml.New(ml.Options{
		Workers: []ml.WorkerSpec{
			{ID: "M1", Model: "resnet50", URL: a.URL, Enabled: true, GPUEnabled: true},
			{ID: "M2", Model: "resnet50", URL: b.URL, Enabled: true, GPUEnabled: true},
		},
		StartupGrace: time.Millisecond,
		Logger:       log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("ml.New: %v", err)
	}
	sub.Health.CheckAll(ctx)
	mlSrv := httptest.NewServer(sub.Handler)
	defer mlSrv.Close()

	// Stop one ML worker; health checks must drop it from the candidate set.
	b.Close()
	for i := 0; i < ml.DefaultFailureThreshold; i++ {
		sub.Health.CheckAll(ctx)
	}
	var unhealthy int
	for _, w := range sub.Pool.Status() {
		if w.ID == "M2" && w.State == ml.StateUnhealthy.String() {
			unhealthy++
		}
	}
	if unhealthy != 1 {
		t.Fatalf("stopped ML worker was not marked UNHEALTHY: %+v", sub.Pool.Status())
	}

	// Requests keep flowing through the surviving ML worker...
	for i := 0; i < 5; i++ {
		resp, err := http.Post(mlSrv.URL+"/v1/predict", "application/json",
			strings.NewReader(`{"model":"resnet50","input":1}`))
		if err != nil {
			t.Fatalf("predict: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		resp.Body.Close()
	}
	mu.Lock()
	gotA := aHits
	mu.Unlock()
	if gotA != 5 {
		t.Fatalf("surviving ML worker handled %d of 5", gotA)
	}

	// ...and the LLM worker is still healthy and untouched.
	snap := llmPool.Snapshot()
	if len(snap) != 1 || !snap[0].Healthy || snap[0].Served != 0 {
		t.Fatalf("LLM pool changed: %+v", snap)
	}
}

func TestMLSubsystemRequiresWorkers(t *testing.T) {
	if _, err := ml.New(ml.Options{Logger: log.New(io.Discard, "", 0)}); err == nil {
		t.Fatal("expected an error when no ML workers are configured")
	}
}

func ExampleWeights_Score() {
	fmt.Printf("%.3f\n", ml.DefaultWeights.Score(0.2, 0.4, 0.5, 0.3))
	// Output: 0.325
}
