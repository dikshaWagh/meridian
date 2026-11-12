package ml

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultStartupTimeout = 60 * time.Second
	maxRestarts           = 2
	healthInterval        = 2 * time.Second
	shutdownGrace         = 5 * time.Second
)

// Options customises service startup. Zero values pick production defaults.
type Options struct {
	// Probe overrides host resource probing (tests use a FakeProbe).
	Probe Probe
	// CommandBuilder overrides child-process creation (tests use a helper process).
	CommandBuilder func(h *WorkerHandle, configPath string) *exec.Cmd
	// TempDir overrides the directory used for generated worker configs.
	TempDir string
	// Logger defaults to the standard logger.
	Logger *log.Logger
}

// WorkerHandle is the parent-side view of one ML model worker.
type WorkerHandle struct {
	ID           string
	Kind         Kind
	ModelID      string
	Engine       string
	Device       string
	Port         int
	Concurrency  int
	cfg          WorkerConfig
	client       *http.Client
	baseURL      string
	startTimeout time.Duration

	mu          sync.RWMutex
	state       State
	pid         int
	lastError   string
	lastCheck   time.Time
	cmd         *exec.Cmd
	reservation Reservation
	releaseOnce sync.Once

	ramMB      int64
	vramMB     int64
	overheadMB int64

	inflight        atomic.Int64
	served          atomic.Int64
	failed          atomic.Int64
	restarts        atomic.Int64
	lastInferenceMS atomic.Int64
}

func (h *WorkerHandle) State() State {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state
}

func (h *WorkerHandle) IsReady() bool {
	return h.State() == StateReady
}

func (h *WorkerHandle) setState(s State) {
	h.mu.Lock()
	h.state = s
	h.mu.Unlock()
}

func (h *WorkerHandle) setError(msg string) {
	h.mu.Lock()
	h.lastError = msg
	h.mu.Unlock()
}

func (h *WorkerHandle) LastError() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastError
}

func (h *WorkerHandle) setCmd(cmd *exec.Cmd) {
	h.mu.Lock()
	h.cmd = cmd
	if cmd != nil && cmd.Process != nil {
		h.pid = cmd.Process.Pid
	}
	h.mu.Unlock()
}

func (h *WorkerHandle) getCmd() *exec.Cmd {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cmd
}

func (h *WorkerHandle) PID() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pid
}

func (h *WorkerHandle) markCheck() {
	h.mu.Lock()
	h.lastCheck = time.Now()
	h.mu.Unlock()
}

