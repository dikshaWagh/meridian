package balancer

import (
	"fmt"
	"strconv"
	"sync"

	"github.com/dikshaWagh/meridian/internal/api"
)

// Candidate is a load snapshot of one eligible replica, as the leader sees it.
type Candidate struct {
	ID        string
	Load      int64
	TokPerSec float64
}

// ClusterRouter applies the standalone routing policies to replicas chosen
// from the cluster registry. The policies are unchanged. Each Candidate is
// wrapped in a Worker that carries only the fields the policies read.
type ClusterRouter struct {
	mu       sync.Mutex
	policies map[string]RoutingPolicy
	order    []string
	def      string
}

// NewClusterRouter builds the router with every standalone policy. An empty
// default means round-robin.
func NewClusterRouter(defaultPolicy string) (*ClusterRouter, error) {
	c := &ClusterRouter{policies: map[string]RoutingPolicy{}}
	for _, p := range newRoutingPolicies() {
		c.policies[p.Name()] = p
		c.order = append(c.order, p.Name())
	}
	if defaultPolicy == "" {
		defaultPolicy = "round-robin"
	}
	if _, ok := c.policies[defaultPolicy]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownRoutingPolicy, defaultPolicy)
	}
	c.def = defaultPolicy
	return c, nil
}

func (c *ClusterRouter) Policies() []api.RoutingPolicyInfo {
	out := make([]api.RoutingPolicyInfo, 0, len(c.order))
	for _, name := range c.order {
		p := c.policies[name]
		out = append(out, api.RoutingPolicyInfo{Name: p.Name(), Description: p.Description()})
	}
	return out
}

func (c *ClusterRouter) Default() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.def
}

func (c *ClusterRouter) SetDefault(name string) error {
	if _, ok := c.policies[name]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownRoutingPolicy, name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.def = name
	return nil
}

// Resolve returns the policy name to use. An empty name means the default.
func (c *ClusterRouter) Resolve(name string) (string, error) {
	if name == "" {
		return c.Default(), nil
	}
	if _, ok := c.policies[name]; !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownRoutingPolicy, name)
	}
	return name, nil
}

// Pick returns the index into cands of the replica to use. req carries the
// request size for size-aware policies. cands must not be empty.
func (c *ClusterRouter) Pick(policy, model string, cands []Candidate, req RouteRequest) (int, error) {
	name, err := c.Resolve(policy)
	if err != nil {
		return -1, err
	}
	if len(cands) == 0 {
		return -1, ErrNoHealthyWorker
	}
	workers := make([]*Worker, len(cands))
	for i, cand := range cands {
		workers[i] = candidateWorker(i, cand)
	}

	p := c.policies[name]
	var w *Worker
	if sized, ok := p.(SizedPolicy); ok {
		w = sized.PickSized(model, workers, req)
	} else {
		w = p.Pick(model, workers)
	}
	if w == nil {
		// A policy with no valid estimate (for example, no measured throughput)
		// falls back to the least-loaded replica.
		w = (&LeastConnectionsPolicy{}).Pick(model, workers)
	}
	idx, err := strconv.Atoi(w.Spec.ID)
	if err != nil {
		return -1, err
	}
	return idx, nil
}

func candidateWorker(index int, cand Candidate) *Worker {
	w := &Worker{Spec: WorkerSpec{ID: strconv.Itoa(index)}}
	w.InFlight.Store(cand.Load)
	w.tokPerSec.Store(cand.TokPerSec)
	return w
}
