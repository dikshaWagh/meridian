// Package health runs the leader's background checks: node lease expiry, the
// registry sweep, and the sync of local replica state into the registry.
// Remote replicas are not probed here. Their workers report state over the
// control channel.
package health

import (
	"context"
	"log"
	"time"

	"github.com/dikshaWagh/meridian/internal/registry"
)

// Config holds the timing and local-node settings for a Monitor.
type Config struct {
	LocalNodeID       string
	NodeTimeout       time.Duration
	HealthInterval    time.Duration
	DiscoveryInterval time.Duration
	// SyncLocal copies the leader's own replica state into the registry. It
	// runs on every health tick. It may be nil.
	SyncLocal func(now time.Time)
	// LocalStats samples the leader's own CPU and RAM. Without it the local
	// node would report zeroes on every tick. It may be nil.
	LocalStats func() registry.NodeStats
	Now        func() time.Time
}

// Monitor expires leases and sweeps stale nodes.
type Monitor struct {
	reg registry.Registry
	cfg Config
}

func New(reg registry.Registry, cfg Config) *Monitor {
	return &Monitor{reg: reg, cfg: cfg}
}

func (m *Monitor) now() time.Time {
	if m.cfg.Now != nil {
		return m.cfg.Now()
	}
	return time.Now()
}

// Tick runs one health pass: sync local replicas, refresh the local node, and
// mark remote nodes offline once their lease has expired.
func (m *Monitor) Tick(now time.Time) {
	if m.cfg.SyncLocal != nil {
		m.cfg.SyncLocal(now)
	}
	// The local node has no connection to lose, so it stays online. Sample its
	// real CPU and RAM when the caller provides a sampler; otherwise keep the
	// old heartbeat-only behaviour.
	local := registry.NodeStats{At: now}
	if m.cfg.LocalStats != nil {
		local = m.cfg.LocalStats()
		if local.At.IsZero() {
			local.At = now
		}
	}
	_ = m.reg.UpdateNodeStats(m.cfg.LocalNodeID, local)
	for _, n := range m.reg.Nodes() {
		if n.NodeID == m.cfg.LocalNodeID || n.State != registry.NodeOnline {
			continue
		}
		if now.Sub(n.LastHeartbeat) > m.cfg.NodeTimeout {
			m.reg.MarkNodeOffline(n.NodeID, now)
			log.Printf("node %s: no heartbeat for %s; marked offline", n.NodeID, m.cfg.NodeTimeout)
		}
	}
}

// Sweep removes remote nodes that have been silent for three node timeouts,
// along with their replicas. It returns the number removed.
func (m *Monitor) Sweep(now time.Time) int {
	removed := 0
	for _, n := range m.reg.Nodes() {
		if n.NodeID == m.cfg.LocalNodeID {
			continue
		}
		if now.Sub(n.LastHeartbeat) > 3*m.cfg.NodeTimeout {
			if err := m.reg.RemoveNode(n.NodeID); err == nil {
				removed++
				log.Printf("node %s: silent for over %s; removed from registry", n.NodeID, 3*m.cfg.NodeTimeout)
			}
		}
	}
	nodes := m.reg.Nodes()
	replicas := m.reg.Replicas()
	log.Printf("registry sweep: %d nodes, %d replicas, %d removed", len(nodes), len(replicas), removed)
	return removed
}

// Run drives Tick and Sweep until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	health := time.NewTicker(m.cfg.HealthInterval)
	defer health.Stop()
	sweep := time.NewTicker(m.cfg.DiscoveryInterval)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-health.C:
			m.Tick(m.now())
		case <-sweep.C:
			m.Sweep(m.now())
		}
	}
}
