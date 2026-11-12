package balancer

import "sync"

// RoutingPolicy picks one healthy worker for a model.
//
// To add your own policy:
//  1. Create a type that implements this interface (a new file is fine).
//  2. Add it to newRoutingPolicies() below.
//
// The balancer guarantees that "healthy" is not empty when Pick is called.
//
// Example skeleton:
//
//	type RandomPolicy struct{}
//
//	func (p *RandomPolicy) Name() string        { return "random" }
//	func (p *RandomPolicy) Description() string { return "pick a random healthy worker" }
//	func (p *RandomPolicy) Pick(model string, healthy []*Worker) *Worker {
//		return healthy[rand.Intn(len(healthy))]
//	}
type RoutingPolicy interface {
	// Name is the unique id used by the server flag, request header and TUI.
	Name() string
	// Description is a short explanation shown by the TUI.
	Description() string
	// Pick returns the worker that should serve the request.
	Pick(model string, healthy []*Worker) *Worker
}

// RoundRobinPolicy sends each request to the next healthy worker.
// This is the default policy and preserves the balancer's original behavior.
type RoundRobinPolicy struct {
	mu   sync.Mutex
	next map[string]int
}

func NewRoundRobinPolicy() *RoundRobinPolicy {
	return &RoundRobinPolicy{next: map[string]int{}}
}

func (p *RoundRobinPolicy) Name() string        { return "round-robin" }
func (p *RoundRobinPolicy) Description() string { return "rotate through healthy workers in order" }

func (p *RoundRobinPolicy) Pick(model string, healthy []*Worker) *Worker {
	if len(healthy) == 0 {
		return nil
	}
	p.mu.Lock()
	idx := p.next[model] % len(healthy)
	p.next[model]++
	p.mu.Unlock()
	return healthy[idx]
}

// newRoutingPolicies lists every policy the balancer knows about.
// Add your own policy here (see the example on RoutingPolicy above).
func newRoutingPolicies() []RoutingPolicy {
	return []RoutingPolicy{
		NewRoundRobinPolicy(),
	}
}
