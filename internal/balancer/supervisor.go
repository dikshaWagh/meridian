package balancer

import (
	"context"
	"encoding/json"
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

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/arbiter"
	"github.com/dikshaWagh/meridian/internal/ml"
	"github.com/dikshaWagh/meridian/internal/webui"
)

type Options struct {
	Host          string
	Port          int
	ModelsDir     string
	LlamaBin      string
	Threads       int
	Context       int
	Parallel      int
	NGL           int    // layers offloaded per GPU worker; 0 = CPU only
	GPUWorkers    int    // first N workers get GPU offload
	BackendLib    string // e.g. /usr/local/lib/ollama/vulkan/libggml-vulkan.so
	GPUDevice     string // e.g. Vulkan1; empty lets llama.cpp choose
	RoutingPolicy string // default routing policy; empty keeps round-robin

	EnableLLM bool   // start the existing LLM subsystem
	EnableML  bool   // start the opt-in ML (OCR/image) subsystem
	MLConfig  string // path to ML YAML configuration

	// ServingMode is "exclusive" (LLM and ML never resident together) or
	// "concurrent" (legacy behavior: both start at boot).
	ServingMode string
	// InitialMode is the mode activated at boot in exclusive serving:
	// "llm" (default) or "ml".
	InitialMode string
}

const (
	ServingExclusive  = "exclusive"
	ServingConcurrent = "concurrent"
)

func Run(o Options) error {
	if o.Parallel <= 0 {
		o.Parallel = 2
	}
	if o.Context <= 0 {
		o.Context = 8192
	}
	if !o.EnableLLM && !o.EnableML {
		return errors.New("at least one of --enable-llm or --enable-ml must be enabled")
	}
	if o.ServingMode == "" {
		o.ServingMode = ServingExclusive
	}
	if o.ServingMode != ServingExclusive && o.ServingMode != ServingConcurrent {
		return fmt.Errorf("invalid --serving-mode %q (want %q or %q)", o.ServingMode, ServingExclusive, ServingConcurrent)
	}
	if o.EnableML && o.MLConfig == "" {
		o.MLConfig = "configs/ml.yaml"
	}
	mlGrace := 10 * time.Second
	if o.EnableLLM {
		o.ModelsDir, _ = expandHome(o.ModelsDir)
		if err := preflight(o); err != nil {
			return err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var pool *Pool
	var llmRT *llmRuntime
	if o.EnableLLM {
		pool = NewPool(DefaultWorkers())
		pool.SetContextAndParallel(o.Context, o.Parallel)
		if o.RoutingPolicy != "" {
			if err := pool.SetDefaultRoutingPolicy(o.RoutingPolicy); err != nil {
				return err
			}
		}
		llmRT = newLLMRuntime(o, pool)
	}

	var mlCfg *ml.Config
	var mlRT *mlRuntime
	if o.EnableML {
		var err error
		mlCfg, err = ml.LoadConfig(o.MLConfig)
		if err != nil {
			return err
		}
		mlRT = newMLRuntime(mlCfg, ml.Options{})
		mlGrace = time.Duration(mlCfg.ShutdownGraceSeconds) * time.Second
	}

	// Exclusive serving requires both subsystems: one lease, one resident
	// subsystem. With a single subsystem there is nothing to arbitrate.
	exclusive := o.EnableLLM && o.EnableML && o.ServingMode != ServingConcurrent

	var arb *arbiter.Arbiter
	if exclusive {
		initial := o.InitialMode
		if initial == "" {
			initial = string(arbiter.ModeLLM)
		}
		switch arbiter.Mode(initial) {
		case arbiter.ModeLLM, arbiter.ModeML:
		default:
			return fmt.Errorf("invalid --initial-mode %q (want llm or ml)", o.InitialMode)
		}
		arb = arbiter.New(arbiter.Options{
			Subsystems: map[arbiter.Mode]arbiter.Subsystem{
				arbiter.ModeLLM: llmRT,
				arbiter.ModeML:  mlRT,
			},
			Initial: arbiter.Mode(initial),
		})
		if err := arb.Start(); err != nil {
			return err
		}
	} else {
		if llmRT != nil {
			if err := llmRT.Start(context.Background()); err != nil {
				return err
			}
		}
		if mlRT != nil {
			if err := mlRT.Start(context.Background()); err != nil {
				if llmRT != nil {
					_ = llmRT.Stop(context.Background())
				}
				return err
			}
		}
	}
	if pool != nil {
		pool.StartHealthChecks(ctx)
		pool.StartSessionSweeper(ctx)
	}

	root := http.NewServeMux()
	if pool != nil {
		root.Handle("/", NewHandler(pool))
	} else {
		root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
			api.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		})
	}
	// ML endpoints are always registered: in LLM-only deployments they return
	// a stable service_disabled error instead of a bare 404.
	ml.RegisterFunc(root, func() *ml.Service {
		if mlRT == nil {
			return nil
		}
		return mlRT.Current()
	})
	root.Handle("GET /v1/catalog", handleCatalog(pool, mlRT, arb, o.ServingMode))
	if arb != nil {
		arb.Register(root)
	}

	var handler http.Handler = withCORS(root)
	handler = webui.Handler(handler)
	if arb != nil {
		handler = arb.Middleware(handler)
	}

	srv := &http.Server{Addr: fmt.Sprintf("%s:%d", o.Host, o.Port), Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	llmState := "disabled"
	if llmRT != nil {
		llmState = fmt.Sprintf("%d workers", len(pool.Workers()))
	}
	mlState := "disabled"
	if mlCfg != nil {
		mlState = "configured"
	}
	serving := o.ServingMode
	if !exclusive {
		serving = "single/concurrent"
	}
	log.Printf("meridian listening on %s:%d (LAN: %s) llm=%s ml=%s serving=%s", o.Host, o.Port, lanURL(o.Port), llmState, mlState, serving)
	err := srv.ListenAndServe()
	stop()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), maxDuration(mlGrace, 60*time.Second))
	defer cancel()
	if arb != nil {
		if err := arb.Close(shutdownCtx); err != nil {
			log.Printf("arbiter shutdown: %v", err)
		}
	} else {
		if llmRT != nil {
			if err := llmRT.Stop(shutdownCtx); err != nil {
				log.Printf("llm shutdown: %v", err)
			}
		}
		if mlRT != nil {
			if err := mlRT.Stop(shutdownCtx); err != nil {
				log.Printf("ml shutdown: %v", err)
			}
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// withCORS keeps the Vite dev server usable without configuring a proxy. The
// server has no authentication, so this does not widen an existing boundary.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Conversation-ID, X-Meridian-Routing-Policy, X-Request-ID")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "X-Meridian-Worker, X-Meridian-Model, X-Meridian-Routing-Policy, X-Meridian-Mode, X-Meridian-Phase")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleSetDefaultPolicy(pool *Pool, w http.ResponseWriter, r *http.Request) {
	var req api.PolicyUpdateRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if err := dec.Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid_request", "body must be {\"name\":\"<policy>\"}")
		return
	}
	if err := pool.SetDefaultRoutingPolicy(strings.TrimSpace(req.Name)); err != nil {
		api.WriteError(w, http.StatusBadRequest, "unknown_routing_policy", err.Error())
		return
	}
	handlePolicies(pool, w)
}

