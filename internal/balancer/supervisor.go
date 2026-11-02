package balancer

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Run is the entry point for `meridian serve`.
func Run(port int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool := NewPool(DefaultWorkers())
	children := startChildren(ctx, pool)
	pool.StartHealthChecks(ctx)

	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: NewHandler(pool)}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	log.Printf("meridian listening on :%d with %d workers", port, len(pool.Workers()))
	err := srv.ListenAndServe() // returns http.ErrServerClosed on shutdown
	stop()                      // make sure children get killed even if the server failed
	children.Wait()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// startChildren spawns one mock worker process per pool worker.
func startChildren(ctx context.Context, pool *Pool) *sync.WaitGroup {
	var wg sync.WaitGroup
	exe, err := os.Executable()
	if err != nil {
		log.Printf("cannot find own binary: %v", err)
		return &wg
	}
	for _, w := range pool.Workers() {
		s := w.Spec
		cmd := exec.CommandContext(ctx, exe, "worker",
			"--id", s.ID,
			"--model", s.Model,
			"--port", strconv.Itoa(s.Port),
			"--prefill-ms", strconv.Itoa(s.PrefillMS),
			"--decode-ms", strconv.Itoa(s.DecodeMS))
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Printf("failed to start worker %s: %v", s.ID, err)
			continue // worker simply stays unhealthy
		}
		w.PID.Store(int64(cmd.Process.Pid))
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cmd.Wait()
			log.Printf("worker %s exited", s.ID)
		}()
	}
	return &wg
}
