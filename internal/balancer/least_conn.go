package balancer

import "math/rand"

// LeastConnectionsPolicy picks the worker with the fewest assigned requests.
// A request counts as assigned from the moment it is selected until it finishes.
type LeastConnectionsPolicy struct{}

func (p *LeastConnectionsPolicy) Name() string { return "least-connections" }
func (p *LeastConnectionsPolicy) Description() string {
	return "pick the worker with the fewest active requests"
}

func (p *LeastConnectionsPolicy) Pick(model string, healthy []*Worker) *Worker {
	best := healthy[0]
	bestLoad := best.Load()
	ties := []*Worker{best}

	for _, w := range healthy[1:] {
		load := w.Load()
		switch {
		case load < bestLoad:
			best, bestLoad = w, load
			ties = []*Worker{w}
		case load == bestLoad:
			ties = append(ties, w)
		}
	}
	return ties[rand.Intn(len(ties))]
}