func handleCatalog(pool *Pool, mlRT *mlRuntime, arb *arbiter.Arbiter, servingMode string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := api.CatalogResponse{
			LLM:                  []api.CatalogModel{},
			ML:                   []api.CatalogModel{},
			RoutingPolicies:      []api.RoutingPolicyInfo{},
			DefaultRoutingPolicy: "",
			ServingMode:          servingMode,
		}
		if pool != nil {
			out.LLM = pool.ModelCatalog()
			out.RoutingPolicies = pool.RoutingPolicies()
			out.DefaultRoutingPolicy = pool.DefaultRoutingPolicy()
		}
		if mlRT != nil {
			out.ML = mlRT.Catalog()
		}
		if arb == nil {
			out.ServingMode = "single"
		}
		w.Header().Set("Cache-Control", "no-store")
		api.WriteJSON(w, http.StatusOK, out)
	}
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
	if o.BackendLib != "" {
		if _, err := os.Stat(o.BackendLib); err != nil {
			missing = append(missing, o.BackendLib)
		}
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
	for i, w := range pool.Workers() {
		s := w.Spec
		args := []string{"-m", filepath.Join(o.ModelsDir, s.File), "--alias", s.Model, "--host", "127.0.0.1", "--port", strconv.Itoa(s.Port), "-t", strconv.Itoa(o.Threads), "-c", strconv.Itoa(o.Context), "-np", strconv.Itoa(o.Parallel)}
		if o.NGL > 0 && i < o.GPUWorkers {
			args = append(args, "-ngl", strconv.Itoa(o.NGL))
			if o.GPUDevice != "" {
				args = append(args, "--device", o.GPUDevice)
			}
		}
		cmd := exec.CommandContext(ctx, o.LlamaBin, args...)
		cmd.Env = workerEnv(o)
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

// workerEnv points llama.cpp at a specific backend (for example Vulkan).
func workerEnv(o Options) []string {
	if o.BackendLib == "" {
		return nil // inherit the parent environment
	}
	dir := filepath.Dir(o.BackendLib)
	env := make([]string, 0, len(os.Environ())+2)
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GGML_BACKEND_PATH=") || strings.HasPrefix(e, "LD_LIBRARY_PATH=") {
			continue
		}
		env = append(env, e)
	}
	ld := dir
	if old := os.Getenv("LD_LIBRARY_PATH"); old != "" {
		ld += ":" + old
	}
	return append(env, "GGML_BACKEND_PATH="+o.BackendLib, "LD_LIBRARY_PATH="+ld)
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
