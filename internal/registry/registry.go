// Package registry holds the cluster's nodes and replicas. It is the only
// source the leader schedules from. The store is in memory; the interface
// allows a persistent store later.
package registry

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("registry: not found")
	ErrConflict = errors.New("registry: replica id belongs to another node")
)

// Registry is the scheduling state. Reads return copies, never internal pointers.
type Registry interface {
	UpsertNode(n NodeRecord) error
	// ReplaceNode sets a node and its complete replica set in one step.
	// Replicas it no longer reports are removed.
	ReplaceNode(n NodeRecord, replicas []ReplicaRecord) error
	RemoveNode(nodeID string) error
	MarkNodeOffline(nodeID string, at time.Time)
	UpdateNodeStats(nodeID string, stats NodeStats) error

	UpsertReplica(r ReplicaRecord) error
	UpdateReplicaState(replicaID string, s ReplicaState) error
	UpdateReplicaStats(replicaID string, stats ReplicaStats) error
	// Reserve takes one in-flight slot on an eligible replica. It reports false
	// when the replica is no longer eligible. Release returns the slot.
	Reserve(replicaID string) bool
	Release(replicaID string, served bool)

	Node(nodeID string) (NodeRecord, bool)
	Replica(replicaID string) (ReplicaRecord, bool)
	Nodes() []NodeRecord
	Replicas() []ReplicaRecord
	ReplicasForModel(model string) []ReplicaRecord
	ReplicasOnNode(nodeID string) []ReplicaRecord
	EligibleReplicas(model string) []ReplicaRecord
	CountHealthy(model string) int
}

type memRegistry struct {
	mu       sync.RWMutex
	nodes    map[string]*NodeRecord
	replicas map[string]*ReplicaRecord
}

// New returns an empty in-memory registry.
func New() Registry {
	return &memRegistry{
		nodes:    map[string]*NodeRecord{},
		replicas: map[string]*ReplicaRecord{},
	}
}

