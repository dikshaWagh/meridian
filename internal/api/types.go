// Package api holds the JSON shapes shared by the balancer, llama workers and CLI.
package api

import (
	"encoding/json"
	"net/http"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Stream    bool      `json:"stream,omitempty"`
}

type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type ModelInfo struct {
	ID     string `json:"id"`
	Object string `json:"object"`
}

type ModelList struct {
	Object string      `json:"object"`
	Data   []ModelInfo `json:"data"`
}

type WorkerStatus struct {
	ID                  string  `json:"id"`
	Model               string  `json:"model"`
	Port                int     `json:"port"`
	PID                 int     `json:"pid"`
	State               string  `json:"state"`
	Healthy             bool    `json:"healthy"`
	InFlight            int64   `json:"in_flight"`
	Served              int64   `json:"served"`
	EstTokensInFlight   int64   `json:"est_tokens_in_flight"`
	TokPerSec           float64 `json:"tok_per_sec"`
	ConsecutiveFailures int     `json:"consecutive_failures"`
	LastCheck           string  `json:"last_check"`
	LastError           string  `json:"last_error"`
	Sessions            int     `json:"sessions"`
}

type StatusResponse struct {
	Workers      []WorkerStatus `json:"workers"`
	SessionCount int            `json:"sessionCount"`
}

// NodeStatus is one machine in the cluster, as served by GET /v1/status.
type NodeStatus struct {
	NodeID        string   `json:"node_id"`
	Address       string   `json:"address"`
	HTTPPort      int      `json:"http_port"`
	Local         bool     `json:"local"`
	State         string   `json:"state"`
	CPUCores      int      `json:"cpu_cores"`
	RAMTotalMB    int64    `json:"ram_total_mb"`
	CPUPercent    float64  `json:"cpu_percent"`
	RAMUsedMB     int64    `json:"ram_used_mb"`
	LastHeartbeat string   `json:"last_heartbeat"`
	RegisteredAt  string   `json:"registered_at"`
	ReplicaIDs    []string `json:"replica_ids"`
}

// ReplicaStatus is one replica in GET /v1/status. State is "unavailable" while
// its node is offline; LastState keeps the state it had before.
type ReplicaStatus struct {
	ID                string  `json:"id"`
	NodeID            string  `json:"node_id"`
	Model             string  `json:"model"`
	Instance          int     `json:"instance"`
	ModelVersion      string  `json:"model_version,omitempty"`
	Kind              string  `json:"kind"`
	Runtime           string  `json:"runtime"`
	Endpoint          string  `json:"endpoint"`
	ContextWindow     int     `json:"context_window"`
	MaxConcurrent     int     `json:"max_concurrent"`
	State             string  `json:"state"`
	LastState         string  `json:"last_state,omitempty"`
	Healthy           bool    `json:"healthy"`
	InFlight          int64   `json:"in_flight"`
	QueueDepth        int64   `json:"queue_depth"`
	Served            int64   `json:"served"`
	EstTokensInFlight int64   `json:"est_tokens_in_flight"`
	TokPerSec         float64 `json:"tok_per_sec"`
	LastCheck         string  `json:"last_check"`
	LastError         string  `json:"last_error"`
}

// ClusterStatusResponse is the body of GET /v1/status on the leader.
type ClusterStatusResponse struct {
	LeaderID string          `json:"leader_id"`
	Nodes    []NodeStatus    `json:"nodes"`
	Replicas []ReplicaStatus `json:"replicas"`
}

type RoutingPolicyInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RoutingPolicyList struct {
	Default  string              `json:"default"`
	Policies []RoutingPolicyInfo `json:"policies"`
}

// CatalogModel is one selectable model in GET /v1/catalog.
type CatalogModel struct {
	ID             string `json:"id"`
	Object         string `json:"object"`
	Kind           string `json:"kind"` // llm | ocr
	State          string `json:"state"`
	Healthy        bool   `json:"healthy"`
	Workers        int    `json:"workers,omitempty"`
	HealthyWorkers int    `json:"healthy_workers,omitempty"`
	ContextWindow  int    `json:"context_window,omitempty"`
	Description    string `json:"description,omitempty"`
}

// CatalogResponse is the single payload the React UI uses to render model and
// policy pickers.
type CatalogResponse struct {
	LLM                  []CatalogModel      `json:"llm"`
	ML                   []CatalogModel      `json:"ml"`
	RoutingPolicies      []RoutingPolicyInfo `json:"routing_policies"`
	DefaultRoutingPolicy string              `json:"default_routing_policy"`
}

// PolicyUpdateRequest is the body of PUT /v1/policies/default.
type PolicyUpdateRequest struct {
	Name string `json:"name"`
}

type ErrorBody struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the standard error body used by balancer and worker.
func WriteError(w http.ResponseWriter, status int, errType, msg string) {
	var b ErrorBody
	b.Error.Message = msg
	b.Error.Type = errType
	WriteJSON(w, status, b)
}
