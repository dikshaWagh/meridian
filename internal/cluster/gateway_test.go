package cluster

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dikshaWagh/meridian/internal/balancer"
	"github.com/dikshaWagh/meridian/internal/control"
	"github.com/dikshaWagh/meridian/internal/registry"
)

const (
	testSecret = "test-secret"
	leaderID   = "leader-01"
	remoteID   = "node-b"
)

// fakeLlama stands in for llama-server. Its behaviour is set per test.
type fakeLlama struct {
	srv      *httptest.Server
	fail     atomic.Bool
	hits     atomic.Int64
	streamTo chan struct{} // if set, the stream waits for a signal after the first chunk
}

func newFakeLlama(t *testing.T) *fakeLlama {
	f := &fakeLlama{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		if f.fail.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		flusher.Flush()
		if f.streamTo != nil {
			<-f.streamTo
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLlama) port(t *testing.T) int {
	t.Helper()
	var p int
	fmt.Sscanf(f.srv.URL[strings.LastIndex(f.srv.URL, ":")+1:], "%d", &p)
	return p
}

type fixture struct {
	t       *testing.T
	reg     registry.Registry
	gateway *httptest.Server
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	router, err := balancer.NewClusterRouter("round-robin")
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.ReplaceNode(registry.NodeRecord{NodeID: leaderID, Address: "127.0.0.1", HTTPPort: 8080,
		State: registry.NodeOnline, LastHeartbeat: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	gw := httptest.NewServer(newGateway(reg, router, []byte(testSecret), leaderID).handler())
	t.Cleanup(gw.Close)
	return &fixture{t: t, reg: reg, gateway: gw}
}

// addLocal registers a replica on the leader's own node, pointing at a fake llama.
func (fx *fixture) addLocal(id, model string, llama *fakeLlama, state registry.ReplicaState) {
	fx.t.Helper()
	rep := registry.ReplicaRecord{ReplicaID: id, NodeID: leaderID, Model: model, Instance: 1, Kind: "llm",
		Endpoint: llama.srv.URL, ContextWindow: 4096, MaxConcurrent: 2, State: state, Healthy: state == registry.ReplicaReady, TokPerSec: 20}
	n, _ := fx.reg.Node(leaderID)
	ids := append(append([]registry.ReplicaRecord{}, fx.localReplicas(n.NodeID)...), rep)
	if err := fx.reg.ReplaceNode(n, ids); err != nil {
		fx.t.Fatal(err)
	}
}

func (fx *fixture) localReplicas(nodeID string) []registry.ReplicaRecord {
	return fx.reg.ReplicasOnNode(nodeID)
}

// addRemote runs a real worker handler (signed /replicas path) in front of a
// fake llama, and registers the replica the way the leader would see it.
func (fx *fixture) addRemote(secret string, llama *fakeLlama) (replicaID string, workerURL string) {
	fx.t.Helper()
	pool := balancer.NewPool([]balancer.WorkerSpec{{ID: "w1", Model: "remote-model", Port: llama.port(fx.t)}})
	worker := httptest.NewServer(workerMux(remoteID, []byte(secret), pool))
	fx.t.Cleanup(worker.Close)

	replicaID = "remote-model-node-b-1"
	rep := registry.ReplicaRecord{ReplicaID: replicaID, NodeID: remoteID, Model: "remote-model", Instance: 1, Kind: "llm",
		Endpoint: worker.URL + "/replicas/" + replicaID, ContextWindow: 4096, MaxConcurrent: 2,
		State: registry.ReplicaReady, Healthy: true, TokPerSec: 20}
	if err := fx.reg.ReplaceNode(registry.NodeRecord{NodeID: remoteID, Address: "127.0.0.1", HTTPPort: 8080,
		State: registry.NodeOnline, LastHeartbeat: time.Now()}, []registry.ReplicaRecord{rep}); err != nil {
		fx.t.Fatal(err)
	}
	return replicaID, worker.URL
}

func chatBody(model, content string, stream bool) []byte {
	b, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": content}},
		"stream":   stream,
	})
	return b
}

func (fx *fixture) chat(body []byte, headers map[string]string) *http.Response {
	fx.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, fx.gateway.URL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fx.t.Fatal(err)
	}
	return resp
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestChatStreamsThroughRemoteReplica(t *testing.T) {
	fx := newFixture(t)
	llama := newFakeLlama(t)
	id, _ := fx.addRemote(testSecret, llama)

	resp := fx.chat(chatBody("remote-model", "hi", true), nil)
	body := readAll(t, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Meridian-Worker"); got != id {
		t.Fatalf("X-Meridian-Worker = %q, want %q", got, id)
	}
	if !strings.Contains(body, "hel") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("stream not relayed: %q", body)
	}
	if llama.hits.Load() != 1 {
		t.Fatalf("llama hits = %d", llama.hits.Load())
	}
	rep, _ := fx.reg.Replica(id)
	if rep.Served != 1 || rep.InFlight != 0 {
		t.Fatalf("after request: served=%d in_flight=%d", rep.Served, rep.InFlight)
	}
}

func TestFirstChunkReachesClientBeforeGenerationEnds(t *testing.T) {
	fx := newFixture(t)
	llama := newFakeLlama(t)
	llama.streamTo = make(chan struct{})
	fx.addLocal("m-1", "m", llama, registry.ReplicaReady)

	resp := fx.chat(chatBody("m", "hi", true), nil)
	defer resp.Body.Close()
	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if !strings.Contains(line, "hel") {
			t.Fatalf("first line = %q", line)
		}
	case <-time.After(3 * time.Second):
		close(llama.streamTo)
		t.Fatal("first chunk was buffered until the generation finished")
	}
	close(llama.streamTo)
}

