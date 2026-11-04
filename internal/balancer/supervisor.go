package balancer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Options struct {
	Host      string
	Port      int
	ModelsDir string
	LlamaBin  string
	Threads   int
	Context   int
	Mock      bool
}

func Run(o Options) error {
	if o.ModelsDir, _ = expandHome(o.ModelsDir); !o.Mock {
		if err := preflight(o); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool := NewPool(DefaultWorkers())
	children := startChildren(ctx, pool, o)
	pool.StartHealthChecks(ctx)
	srv := &http.Server{Addr: fmt.Sprintf("%s:%d", o.Host, o.Port), Handler: NewHandler(pool), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("meridian listening on %s:%d (LAN: %s)", o.Host, o.Port, lanURL(o.Port))
	err := srv.ListenAndServe()
	stop()
	children.Wait()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func expandHome(path string) (string, error) {
	if path == "~" {
		h, e := os.UserHomeDir()
		return h, e
	}
	if strings.HasPrefix(path, "~/") {
		h, e := os.UserHomeDir()
		if e != nil {
			return path, e
		}
		return filepath.Join(h, path[2:]), nil
	}
	return path, nil
}

func preflight(o Options) error {
	var missing []string
	seen := map[string]bool{}
	if _, err := exec.LookPath(o.LlamaBin); err != nil {
		missing = append(missing, "llama-server binary "+strconv.Quote(o.LlamaBin))
	}
	for _, w := range DefaultWorkers() {
		file := filepath.Join(o.ModelsDir, w.File)
		if _, err := os.Stat(file); err != nil && !seen[file] {
			missing = append(missing, file)
			seen[file] = true
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("preflight failed; missing:\n- %s", strings.Join(missing, "\n- "))
	}
	return nil
}

func startChildren(ctx context.Context, pool *Pool, o Options) *sync.WaitGroup {
	var wg sync.WaitGroup
	exe, _ := os.Executable()
	for _, w := range pool.Workers() {
		s := w.Spec
		var cmd *exec.Cmd
		if o.Mock {
			cmd = exec.CommandContext(ctx, exe, "mock-worker", "--id", s.ID, "--model", s.Model, "--port", strconv.Itoa(s.Port), "--prefill-ms", strconv.Itoa(s.PrefillMS), "--decode-ms", strconv.Itoa(s.DecodeMS))
		} else {
			cmd = exec.CommandContext(ctx, o.LlamaBin, "-m", filepath.Join(o.ModelsDir, s.File), "--alias", s.Model, "--host", "127.0.0.1", "--port", strconv.Itoa(s.Port), "-t", strconv.Itoa(o.Threads), "-c", strconv.Itoa(o.Context), "-np", "1")
		}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Signal(syscall.SIGTERM)
		}
		cmd.WaitDelay = 5 * time.Second
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Printf("failed to start worker %s: %v", s.ID, err)
			if w.markExited() {
				log.Printf("worker %s (%s) is now unhealthy", s.ID, s.Model)
			}
			continue
		}
		w.PID.Store(int64(cmd.Process.Pid))
		wg.Add(1)
		go func(w *Worker, cmd *exec.Cmd) {
			defer wg.Done()
			_ = cmd.Wait()
			if w.markExited() {
				log.Printf("worker %s (%s) is now unhealthy", w.Spec.ID, w.Spec.Model)
			}
			log.Printf("worker %s exited", w.Spec.ID)
		}(w, cmd)
	}
	return &wg
}

func lanURL(port int) string {
	ip := "localhost"
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
				ip = n.IP.String()
				break
			}
		}
	}
	return fmt.Sprintf("http://%s:%d", ip, port)
}
