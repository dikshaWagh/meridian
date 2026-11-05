// Package ml is Meridian's ML inference load balancer.
//
// It is deliberately isolated from the LLM balancer in internal/balancer: no
// worker state, routing state, health semantics or metrics are shared between
// the two subsystems. Small duplicated structs are preferred over abstractions
// that could change LLM behaviour.
package ml

import (
	"encoding/json"
	"time"
)

// PredictRequest is the body of POST /v1/predict. Input is forwarded verbatim;
// Meridian is an L7 reverse proxy and never interprets it.
type PredictRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"`
}

// PredictResponse is the ML worker contract for POST /predict. Meridian streams
// the worker body through untouched; this type exists for workers and tests.
type PredictResponse struct {
	Prediction any      `json:"prediction"`
	Confidence *float64 `json:"confidence,omitempty"`
	Model      string   `json:"model,omitempty"`
	Worker     string   `json:"worker,omitempty"`
}

// State is the ML worker health state. High load is never a state: a worker at
// 95% GPU is HEALTHY.
type State int32

const (
	StateStarting State = iota
	StateHealthy
	StateUnhealthy
	StateStopped
)

func (s State) String() string {
	switch s {
	case StateHealthy:
		return "HEALTHY"
	case StateUnhealthy:
		return "UNHEALTHY"
	case StateStopped:
		return "STOPPED"
	default:
		return "STARTING"
	}
}

// LatencyStats is measured end-to-end around the real proxy call.
type LatencyStats struct {
	Samples int     `json:"samples"`
	AvgMS   float64 `json:"avg_ms"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
	LastMS  float64 `json:"last_ms"`
}

// Telemetry holds host-measured resource usage for a worker. Availability flags
// are honest: when a signal cannot be measured it is reported as unavailable
// and scored as 0 rather than guessed.
type WorkerTelemetry struct {
	CPUPercent    float64 `json:"cpu_percent"`
	CPUAvailable  bool    `json:"cpu_available"`
	CPUSource     string  `json:"cpu_source,omitempty"`
	GPUPercent    float64 `json:"gpu_percent"`
	GPUMemPercent float64 `json:"gpu_mem_percent"`
	GPUAvailable  bool    `json:"gpu_available"`
	GPUSource     string  `json:"gpu_source,omitempty"`
}

// CandidateScore is one worker as seen by a single routing decision. Raw
// metrics and normalized scores are both kept so the TUI can show the algorithm.
type CandidateScore struct {
	WorkerID string `json:"worker_id"`
	State    string `json:"state"`
	Healthy  bool   `json:"healthy"`
	// Scored is false for candidates dropped by the health filter.
	Scored bool `json:"scored"`

	LatencyMS     float64 `json:"latency_ms"`
	InFlight      int64   `json:"in_flight"`
	CPUPercent    float64 `json:"cpu_percent"`
	CPUAvailable  bool    `json:"cpu_available"`
	GPUPercent    float64 `json:"gpu_percent"`
	GPUMemPercent float64 `json:"gpu_mem_percent"`
	GPUAvailable  bool    `json:"gpu_available"`

	LatencyScore float64 `json:"latency_score"`
	QueueScore   float64 `json:"queue_score"`
	GPUScore     float64 `json:"gpu_score"`
	CPUScore     float64 `json:"cpu_score"`
	FinalScore   float64 `json:"final_score"`

	Selected bool `json:"selected"`
}

// RoutingDecision is the full record of one two-stage routing pass.
type RoutingDecision struct {
	Model            string           `json:"model"`
	CandidateWorkers []string         `json:"candidate_workers"`
	HealthyWorkers   []string         `json:"healthy_workers"`
	SelectedWorker   string           `json:"selected_worker"`
	Candidates       []CandidateScore `json:"candidates"`

	LatencyScore float64 `json:"latency_score"`
	QueueScore   float64 `json:"queue_score"`
	GPUScore     float64 `json:"gpu_score"`
	CPUScore     float64 `json:"cpu_score"`
	FinalScore   float64 `json:"final_score"`

	Reason    string    `json:"reason"`
	Timestamp time.Time `json:"timestamp"`
}

// WorkerStatus is the JSON view of one ML worker.
type WorkerStatus struct {
	ID                  string          `json:"id"`
	Model               string          `json:"model"`
	URL                 string          `json:"url"`
	State               string          `json:"state"`
	Healthy             bool            `json:"healthy"`
	InFlight            int64           `json:"in_flight"`
	Served              int64           `json:"served"`
	Success             int64           `json:"success"`
	Failures            int64           `json:"failures"`
	SuccessRate         float64         `json:"success_rate"`
	Latency             LatencyStats    `json:"latency"`
	Telemetry           WorkerTelemetry `json:"telemetry"`
	ConsecutiveFailures int             `json:"consecutive_failures"`
	LastCheck           string          `json:"last_check"`
	LastError           string          `json:"last_error"`
}

// ModelStatus summarises one entry of the model -> workers map.
type ModelStatus struct {
	Model    string   `json:"model"`
	Workers  int      `json:"workers"`
	Healthy  int      `json:"healthy"`
	WorkerID []string `json:"worker_ids"`
}

// Totals aggregates request metrics across every ML worker.
type Totals struct {
	Served       int64   `json:"served"`
	Success      int64   `json:"success"`
	Failures     int64   `json:"failures"`
	InFlight     int64   `json:"in_flight"`
	SuccessRate  float64 `json:"success_rate"`
	AvgLatencyMS float64 `json:"avg_latency_ms"`
}

// RequestEvent is one recent /v1/predict request, with the routing decision
// that produced it. Used by the ML TUI event log.
type RequestEvent struct {
	Seq        int64            `json:"seq"`
	Time       string           `json:"time"`
	Model      string           `json:"model"`
	Candidates []CandidateScore `json:"candidates"`
	Selected   string           `json:"selected_worker"`
	Reason     string           `json:"reason"`
	Status     int              `json:"status"`
	LatencyMS  float64          `json:"latency_ms"`
	RoutingMS  float64          `json:"routing_ms"`
	Error      string           `json:"error,omitempty"`
}

// StatusResponse is served by GET /ml/status and consumed by the ML TUI.
type StatusResponse struct {
	Time    string         `json:"time"`
	Weights Weights        `json:"weights"`
	Models  []ModelStatus  `json:"models"`
	Workers []WorkerStatus `json:"workers"`
	Totals  Totals         `json:"totals"`
	Recent  []RequestEvent `json:"recent"`
	Host    HostStatus     `json:"host"`
}

// HostStatus reports what host telemetry Meridian could actually measure.
type HostStatus struct {
	CPUPercent   float64    `json:"cpu_percent"`
	CPUAvailable bool       `json:"cpu_available"`
	GPUAvailable bool       `json:"gpu_available"`
	GPUs         []GPUStats `json:"gpus"`
}

// GPUStats is one physical GPU as reported by the GPU sampler.
type GPUStats struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	UtilPercent float64 `json:"util_percent"`
	MemPercent  float64 `json:"mem_percent"`
}
