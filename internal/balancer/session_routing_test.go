package balancer

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

type mockServer struct {
	server   *httptest.Server
	healthy  atomic.Bool
	received []api.ChatRequest
	delay    time.Duration
	mu       sync.Mutex
}

func newMockWorker(id, model string) (*Worker, *mockServer) {
	ms := &mockServer{}
	ms.healthy.Store(true)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			if ms.healthy.Load() {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			return
		}

		if r.URL.Path == "/v1/chat/completions" {
			if ms.delay > 0 {
				time.Sleep(ms.delay)
			}

			var req api.ChatRequest
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &req)

			ms.mu.Lock()
			ms.received = append(ms.received, req)
			ms.mu.Unlock()

			last := ""
			if len(req.Messages) > 0 {
				last = req.Messages[len(req.Messages)-1].Content
			}

			resp := api.ChatResponse{
				ID:     "chatcmpl-test",
				Object: "chat.completion",
				Model:  model,
				Choices: []api.Choice{{
					Index: 0,
					Message: api.Message{
						Role:    "assistant",
						Content: "reply to: " + last,
					},
					FinishReason: "stop",
				}},
				Usage: api.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.NotFound(w, r)
	}))

	ms.server = ts
	u, _ := url.Parse(ts.URL)
	proxy := httputil.NewSingleHostReverseProxy(u)
	proxy.ModifyResponse = func(resp *http.Response) error {
		if usage, ok := resp.Request.Context().Value(usageKey{}).(*requestUsage); ok {
			return captureUsage(resp, usage)
		}
		return nil
	}

	worker := &Worker{
		Spec:      WorkerSpec{ID: id, Model: model, Port: 0},
		URL:       u,
		Proxy:     proxy,
		startedAt: time.Now(),
		grace:     10 * time.Second,
	}
	worker.setHealthy(true)
	return worker, ms
}

func setupTestPool(workers []*Worker) (*Pool, http.Handler) {
	pool := NewPool(nil)
	pool.workers = workers
	handler := NewHandler(pool)
	return pool, handler
}

func doRequest(handler http.Handler, method, path string, convID string, body interface{}) (*httptest.ResponseRecorder, api.ChatResponse, api.ErrorBody) {
	var bodyReader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		bodyReader = bytes.NewReader(b)
	}

	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	if convID != "" {
		req.Header.Set("X-Conversation-ID", convID)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var chatResp api.ChatResponse
	var errResp api.ErrorBody
	if rec.Code == http.StatusOK {
		_ = json.Unmarshal(rec.Body.Bytes(), &chatResp)
	} else {
		_ = json.Unmarshal(rec.Body.Bytes(), &errResp)
	}

	return rec, chatResp, errResp
}

// Acceptance Test 1: Two requests with the same ID hit the same worker and second answer uses context from first.
func TestAcceptance1_SessionAffinityAndHistory(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	w2, s2 := newMockWorker("w2", "m1")
	defer s2.server.Close()

	_, handler := setupTestPool([]*Worker{w1, w2})

	// Req 1
	rec1, resp1, _ := doRequest(handler, "POST", "/v1/chat/completions", "conv-1", api.ChatRequest{
		Model:    "m1",
		Messages: []api.Message{{Role: "user", Content: "Hello"}},
	})
	if rec1.Code != 200 {
		t.Fatalf("req1 status: %d", rec1.Code)
	}
	worker1 := rec1.Header().Get("X-Meridian-Worker")
	if worker1 == "" {
		t.Fatal("missing X-Meridian-Worker header")
	}
	if resp1.Choices[0].Message.Content != "reply to: Hello" {
		t.Fatalf("unexpected content: %s", resp1.Choices[0].Message.Content)
	}

	// Req 2
	rec2, resp2, _ := doRequest(handler, "POST", "/v1/chat/completions", "conv-1", api.ChatRequest{
		Model:    "m1",
		Messages: []api.Message{{Role: "user", Content: "Followup"}},
	})
	if rec2.Code != 200 {
		t.Fatalf("req2 status: %d", rec2.Code)
	}
	worker2 := rec2.Header().Get("X-Meridian-Worker")
	if worker2 != worker1 {
		t.Fatalf("expected sticky worker %s, got %s", worker1, worker2)
	}
	if resp2.Choices[0].Message.Content != "reply to: Followup" {
		t.Fatalf("unexpected content: %s", resp2.Choices[0].Message.Content)
	}

	// Check upstream worker received accumulated history
	targetServer := s1
	if worker1 == "w2" {
		targetServer = s2
	}
	targetServer.mu.Lock()
	defer targetServer.mu.Unlock()
	if len(targetServer.received) != 2 {
		t.Fatalf("expected 2 received requests, got %d", len(targetServer.received))
	}
	req2Upstream := targetServer.received[1]
	if len(req2Upstream.Messages) != 3 { // user(Hello), assistant(reply to: Hello), user(Followup)
		t.Fatalf("expected 3 messages in upstream prompt, got %d: %+v", len(req2Upstream.Messages), req2Upstream.Messages)
	}
	if req2Upstream.Messages[0].Content != "Hello" || req2Upstream.Messages[1].Content != "reply to: Hello" || req2Upstream.Messages[2].Content != "Followup" {
		t.Fatalf("history message sequence mismatch: %+v", req2Upstream.Messages)
	}
}

