package balancer

import (
	"errors"
	"testing"
)

func TestClusterRouterRoundRobinRotates(t *testing.T) {
	r, _ := NewClusterRouter("round-robin")
	cands := []Candidate{{ID: "a", TokPerSec: 20}, {ID: "b", TokPerSec: 20}}
	var got []int
	for i := 0; i < 4; i++ {
		idx, err := r.Pick("round-robin", "m", cands, RouteRequest{})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, idx)
	}
	if got[0] == got[1] || got[0] != got[2] {
		t.Fatalf("round-robin order = %v", got)
	}
}

func TestClusterRouterLeastConnectionsPicksLightest(t *testing.T) {
	r, _ := NewClusterRouter("least-connections")
	cands := []Candidate{{ID: "busy", Load: 3, TokPerSec: 20}, {ID: "idle", Load: 0, TokPerSec: 20}}
	for i := 0; i < 5; i++ {
		idx, _ := r.Pick("least-connections", "m", cands, RouteRequest{})
		if idx != 1 {
			t.Fatalf("picked %d, want the idle replica (1)", idx)
		}
	}
}

func TestClusterRouterTokenAwarePrefersFasterIdleReplica(t *testing.T) {
	r, _ := NewClusterRouter("token-aware")
	cands := []Candidate{
		{ID: "slow", Load: 0, TokPerSec: 5},
		{ID: "fast", Load: 0, TokPerSec: 40},
	}
	idx, err := r.Pick("token-aware", "m", cands, RouteRequest{InputTokens: 100, OutputTokens: 128, AvgOutputTokens: 128})
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Fatalf("picked %d, want the fast replica (1)", idx)
	}
}

func TestClusterRouterTokenAwareFallsBackWithoutThroughput(t *testing.T) {
	r, _ := NewClusterRouter("token-aware")
	cands := []Candidate{{ID: "a", Load: 2, TokPerSec: 0}, {ID: "b", Load: 0, TokPerSec: 0}}
	idx, err := r.Pick("token-aware", "m", cands, RouteRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Fatalf("picked %d, want least-loaded fallback (1)", idx)
	}
}

func TestClusterRouterDefaultsAndUnknownPolicy(t *testing.T) {
	r, err := NewClusterRouter("")
	if err != nil || r.Default() != "round-robin" {
		t.Fatalf("default = %q, err = %v", r.Default(), err)
	}
	if _, err := NewClusterRouter("nope"); !errors.Is(err, ErrUnknownRoutingPolicy) {
		t.Fatalf("err = %v, want unknown policy", err)
	}
	if _, err := r.Pick("nope", "m", []Candidate{{ID: "a"}}, RouteRequest{}); !errors.Is(err, ErrUnknownRoutingPolicy) {
		t.Fatalf("pick err = %v", err)
	}
	if err := r.SetDefault("least-connections"); err != nil || r.Default() != "least-connections" {
		t.Fatalf("SetDefault failed: %v", err)
	}
	if len(r.Policies()) != 4 {
		t.Fatalf("policies = %d, want 4", len(r.Policies()))
	}
}
