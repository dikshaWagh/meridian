// Package api holds the JSON shapes shared by the balancer, mock worker and CLI.
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
	ID                  string `json:"id"`
	Model               string `json:"model"`
	Port                int    `json:"port"`
	PID                 int    `json:"pid"`
	State               string `json:"state"`
	Healthy             bool   `json:"healthy"`
	InFlight            int64  `json:"in_flight"`
	Served              int64  `json:"served"`
	EstTokensInFlight   int64  `json:"est_tokens_in_flight"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastCheck           string `json:"last_check"`
	LastError           string `json:"last_error"`
	Sessions            int    `json:"sessions"`
}

type StatusResponse struct {
	Workers      []WorkerStatus `json:"workers"`
	SessionCount int            `json:"sessionCount"`
}

type RoutingPolicyInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RoutingPolicyList struct {
	Default  string              `json:"default"`
	Policies []RoutingPolicyInfo `json:"policies"`
}

// CatalogModel is one selectable model in GET /v1/catalog. LLM entries stay
// listed while the LLM subsystem is stopped so a client can choose one before
// switching modes.
type CatalogModel struct {
	ID             string `json:"id"`
	Object         string `json:"object"`
	Kind           string `json:"kind"` // llm | ocr | image-generation
	State          string `json:"state"`
	Healthy        bool   `json:"healthy"`
	Workers        int    `json:"workers,omitempty"`
	HealthyWorkers int    `json:"healthy_workers,omitempty"`
	ContextWindow  int    `json:"context_window,omitempty"`
	Description    string `json:"description,omitempty"`
}

// CatalogResponse is the single payload the React UI uses to render model and
// policy pickers, regardless of the active serving mode.
type CatalogResponse struct {
	LLM                  []CatalogModel      `json:"llm"`
	ML                   []CatalogModel      `json:"ml"`
	RoutingPolicies      []RoutingPolicyInfo `json:"routing_policies"`
	DefaultRoutingPolicy string              `json:"default_routing_policy"`
	ServingMode          string              `json:"serving_mode"` // exclusive | concurrent
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
