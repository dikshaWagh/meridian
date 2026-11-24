package balancer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
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