// Acceptance Test 2: Four new IDs against 2 workers alternate workers (RR preserved for new conversations).
func TestAcceptance2_RoundRobinForNewConversations(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	w2, s2 := newMockWorker("w2", "m1")
	defer s2.server.Close()

	_, handler := setupTestPool([]*Worker{w1, w2})

	r1, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "id1", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "1"}}})
	r2, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "id2", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "2"}}})
	r3, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "id3", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "3"}}})
	r4, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "id4", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "4"}}})

	wID1 := r1.Header().Get("X-Meridian-Worker")
	wID2 := r2.Header().Get("X-Meridian-Worker")
	wID3 := r3.Header().Get("X-Meridian-Worker")
	wID4 := r4.Header().Get("X-Meridian-Worker")

	if wID1 != "w1" || wID2 != "w2" || wID3 != "w1" || wID4 != "w2" {
		t.Fatalf("expected alternating w1, w2, w1, w2; got %s, %s, %s, %s", wID1, wID2, wID3, wID4)
	}

	// Sticky hit should NOT advance RR
	r1Sticky, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "id1", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "1-again"}}})
	if r1Sticky.Header().Get("X-Meridian-Worker") != "w1" {
		t.Fatalf("sticky hit failed")
	}

	// Next NEW conversation id5 should get w1 (RR counter = 4 % 2 = 0 -> w1)
	r5, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "id5", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "5"}}})
	if r5.Header().Get("X-Meridian-Worker") != "w1" {
		t.Fatalf("expected w1 for new conversation id5, got %s", r5.Header().Get("X-Meridian-Worker"))
	}
}

// Acceptance Test 3: Request without X-Conversation-ID behaves exactly as V1.
func TestAcceptance3_NoConversationID_V1Behaviour(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	w2, s2 := newMockWorker("w2", "m1")
	defer s2.server.Close()

	pool, handler := setupTestPool([]*Worker{w1, w2})

	r1, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "A"}}})
	r2, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "B"}}})

	if r1.Header().Get("X-Meridian-Worker") != "w1" || r2.Header().Get("X-Meridian-Worker") != "w2" {
		t.Fatalf("V1 requests did not round robin")
	}

	if pool.SessionStore().TotalCount() != 0 {
		t.Fatalf("expected 0 sessions created for V1 requests, got %d", pool.SessionStore().TotalCount())
	}
}

// Acceptance Test 4: Kill pinned worker; next request with that ID succeeds on another worker and answers with context.
func TestAcceptance4_PinnedWorkerDies_MigrationAndHistory(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	w2, s2 := newMockWorker("w2", "m1")
	defer s2.server.Close()

	_, handler := setupTestPool([]*Worker{w1, w2})

	// req 1 -> lands on w1
	r1, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "conv-fail", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "Msg1"}}})
	if r1.Header().Get("X-Meridian-Worker") != "w1" {
		t.Fatalf("expected w1, got %s", r1.Header().Get("X-Meridian-Worker"))
	}

	// Kill w1
	w1.setHealthy(false)

	// req 2 -> should move to w2 and send full history
	r2, resp2, _ := doRequest(handler, "POST", "/v1/chat/completions", "conv-fail", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "Msg2"}}})
	if r2.Code != 200 {
		t.Fatalf("req2 failed after worker kill: %d", r2.Code)
	}
	if r2.Header().Get("X-Meridian-Worker") != "w2" {
		t.Fatalf("expected move to w2, got %s", r2.Header().Get("X-Meridian-Worker"))
	}
	if resp2.Choices[0].Message.Content != "reply to: Msg2" {
		t.Fatalf("unexpected content: %s", resp2.Choices[0].Message.Content)
	}

	s2.mu.Lock()
	defer s2.mu.Unlock()
	if len(s2.received) != 1 {
		t.Fatalf("expected 1 request on w2, got %d", len(s2.received))
	}
	if len(s2.received[0].Messages) != 3 {
		t.Fatalf("expected 3 history messages sent to w2, got %d", len(s2.received[0].Messages))
	}
}

