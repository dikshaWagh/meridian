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
	ID       string `json:"id"`
	Model    string `json:"model"`
	Port     int    `json:"port"`
	PID      int    `json:"pid"`
	Healthy  bool   `json:"healthy"`
	InFlight int64  `json:"in_flight"`
	Served   int64  `json:"served"`
}

type StatusResponse struct {
	Workers []WorkerStatus `json:"workers"`
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
