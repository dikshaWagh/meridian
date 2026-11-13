package arbiter

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSub struct {
	mu         sync.Mutex
	name       string
	order      *[]string
	starts     int
	stops      int
	startErr   error
	readyErr   error
	readyDelay time.Duration
	blockStop  bool
}

func (f *fakeSub) record(what string) {
	f.mu.Lock()
	if f.order != nil {
		*f.order = append(*f.order, f.name+"."+what)
	}
	if what == "start" {
		f.starts++
	} else {
		f.stops++
	}
	f.mu.Unlock()
}

func (f *fakeSub) Start(context.Context) error {
	f.record("start")
	return f.startErr
}

func (f *fakeSub) Stop(ctx context.Context) error {
	// Record the stop before blocking so a test can observe the ordering.
	f.mu.Lock()
	if f.order != nil && !f.blockStop {
		*f.order = append(*f.order, f.name+".stop")
	}
	if !f.blockStop {
		f.stops++
	}
	f.mu.Unlock()
	if f.blockStop {
		<-ctx.Done()
	}
	return nil
}

func (f *fakeSub) WaitReady(ctx context.Context) error {
	if f.readyDelay > 0 {
		select {
		case <-time.After(f.readyDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.readyErr
}

func (f *fakeSub) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

func testArb(t *testing.T, llm, ml *fakeSub, initial Mode) *Arbiter {
	t.Helper()
	subs := map[Mode]Subsystem{}
	if llm != nil {
		subs[ModeLLM] = llm
	}
	if ml != nil {
		subs[ModeML] = ml
	}
	return New(Options{
		Subsystems:   subs,
		Initial:      initial,
		DrainTimeout: 2 * time.Second,
		StopTimeout:  2 * time.Second,
		StartTimeout: 2 * time.Second,
		ReadyTimeout: 2 * time.Second,
		SettleDelay:  time.Millisecond,
		Logger:       log.New(io.Discard, "", 0),
	})
}

func waitFor(t *testing.T, arb *Arbiter, timeout time.Duration, pred func(State) bool) State {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st := arb.State()
		if pred(st) {
			return st
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s; last state: %+v", timeout, arb.State())
	return State{}
}

func serve(arb *Arbiter, method, path string) *httptest.ResponseRecorder {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	arb.Middleware(next).ServeHTTP(rec, req)
	return rec
}

func TestActivateAndGuard(t *testing.T) {
	llm, ml := &fakeSub{name: "llm"}, &fakeSub{name: "ml"}
	arb := testArb(t, llm, ml, ModeIdle)
	if err := arb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive && s.Mode == ModeLLM })

	if rec := serve(arb, http.MethodPost, "/v1/chat/completions"); rec.Code != 200 {
		t.Fatalf("chat in llm mode: got %d", rec.Code)
	}
	rec := serve(arb, http.MethodPost, "/v1/ocr")
	if rec.Code != http.StatusConflict {
		t.Fatalf("ocr in llm mode: got %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "mode_conflict") {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if rec := serve(arb, http.MethodGet, "/v1/ocr"); rec.Code != 200 {
		t.Fatalf("GET /v1/ocr should not be guarded, got %d", rec.Code)
	}

	if _, _, err := arb.Activate(ModeML); err != nil {
		t.Fatalf("Activate(ml): %v", err)
	}
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive && s.Mode == ModeML })
	if rec := serve(arb, http.MethodPost, "/v1/chat/completions"); rec.Code != http.StatusConflict {
		t.Fatalf("chat in ml mode: got %d, want 409", rec.Code)
	}
	if rec := serve(arb, http.MethodPost, "/v1/images/generations"); rec.Code != 200 {
		t.Fatalf("image gen in ml mode: got %d", rec.Code)
	}
}

func TestSwitchDrainsBeforeStopping(t *testing.T) {
	var order []string
	llm := &fakeSub{name: "llm", order: &order}
	ml := &fakeSub{name: "ml", order: &order}
	arb := testArb(t, llm, ml, ModeLLM)
	if err := arb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive && s.Mode == ModeLLM })

	entered := make(chan struct{})
	release := make(chan struct{})
	blocking := arb.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	go blocking.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	<-entered
	if got := arb.InFlight(ModeLLM); got != 1 {
		t.Fatalf("in-flight = %d, want 1", got)
	}

	if _, _, err := arb.Activate(ModeML); err != nil {
		t.Fatalf("Activate(ml): %v", err)
	}
	waitFor(t, arb, time.Second, func(s State) bool { return s.Phase == PhaseDraining })

	llm.mu.Lock()
	stopsDuringDrain := llm.stops
	llm.mu.Unlock()
	ml.mu.Lock()
	startsDuringDrain := ml.starts
	ml.mu.Unlock()
	if stopsDuringDrain != 0 || startsDuringDrain != 0 {
		t.Fatalf("stop/start happened before drain: stops=%d starts=%d", stopsDuringDrain, startsDuringDrain)
	}

	close(release)
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive && s.Mode == ModeML })

	joined := strings.Join(order, ",")
	if !strings.Contains(joined, "llm.stop,ml.start") {
		t.Fatalf("expected llm.stop before ml.start, got %q", joined)
	}
}

