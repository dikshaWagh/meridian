package control

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/dikshaWagh/meridian/internal/registry"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	b := NewBackoff(time.Second, 30*time.Second)
	b.rnd = func() float64 { return 0.5 } // jitter factor exactly 1.0
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	for i, w := range want {
		if got := b.Next(); got != w*time.Second {
			t.Fatalf("attempt %d: got %s, want %s", i, got, w*time.Second)
		}
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("after Reset: got %s, want 1s", got)
	}
}

func TestBackoffJitterBounds(t *testing.T) {
	for _, r := range []float64{0, 0.999} {
		b := NewBackoff(4*time.Second, time.Minute)
		b.rnd = func() float64 { return r }
		got := b.Next()
		lo, hi := 2*time.Second, 6*time.Second
		if got < lo || got >= hi {
			t.Fatalf("rnd=%v: %s outside [%s, %s)", r, got, lo, hi)
		}
	}
}

func TestBackoffJitterIsNotConstant(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		b := NewBackoff(time.Second, time.Minute)
		seen[b.Next()] = true
	}
	if len(seen) < 5 {
		t.Fatalf("only %d distinct first waits in 50 tries; jitter looks broken", len(seen))
	}
}

func TestBackoffNeverExceedsMaxEvenWithJitter(t *testing.T) {
	b := NewBackoff(time.Second, 2*time.Second)
	b.rnd = func() float64 { return 0.999 }
	for i := 0; i < 10; i++ {
		if got := b.Next(); got > 2*time.Second {
			t.Fatalf("attempt %d: %s exceeds max", i, got)
		}
	}
}

// workerFixture is a fake replica view that tests can change while the worker runs.
type workerFixture struct {
	mu    sync.Mutex
	state string
}

func (f *workerFixture) set(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = s
}

func (f *workerFixture) snapshot() WorkerSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return WorkerSnapshot{
		Hardware: HardwareInfo{CPUCores: 4, RAMTotalMB: 8192},
		Replicas: []ReplicaInfo{{ID: "llama-w-1", Model: "llama", Instance: 1, Kind: "llm", Runtime: "llama.cpp",
			Endpoint: "http://127.0.0.1:8080/replicas/llama-w-1", State: f.state}},
		Statuses: []ReplicaStatus{{ID: "llama-w-1", State: f.state, Served: 3, TokPerSec: 12.5}},
	}
}

func startWorker(t *testing.T, addr string, fix *workerFixture, hb time.Duration) (cancel func(), done chan struct{}) {
	t.Helper()
	w := NewWorker(WorkerConfig{
		NodeID:            "node-w",
		Secret:            secret,
		Address:           "127.0.0.1",
		HTTPPort:          8080,
		HeartbeatInterval: hb,
		MaxBackoff:        2 * time.Second,
		Snapshot:          fix.snapshot,
	})
	ctx, c := context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		w.Run(ctx, func(context.Context) (string, error) { return addr, nil })
		close(done)
	}()
	t.Cleanup(func() { c(); <-done })
	return c, done
}

func TestWorkerRegistersReportsStateAndDeregisters(t *testing.T) {
	tl := startLeader(t)
	fix := &workerFixture{state: "loading"}
	cancel, done := startWorker(t, tl.addr, fix, 100*time.Millisecond)

	tl.waitFor(func() bool {
		n, ok := tl.reg.Node("node-w")
		return ok && n.State == registry.NodeOnline
	})
	if rep, _ := tl.reg.Replica("llama-w-1"); rep.State != registry.ReplicaLoading || rep.Healthy {
		t.Fatalf("loading replica = %+v", rep)
	}

	fix.set("ready")
	tl.waitFor(func() bool {
		rep, _ := tl.reg.Replica("llama-w-1")
		return rep.State == registry.ReplicaReady && rep.Healthy
	})

	cancel()
	<-done
	tl.waitFor(func() bool {
		n, _ := tl.reg.Node("node-w")
		return n.State == registry.NodeOffline
	})
}

func TestWorkerReRegistersAfterLeaderRestart(t *testing.T) {
	first := startLeader(t)
	fix := &workerFixture{state: "ready"}
	startWorker(t, first.addr, fix, 100*time.Millisecond)
	first.waitFor(func() bool {
		n, ok := first.reg.Node("node-w")
		return ok && n.State == registry.NodeOnline
	})

	// Stop the leader. The worker must notice and fall back to discovery.
	first.stop()

	// Start a fresh leader (new registry) on the same address.
	reg2 := registry.New()
	ln, err := listenSameAddr(first.addr)
	if err != nil {
		t.Fatal(err)
	}
	l2 := NewLeader(LeaderConfig{NodeID: "leader-01", Secret: secret, LeaseSeconds: 15}, reg2)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { _ = l2.Serve(ctx2, ln); close(done2) }()
	t.Cleanup(func() { cancel2(); <-done2 })

	second := &testLeader{t: t, reg: reg2, addr: first.addr}
	second.waitFor(func() bool {
		n, ok := reg2.Node("node-w")
		return ok && n.State == registry.NodeOnline
	})
}

func listenSameAddr(addr string) (net.Listener, error) {
	var lastErr error
	for i := 0; i < 50; i++ {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	return nil, lastErr
}
