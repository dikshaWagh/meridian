package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
)

// RegisterPayload is the REGISTER body (§5.5).
type RegisterPayload struct {
	Address  string        `json:"address"`
	HTTPPort int           `json:"http_port"`
	Hardware HardwareInfo  `json:"hardware"`
	Replicas []ReplicaInfo `json:"replicas"`
}

type HardwareInfo struct {
	CPUCores   int       `json:"cpu_cores"`
	RAMTotalMB int64     `json:"ram_total_mb"`
	GPUs       []GPUInfo `json:"gpus"`
}

type GPUInfo struct {
	Name   string `json:"name"`
	VRAMMB int64  `json:"vram_mb"`
}

type ReplicaInfo struct {
	ID            string `json:"id"`
	Model         string `json:"model"`
	Instance      int    `json:"instance"`
	ModelVersion  string `json:"model_version"`
	Kind          string `json:"kind"`
	Runtime       string `json:"runtime"`
	Endpoint      string `json:"endpoint"`
	ContextWindow int    `json:"context_window"`
	MaxConcurrent int    `json:"max_concurrent"`
	State         string `json:"state"`
	PID           int    `json:"pid"`
}

// RegisterAckPayload is the REGISTER_ACK body.
type RegisterAckPayload struct {
	LeaseSeconds int `json:"lease_seconds"`
}

// RegisterRejectPayload is the REGISTER_REJECT body.
type RegisterRejectPayload struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}

// HeartbeatPayload is the HEARTBEAT body.
type HeartbeatPayload struct {
	CPUPercent float64         `json:"cpu_percent"`
	RAMUsedMB  int64           `json:"ram_used_mb"`
	Replicas   []ReplicaStatus `json:"replicas"`
}

// ReplicaStatus is one replica's live numbers, sent in HEARTBEAT and REPLICA_UPDATE.
type ReplicaStatus struct {
	ID                string  `json:"id"`
	State             string  `json:"state"`
	InFlight          int64   `json:"in_flight"`
	QueueDepth        int64   `json:"queue_depth"`
	Served            int64   `json:"served"`
	EstTokensInFlight int64   `json:"est_tokens_in_flight"`
	TokPerSec         float64 `json:"tok_per_sec"`
	LastError         string  `json:"last_error"`
	PID               int     `json:"pid"`
}

// DeregisterPayload is the DEREGISTER body.
type DeregisterPayload struct {
	Reason string `json:"reason,omitempty"`
}

// ErrorPayload is the ERROR body.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var replicaStates = map[string]bool{
	"loading": true, "ready": true, "draining": true, "unhealthy": true, "stopped": true,
}

// ValidReplicaState reports whether s is a state a worker may report.
func ValidReplicaState(s string) bool { return replicaStates[s] }

// DecodeStrict decodes a payload and rejects unknown fields, so a typo in a
// worker's payload is an error rather than silently ignored.
func DecodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return protoErr(ReasonInvalidPayload, "payload: %v", err)
	}
	return nil
}

// Validate checks every REGISTER field. It returns the first problem found.
func (p *RegisterPayload) Validate() error {
	if ip := net.ParseIP(p.Address); ip == nil {
		return protoErr(ReasonInvalidPayload, "address %q is not an IP address", p.Address)
	}
	if p.HTTPPort < 1 || p.HTTPPort > 65535 {
		return protoErr(ReasonInvalidPayload, "http_port %d is outside 1-65535", p.HTTPPort)
	}
	if p.Hardware.CPUCores < 0 || p.Hardware.RAMTotalMB < 0 {
		return protoErr(ReasonInvalidPayload, "hardware values must not be negative")
	}
	seen := map[string]bool{}
	for i, r := range p.Replicas {
		if err := r.validate(); err != nil {
			return protoErr(ReasonInvalidPayload, "replicas[%d]: %v", i, err)
		}
		if seen[r.ID] {
			return protoErr(ReasonInvalidPayload, "duplicate replica id %q", r.ID)
		}
		seen[r.ID] = true
	}
	return nil
}

func (r *ReplicaInfo) validate() error {
	if err := ValidID(r.ID); err != nil {
		return fmt.Errorf("id: %v", err)
	}
	if len(r.Model) == 0 || len(r.Model) > MaxIDLen {
		return fmt.Errorf("model must be 1-%d characters", MaxIDLen)
	}
	if r.Instance < 1 {
		return fmt.Errorf("instance must be at least 1")
	}
	if r.Kind != "llm" {
		return fmt.Errorf("kind %q must be llm", r.Kind)
	}
	if err := ValidEndpoint(r.Endpoint); err != nil {
		return fmt.Errorf("endpoint: %v", err)
	}
	if r.PID < 0 {
		return fmt.Errorf("pid must not be negative")
	}
	if r.ContextWindow < 0 || r.MaxConcurrent < 0 {
		return fmt.Errorf("context_window and max_concurrent must not be negative")
	}
	if !ValidReplicaState(r.State) {
		return fmt.Errorf("state %q is not a known replica state", r.State)
	}
	return nil
}

// Validate checks a HEARTBEAT body.
func (p *HeartbeatPayload) Validate() error {
	if p.CPUPercent < 0 || p.RAMUsedMB < 0 {
		return protoErr(ReasonInvalidPayload, "heartbeat stats are out of range")
	}
	for i, r := range p.Replicas {
		if err := r.validate(); err != nil {
			return protoErr(ReasonInvalidPayload, "replicas[%d]: %v", i, err)
		}
	}
	return nil
}

func (r *ReplicaStatus) validate() error {
	if err := ValidID(r.ID); err != nil {
		return fmt.Errorf("id: %v", err)
	}
	if !ValidReplicaState(r.State) {
		return fmt.Errorf("state %q is not a known replica state", r.State)
	}
	if r.PID < 0 {
		return fmt.Errorf("pid must not be negative")
	}
	if r.InFlight < 0 || r.QueueDepth < 0 || r.Served < 0 || r.EstTokensInFlight < 0 || r.TokPerSec < 0 {
		return fmt.Errorf("counters must not be negative")
	}
	return nil
}