func TestDrainTimeoutAbortsSwitch(t *testing.T) {
	llm, ml := &fakeSub{name: "llm"}, &fakeSub{name: "ml"}
	subs := map[Mode]Subsystem{ModeLLM: llm, ModeML: ml}
	arb := New(Options{
		Subsystems:   subs,
		Initial:      ModeLLM,
		DrainTimeout: 80 * time.Millisecond,
		StopTimeout:  time.Second,
		StartTimeout: time.Second,
		ReadyTimeout: time.Second,
		SettleDelay:  time.Millisecond,
		Logger:       log.New(io.Discard, "", 0),
	})
	if err := arb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive })

	release := make(chan struct{})
	defer close(release)
	blocking := arb.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	go blocking.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	waitFor(t, arb, time.Second, func(s State) bool { return arb.InFlight(ModeLLM) == 1 })

	if _, _, err := arb.Activate(ModeML); err != nil {
		t.Fatalf("Activate(ml): %v", err)
	}
	st := waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive && s.Mode == ModeLLM && s.LastError != "" })
	if !strings.Contains(st.LastError, "draining") {
		t.Fatalf("last error = %q", st.LastError)
	}
	if starts, _ := ml.counts(); starts != 0 {
		t.Fatalf("ml started despite aborted switch: %d", starts)
	}
}

func TestStartFailureLeavesIdle(t *testing.T) {
	llm := &fakeSub{name: "llm"}
	ml := &fakeSub{name: "ml", startErr: errors.New("boom")}
	arb := testArb(t, llm, ml, ModeLLM)
	if err := arb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive })

	if _, _, err := arb.Activate(ModeML); err != nil {
		t.Fatalf("Activate(ml): %v", err)
	}
	st := waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseFailed })
	if st.Mode != ModeIdle {
		t.Fatalf("mode = %s, want idle", st.Mode)
	}
	if !strings.Contains(st.LastError, "boom") {
		t.Fatalf("last error = %q", st.LastError)
	}
	// Both subsystems are unavailable while idle: reject, do not pretend.
	if rec := serve(arb, http.MethodPost, "/v1/ocr"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ocr while failed: got %d, want 503", rec.Code)
	}
}

func TestUnavailableMode(t *testing.T) {
	llm := &fakeSub{name: "llm"}
	arb := testArb(t, llm, nil, ModeLLM)
	if err := arb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive })
	if _, _, err := arb.Activate(ModeML); !errors.Is(err, ErrModeUnavailable) {
		t.Fatalf("err = %v, want ErrModeUnavailable", err)
	}
}

func TestEvents(t *testing.T) {
	llm, ml := &fakeSub{name: "llm"}, &fakeSub{name: "ml"}
	arb := testArb(t, llm, ml, ModeLLM)
	ch, unsub := arb.Subscribe()
	defer unsub()
	if err := arb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("no active event")
		case ev := <-ch:
			if ev.Seq == 0 {
				t.Fatalf("event sequence not set: %+v", ev)
			}
			if ev.State.Phase == PhaseActive && ev.State.Mode == ModeLLM {
				return
			}
		}
	}
}

func TestModeEndpoint(t *testing.T) {
	llm, ml := &fakeSub{name: "llm"}, &fakeSub{name: "ml"}
	arb := testArb(t, llm, ml, ModeLLM)
	mux := http.NewServeMux()
	arb.Register(mux)
	if err := arb.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive })

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/system/mode", strings.NewReader(`{"mode":"bogus"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mode: got %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/system/mode", strings.NewReader(`{"mode":"ml"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("switch: got %d, want 202", rec.Code)
	}
	waitFor(t, arb, 2*time.Second, func(s State) bool { return s.Phase == PhaseActive && s.Mode == ModeML })

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/system/state", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"mode":"ml"`) {
		t.Fatalf("state: %d %s", rec.Code, rec.Body.String())
	}
}