// tryAcquire reserves one concurrency slot, respecting the worker limit.
func (h *WorkerHandle) tryAcquire() bool {
	for {
		n := h.inflight.Load()
		if n >= int64(h.Concurrency) {
			return false
		}
		if h.inflight.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (h *WorkerHandle) releaseSlot() { h.inflight.Add(-1) }

func (h *WorkerHandle) releaseReservation(m *Manager) {
	h.releaseOnce.Do(func() {
		if h.reservation.ID != 0 {
			m.Release(h.reservation)
		}
	})
}

// Service owns the ML registry, router, supervisor and resource admission.
type Service struct {
	cfg          *Config
	res          *Manager
	workers      []*WorkerHandle
	artifacts    string
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	tmpDir       string
	cmdBuilder   func(h *WorkerHandle, configPath string) *exec.Cmd
	logger       *log.Logger
	shuttingDown atomic.Bool
}

// Start validates configuration, admits workers and launches their supervisors.
// A resource-blocked or failed ML worker never prevents Meridian from serving
// the LLM subsystem.
func Start(parent context.Context, cfg *Config, opts Options) (*Service, error) {
	if cfg == nil {
		return nil, errors.New("ml: nil config")
	}
	if parent == nil {
		parent = context.Background()
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.Default()
	}
	probe := opts.Probe
	if probe == nil {
		probe = newHostProbe(cfg.Resources.GPUProbe)
	}
	cmdBuilder := opts.CommandBuilder
	if cmdBuilder == nil {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("ml: locate executable: %w", err)
		}
		cmdBuilder = func(h *WorkerHandle, configPath string) *exec.Cmd {
			return exec.Command(exe, "ml-worker",
				"--kind", string(h.Kind),
				"--config", configPath,
				"--listen", fmt.Sprintf("127.0.0.1:%d", h.Port))
		}
	}
	tmpBase := opts.TempDir
	if tmpBase == "" {
		tmpBase = os.TempDir()
	}
	tmpDir, err := os.MkdirTemp(tmpBase, "meridian-ml-*")
	if err != nil {
		return nil, fmt.Errorf("ml: create temp dir: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	s := &Service{
		cfg:        cfg,
		res:        NewManager(cfg.Resources, probe),
		ctx:        ctx,
		cancel:     cancel,
		tmpDir:     tmpDir,
		cmdBuilder: cmdBuilder,
		logger:     logger,
	}

	if cfg.ImageGeneration != nil && cfg.ImageGeneration.Enabled {
		if err := os.MkdirAll(cfg.ImageGeneration.OutputDir, 0o755); err != nil {
			cancel()
			os.RemoveAll(tmpDir)
			return nil, fmt.Errorf("ml: create artifact dir %q: %w", cfg.ImageGeneration.OutputDir, err)
		}
		s.artifacts = cfg.ImageGeneration.OutputDir
	}

	if cfg.OCR != nil && cfg.OCR.Enabled {
		o := cfg.OCR
		s.workers = append(s.workers, s.newHandle(&WorkerHandle{
			ID:           "ml-ocr-1",
			Kind:         KindOCR,
			ModelID:      o.ModelID,
			Engine:       o.Engine,
			Device:       o.Device,
			Port:         o.Port,
			Concurrency:  o.Concurrency,
			startTimeout: time.Duration(o.StartupTimeoutSeconds) * time.Second,
			cfg: WorkerConfig{
				Kind:        KindOCR,
				ModelID:     o.ModelID,
				Engine:      o.Engine,
				Device:      o.Device,
				ModelDir:    o.ModelDir,
				Concurrency: o.Concurrency,
				MaxUploadMB: o.MaxUploadMB,
			},
		}, o.ExpectedRAMMB, o.ExpectedVRAMMB, 0))
	}
	if cfg.ImageGeneration != nil && cfg.ImageGeneration.Enabled {
		g := cfg.ImageGeneration
		s.workers = append(s.workers, s.newHandle(&WorkerHandle{
			ID:           "ml-image-1",
			Kind:         KindImageGeneration,
			ModelID:      g.ModelID,
			Engine:       g.Engine,
			Device:       g.Device,
			Port:         g.Port,
			Concurrency:  g.Concurrency,
			startTimeout: time.Duration(g.StartupTimeoutSeconds) * time.Second,
			cfg: WorkerConfig{
				Kind:           KindImageGeneration,
				ModelID:        g.ModelID,
				Engine:         g.Engine,
				Device:         g.Device,
				ModelPath:      g.ModelPath,
				Concurrency:    g.Concurrency,
				MaxWidth:       g.MaxWidth,
				MaxHeight:      g.MaxHeight,
				MaxSteps:       g.MaxSteps,
				DefaultSteps:   g.DefaultSteps,
				MaxPromptChars: g.MaxPromptChars,
			},
		}, g.ExpectedRAMMB, g.ExpectedVRAMMB, g.RequestOverheadVRAMMB))
	}

	for _, h := range s.workers {
		h.setState(StateStarting)
		if reason := backendUnavailableReason(h.cfg); reason != "" {
			// Report the native-binding blocker immediately instead of spawning a
			// child that would exit.
			h.setState(StateExited)
			h.setError(reason)
			logger.Printf("ml worker %s unavailable: %s", h.ID, reason)
			continue
		}
		if err := portAvailable(h.Port); err != nil {
			h.setState(StateResourceBlocked)
			h.setError("worker_start_failed: " + err.Error())
			logger.Printf("ml worker %s blocked: %v", h.ID, err)
			continue
		}
		res, ok, reason := s.res.Reserve(h.ModelID, h.Kind, h.ramMB, h.vramMB, h.overheadMB)
		if !ok {
			h.setState(StateResourceBlocked)
			h.setError(reason)
			logger.Printf("ml worker %s blocked by resource admission: %s", h.ID, reason)
			continue
		}
		h.reservation = res
		s.wg.Add(1)
		go s.supervise(h)
	}
	return s, nil
}

// newHandle is a helper that stores admission budgets.
func (s *Service) newHandle(h *WorkerHandle, ramMB, vramMB, overhead int64) *WorkerHandle {
	h.ramMB = ramMB
	h.vramMB = vramMB
	h.overheadMB = overhead
	h.client = &http.Client{}
	h.baseURL = fmt.Sprintf("http://127.0.0.1:%d", h.Port)
	if h.Concurrency < 1 {
		h.Concurrency = 1
	}
	if h.startTimeout <= 0 {
		h.startTimeout = defaultStartupTimeout
	}
	return h
}

func (s *Service) writeWorkerConfig(h *WorkerHandle) (string, error) {
	raw, err := workerConfigJSON(h.cfg)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.tmpDir, h.ID+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (s *Service) supervise(h *WorkerHandle) {
	defer s.wg.Done()
	defer h.releaseReservation(s.res)

	for attempt := 0; ; attempt++ {
		if s.ctx.Err() != nil {
			h.setState(StateStopping)
			return
		}
		if attempt > 0 {
			h.restarts.Add(1)
			s.logger.Printf("ml worker %s has been ready before; restarting (attempt %d/%d)", h.ID, attempt, maxRestarts)
			select {
			case <-time.After(time.Duration(attempt) * time.Second):
			case <-s.ctx.Done():
				h.setState(StateStopping)
				return
			}
		}

		configPath, err := s.writeWorkerConfig(h)
		if err != nil {
			h.setState(StateExited)
			h.setError("worker_start_failed: " + err.Error())
			return
		}

		h.setState(StateStarting)
		cmd := s.cmdBuilder(h, configPath)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			h.setState(StateExited)
			h.setError("worker_start_failed: " + err.Error())
			s.logger.Printf("ml worker %s failed to start: %v", h.ID, err)
			return
		}
		h.setCmd(cmd)

		exit := &processExit{done: make(chan struct{})}
		go exit.wait(cmd)

		if !s.waitReady(h, cmd, exit) {
			// Initialisation failures (bad model path, missing native library,
			// port conflict) are not restarted.
			stopProcess(cmd, exit.done, shutdownGrace)
			h.setState(StateExited)
			if h.LastError() == "" {
				h.setError("worker_start_failed: worker did not become ready")
			}
			s.logger.Printf("ml worker %s initialisation failed: %s", h.ID, h.LastError())
			return
		}

		h.setState(StateReady)
		s.logger.Printf("ml worker %s ready (model=%s pid=%d port=%d)", h.ID, h.ModelID, cmd.Process.Pid, h.Port)

		runErr := s.monitor(h, cmd, exit)
		h.markCheck()
		if s.ctx.Err() != nil {
			h.setState(StateStopping)
			return
		}
		h.setState(StateExited)
		if runErr != nil {
			h.setError("worker exited: " + runErr.Error())
		} else {
			h.setError("worker exited")
		}
		s.logger.Printf("ml worker %s exited: %s", h.ID, h.LastError())
		if attempt >= maxRestarts {
			s.logger.Printf("ml worker %s reached the restart limit; leaving it exited", h.ID)
			return
		}
	}
}

// processExit tracks when a child exits and its wait error. The done channel is
// closed exactly once, so any number of waiters can observe the exit.
type processExit struct {
	done chan struct{}
	mu   sync.Mutex
	err  error
}

func (p *processExit) wait(cmd *exec.Cmd) {
	err := cmd.Wait()
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	close(p.done)
}

func (p *processExit) Err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

func (s *Service) waitReady(h *WorkerHandle, cmd *exec.Cmd, exit *processExit) bool {
	deadline := time.Now().Add(h.startTimeout)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	check := func() bool {
		h.markCheck()
		req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, h.baseURL+"/ready", nil)
		if err != nil {
			return false
		}
		resp, err := h.client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}

	if check() {
		return true
	}
	for {
		select {
		case <-exit.done:
			h.setError("worker_start_failed: exited during startup: " + errString(exit.Err()))
			return false
		case <-s.ctx.Done():
			return false
		case <-ticker.C:
			if time.Now().After(deadline) {
				h.setError("worker_start_failed: readiness timeout")
				return false
			}
			if check() {
				return true
			}
		}
	}
}

func (s *Service) monitor(h *WorkerHandle, cmd *exec.Cmd, exit *processExit) error {
	ticker := time.NewTicker(healthInterval)
	defer ticker.Stop()
	failures := 0

	for {
		select {
		case <-s.ctx.Done():
			h.setState(StateStopping)
			stopProcess(cmd, exit.done, shutdownGrace)
			return nil
		case <-exit.done:
			return exit.Err()
		case <-ticker.C:
			h.markCheck()
			if s.healthCheck(h) {
				failures = 0
				if h.State() != StateReady {
					h.setState(StateReady)
					s.logger.Printf("ml worker %s recovered", h.ID)
				}
			} else {
				failures++
				if failures >= 3 {
					h.setState(StateUnhealthy)
				}
			}
		}
	}
}

func (s *Service) healthCheck(h *WorkerHandle) bool {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, h.baseURL+"/ready", nil)
	if err != nil {
		return false
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func stopProcess(cmd *exec.Cmd, done <-chan struct{}, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	select {
	case <-done:
		return
	default:
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(grace):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}

// Shutdown stops accepting ML work, drains in-flight requests until the caller's
// deadline, terminates children gracefully, then force-kills any remaining
// child. Reservations are released by the supervisors.
func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.shuttingDown.Store(true)

	// Let in-flight OCR/image requests finish before stopping workers.
	for s.inFlight() > 0 {
		if ctx.Err() != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	s.cancel()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		for _, h := range s.workers {
			if cmd := h.getCmd(); cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	os.RemoveAll(s.tmpDir)
	return nil
}

// inFlight is the total number of in-flight ML inference requests.
func (s *Service) inFlight() int64 {
	var total int64
	for _, h := range s.workers {
		total += h.inflight.Load()
	}
	return total
}

// Models returns the current state of every configured ML model.
func (s *Service) Models() []ModelStatus {
	if s == nil {
		return nil
	}
	out := make([]ModelStatus, 0, len(s.workers))
	for _, h := range s.workers {
		out = append(out, ModelStatus{
			ID:      h.ModelID,
			Object:  "ml.model",
			Kind:    h.Kind,
			State:   h.State(),
			Healthy: h.IsReady(),
			Worker:  h.ID,
		})
	}
	return out
}

// Snapshot returns worker and resource status for /ml/status.
func (s *Service) Snapshot() StatusResponse {
	if s == nil {
		return StatusResponse{Enabled: false}
	}
	out := StatusResponse{Enabled: true, Models: s.Models(), Workers: make([]WorkerStatus, 0, len(s.workers))}
	for _, h := range s.workers {
		h.mu.RLock()
		lastCheck := ""
		if !h.lastCheck.IsZero() {
			lastCheck = h.lastCheck.UTC().Format(time.RFC3339)
		}
		out.Workers = append(out.Workers, WorkerStatus{
			ID:              h.ID,
			Kind:            h.Kind,
			ModelID:         h.ModelID,
			Engine:          h.Engine,
			Device:          h.Device,
			PID:             h.pid,
			Port:            h.Port,
			State:           h.state,
			Healthy:         h.state == StateReady,
			InFlight:        h.inflight.Load(),
			Concurrency:     int64(h.Concurrency),
			Served:          h.served.Load(),
			Failed:          h.failed.Load(),
			Restarts:        h.restarts.Load(),
			LastCheck:       lastCheck,
			LastError:       h.lastError,
			LastInferenceMS: h.lastInferenceMS.Load(),
		})
		h.mu.RUnlock()
	}
	out.Resources = s.res.Status()
	return out
}

func portAvailable(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("port %d is not available: %w", port, err)
	}
	return ln.Close()
}

func errString(err error) string {
	if err == nil {
		return "no error"
	}
	return err.Error()
}