func TestRetryOnAnotherReplicaBeforeAnyBytes(t *testing.T) {
	fx := newFixture(t)
	bad := newFakeLlama(t)
	bad.fail.Store(true)
	good := newFakeLlama(t)
	fx.addLocal("m-bad", "m", bad, registry.ReplicaReady)
	fx.addLocal("m-good", "m", good, registry.ReplicaReady)

	for i := 0; i < 3; i++ {
		resp := fx.chat(chatBody("m", "hi", false), nil)
		body := readAll(t, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "hello") {
			t.Fatalf("attempt %d: status %d body %q", i, resp.StatusCode, body)
		}
		if resp.Header.Get("X-Meridian-Worker") != "m-good" {
			t.Fatalf("attempt %d served by %q", i, resp.Header.Get("X-Meridian-Worker"))
		}
	}
	// Round-robin tried the bad replica at least once, and that try was retried.
	if bad.hits.Load() == 0 {
		t.Fatal("bad replica was never tried, so the retry path was not exercised")
	}
}

func TestErrorResponses(t *testing.T) {
	fx := newFixture(t)
	llama := newFakeLlama(t)
	fx.addLocal("m-1", "m", llama, registry.ReplicaLoading)

	cases := []struct {
		name    string
		body    []byte
		headers map[string]string
		status  int
		errType string
	}{
		{"unknown model", chatBody("nope", "hi", false), nil, 404, "model_not_found"},
		{"no ready replica", chatBody("m", "hi", false), nil, 503, "no_healthy_worker"},
		{"unknown policy", chatBody("m", "hi", false), map[string]string{"X-Meridian-Routing-Policy": "nope"}, 400, "unknown_routing_policy"},
		{"missing model", []byte(`{"messages":[]}`), nil, 400, "invalid_request"},
	}
	for _, c := range cases {
		resp := fx.chat(c.body, c.headers)
		body := readAll(t, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.status || !strings.Contains(body, c.errType) {
			t.Errorf("%s: status %d body %s; want %d %s", c.name, resp.StatusCode, body, c.status, c.errType)
		}
	}
}

func TestContextTooLongIsRejected(t *testing.T) {
	fx := newFixture(t)
	llama := newFakeLlama(t)
	fx.addLocal("m-1", "m", llama, registry.ReplicaReady)
	// Slot context is 4096/2 = 2048 tokens; this prompt is far larger.
	resp := fx.chat(chatBody("m", strings.Repeat("word ", 20000), false), nil)
	body := readAll(t, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge || !strings.Contains(body, "context_too_long") {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if llama.hits.Load() != 0 {
		t.Fatal("oversized request reached a replica")
	}
}

func TestWorkerRejectsUnsignedRequest(t *testing.T) {
	fx := newFixture(t)
	llama := newFakeLlama(t)
	_, workerURL := fx.addRemote(testSecret, llama)
	resp, err := http.Post(workerURL+"/replicas/remote-model-node-b-1/v1/chat/completions",
		"application/json", bytes.NewReader(chatBody("remote-model", "hi", false)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unsigned request got %d, want 403", resp.StatusCode)
	}
	if llama.hits.Load() != 0 {
		t.Fatal("unsigned request reached the replica")
	}
}

func TestWorkerRejectsWrongSecretAndTamperedBody(t *testing.T) {
	fx := newFixture(t)
	llama := newFakeLlama(t)
	_, workerURL := fx.addRemote(testSecret, llama)
	path := "/replicas/remote-model-node-b-1/v1/chat/completions"
	signed := chatBody("remote-model", "hi", false)
	ts, sig := control.SignForward([]byte("other-secret"), http.MethodPost, path, signed, time.Now())

	req, _ := http.NewRequest(http.MethodPost, workerURL+path, bytes.NewReader(signed))
	req.Header.Set(control.HeaderTimestamp, ts)
	req.Header.Set(control.HeaderSignature, sig)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong secret got %d", resp.StatusCode)
	}

	ts, sig = control.SignForward([]byte(testSecret), http.MethodPost, path, signed, time.Now())
	tampered := chatBody("remote-model", "changed", false)
	req, _ = http.NewRequest(http.MethodPost, workerURL+path, bytes.NewReader(tampered))
	req.Header.Set(control.HeaderTimestamp, ts)
	req.Header.Set(control.HeaderSignature, sig)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tampered body got %d", resp.StatusCode)
	}
}

func TestCatalogAndModelsReflectRegistry(t *testing.T) {
	fx := newFixture(t)
	fx.addLocal("m-1", "m", newFakeLlama(t), registry.ReplicaReady)
	resp, err := http.Get(fx.gateway.URL + "/v1/catalog")
	if err != nil {
		t.Fatal(err)
	}
	var cat struct {
		LLM []struct {
			ID             string `json:"id"`
			HealthyWorkers int    `json:"healthy_workers"`
		} `json:"llm"`
		DefaultRoutingPolicy string `json:"default_routing_policy"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cat); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(cat.LLM) != 1 || cat.LLM[0].ID != "m" || cat.LLM[0].HealthyWorkers != 1 || cat.DefaultRoutingPolicy != "round-robin" {
		t.Fatalf("catalog = %+v", cat)
	}
}
