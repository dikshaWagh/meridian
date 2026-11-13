package balancer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/ml"
)

// llmRuntime makes the llama-server child processes startable and stoppable at
// runtime. It owns one generation of children; the health checker and session
// store live on the Pool and survive a stop/start cycle.
type llmRuntime struct {
	o    Options
	pool *Pool

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      *sync.WaitGroup
	running bool
}

func newLLMRuntime(o Options, pool *Pool) *llmRuntime {
	return &llmRuntime{o: o, pool: pool}
}

func (r *llmRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return nil
	}
	runCtx, cancel := context.WithCancel(context.Background())
	for _, w := range r.pool.Workers() {
		w.markStarting()
	}
	wg := startChildren(runCtx, r.pool, r.o)
	r.cancel = cancel
	r.wg = wg
	r.running = true
	return nil
}

func (r *llmRuntime) Stop(ctx context.Context) error {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return nil
	}
	cancel, wg := r.cancel, r.wg
	r.cancel, r.wg, r.running = nil, nil, false
	r.mu.Unlock()

	cancel()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stopping LLM workers: %w", ctx.Err())
	}
}

// WaitReady waits until no worker is still starting. At least one healthy
// worker is required; a partial failure is reported but does not block the
// switch forever.
func (r *llmRuntime) WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		starting, healthy := 0, 0
		for _, w := range r.pool.Workers() {
			switch w.State() {
			case "starting":
				starting++
			case "healthy":
				healthy++
			}
		}
		if starting == 0 {
			if healthy == 0 {
				return errors.New("no LLM worker became healthy")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// mlRuntime owns the current ML Service. A stop/start cycle creates a fresh
// Service so worker processes and admission reservations start clean.
type mlRuntime struct {
	cfg  *ml.Config
	opts ml.Options

	mu  sync.Mutex
	svc *ml.Service
}

func newMLRuntime(cfg *ml.Config, opts ml.Options) *mlRuntime {
	return &mlRuntime{cfg: cfg, opts: opts}
}

// Current is safe to call while a switch is running; the ML handlers read the
// pointer on every request.
func (r *mlRuntime) Current() *ml.Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.svc
}

func (r *mlRuntime) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.svc != nil {
		return nil
	}
	// The service has its own lifetime; Stop drives shutdown explicitly.
	svc, err := ml.Start(context.Background(), r.cfg, r.opts)
	if err != nil {
		return err
	}
	r.svc = svc
	return nil
}

func (r *mlRuntime) Stop(ctx context.Context) error {
	r.mu.Lock()
	svc := r.svc
	r.svc = nil
	r.mu.Unlock()
	if svc == nil {
		return nil
	}
	return svc.Shutdown(ctx)
}

// Catalog returns the configured ML models even while the ML subsystem is
// stopped, so the UI can offer them before a mode switch.
func (r *mlRuntime) Catalog() []api.CatalogModel {
	r.mu.Lock()
	cfg, svc := r.cfg, r.svc
	r.mu.Unlock()
	if cfg == nil {
		return []api.CatalogModel{}
	}
	states := map[string]ml.ModelStatus{}
	if svc != nil {
		for _, m := range svc.Models() {
			states[m.ID] = m
		}
	}
	out := make([]api.CatalogModel, 0, 2)
	add := func(modelID string, kind ml.Kind, engine, device string, enabled bool) {
		if !enabled || modelID == "" {
			return
		}
		state := "stopped"
		healthy := false
		if st, ok := states[modelID]; ok {
			state = string(st.State)
			healthy = st.Healthy
		}
		desc := engine
		if device != "" {
			desc += " · " + device
		}
		out = append(out, api.CatalogModel{
			ID:          modelID,
			Object:      "ml.model",
			Kind:        string(kind),
			State:       state,
			Healthy:     healthy,
			Description: desc,
		})
	}
	if o := cfg.OCR; o != nil {
		add(o.ModelID, ml.KindOCR, o.Engine, o.Device, o.Enabled)
	}
	if g := cfg.ImageGeneration; g != nil {
		add(g.ModelID, ml.KindImageGeneration, g.Engine, g.Device, g.Enabled)
	}
	return out
}

func (r *mlRuntime) WaitReady(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		svc := r.Current()
		if svc == nil {
			return errors.New("ML service is not running")
		}
		models := svc.Models()
		if len(models) == 0 {
			return errors.New("no ML models are enabled")
		}
		starting, ready := 0, 0
		for _, m := range models {
			switch m.State {
			case ml.StateStarting:
				starting++
			case ml.StateReady:
				ready++
			}
		}
		if starting == 0 {
			if ready == 0 {
				return errors.New("no ML worker became ready")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
