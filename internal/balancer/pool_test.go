package balancer

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func testPool() *Pool {
	return NewPool([]WorkerSpec{
		{ID: "w1", Model: "qwen-0.5b", Port: 1},
		{ID: "w2", Model: "qwen-0.5b", Port: 2},
		{ID: "w3", Model: "tinyllama-1.1b", Port: 3},
		{ID: "w4", Model: "tinyllama-1.1b", Port: 4},
	})
}

func TestHealthStateMachine(t *testing.T) {
	p := testPool()
	w := p.Workers()[0]
	w.grace = time.Hour
	now := time.Now()
	if w.State() != "starting" {
		t.Fatalf("initial state: %s", w.State())
	}
	w.applyProbe(probeSuccess, now)
	if !w.IsHealthy() {
		t.Fatal("successful probe did not make worker healthy")
	}
	w.applyProbe(probeFailure, now)
	w.applyProbe(probeFailure, now)
	if !w.IsHealthy() {
		t.Fatal("worker went unhealthy too early")
	}
	w.applyProbe(probeFailure, now)
	if w.State() != "unhealthy" {
		t.Fatalf("state after third failure: %s", w.State())
	}
	w.applyProbe(probeSuccess, now)
	if !w.IsHealthy() {
		t.Fatal("worker did not recover")
	}
}

func TestExitedWorkerCannotRecover(t *testing.T) {
	w := testPool().Workers()[0]
	w.markExited()
	w.applyProbe(probeSuccess, time.Now())
	if w.State() != "unhealthy" || w.IsHealthy() {
		t.Fatal("exited worker recovered")
	}
}

func TestStartingGraceAndMessage(t *testing.T) {
	p := testPool()
	w := p.Workers()[0]
	w.grace = time.Second
	now := w.startedAt
	w.applyProbe(probeFailure, now.Add(500*time.Millisecond))
	if w.State() != "starting" {
		t.Fatal("worker left starting during grace")
	}
	w.applyProbe(probeFailure, now.Add(2*time.Second))
	if w.State() != "unhealthy" {
		t.Fatal("worker did not leave starting after grace")
	}
	if _, err := p.Pick("qwen-0.5b"); !errors.Is(err, ErrNoHealthyWorker) {
		t.Fatalf("Pick error: %v", err)
	}
}

func allHealthy(p *Pool) {
	for _, w := range p.Workers() {
		w.setHealthy(true)
	}
}

func pickID(t *testing.T, p *Pool, model string) string {
	t.Helper()
	w, err := p.Pick(model)
	if err != nil {
		t.Fatalf("Pick(%q): %v", model, err)
	}
	return w.Spec.ID
}

func TestPickUnknownModel(t *testing.T) {
	p := testPool()
	allHealthy(p)
	if _, err := p.Pick("gpt-9"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestPickNoHealthy(t *testing.T) {
	p := testPool()
	if _, err := p.Pick("qwen-0.5b"); !errors.Is(err, ErrNoHealthyWorker) {
		t.Fatalf("got %v", err)
	}
}

func TestRoundRobin(t *testing.T) {
	p := testPool()
	allHealthy(p)
	for i, want := range []string{"w1", "w2", "w1", "w2"} {
		if got := pickID(t, p, "qwen-0.5b"); got != want {
			t.Fatalf("pick %d: got %s want %s", i, got, want)
		}
	}
}

func TestSkipsUnhealthy(t *testing.T) {
	p := testPool()
	allHealthy(p)
	p.Workers()[0].setHealthy(false)
	for i := 0; i < 5; i++ {
		if got := pickID(t, p, "qwen-0.5b"); got != "w2" {
			t.Fatalf("got %s", got)
		}
	}
}

func TestCountersPerModel(t *testing.T) {
	p := testPool()
	allHealthy(p)
	pickID(t, p, "qwen-0.5b")
	pickID(t, p, "qwen-0.5b")
	pickID(t, p, "qwen-0.5b")
	if got := pickID(t, p, "tinyllama-1.1b"); got != "w3" {
		t.Fatalf("got %s want w3", got)
	}
}

func TestModels(t *testing.T) {
	p := testPool()
	if got := p.Models(); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	p.Workers()[2].setHealthy(true)
	p.Workers()[0].setHealthy(true)
	want := []string{"qwen-0.5b", "tinyllama-1.1b"}
	if got := p.Models(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
