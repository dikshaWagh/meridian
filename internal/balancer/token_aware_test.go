package balancer

import (
	"errors"
	"testing"
)

func taWorker(id string, tokPerSec float64, inFlight int64) *Worker {
	w := &Worker{Spec: WorkerSpec{ID: id, Model: "m"}}
	w.tokPerSec.Store(tokPerSec)
	w.InFlight.Store(inFlight)
	return w
}

func TestTokenAwarePrefersLowestCompletionTime(t *testing.T) {
	slow := taWorker("slow", 10, 0)
	fast := taWorker("fast", 50, 0)
	req := RouteRequest{InputTokens: 100, OutputTokens: 100, AvgOutputTokens: 100}
	if got := (&TokenAwarePolicy{}).PickSized("m", []*Worker{slow, fast}, req); got.Spec.ID != "fast" {
		t.Fatalf("expected fast, got %s", got.Spec.ID)
	}
}

func TestTokenAwareCountsQueuedWork(t *testing.T) {
	// fast worker: (10*200 + 200)/50 = 44s. slow worker: 200/20 = 10s.
	busyFast := taWorker("busy-fast", 50, 10)
	idleSlow := taWorker("idle-slow", 20, 0)
	req := RouteRequest{InputTokens: 100, OutputTokens: 100, AvgOutputTokens: 100}
	if got := (&TokenAwarePolicy{}).PickSized("m", []*Worker{busyFast, idleSlow}, req); got.Spec.ID != "idle-slow" {
		t.Fatalf("expected idle-slow, got %s", got.Spec.ID)
	}
}

func TestTokenAwareSkipsInvalidThroughput(t *testing.T) {
	zero := taWorker("zero", 0, 0)
	ok := taWorker("ok", 5, 0)
	req := RouteRequest{InputTokens: 10, OutputTokens: 10, AvgOutputTokens: 10}
	if got := (&TokenAwarePolicy{}).PickSized("m", []*Worker{zero, ok}, req); got.Spec.ID != "ok" {
		t.Fatalf("expected ok, got %s", got.Spec.ID)
	}
	if got := (&TokenAwarePolicy{}).PickSized("m", []*Worker{zero}, req); got != nil {
		t.Fatalf("expected nil when no worker has throughput, got %s", got.Spec.ID)
	}
}

func TestTokenAwarePoolRejectsOversizedRequest(t *testing.T) {
	p := NewPool([]WorkerSpec{{ID: "w1", Model: "m", Port: 9001}, {ID: "w2", Model: "m", Port: 9002}})
	p.SetContextAndParallel(4096, 2) // 2048 tokens per slot
	for _, w := range p.Workers() {
		w.state.Store(StateHealthy)
	}
	route := RouteRequest{InputTokens: 3000, OutputTokens: 128}
	_, _, _, err := p.PickForSessionWithRouteReserved("m", "", "token-aware", route)
	if !errors.Is(err, ErrContextTooLong) {
		t.Fatalf("expected ErrContextTooLong, got %v", err)
	}
}

func TestTokenAwarePoolUsesHistoricalOutput(t *testing.T) {
	p := NewPool([]WorkerSpec{{ID: "w1", Model: "m", Port: 9001}})
	p.recordOutput("m", 100)
	p.recordOutput("m", 300)
	if got := p.averageOutput("m"); got != 200 {
		t.Fatalf("expected average 200, got %v", got)
	}
	if got := p.averageOutput("other"); got != 128 {
		t.Fatalf("expected default 128 for unseen model, got %v", got)
	}
}