// Acceptance Test 5: Two TUIs running at once, both pinned to same worker, keep separate histories.
func TestAcceptance5_IsolatedHistoriesOnSameWorker(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()

	_, handler := setupTestPool([]*Worker{w1})

	doRequest(handler, "POST", "/v1/chat/completions", "tui-1", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "User1 Msg"}}})
	doRequest(handler, "POST", "/v1/chat/completions", "tui-2", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "User2 Msg"}}})

	doRequest(handler, "POST", "/v1/chat/completions", "tui-1", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "User1 Msg2"}}})

	s1.mu.Lock()
	defer s1.mu.Unlock()
	if len(s1.received) != 3 {
		t.Fatalf("expected 3 requests received, got %d", len(s1.received))
	}

	// The 3rd request was for tui-1, should contain only tui-1's history (3 messages: User1 Msg, reply, User1 Msg2)
	req3 := s1.received[2]
	if len(req3.Messages) != 3 {
		t.Fatalf("expected 3 messages for tui-1, got %d", len(req3.Messages))
	}
	if req3.Messages[0].Content != "User1 Msg" || req3.Messages[2].Content != "User1 Msg2" {
		t.Fatalf("tui-1 history contaminated: %+v", req3.Messages)
	}
}

// Acceptance Test 6: Concurrent duplicate-ID request returns 409; aborted request leaves history unchanged.
func TestAcceptance6_Concurrent409AndAbort(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	s1.delay = 100 * time.Millisecond

	pool, _ := setupTestPool([]*Worker{w1})
	handler := NewHandler(pool)

	// Test concurrent 409
	var wg sync.WaitGroup
	var code1, code2 int

	wg.Add(2)
	go func() {
		defer wg.Done()
		rec, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "dup-id", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "A"}}})
		code1 = rec.Code
	}()

	// Wait until the first request is actually proxying, so it holds inFlight.
	deadline := time.Now().Add(2 * time.Second)
	for w1.InFlight.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	go func() {
		defer wg.Done()
		rec, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "dup-id", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "B"}}})
		code2 = rec.Code
	}()

	wg.Wait()

	if !(code1 == 200 && code2 == 409) && !(code1 == 409 && code2 == 200) {
		t.Fatalf("expected one 200 and one 409, got code1=%d code2=%d", code1, code2)
	}

	// Verify history only has the successful request
	sess, ok := pool.SessionStore().Get("dup-id")
	if !ok {
		t.Fatal("session not found")
	}
	sess.mu.Lock()
	msgCount := len(sess.Messages)
	sess.mu.Unlock()

	if msgCount != 2 { // 1 user + 1 assistant
		t.Fatalf("expected 2 messages in history after 409 collision, got %d", msgCount)
	}
}

// Acceptance Test 7: /status shows per-worker session counts.
func TestAcceptance7_StatusSessionCounts(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	w2, s2 := newMockWorker("w2", "m1")
	defer s2.server.Close()

	_, handler := setupTestPool([]*Worker{w1, w2})

	doRequest(handler, "POST", "/v1/chat/completions", "s1", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "1"}}})
	doRequest(handler, "POST", "/v1/chat/completions", "s2", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "2"}}})
	doRequest(handler, "POST", "/v1/chat/completions", "s3", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "3"}}})

	rec, _, _ := doRequest(handler, "GET", "/status", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status code: %d", rec.Code)
	}

	var statusResp api.StatusResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &statusResp)

	var raw map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &raw)
	if _, ok := raw["sessionCount"]; !ok {
		t.Fatalf("status JSON missing top-level sessionCount: %s", rec.Body.String())
	}

	if statusResp.SessionCount != 3 {
		t.Fatalf("expected top level sessionCount 3, got %d", statusResp.SessionCount)
	}

	totalWorkerSessions := 0
	for _, w := range statusResp.Workers {
		totalWorkerSessions += w.Sessions
	}
	if totalWorkerSessions != 3 {
		t.Fatalf("expected sum of worker sessions to be 3, got %d", totalWorkerSessions)
	}
}

