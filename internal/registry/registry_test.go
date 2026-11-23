package registry

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

func node(id string) NodeRecord {
	return NodeRecord{NodeID: id, Address: "192.168.1.10", HTTPPort: 8080, State: NodeOnline, LastHeartbeat: t0, RegisteredAt: t0}
}

func replica(id, nodeID, model string, state ReplicaState) ReplicaRecord {
	return ReplicaRecord{
		ReplicaID: id, NodeID: nodeID, Model: model, Instance: 1, Kind: "llm", Runtime: "llama.cpp",
		Endpoint: "http://127.0.0.1:8100", ContextWindow: 4096, MaxConcurrent: 2, State: state,
	}
}

func TestReplaceNodeSetsNodeAndReplicas(t *testing.T) {
	r := New()
	if err := r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady), replica("a-2", "n1", "a", ReplicaLoading)}); err != nil {
		t.Fatal(err)
	}
	n, ok := r.Node("n1")
	if !ok || len(n.ReplicaIDs) != 2 {
		t.Fatalf("node = %+v ok=%v", n, ok)
	}
	if got := r.ReplicasOnNode("n1"); len(got) != 2 || !got[0].Healthy || got[1].Healthy {
		t.Fatalf("replicas on node = %+v", got)
	}
}

func TestReplaceNodeDropsReplicasNoLongerReported(t *testing.T) {
	r := New()
	_ = r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady), replica("a-2", "n1", "a", ReplicaReady)})
	_ = r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady)})
	if got := r.Replicas(); len(got) != 1 || got[0].ReplicaID != "a-1" {
		t.Fatalf("replicas = %+v", got)
	}
}

func TestReplicaIDCannotMoveBetweenNodes(t *testing.T) {
	r := New()
	_ = r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady)})
	err := r.ReplaceNode(node("n2"), []ReplicaRecord{replica("a-1", "n2", "a", ReplicaReady)})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

func TestReplaceNodeRejectsDuplicateReplicaIDs(t *testing.T) {
	r := New()
	err := r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady), replica("a-1", "n1", "a", ReplicaReady)})
	if err == nil {
		t.Fatal("expected duplicate replica error")
	}
}

func TestMarkNodeOfflineMarksReplicasUnavailableAndKeepsThem(t *testing.T) {
	r := New()
	_ = r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady)})
	r.MarkNodeOffline("n1", t0.Add(time.Minute))
	n, _ := r.Node("n1")
	if n.State != NodeOffline {
		t.Fatalf("node state = %s", n.State)
	}
	rep, ok := r.Replica("a-1")
	if !ok || rep.State != ReplicaUnavailable || rep.Healthy || rep.LastState != ReplicaReady {
		t.Fatalf("replica = %+v ok=%v", rep, ok)
	}
	// A second offline call must not overwrite the remembered state.
	r.MarkNodeOffline("n1", t0.Add(2*time.Minute))
	rep, _ = r.Replica("a-1")
	if rep.LastState != ReplicaReady {
		t.Fatalf("LastState overwritten: %s", rep.LastState)
	}
}

func TestNodeRecoversOnStats(t *testing.T) {
	r := New()
	_ = r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady)})
	r.MarkNodeOffline("n1", t0)
	if err := r.UpdateNodeStats("n1", NodeStats{At: t0.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	n, _ := r.Node("n1")
	if n.State != NodeOnline {
		t.Fatalf("state = %s, want online", n.State)
	}
}

func TestUpdateUnknownNodeIsNotFound(t *testing.T) {
	r := New()
	if err := r.UpdateNodeStats("nope", NodeStats{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestEligibleReplicasFilters(t *testing.T) {
	r := New()
	busy := replica("busy", "n1", "m", ReplicaReady)
	_ = r.ReplaceNode(node("n1"), []ReplicaRecord{
		replica("ready", "n1", "m", ReplicaReady),
		replica("loading", "n1", "m", ReplicaLoading),
		replica("unhealthy", "n1", "m", ReplicaUnhealthy),
		replica("draining", "n1", "m", ReplicaDraining),
		busy,
	})
	_ = r.UpdateReplicaStats("busy", ReplicaStats{InFlight: 2})
	got := r.EligibleReplicas("m")
	if len(got) != 1 || got[0].ReplicaID != "ready" {
		t.Fatalf("eligible = %+v", got)
	}
	if r.CountHealthy("m") != 2 {
		t.Fatalf("CountHealthy = %d, want 2 (ready + busy)", r.CountHealthy("m"))
	}
}

func TestEligibleExcludesUnavailableOnOfflineNode(t *testing.T) {
	r := New()
	_ = r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady)})
	r.MarkNodeOffline("n1", t0)
	if got := r.EligibleReplicas("a"); len(got) != 0 {
		t.Fatalf("eligible = %+v", got)
	}
}

func TestReadsReturnCopies(t *testing.T) {
	r := New()
	n := node("n1")
	n.Hardware.GPUs = []GPU{{Name: "gpu0", VRAMMB: 4096}}
	_ = r.ReplaceNode(n, []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady)})

	got := r.Nodes()
	got[0].ReplicaIDs[0] = "tampered"
	got[0].Hardware.GPUs[0].Name = "tampered"
	again, _ := r.Node("n1")
	if again.ReplicaIDs[0] != "a-1" || again.Hardware.GPUs[0].Name != "gpu0" {
		t.Fatalf("registry state changed through a returned copy: %+v", again)
	}

	rep := r.Replicas()
	rep[0].State = ReplicaUnhealthy
	stored, _ := r.Replica("a-1")
	if stored.State != ReplicaReady {
		t.Fatalf("replica state changed through a copy: %s", stored.State)
	}
}

func TestRemoveNodeRemovesReplicas(t *testing.T) {
	r := New()
	_ = r.ReplaceNode(node("n1"), []ReplicaRecord{replica("a-1", "n1", "a", ReplicaReady)})
	if err := r.RemoveNode("n1"); err != nil {
		t.Fatal(err)
	}
	if len(r.Replicas()) != 0 || len(r.Nodes()) != 0 {
		t.Fatal("node and replicas should be gone")
	}
	if err := r.RemoveNode("n1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second remove err = %v", err)
	}
}

func TestUpsertReplicaNeedsNode(t *testing.T) {
	r := New()
	if err := r.UpsertReplica(replica("a-1", "ghost", "a", ReplicaReady)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestConcurrentUpdatesAreRaceFree(t *testing.T) {
	r := New()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("n%d", i)
		_ = r.ReplaceNode(node(id), []ReplicaRecord{replica(id+"-1", id, "m", ReplicaReady)})
	}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("n%d", i)
		wg.Add(3)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = r.UpdateReplicaStats(id+"-1", ReplicaStats{InFlight: int64(j % 3)})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = r.UpdateReplicaState(id+"-1", ReplicaReady)
				_ = r.UpdateNodeStats(id, NodeStats{At: t0})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = r.EligibleReplicas("m")
				_ = r.Nodes()
			}
		}()
	}
	wg.Wait()
}
