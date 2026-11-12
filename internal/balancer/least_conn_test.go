package balancer

import (
	"strings"
	"testing"

	"github.com/dikshaWagh/meridian/internal/api"
)

func TestLeastConnectionsPicksLowestLoad(t *testing.T) {
	w1 := &Worker{Spec: WorkerSpec{ID: "w1"}}
	w2 := &Worker{Spec: WorkerSpec{ID: "w2"}}
	w1.InFlight.Store(5)
	w2.Reserved.Store(1)

	got := (&LeastConnectionsPolicy{}).Pick("m", []*Worker{w1, w2})
	if got != w2 {
		t.Fatalf("picked %s, want w2", got.Spec.ID)
	}
}

func TestLeastConnectionsBreaksTies(t *testing.T) {
	w1 := &Worker{Spec: WorkerSpec{ID: "w1"}}
	w2 := &Worker{Spec: WorkerSpec{ID: "w2"}}
	policy := &LeastConnectionsPolicy{}

	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		seen[policy.Pick("m", []*Worker{w1, w2}).Spec.ID] = true
	}
	if !seen["w1"] || !seen["w2"] {
		t.Fatalf("tie-break did not use both workers: %v", seen)
	}
}

func TestLeastConnectionsReservedPicksDistribute(t *testing.T) {
	p := testPool()
	allHealthy(p)

	counts := map[string]int{}
	var releases []func()
	for i := 0; i < 20; i++ {
		w, _, r, err := p.PickForSessionWithPolicyReserved("qwen-0.5b", "", "least-connections")
		if err != nil {
			t.Fatal(err)
		}
		counts[w.Spec.ID]++
		releases = append(releases, r.Release)
	}

	if counts["w1"] == 0 || counts["w2"] == 0 {
		t.Fatalf("reserved picks did not distribute: %v", counts)
	}

	for _, release := range releases {
		release()
	}
	for _, w := range p.Workers() {
		if w.Load() != 0 {
			t.Fatalf("worker %s leaked load %d", w.Spec.ID, w.Load())
		}
	}
}

func TestInflightReleasedAfterRequest(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	_, handler := setupTestPool([]*Worker{w1})

	doRequest(handler, "POST", "/v1/chat/completions", "", api.ChatRequest{
		Model:    "m1",
		Messages: []api.Message{{Role: "user", Content: "hi"}},
	})

	if got := w1.InFlight.Load(); got != 0 {
		t.Fatalf("InFlight leaked after request: %d", got)
	}
	if got := w1.Reserved.Load(); got != 0 {
		t.Fatalf("Reserved leaked after request: %d", got)
	}
}

func TestReservationReleasedOnRejectedRequest(t *testing.T) {
	w1, s1 := newMockWorker("w1", "m1")
	defer s1.server.Close()
	_, handler := setupTestPool([]*Worker{w1})

	rec, _, _ := doRequest(handler, "POST", "/v1/chat/completions", "too-long", api.ChatRequest{
		Model:    "m1",
		Messages: []api.Message{{Role: "user", Content: strings.Repeat("x", 20000)}},
	})
	if rec.Code != 413 {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	if got := w1.Load(); got != 0 {
		t.Fatalf("worker load leaked after rejected request: %d", got)
	}
}