// Edge case: Same ID sent with different model returns 400 model_mismatch
func TestEdgeCase_ModelMismatch(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	w2, s2 := newMockWorker("w2", "m2")
	defer s2.server.Close()

	_, handler := setupTestPool([]*Worker{w1, w2})

	rec1, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "conv-mismatch", api.ChatRequest{Model: "m1", Messages: []api.Message{{Role: "user", Content: "1"}}})
	if rec1.Code != 200 {
		t.Fatalf("req1 failed: %d", rec1.Code)
	}

	rec2, _, err2 := doRequest(handler, "POST", "/v1/chat/completions", "conv-mismatch", api.ChatRequest{Model: "m2", Messages: []api.Message{{Role: "user", Content: "2"}}})
	if rec2.Code != 400 {
		t.Fatalf("expected status 400 for model mismatch, got %d", rec2.Code)
	}
	if err2.Error.Type != "model_mismatch" {
		t.Fatalf("expected error type model_mismatch, got %s", err2.Error.Type)
	}
}

// Edge case: Context limits 413 context_too_long
func TestEdgeCase_ContextTooLong(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()

	pool, handler := setupTestPool([]*Worker{w1})
	pool.SetContextAndParallel(30, 2) // perConvContext = 15

	// Large prompt that exceeds 15 tokens
	largeText := "This is a very long prompt that will easily exceed fifteen estimated tokens limit"
	rec, _, errResp := doRequest(handler, "POST", "/v1/chat/completions", "conv-ctx", api.ChatRequest{
		Model:    "m1",
		Messages: []api.Message{{Role: "user", Content: largeText}},
	})

	if rec.Code != 413 {
		t.Fatalf("expected 413 context_too_long, got status %d", rec.Code)
	}
	if errResp.Error.Type != "context_too_long" {
		t.Fatalf("expected error type context_too_long, got %s", errResp.Error.Type)
	}

	// Check history was unchanged
	sess, ok := pool.SessionStore().Get("conv-ctx")
	if ok {
		sess.mu.Lock()
		if len(sess.Messages) != 0 {
			t.Fatalf("expected empty history after 413, got %d messages", len(sess.Messages))
		}
		sess.mu.Unlock()
	}
}

// Session store eviction (LRU and Sweep)
func TestSessionStore_EvictionAndSweep(t *testing.T) {
	store := NewSessionStore()
	store.maxCap = 3
	store.ttl = 50 * time.Millisecond

	s1 := store.Create("s1", "m1", "w1")
	s1.LastUsed = time.Now().Add(-100 * time.Millisecond)

	s2 := store.Create("s2", "m1", "w1")
	s2.LastUsed = time.Now()

	s3 := store.Create("s3", "m1", "w1")
	s3.LastUsed = time.Now()

	// 4th session should evict s1 (oldest LastUsed)
	s4 := store.Create("s4", "m1", "w1")
	_ = s4

	if _, ok := store.Get("s1"); ok {
		t.Fatal("expected s1 to be evicted by LRU")
	}

	// Sweep test
	time.Sleep(60 * time.Millisecond)
	store.Sweep()

	if store.TotalCount() != 0 {
		t.Fatalf("expected all sessions swept after TTL, got %d", store.TotalCount())
	}
}

// Concurrent first requests for the same new ID must share one Session object.
func TestPickForSessionConcurrentNewIDIsAtomic(t *testing.T) {
	p := testPool()
	allHealthy(p)

	const n = 16
	sessions := make([]*Session, n)
	workers := make([]*Worker, n)
	errs := make([]error, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			workers[i], sessions[i], errs[i] = p.PickForSession("qwen-0.5b", "race-id")
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if sessions[i] != sessions[0] {
			t.Fatalf("goroutine %d got a different Session object", i)
		}
		if workers[i].Spec.ID != workers[0].Spec.ID {
			t.Fatalf("goroutine %d pinned %s, want %s", i, workers[i].Spec.ID, workers[0].Spec.ID)
		}
	}
	if p.SessionStore().TotalCount() != 1 {
		t.Fatalf("expected exactly 1 session, got %d", p.SessionStore().TotalCount())
	}
}

func TestSessionStoreCreateIfAbsent(t *testing.T) {
	store := NewSessionStore()

	const n = 32
	var wg sync.WaitGroup
	var created atomic.Int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := store.CreateIfAbsent("same", "m1", "w1"); ok {
				created.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := created.Load(); got != 1 {
		t.Fatalf("expected exactly 1 creation, got %d", got)
	}
	if got := store.TotalCount(); got != 1 {
		t.Fatalf("expected exactly 1 stored session, got %d", got)
	}
}
