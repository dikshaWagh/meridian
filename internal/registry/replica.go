package registry

import "time"

// ReplicaState is what the replica can do right now.
type ReplicaState string

const (
	ReplicaLoading     ReplicaState = "loading"
	ReplicaReady       ReplicaState = "ready"
	ReplicaDraining    ReplicaState = "draining"
	ReplicaUnhealthy   ReplicaState = "unhealthy"
	ReplicaStopped     ReplicaState = "stopped"
	ReplicaUnavailable ReplicaState = "unavailable"
)

// ReplicaRecord is one llama-server instance. A replica is unavailable while
// its node is offline; LastState keeps the state it had before that.
type ReplicaRecord struct {
	ReplicaID         string
	NodeID            string
	Model             string
	Instance          int
	ModelVersion      string
	Kind              string
	Runtime           string
	Endpoint          string
	ContextWindow     int
	MaxConcurrent     int
	State             ReplicaState
	LastState         ReplicaState
	PID               int
	Healthy           bool
	InFlight          int64
	QueueDepth        int64
	Served            int64
	EstTokensInFlight int64
	TokPerSec         float64
	LastCheck         time.Time
	LastError         string
}

// ReplicaStats is the per-replica counters reported with heartbeats.
type ReplicaStats struct {
	PID               int
	QueueDepth        int64
	Served            int64
	EstTokensInFlight int64
	TokPerSec         float64
	LastCheck         time.Time
	LastError         string
}
