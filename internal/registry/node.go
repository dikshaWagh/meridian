package registry

import (
	"io"
	"slices"
	"time"
)

// NodeState is the liveness of a machine as seen by the leader.
type NodeState string

const (
	NodeOnline  NodeState = "online"
	NodeOffline NodeState = "offline"
)

// GPU describes one accelerator reported by a node.
type GPU struct {
	Name   string
	VRAMMB int64
}

// Hardware is the static description a node reports at registration.
type Hardware struct {
	CPUCores   int
	RAMTotalMB int64
	GPUs       []GPU
}

// NodeRecord is one machine. Its replicas are separate ReplicaRecords.
// ControlConn is nil for the leader's own node.
type NodeRecord struct {
	NodeID        string
	Address       string
	HTTPPort      int
	ControlConn   io.Closer
	State         NodeState
	LastHeartbeat time.Time
	Hardware      Hardware
	CPUPercent    float64
	RAMUsedMB     int64
	ReplicaIDs    []string
	RegisteredAt  time.Time
}

// NodeStats is the periodic report that refreshes a node's lease.
type NodeStats struct {
	At         time.Time
	CPUPercent float64
	RAMUsedMB  int64
}

func (n NodeRecord) clone() NodeRecord {
	n.ReplicaIDs = slices.Clone(n.ReplicaIDs)
	n.Hardware.GPUs = slices.Clone(n.Hardware.GPUs)
	return n
}
