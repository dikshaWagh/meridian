// Package mockworker is a fake LLM provider: it just sleeps.
package mockworker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

// streamReply emulates an OpenAI-compatible SSE stream so the React client can
// exercise incremental rendering without a real model.
func streamReply(w http.ResponseWriter, r *http.Request, id, model, reply string, prefill, perWord time.Duration) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	send := func(delta map[string]string, finish *string) bool {
		chunk := map[string]any{
			"id":     id,
			"object": "chat.completion.chunk",
			"model":  model,
			"choices": []map[string]any{{
				"index":         0,
				"delta":         delta,
				"finish_reason": finish,
			}},
		}
		raw, _ := json.Marshal(chunk)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	pause := func(d time.Duration) bool {
		select {
		case <-time.After(d):
			return true
		case <-r.Context().Done():
			return false
		}
	}
	if !pause(prefill) {
		return
	}
	if !send(map[string]string{"role": "assistant", "content": ""}, nil) {
		return
	}
	for _, word := range strings.Fields(reply) {
		if !pause(perWord) {
			return
		}
		if !send(map[string]string{"content": word + " "}, nil) {
			return
		}
	}
	stop := "stop"
	send(map[string]string{}, &stop)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func Run(id, model string, port, prefillMS, decodeMS int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stderr, "["+id+"] ", log.LstdFlags)
	var counter atomic.Int64

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		var req api.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_request", "body is not valid JSON")
			return
		}

		chars := 0
		last := ""
		for _, m := range req.Messages {
			chars += len([]rune(m.Content))
			if m.Role == "user" {
				last = m.Content
			}
		}
		promptTokens := max(chars/4, 1)
		completionTokens := 32
		if req.MaxTokens > 0 {
			completionTokens = min(req.MaxTokens, 256)
		}

		delay := time.Duration(promptTokens*prefillMS+completionTokens*decodeMS) * time.Millisecond

		if rs := []rune(last); len(rs) > 60 {
			last = string(rs[:60])
		}
		n := counter.Add(1)
		id := fmt.Sprintf("mock-%s-%d", id, n)
		reply := fmt.Sprintf("[%s · %s] simulated reply to: %s", id, model, last)

		if req.Stream {
			streamReply(w, r, id, model, reply, time.Duration(promptTokens*prefillMS)*time.Millisecond, time.Duration(decodeMS)*time.Millisecond)
			logger.Printf("model=%s prompt_tokens=%d completion_tokens=%d stream=true", model, promptTokens, completionTokens)
			return
		}

		select {
		case <-time.After(delay):
		case <-r.Context().Done(): // client left, stop "computing"
			return
		}

		api.WriteJSON(w, http.StatusOK, api.ChatResponse{
			ID:     id,
			Object: "chat.completion",
			Model:  model,
			Choices: []api.Choice{{
				Message: api.Message{
					Role:    "assistant",
					Content: reply,
				},
				FinishReason: "stop",
			}},
			Usage: api.Usage{PromptTokens: promptTokens, CompletionTokens: completionTokens, TotalTokens: promptTokens + completionTokens},
		})
		logger.Printf("model=%s prompt_tokens=%d completion_tokens=%d time=%dms",
			model, promptTokens, completionTokens, time.Since(start).Milliseconds())
	})

	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	logger.Printf("serving %s on 127.0.0.1:%d", model, port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
