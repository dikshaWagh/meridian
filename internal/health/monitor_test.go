package health

import (
	"testing"
	"time"

	"github.com/dikshaWagh/meridian/internal/registry"
)

var t0 = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

const nodeTimeout = 15 * time.Second

func setup(t *testing.T) (registry.Registry, *Monitor) {
	t.Helper()
	reg := registry.New()
	local := registry.NodeRecord{NodeID: "leader-01", Address: "192.168.1.10", HTTPPort: 8080, State: registry.NodeOnline, LastHeartbeat: t0, RegisteredAt: t0}
	if err := reg.ReplaceNode(local, nil); err != nil {
		t.Fatal(err)
	}
	remote := registry.NodeRecord{NodeID: "node-a", Address: "192.168.1.11", HTTPPort: 8080, State: registry.NodeOnline, LastHeartbeat: t0, RegisteredAt: t0}
	rep := registry.ReplicaRecord{ReplicaID: "llama-a-1", Model: "llama", Instance: 1, Kind: "llm", Endpoint: "http://192.168.1.11:8080/replicas/llama-a-1", State: registry.ReplicaReady, MaxConcurrent: 2}
	if err := reg.ReplaceNode(remote, []registry.ReplicaRecord{rep}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, Config{LocalNodeID: "leader-01", NodeTimeout: nodeTimeout, HealthInterval: 2 * time.Second, DiscoveryInterval: 20 * time.Second})
	return reg, m
}

func TestMissingHeartbeatsMarkNodeAndReplicasOffline(t *testing.T) {
	reg, m := setup(t)

	m.Tick(t0.Add(nodeTimeout))
	if n, _ := reg.Node("node-a"); n.State != registry.NodeOnline {
		t.Fatalf("node offline at exactly node-timeout; must be strictly longer")
	}

	m.Tick(t0.Add(nodeTimeout + 2*time.Second))
	n, _ := reg.Node("node-a")
	if n.State != registry.NodeOffline {
		t.Fatalf("node state = %s, want offline", n.State)
	}
	rep, _ := reg.Replica("llama-a-1")
	if rep.State != registry.ReplicaUnavailable || rep.LastState != registry.ReplicaReady {
		t.Fatalf("replica = %+v", rep)
	}
}

func TestLocalNodeNeverExpires(t *testing.T) {
	reg, m := setup(t)
	m.Tick(t0.Add(time.Hour))
	n, _ := reg.Node("leader-01")
	if n.State != registry.NodeOnline {
		t.Fatalf("leader node state = %s, want online", n.State)
	}
}

func TestHeartbeatKeepsNodeOnline(t *testing.T) {
	reg, m := setup(t)
	_ = reg.UpdateNodeStats("node-a", registry.NodeStats{At: t0.Add(10 * time.Second)})
	m.Tick(t0.Add(20 * time.Second))
	if n, _ := reg.Node("node-a"); n.State != registry.NodeOnline {
		t.Fatalf("state = %s, want online after a recent heartbeat", n.State)
	}
}

func TestSweepRemovesNodeAfterThreeTimeouts(t *testing.T) {
	reg, m := setup(t)
	if removed := m.Sweep(t0.Add(3*nodeTimeout + time.Second)); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, ok := reg.Node("node-a"); ok {
		t.Fatal("stale node still registered")
	}
	if _, ok := reg.Replica("llama-a-1"); ok {
		t.Fatal("replica of removed node still registered")
	}
	if _, ok := reg.Node("leader-01"); !ok {
		t.Fatal("local node must never be swept")
	}
}

func TestSweepKeepsRecentNodes(t *testing.T) {
	_, m := setup(t)
	if removed := m.Sweep(t0.Add(2 * nodeTimeout)); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
}

func TestTickRunsLocalSync(t *testing.T) {
	reg, m := setup(t)
	called := false
	m.cfg.SyncLocal = func(time.Time) { called = true }
	m.Tick(t0)
	if !called {
		t.Fatal("SyncLocal not called")
	}
	_ = reg
}
