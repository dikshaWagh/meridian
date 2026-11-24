package balancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	"github.com/dikshaWagh/meridian/internal/webui"
)

type LLMConfig struct {
	Model   string // e.g., "qwen.gguf"
	Workers int    // e.g., 2
}

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
	// RAMHeadroomMB is kept free after all LLM instances are admitted.
	RAMHeadroomMB int

	// RestartWorkers restarts a llama-server process after it exits.
	// MaxRestarts is the number of consecutive failed restarts before giving up.
	RestartWorkers bool
	MaxRestarts    int

	// LLMConfigs lists every model this node serves and its instance count.
	LLMConfigs []LLMConfig
	// ReplicaPortBase is the first loopback port for replicas. Zero keeps the
	// standalone default of 8081.
	ReplicaPortBase int

	// DEPRECATED: kept for backwards compatibility; converted to LLMConfigs
	// LLMModel serves one GGUF file instead of the built-in worker set.
	// Relative paths are resolved under ModelsDir.
	LLMModel     string
	LLMModelName string
	LLMWorkers   int

	EnableLLM bool // start the LLM subsystem
}

func Run(o Options) error {
	// Backwards compatibility: convert old flags to new format
	if o.LLMModel != "" && len(o.LLMConfigs) == 0 {
		if o.LLMWorkers < 1 {
			o.LLMWorkers = 1
		}
		o.LLMConfigs = []LLMConfig{{Model: o.LLMModel, Workers: o.LLMWorkers}}
	}

	if o.Parallel <= 0 {
		o.Parallel = 2
	}
	if o.Context <= 0 {
		o.Context = 8192
	}
	if !o.EnableLLM {
		return errors.New("at least one of --enable-llm must be enabled")
	}
	if o.EnableLLM {
		o.ModelsDir, _ = expandHome(o.ModelsDir)
		exe, _ := os.Executable()
		log.Printf("models dir: %s (binary: %s)", o.ModelsDir, exe)
		if err := validateLLMConfigs(o.LLMConfigs); err != nil {
			return err
		}
		if err := preflight(o); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var pool *Pool
	var llmRT *llmRuntime
	if o.EnableLLM {
		pool = NewPool(workerSpecs(o))
		pool.SetContextAndParallel(o.Context, o.Parallel)
		if o.RoutingPolicy != "" {
			if err := pool.SetDefaultRoutingPolicy(o.RoutingPolicy); err != nil {
				return err
			}
		}
		llmRT = newLLMRuntime(o, pool)
	}

	// Start every enabled subsystem directly.
	if llmRT != nil {
		if err := llmRT.Start(context.Background()); err != nil {
			return err
		}
	}

	root := http.NewServeMux()

	var handler http.Handler = withCORS(root)
	handler = webui.Handler(handler)

	srv := &http.Server{Addr: fmt.Sprintf("%s:%d", o.Host, o.Port), Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("meridian listening on %s:%d (LAN: %s) llm=%s", o.Host, o.Port, lanURL(o.Port), "disabled")
	err := srv.ListenAndServe()
	stop()

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
		w.Header().Set("Access-Control-Expose-Headers", "X-Meridian-Worker, X-Meridian-Model, X-Meridian-Routing-Policy, X-Meridian-Queue-Ms, X-Meridian-Predicted-Ms")
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

// modelName is the public id for a GGUF file: its base name without extension.
func modelName(file string) string {
	base := filepath.Base(file)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// validateLLMConfigs rejects a configuration that cannot be served. Each model
// is listed once with its instance count, because two entries with the same
// public id would silently merge into one pool.
func validateLLMConfigs(cfgs []LLMConfig) error {
	if len(cfgs) == 0 {
		return errors.New("no LLM models configured: pass --llm <file.gguf>[:workers] (repeat for each model)")
	}
	seen := map[string]string{}
	for _, c := range cfgs {
		file := strings.TrimSpace(c.Model)
		if file == "" {
			return errors.New("empty LLM model path")
		}
		name := modelName(file)
		if prev, ok := seen[name]; ok {
			return fmt.Errorf("model %q is listed twice (%s and %s); list each model once with its worker count", name, prev, file)
		}
		seen[name] = file
	}
	return nil
}

// workerSpecs expands each LLMConfig into one WorkerSpec per instance. Ports
// and worker IDs are assigned sequentially across all models, starting at 8081.
func workerSpecs(o Options) []WorkerSpec {
	var allSpecs []WorkerSpec
	portNum := 8081
	if o.ReplicaPortBase > 0 {
		portNum = o.ReplicaPortBase
	}

	for _, cfg := range o.LLMConfigs {
		file := strings.TrimSpace(cfg.Model)
		if file == "" {
			continue
		}

		name := modelName(file)

		workers := cfg.Workers
		if workers < 1 {
			workers = 1
		}

		// Create worker specs for this model
		for i := 0; i < workers; i++ {
			allSpecs = append(allSpecs, WorkerSpec{
				ID:    fmt.Sprintf("w%d", len(allSpecs)+1),
				Model: name,
				Port:  portNum,
				File:  file,
			})
			portNum++
		}
	}

	return allSpecs
}

// modelPath joins relative model files onto dir and leaves absolute paths alone.
func modelPath(dir, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(dir, file)
}

// describePath reports why path is unusable. The underlying OS error is
// included so a permission, mount, or SELinux denial is not mistaken for a
// missing file.
func describePath(path string, err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return fmt.Sprintf("%q: %v", path, err)
}

func preflight(o Options) error {
	var problems []string
	seen := map[string]bool{}
	var modelBytes int64
	if _, err := exec.LookPath(o.LlamaBin); err != nil {
		problems = append(problems, "llama-server binary "+strconv.Quote(o.LlamaBin))
	}
	if o.BackendLib != "" {
		if _, err := os.Stat(o.BackendLib); err != nil {
			problems = append(problems, describePath(o.BackendLib, err))
		}
	}
	for _, w := range workerSpecs(o) {
		file := modelPath(o.ModelsDir, w.File)
		fi, err := os.Stat(file)
		if err != nil {
			if !seen[file] {
				problems = append(problems, describePath(file, err))
				seen[file] = true
			}
		} else {
			// Each instance loads its own copy, so every instance counts.
			modelBytes += fi.Size()
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", w.Port))
		if err != nil {
			problems = append(problems, fmt.Sprintf("port %d for worker %s (%s) is unavailable: %v", w.Port, w.ID, w.Model, err))
		} else {
			_ = ln.Close()
		}
	}
	if len(problems) == 0 {
		avail, err := memAvailableBytes()
		if err != nil {
			log.Printf("RAM admission skipped: %v", err)
		} else if need := modelBytes + int64(o.RAMHeadroomMB)<<20; avail < need {
			problems = append(problems, fmt.Sprintf(
				"insufficient RAM: %d worker instances need %d MB plus %d MB headroom, but MemAvailable is %d MB; reduce instance counts or models",
				len(workerSpecs(o)), modelBytes>>20, o.RAMHeadroomMB, avail>>20))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("preflight failed:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

// memAvailableBytes reads MemAvailable from /proc/meminfo (Linux only).
func memAvailableBytes() (int64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, err
			}
			return kb << 10, nil
		}
	}
	return 0, errors.New("MemAvailable not found in /proc/meminfo")
}

func startChildren(ctx context.Context, pool *Pool, o Options) *sync.WaitGroup {
	var wg sync.WaitGroup
	for i, w := range pool.Workers() {
		wg.Add(1)
		go func(w *Worker, index int) {
			defer wg.Done()
			superviseWorker(ctx, w, index, o)
		}(w, i)
	}
	return &wg
}

// superviseWorker runs one worker and, when its process exits, starts it
// again with exponential backoff. It returns when ctx is cancelled (server
// shutdown), when restarts are disabled, or after too many crashes in a row.
func superviseWorker(ctx context.Context, w *Worker, index int, o Options) {
	const (
		minBackoff = time.Second
		maxBackoff = 30 * time.Second
		stableRun  = time.Minute
	)
	backoff := minBackoff
	crashes := 0
	for {
		started := time.Now()
		cmd, err := startWorkerProcess(ctx, w, index, o)
		if err != nil {
			log.Printf("failed to start worker %s: %v", w.Spec.ID, err)
		} else {
			w.PID.Store(int64(cmd.Process.Pid))
			_ = cmd.Wait()
			log.Printf("worker %s exited", w.Spec.ID)
		}
		if w.markExited() {
			log.Printf("worker %s (%s) is now unhealthy", w.Spec.ID, w.Spec.Model)
		}

		if ctx.Err() != nil || !o.RestartWorkers {
			return
		}
		if time.Since(started) >= stableRun {
			crashes = 0
			backoff = minBackoff
		}
		crashes++
		if crashes > o.MaxRestarts {
			log.Printf("worker %s (%s): giving up after %d failed restarts; restart the server to bring it back", w.Spec.ID, w.Spec.Model, o.MaxRestarts)
			return
		}
		log.Printf("restarting worker %s in %s (attempt %d of %d)", w.Spec.ID, backoff, crashes, o.MaxRestarts)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
		w.markStarting()
	}
}

// startWorkerProcess launches one llama-server process for w. The process is
// tied to ctx, so cancelling ctx stops it.
func startWorkerProcess(ctx context.Context, w *Worker, index int, o Options) (*exec.Cmd, error) {
	s := w.Spec
	args := []string{"-m", modelPath(o.ModelsDir, s.File), "--alias", s.Model, "--host", "127.0.0.1", "--port", strconv.Itoa(s.Port), "-t", strconv.Itoa(o.Threads), "-c", strconv.Itoa(o.Context), "-np", strconv.Itoa(o.Parallel)}
	useGPU := o.NGL > 0 && index < o.GPUWorkers
	if useGPU {
		args = append(args, "-ngl", strconv.Itoa(o.NGL))
		if o.GPUDevice != "" {
			args = append(args, "--device", o.GPUDevice)
		}
	} else {
		// llama-server offloads layers to a visible GPU by default, so CPU
		// workers must opt out explicitly or they fill VRAM.
		args = append(args, "-ngl", "0")
	}
	cmd := exec.CommandContext(ctx, o.LlamaBin, args...)
	cmd.Env = workerEnv(o)
	if !useGPU {
		cmd.Env = hideGPU(cmd.Env)
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
		return nil, err
	}
	return cmd, nil
}

// hideGPU makes CUDA see no devices. Prompt processing otherwise still
// reserves VRAM on CPU-only workers, which starves the GPU workers.
func hideGPU(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	return append(env, "CUDA_VISIBLE_DEVICES=")
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

// WithCORS applies the same CORS policy as the standalone gateway. The cluster
// leader uses it so the Vite dev server and the embedded UI behave the same way.
func WithCORS(next http.Handler) http.Handler {
	return withCORS(next)
}