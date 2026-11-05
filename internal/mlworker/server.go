// Package mlworker is a fake ML inference worker: it sleeps, then answers with
// a prediction. It exists so the ML balancer can be demonstrated and tested
// without real models.
package mlworker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/ml"
)

// Run serves the ML worker contract on 127.0.0.1:port.
//
// predictMS is the simulated inference time. It can be changed at runtime with
// POST /admin/latency {"ms": N} so the demo can slow one worker down and watch
// its score rise.
func Run(id, model string, port, predictMS, loadMS int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stderr, "["+id+"] ", log.LstdFlags)
	var counter atomic.Int64
	var latency atomic.Int64
	latency.Store(int64(predictMS))
	var loaded atomic.Bool
	if loadMS <= 0 {
		loaded.Store(true)
	} else {
		go func() {
			select {
			case <-time.After(time.Duration(loadMS) * time.Millisecond):
				loaded.Store(true)
				logger.Printf("model %s loaded", model)
			case <-ctx.Done():
			}
		}()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		status := http.StatusOK
		if !loaded.Load() {
			status = http.StatusServiceUnavailable
		}
		api.WriteJSON(w, status, map[string]any{
			"status":       map[bool]string{true: "ok", false: "loading"}[loaded.Load()],
			"model":        model,
			"model_loaded": loaded.Load(),
		})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		if !loaded.Load() {
			api.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "loading"})
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("POST /admin/latency", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MS int `json:"ms"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.MS < 0 {
			api.WriteError(w, http.StatusBadRequest, "invalid_request", "expected {\"ms\": N}")
			return
		}
		latency.Store(int64(body.MS))
		logger.Printf("simulated inference latency is now %dms", body.MS)
		api.WriteJSON(w, http.StatusOK, map[string]int{"ms": body.MS})
	})
	mux.HandleFunc("POST /predict", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if !loaded.Load() {
			api.WriteError(w, http.StatusServiceUnavailable, "model_loading", "model is still loading")
			return
		}
		var req ml.PredictRequest
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_request", "body is not valid JSON")
			return
		}
		delay := time.Duration(latency.Load()) * time.Millisecond
		select {
		case <-time.After(delay):
		case <-r.Context().Done(): // client left, stop "inferring"
			return
		}
		n := counter.Add(1)
		confidence := 0.90 + float64(n%10)/1000
		api.WriteJSON(w, http.StatusOK, ml.PredictResponse{
			Prediction: fmt.Sprintf("%s-class-%d", model, n%10),
			Confidence: &confidence,
			Model:      model,
			Worker:     id,
		})
		logger.Printf("predict model=%s n=%d time=%dms", model, n, time.Since(start).Milliseconds())
	})

	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: mux}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	logger.Printf("ml worker serving %s on 127.0.0.1:%d (inference %dms)", model, port, predictMS)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
