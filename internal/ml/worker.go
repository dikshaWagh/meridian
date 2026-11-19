package ml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
)

// WorkerConfig is the per-worker configuration written by the parent and
// loaded by the `meridian-server ml-worker` child process.
type WorkerConfig struct {
	Kind        Kind   `json:"kind"`
	ModelID     string `json:"model_id"`
	Engine      string `json:"engine"`
	Device      string `json:"device"`
	ModelDir    string `json:"model_dir,omitempty"`
	Language    string `json:"language,omitempty"`
	Concurrency int    `json:"concurrency"`
	MaxUploadMB int64  `json:"max_upload_mb,omitempty"`
	// StartupDelayMS simulates model load time (used by tests).
	StartupDelayMS int `json:"startup_delay_ms,omitempty"`
}

func loadWorkerConfig(path string) (WorkerConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return WorkerConfig{}, fmt.Errorf("read worker config: %w", err)
	}
	var cfg WorkerConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return WorkerConfig{}, fmt.Errorf("parse worker config: %w", err)
	}
	if cfg.Kind != KindOCR {
		return WorkerConfig{}, fmt.Errorf("unsupported worker kind %q", cfg.Kind)
	}
	if cfg.ModelID == "" {
		return WorkerConfig{}, fmt.Errorf("worker model_id is required")
	}
	if cfg.Concurrency < 1 {
		cfg.Concurrency = 1
	}
	return cfg, nil
}

// RunWorker is the entry point for the ml-worker child process. It exits with
// a non-zero status if the backend fails to initialise.
func RunWorker(kindFlag, configPath, listen string) error {
	cfg, err := loadWorkerConfig(configPath)
	if err != nil {
		return err
	}
	if kindFlag != "" && Kind(kindFlag) != cfg.Kind {
		return fmt.Errorf("worker kind flag %q does not match config kind %q", kindFlag, cfg.Kind)
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s: %w", listen, err)
	}
	srv, closeBackend, err := newWorkerServer(cfg)
	if err != nil {
		ln.Close()
		return err
	}
	defer closeBackend()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("ml-worker kind=%s model=%s engine=%s listening on %s", cfg.Kind, cfg.ModelID, cfg.Engine, listen)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newWorkerServer(cfg WorkerConfig) (*http.Server, func() error, error) {
	handler, closeBackend, err := newWorkerHandler(cfg)
	if err != nil {
		return nil, nil, err
	}
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}, closeBackend, nil
}

// newWorkerHandler loads the configured backend and returns the worker's
// private HTTP handler. /ready stays 503 until the backend has loaded.
func newWorkerHandler(cfg WorkerConfig) (http.Handler, func() error, error) {
	if cfg.Kind != KindOCR {
		return nil, nil, fmt.Errorf("unsupported worker kind %q", cfg.Kind)
	}
	ocr, err := newOCRBackend(cfg)
	if err != nil {
		return nil, nil, err
	}
	closeBackend := func() error { return ocr.Close() }

	var ready atomic.Bool
	if cfg.StartupDelayMS > 0 {
		go func() {
			time.Sleep(time.Duration(cfg.StartupDelayMS) * time.Millisecond)
			ready.Store(true)
		}()
	} else {
		ready.Store(true)
	}

	sem := make(chan struct{}, cfg.Concurrency)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			api.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "loading"})
			return
		}
		api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("POST /v1/ocr", func(w http.ResponseWriter, r *http.Request) {
		maxBytes := cfg.MaxUploadMB << 20
		if maxBytes <= 0 {
			maxBytes = 10 << 20
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
		if err != nil {
			api.WriteError(w, http.StatusRequestEntityTooLarge, "request_too_large", "image exceeds worker upload limit")
			return
		}
		if !acquire(sem) {
			api.WriteError(w, http.StatusServiceUnavailable, "worker_busy", "worker concurrency limit reached")
			return
		}
		defer release(sem)
		result, err := ocr.Recognize(r.Context(), raw)
		if err != nil {
			api.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		result.Model = cfg.ModelID
		api.WriteJSON(w, http.StatusOK, result)
	})
	return mux, closeBackend, nil
}

func acquire(sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func release(sem chan struct{}) { <-sem }

// workerConfigJSON serialises the child config.
func workerConfigJSON(cfg WorkerConfig) ([]byte, error) {
	return json.MarshalIndent(cfg, "", "  ")
}
