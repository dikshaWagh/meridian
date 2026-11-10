// Package metrics writes Meridian request metrics as newline-delimited JSON.
package metrics

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const RequestLogPath = "logs/requests.jsonl"

var requestLogMu sync.Mutex

type RequestLog struct {
	Timestamp              string `json:"timestamp"`
	RequestID              string `json:"request_id"`
	Model                  string `json:"model"`
	WorkerID               string `json:"worker_id"`
	ConversationID         string `json:"conversation_id"`
	StatusCode             int    `json:"status_code"`
	LatencyMS              int64  `json:"latency_ms"`
	EstInputTokens         int    `json:"est_input_tokens"`
	EstOutputTokens        int    `json:"est_output_tokens"`
	EstTotalTokens         int    `json:"est_total_tokens"`
	ActualPromptTokens     int    `json:"actual_prompt_tokens"`
	ActualCompletionTokens int    `json:"actual_completion_tokens"`
}

func NewRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("req-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func AppendRequest(entry RequestLog) {
	requestLogMu.Lock()
	defer requestLogMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(RequestLogPath), 0755); err != nil {
		log.Printf("metrics: create log dir: %v", err)
		return
	}

	f, err := os.OpenFile(RequestLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("metrics: open request log: %v", err)
		return
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(entry); err != nil {
		log.Printf("metrics: write request log: %v", err)
	}
}
