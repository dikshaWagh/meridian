package ml

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestHelperMLWorkerProcess is re-executed by the supervisor tests to act as a
// real ML child process. It exits immediately when not invoked by a test.
func TestHelperMLWorkerProcess(t *testing.T) {
	if os.Getenv("GO_WANT_ML_WORKER") != "1" {
		return
	}
	if err := RunWorker(os.Getenv("ML_WORKER_KIND"), os.Getenv("ML_WORKER_CONFIG"), os.Getenv("ML_WORKER_LISTEN")); err != nil {
		fmt.Fprintln(os.Stderr, "helper worker:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestHelperMLWorkerInitFailure(t *testing.T) {
	if os.Getenv("GO_WANT_ML_FAIL") != "1" {
		return
	}
	os.Exit(1)
}

// TestHelperStubbornWorker ignores SIGTERM to exercise forced shutdown.
func TestHelperStubbornWorker(t *testing.T) {
	if os.Getenv("GO_WANT_ML_STUBBORN") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }
	mux.HandleFunc("/health", ok)
	mux.HandleFunc("/ready", ok)
	_ = http.ListenAndServe(os.Getenv("ML_WORKER_LISTEN"), mux)
	os.Exit(0)
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func helperCommand() func(h *WorkerHandle, configPath string) *exec.Cmd {
	return func(h *WorkerHandle, configPath string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperMLWorkerProcess", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_ML_WORKER=1",
			"ML_WORKER_KIND="+string(h.Kind),
			"ML_WORKER_CONFIG="+configPath,
			"ML_WORKER_LISTEN="+fmt.Sprintf("127.0.0.1:%d", h.Port),
		)
		return cmd
	}
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func testServiceConfig(t *testing.T) *Config {
	t.Helper()
	cfg := testConfig(t)
	cfg.OCR.Port = freePort(t)
	cfg.ImageGeneration.Port = freePort(t)
	cfg.Resources.RAMHeadroomMB = 256
	cfg.OCR.ExpectedRAMMB = 256
	cfg.ImageGeneration.ExpectedRAMMB = 512
	return cfg
}

func TestSupervisorStartsWorkersAndShutsDown(t *testing.T) {
	cfg := testServiceConfig(t)
	probe := fakeProbe(1<<20, 1<<20, 0, 0, false)
	svc, err := Start(context.Background(), cfg, Options{Probe: probe, CommandBuilder: helperCommand()})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "both workers ready", func() bool {
		models := svc.Models()
		if len(models) != 2 {
			return false
		}
		for _, m := range models {
			if !m.Healthy {
				return false
			}
		}
		return true
	})

	status := svc.Snapshot()
	if len(status.Workers) != 2 {
		t.Fatalf("workers = %d", len(status.Workers))
	}
	pids := make([]int, 0, 2)
	for _, w := range status.Workers {
		if w.PID <= 0 || w.State != StateReady || !w.Healthy {
			t.Fatalf("unexpected worker status: %+v", w)
		}
		pids = append(pids, w.PID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err == nil {
			t.Fatalf("ml child %d still alive after Shutdown", pid)
		}
	}
}

func TestSupervisorDoesNotRestartInitFailure(t *testing.T) {
	cfg := testServiceConfig(t)
	cfg.ImageGeneration.Enabled = false
	probe := fakeProbe(1<<20, 1<<20, 0, 0, false)
	builder := func(h *WorkerHandle, configPath string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperMLWorkerInitFailure", "--")
		cmd.Env = append(os.Environ(), "GO_WANT_ML_FAIL=1")
		return cmd
	}
	svc, err := Start(context.Background(), cfg, Options{Probe: probe, CommandBuilder: builder})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	}()

	waitFor(t, 10*time.Second, "worker to fail", func() bool {
		return svc.Models()[0].State == StateExited
	})
	time.Sleep(1200 * time.Millisecond) // longer than the restart backoff
	status := svc.Snapshot()
	if status.Workers[0].Restarts != 0 {
		t.Fatalf("initialisation failure triggered %d restarts", status.Workers[0].Restarts)
	}
	if status.Workers[0].State != StateExited {
		t.Fatalf("state = %s, want exited", status.Workers[0].State)
	}
	if !strings.Contains(status.Workers[0].LastError, "worker_start_failed") {
		t.Fatalf("LastError = %q", status.Workers[0].LastError)
	}
}

func TestSupervisorMarksPortConflictBlocked(t *testing.T) {
	cfg := testServiceConfig(t)
	cfg.ImageGeneration.Enabled = false

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.OCR.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var spawned atomic.Bool
	svc, err := Start(context.Background(), cfg, Options{
		Probe: fakeProbe(1<<20, 1<<20, 0, 0, false),
		CommandBuilder: func(h *WorkerHandle, p string) *exec.Cmd {
			spawned.Store(true)
			return exec.Command("false")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	}()
	if spawned.Load() {
		t.Fatal("port-conflicted worker must not be spawned")
	}

	status := svc.Snapshot()
	if status.Workers[0].State != StateResourceBlocked {
		t.Fatalf("state = %s, want resource_blocked", status.Workers[0].State)
	}
	if !strings.Contains(status.Workers[0].LastError, "port") {
		t.Fatalf("LastError = %q", status.Workers[0].LastError)
	}
}

func TestSupervisorForceKillsAfterTimeout(t *testing.T) {
	cfg := testServiceConfig(t)
	cfg.ImageGeneration.Enabled = false
	builder := func(h *WorkerHandle, configPath string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperStubbornWorker", "--")
		cmd.Env = append(os.Environ(),
			"GO_WANT_ML_STUBBORN=1",
			"ML_WORKER_LISTEN="+fmt.Sprintf("127.0.0.1:%d", h.Port),
		)
		return cmd
	}
	svc, err := Start(context.Background(), cfg, Options{
		Probe: fakeProbe(1<<20, 1<<20, 0, 0, false), CommandBuilder: builder,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "stubborn worker ready", func() bool { return svc.Models()[0].Healthy })
	pid := svc.Snapshot().Workers[0].PID

	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	start := time.Now()
	_ = svc.Shutdown(ctx)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("forced shutdown took %v", elapsed)
	}
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("stubborn child %d still alive after forced shutdown", pid)
	}
}

// TestMLAdmissionKeepsLLMProcessAlive is the end-to-end form of the protect-llm
// invariant: a running (fake) LLM process is untouched when ML workers are
// rejected for lack of resources.
func TestMLAdmissionKeepsLLMProcessAlive(t *testing.T) {
	llm := exec.Command("sleep", "30")
	if err := llm.Start(); err != nil {
		t.Skipf("cannot start helper process: %v", err)
	}
	defer func() {
		_ = llm.Process.Kill()
		_ = llm.Wait()
	}()

	cfg := testServiceConfig(t)
	svc, err := Start(context.Background(), cfg, Options{
		Probe:          fakeProbe(128, 256, 0, 0, false), // less than headroom + demand
		CommandBuilder: nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	}()

	for _, m := range svc.Models() {
		if m.State != StateResourceBlocked {
			t.Fatalf("model %s state = %s, want resource_blocked", m.ID, m.State)
		}
	}
	if err := llm.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("LLM process was signalled during ML admission: %v", err)
	}
}

func TestNativeBackendBlockedWithClearError(t *testing.T) {
	_, _, err := newWorkerHandler(WorkerConfig{Kind: KindOCR, ModelID: "x", Engine: EngineONNXRuntime, Concurrency: 1})
	if err == nil || !strings.Contains(err.Error(), "unavailable in this build") {
		t.Fatalf("expected clear native-backend blocker, got %v", err)
	}
	_, _, err = newWorkerHandler(WorkerConfig{Kind: KindImageGeneration, ModelID: "x", Engine: EngineStableDiffusionCPP, Concurrency: 1})
	if err == nil || !strings.Contains(err.Error(), "unavailable in this build") {
		t.Fatalf("expected clear native-backend blocker, got %v", err)
	}
}

func TestStartBlocksNativeEngineWithoutSpawning(t *testing.T) {
	cfg := testServiceConfig(t)
	cfg.ImageGeneration.Enabled = false
	cfg.OCR.Engine = EngineONNXRuntime
	cfg.OCR.ModelDir = t.TempDir()

	var spawned atomic.Bool
	svc, err := Start(context.Background(), cfg, Options{
		Probe: fakeProbe(1<<20, 1<<20, 0, 0, false),
		CommandBuilder: func(h *WorkerHandle, p string) *exec.Cmd {
			spawned.Store(true)
			return exec.Command("false")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = svc.Shutdown(ctx)
	}()

	status := svc.Snapshot()
	if spawned.Load() {
		t.Fatal("native engine must be blocked before spawning a child")
	}
	if status.Workers[0].State != StateExited {
		t.Fatalf("state = %s, want exited", status.Workers[0].State)
	}
	if !strings.Contains(status.Workers[0].LastError, "unavailable in this build") {
		t.Fatalf("LastError = %q", status.Workers[0].LastError)
	}
}