func (m *memRegistry) UpsertNode(n NodeRecord) error {
	if n.NodeID == "" {
		return errors.New("registry: node id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.nodes[n.NodeID]; ok {
		n.ReplicaIDs = old.ReplicaIDs
	}
	n = n.clone()
	m.nodes[n.NodeID] = &n
	return nil
}

func (m *memRegistry) ReplaceNode(n NodeRecord, replicas []ReplicaRecord) error {
	if n.NodeID == "" {
		return errors.New("registry: node id is empty")
	}
	seen := make(map[string]bool, len(replicas))
	for _, r := range replicas {
		if r.ReplicaID == "" {
			return errors.New("registry: replica id is empty")
		}
		if seen[r.ReplicaID] {
			return fmt.Errorf("registry: duplicate replica id %q", r.ReplicaID)
		}
		seen[r.ReplicaID] = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range replicas {
		if old, ok := m.replicas[r.ReplicaID]; ok && old.NodeID != n.NodeID {
			return fmt.Errorf("%w: %s", ErrConflict, r.ReplicaID)
		}
	}
	for id, old := range m.replicas {
		if old.NodeID == n.NodeID && !seen[id] {
			delete(m.replicas, id)
		}
	}
	ids := make([]string, 0, len(replicas))
	for _, r := range replicas {
		r.NodeID = n.NodeID
		r.Healthy = r.State == ReplicaReady
		rc := r
		m.replicas[r.ReplicaID] = &rc
		ids = append(ids, r.ReplicaID)
	}
	n.ReplicaIDs = ids
	n = n.clone()
	m.nodes[n.NodeID] = &n
	return nil
}

func (m *memRegistry) RemoveNode(nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.nodes[nodeID]; !ok {
		return ErrNotFound
	}
	delete(m.nodes, nodeID)
	for id, r := range m.replicas {
		if r.NodeID == nodeID {
			delete(m.replicas, id)
		}
	}
	return nil
}

func (m *memRegistry) MarkNodeOffline(nodeID string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[nodeID]
	if !ok {
		return
	}
	n.State = NodeOffline
	n.ControlConn = nil
	for _, id := range n.ReplicaIDs {
		r, ok := m.replicas[id]
		if !ok || r.State == ReplicaUnavailable {
			continue
		}
		r.LastState = r.State
		r.State = ReplicaUnavailable
		r.Healthy = false
	}
}

func (m *memRegistry) UpdateNodeStats(nodeID string, stats NodeStats) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[nodeID]
	if !ok {
		return ErrNotFound
	}
	n.State = NodeOnline
	n.LastHeartbeat = stats.At
	n.CPUPercent = stats.CPUPercent
	n.RAMUsedMB = stats.RAMUsedMB
	return nil
}

func (m *memRegistry) UpsertReplica(r ReplicaRecord) error {
	if r.ReplicaID == "" {
		return errors.New("registry: replica id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	node, ok := m.nodes[r.NodeID]
	if !ok {
		return fmt.Errorf("%w: node %s", ErrNotFound, r.NodeID)
	}
	if old, ok := m.replicas[r.ReplicaID]; ok && old.NodeID != r.NodeID {
		return fmt.Errorf("%w: %s", ErrConflict, r.ReplicaID)
	}
	r.Healthy = r.State == ReplicaReady
	m.replicas[r.ReplicaID] = &r
	if !slices.Contains(node.ReplicaIDs, r.ReplicaID) {
		node.ReplicaIDs = append(node.ReplicaIDs, r.ReplicaID)
	}
	return nil
}

func (m *memRegistry) UpdateReplicaState(replicaID string, s ReplicaState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.replicas[replicaID]
	if !ok {
		return ErrNotFound
	}
	r.State = s
	r.Healthy = s == ReplicaReady
	return nil
}

func (m *memRegistry) UpdateReplicaStats(replicaID string, stats ReplicaStats) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.replicas[replicaID]
	if !ok {
		return ErrNotFound
	}
	r.PID = stats.PID
	r.QueueDepth = stats.QueueDepth
	r.Served = stats.Served
	r.EstTokensInFlight = stats.EstTokensInFlight
	r.TokPerSec = stats.TokPerSec
	r.LastCheck = stats.LastCheck
	r.LastError = stats.LastError
	return nil
}

func (m *memRegistry) Reserve(replicaID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.replicas[replicaID]
	if !ok || !m.eligible(*r) {
		return false
	}
	r.InFlight++
	return true
}

func (m *memRegistry) Release(replicaID string, served bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.replicas[replicaID]
	if !ok {
		return
	}
	if r.InFlight > 0 {
		r.InFlight--
	}
	if served {
		r.Served++
	}
}

// eligible reports whether r may take a new request. Callers hold m.mu.
func (m *memRegistry) eligible(r ReplicaRecord) bool {
	if r.State != ReplicaReady || !r.Healthy {
		return false
	}
	if n, ok := m.nodes[r.NodeID]; !ok || n.State != NodeOnline {
		return false
	}
	return r.MaxConcurrent <= 0 || r.InFlight < int64(r.MaxConcurrent)
}

func (m *memRegistry) Node(nodeID string) (NodeRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n, ok := m.nodes[nodeID]
	if !ok {
		return NodeRecord{}, false
	}
	return n.clone(), true
}

func (m *memRegistry) Replica(replicaID string) (ReplicaRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.replicas[replicaID]
	if !ok {
		return ReplicaRecord{}, false
	}
	return *r, true
}

func (m *memRegistry) Nodes() []NodeRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]NodeRecord, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

func (m *memRegistry) Replicas() []ReplicaRecord {
	return m.filterReplicas(func(ReplicaRecord) bool { return true })
}

func (m *memRegistry) ReplicasForModel(model string) []ReplicaRecord {
	return m.filterReplicas(func(r ReplicaRecord) bool { return r.Model == model })
}

func (m *memRegistry) ReplicasOnNode(nodeID string) []ReplicaRecord {
	return m.filterReplicas(func(r ReplicaRecord) bool { return r.NodeID == nodeID })
}

// EligibleReplicas returns replicas that may receive a new request: ready and
// healthy, on an online node, and below their max_concurrent limit.
func (m *memRegistry) EligibleReplicas(model string) []ReplicaRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ReplicaRecord
	for _, r := range m.replicas {
		if r.Model == model && m.eligible(*r) {
			out = append(out, *r)
		}
	}
	sortReplicas(out)
	return out
}

func (m *memRegistry) CountHealthy(model string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, r := range m.replicas {
		if r.Model == model && r.State == ReplicaReady && r.Healthy {
			n++
		}
	}
	return n
}

func (m *memRegistry) filterReplicas(keep func(ReplicaRecord) bool) []ReplicaRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []ReplicaRecord
	for _, r := range m.replicas {
		if keep(*r) {
			out = append(out, *r)
		}
	}
	sortReplicas(out)
	return out
}

func sortReplicas(rs []ReplicaRecord) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].ReplicaID < rs[j].ReplicaID })
}
