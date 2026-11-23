package balancer

import (
	"context"
	"sync"
	"time"
)

// Replicas runs one node's llama-server replicas and their /health probes
// without the public chat API. Cluster roles use it. Standalone mode keeps
// using Run.
type Replicas struct {
	Pool   *Pool
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

// StartReplicas applies the same preflight and admission as standalone mode,
// then starts each replica under its own supervisor.
func StartReplicas(o Options, healthInterval time.Duration) (*Replicas, error) {
	if o.Parallel <= 0 {
		o.Parallel = 2
	}
	if o.Context <= 0 {
		o.Context = 8192
	}
	dir, err := expandHome(o.ModelsDir)
	if err != nil {
		return nil, err
	}
	o.ModelsDir = dir
	if err := validateLLMConfigs(o.LLMConfigs); err != nil {
		return nil, err
	}
	if err := preflight(o); err != nil {
		return nil, err
	}

	pool := NewPool(workerSpecs(o))
	pool.SetContextAndParallel(o.Context, o.Parallel)
	if o.RoutingPolicy != "" {
		if err := pool.SetDefaultRoutingPolicy(o.RoutingPolicy); err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg := startChildren(ctx, pool, o)
	pool.StartHealthChecksEvery(ctx, healthInterval)
	return &Replicas{Pool: pool, cancel: cancel, wg: wg}, nil
}

// Stop terminates every replica and waits until they exit or ctx ends.
func (r *Replicas) Stop(ctx context.Context) error {
	r.cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
