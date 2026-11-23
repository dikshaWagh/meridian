package cluster

import "sync"

// RouteEvent is one step of a request's path through the cluster. The admin
// console lights up the leader → node → replica path from these.
type RouteEvent struct {
	Kind      string `json:"kind"`
	At        string `json:"at"`
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
	Policy    string `json:"policy"`
	NodeID    string `json:"node_id"`
	ReplicaID string `json:"replica_id"`
	// Phase is routing (picked, forwarding), done, retry (moving to another
	// replica) or failed.
	Phase   string `json:"phase"`
	Attempt int    `json:"attempt"`
}

// hub fans events out to admin subscribers. Publish never blocks: a
// subscriber that falls behind misses events instead of slowing routing.
type hub struct {
	mu   sync.Mutex
	subs map[chan RouteEvent]struct{}
}

func newHub() *hub {
	return &hub{subs: map[chan RouteEvent]struct{}{}}
}

func (h *hub) subscribe() (<-chan RouteEvent, func()) {
	ch := make(chan RouteEvent, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *hub) publish(e RouteEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}
