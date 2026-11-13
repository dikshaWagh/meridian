// Package arbiter enforces exclusive serving between the LLM and ML
// subsystems.
//
// Some hosts cannot keep llama.cpp and the ML workers resident at the same
// time: the model weights compete for VRAM/RAM and both processes cannot
// coexist. The arbiter owns that resource as a single lease. Exactly one
// subsystem is active at a time; switching drains in-flight requests, stops
// the current subsystem, waits for it to release memory, starts the target and
// waits for it to warm up.
//
// The package knows nothing about llama.cpp or OCR: subsystems are injected
// through the Subsystem interface, which keeps it testable with fakes.
package arbiter

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Mode identifies a mutually exclusive serving subsystem.
type Mode string

const (
	ModeIdle Mode = "idle"
	ModeLLM  Mode = "llm"
	ModeML   Mode = "ml"
)

// Activation phases reported to clients.
const (
	PhaseIdle     = "idle"
	PhaseDraining = "draining"
	PhaseStopping = "stopping"
	PhaseStarting = "starting"
	PhaseWarming  = "warming"
	PhaseActive   = "active"
	PhaseFailed   = "failed"
)

var (
	ErrUnknownMode      = errors.New("unknown mode")
	ErrModeUnavailable  = errors.New("mode is not enabled on this server")
	ErrSwitchInProgress = errors.New("a mode switch is already in progress")
	ErrClosed           = errors.New("arbiter is closed")
	ErrNoSubsystems     = errors.New("arbiter has no subsystems")
)

// ParseMode validates a client-supplied mode string.
func ParseMode(s string) (Mode, bool) {
	switch Mode(s) {
	case ModeLLM:
		return ModeLLM, true
	case ModeML:
		return ModeML, true
	}
	return ModeIdle, false
}

// Subsystem is one side of the exclusive lease.
//
// Start must return once the process(es) have been launched; readiness is
// reported separately by WaitReady. Stop must block until the subsystem has
// released its resources (child processes exited, memory unloaded).
type Subsystem interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	WaitReady(ctx context.Context) error
}

// Options configures an Arbiter. Zero durations pick production defaults.
type Options struct {
	Subsystems map[Mode]Subsystem
	// Initial is the mode activated by Start. Empty picks llm, then ml.
	Initial Mode

	DrainTimeout time.Duration // wait for in-flight requests before stopping
	StopTimeout  time.Duration
	StartTimeout time.Duration
	ReadyTimeout time.Duration
	SettleDelay  time.Duration // pause after Stop for memory to be reclaimed

	Logger *log.Logger
}

type options struct {
	subsystems   map[Mode]Subsystem
	initial      Mode
	drainTimeout time.Duration
	stopTimeout  time.Duration
	startTimeout time.Duration
	readyTimeout time.Duration
	settleDelay  time.Duration
	logger       *log.Logger
}

func withDefaults(o Options) options {
	opt := options{
		subsystems:   o.Subsystems,
		initial:      o.Initial,
		drainTimeout: o.DrainTimeout,
		stopTimeout:  o.StopTimeout,
		startTimeout: o.StartTimeout,
		readyTimeout: o.ReadyTimeout,
		settleDelay:  o.SettleDelay,
		logger:       o.Logger,
	}
	if opt.drainTimeout <= 0 {
		opt.drainTimeout = 30 * time.Second
	}
	if opt.stopTimeout <= 0 {
		opt.stopTimeout = 60 * time.Second
	}
	if opt.startTimeout <= 0 {
		opt.startTimeout = 60 * time.Second
	}
	if opt.readyTimeout <= 0 {
		opt.readyTimeout = 5 * time.Minute
	}
	if opt.settleDelay < 0 {
		opt.settleDelay = 0
	}
	if opt.settleDelay == 0 {
		opt.settleDelay = 750 * time.Millisecond
	}
	if opt.logger == nil {
		opt.logger = log.Default()
	}
	return opt
}

// State is the public snapshot returned by GET /v1/system/state.
type State struct {
	Mode       Mode             `json:"mode"`
	Target     Mode             `json:"target"`
	Phase      string           `json:"phase"`
	Switching  bool             `json:"switching"`
	Progress   int              `json:"progress"`
	Message    string           `json:"message"`
	LastError  string           `json:"last_error,omitempty"`
	Available  []Mode           `json:"available"`
	InFlight   map[string]int64 `json:"in_flight"`
	Attempt    uint64           `json:"attempt"`
	Exclusive  bool             `json:"exclusive"`
	ObservedAt string           `json:"observed_at"`
}

// Event is broadcast to SSE subscribers whenever the state changes.
type Event struct {
	Type  string `json:"type"`
	Seq   uint64 `json:"seq"`
	State State  `json:"state"`
}

// Arbiter owns the exclusive lease.
type Arbiter struct {
	opts      options
	subs      map[Mode]Subsystem
	available []Mode
	inFlight  map[Mode]*atomic.Int64

	mu         sync.Mutex
	mode       Mode
	target     Mode
	phase      string
	progress   int
	message    string
	lastError  string
	since      time.Time
	switching  bool
	closed     bool
	attempt    uint64
	activation context.CancelFunc
	done       chan struct{}

	subSeq      int64
	subscribers map[int64]chan Event
	eventSeq    uint64
}

// New builds an arbiter. It does not start anything until Start is called.
func New(o Options) *Arbiter {
	opt := withDefaults(o)
	a := &Arbiter{
		opts:        opt,
		subs:        make(map[Mode]Subsystem, len(opt.subsystems)),
		available:   make([]Mode, 0, len(opt.subsystems)),
		inFlight:    map[Mode]*atomic.Int64{ModeLLM: &atomic.Int64{}, ModeML: &atomic.Int64{}},
		mode:        ModeIdle,
		target:      ModeIdle,
		phase:       PhaseIdle,
		message:     "idle",
		since:       time.Now().UTC(),
		subscribers: map[int64]chan Event{},
	}
	a.available = append(a.available, ModeLLM)
	a.available = append(a.available, ModeML)
	for _, m := range a.available {
		if sub, ok := opt.subsystems[m]; ok && sub != nil {
			a.subs[m] = sub
		}
	}
	a.available = a.available[:0]
	for m := range a.subs {
		a.available = append(a.available, m)
	}
	sort.Slice(a.available, func(i, j int) bool { return a.available[i] < a.available[j] })
	return a
}

// Start activates the initial mode asynchronously. The HTTP server starts
// listening immediately; GET /v1/system/state reports warm-up progress.
func (a *Arbiter) Start() error {
	if len(a.subs) == 0 {
		return ErrNoSubsystems
	}
	a.mu.Lock()
	initial := a.opts.initial
	if initial == "" || initial == ModeIdle {
		if _, ok := a.subs[ModeLLM]; ok {
			initial = ModeLLM
		} else {
			initial = ModeML
		}
	}
	a.mu.Unlock()
	if _, ok := a.subs[initial]; !ok {
		return fmt.Errorf("%w: %s", ErrModeUnavailable, initial)
	}
	_, _, err := a.Activate(initial)
	return err
}

// Activate starts a switch. It returns immediately with the new state; the
// bool reports whether a new switch was launched (false means already there).
func (a *Arbiter) Activate(mode Mode) (State, bool, error) {
	if mode != ModeLLM && mode != ModeML {
		return a.State(), false, fmt.Errorf("%w: %q", ErrUnknownMode, mode)
	}
	a.mu.Lock()
	if a.closed {
		st := a.snapshotLocked()
		a.mu.Unlock()
		return st, false, ErrClosed
	}
	if _, ok := a.subs[mode]; !ok {
		st := a.snapshotLocked()
		a.mu.Unlock()
		return st, false, fmt.Errorf("%w: %s", ErrModeUnavailable, mode)
	}
	if a.switching {
		st := a.snapshotLocked()
		if st.Target == mode {
			a.mu.Unlock()
			return st, false, nil
		}
		a.mu.Unlock()
		return st, false, ErrSwitchInProgress
	}
	if a.mode == mode && a.phase == PhaseActive {
		st := a.snapshotLocked()
		a.mu.Unlock()
		return st, false, nil
	}

	from := a.mode
	a.switching = true
	a.target = mode
	a.progress = 2
	a.phase = PhaseDraining
	a.message = "draining in-flight requests"
	a.lastError = ""
	a.attempt++
	a.since = time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	a.activation = cancel
	a.done = make(chan struct{})
	done := a.done
	st := a.snapshotLocked()
	a.broadcastLocked()
	a.mu.Unlock()

	a.opts.logger.Printf("arbiter: switching %s -> %s", from, mode)
	go a.runActivation(ctx, done, from, mode)
	return st, true, nil
}

func (a *Arbiter) runActivation(ctx context.Context, done chan struct{}, from, to Mode) {
	defer close(done)
	startedTarget := false
	defer func() {
		a.mu.Lock()
		a.switching = false
		a.activation = nil
		a.done = nil
		a.mu.Unlock()
	}()
	defer func() {
		if ctx.Err() != nil && startedTarget {
			a.stopQuietly(to)
		}
	}()

	if from != ModeIdle {
		if !a.waitDrain(ctx, from) {
			if ctx.Err() != nil {
				return
			}
			a.abortSwitch(from, errors.New("timed out draining in-flight requests"))
			return
		}
	}

	// Nothing new can acquire either subsystem from here on.
	a.update(func(st *State) {
		st.Mode = ModeIdle
		st.Phase = PhaseStopping
		st.Progress = 25
		st.Message = "stopping " + string(from)
	})

	if from != ModeIdle {
		sub := a.subs[from]
		if sub != nil {
			sctx, cancel := context.WithTimeout(ctx, a.opts.stopTimeout)
			err := sub.Stop(sctx)
			cancel()
			if err != nil && ctx.Err() == nil {
				a.fail(to, fmt.Errorf("stopping %s: %w", from, err))
				return
			}
			if ctx.Err() != nil {
				return
			}
		}
	}

	a.update(func(st *State) {
		st.Phase = PhaseStarting
		st.Progress = 55
		st.Message = "starting " + string(to)
	})
	a.opts.logger.Printf("arbiter: starting %s", to)
	if err := a.startWithTimeout(ctx, to); err != nil {
		if ctx.Err() != nil {
			return
		}
		a.fail(to, fmt.Errorf("starting %s: %w", to, err))
		return
	}
	startedTarget = true

	a.update(func(st *State) {
		st.Phase = PhaseWarming
		st.Progress = 70
		st.Message = "waiting for " + string(to) + " workers"
	})
	a.opts.logger.Printf("arbiter: waiting for %s workers to become ready", to)

	readyCtx, cancel := context.WithTimeout(ctx, a.opts.readyTimeout)
	progressCtx, stopProgress := context.WithCancel(readyCtx)
	go a.progressLoop(progressCtx, 70, 95)
	err := a.subs[to].WaitReady(readyCtx)
	stopProgress()
	cancel()
	if ctx.Err() != nil {
		return
	}
	warning := ""
	if err != nil {
		warning = err.Error()
		a.opts.logger.Printf("arbiter: %s did not fully warm up: %v", to, err)
	}

	a.mu.Lock()
	a.mode = to
	a.target = to
	a.phase = PhaseActive
	a.switching = false
	a.progress = 100
	a.message = string(to) + " ready"
	a.lastError = warning
	a.since = time.Now().UTC()
	a.broadcastLocked()
	a.mu.Unlock()
	a.opts.logger.Printf("arbiter: %s is now active", to)
}

func (a *Arbiter) startWithTimeout(ctx context.Context, mode Mode) error {
	sctx, cancel := context.WithTimeout(ctx, a.opts.startTimeout)
	defer cancel()
	return a.subs[mode].Start(sctx)
}

func (a *Arbiter) stopQuietly(mode Mode) {
	sub := a.subs[mode]
	if sub == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.opts.stopTimeout)
	defer cancel()
	if err := sub.Stop(ctx); err != nil {
		a.opts.logger.Printf("arbiter: cleanup stop %s: %v", mode, err)
	}
}

// waitDrain waits until no request holds the given mode, or the timeout fires.
func (a *Arbiter) waitDrain(ctx context.Context, mode Mode) bool {
	counter := a.inFlight[mode]
	if counter == nil {
		return true
	}
	if counter.Load() == 0 {
		return true
	}
	timer := time.NewTimer(a.opts.drainTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			a.opts.logger.Printf("arbiter: drain timeout with %d %s requests in flight", counter.Load(), mode)
			return false
		case <-ticker.C:
			if counter.Load() == 0 {
				return true
			}
		}
	}
}

// abortSwitch restores the mode that was already running. Used when the switch
// is abandoned before the current subsystem was stopped.
func (a *Arbiter) abortSwitch(mode Mode, err error) {
	a.opts.logger.Printf("arbiter: switch aborted, keeping %s: %v", mode, err)
	a.mu.Lock()
	a.mode = mode
	a.target = mode
	a.phase = PhaseActive
	a.switching = false
	a.progress = 100
	a.message = string(mode) + " ready"
	a.lastError = err.Error()
	a.since = time.Now().UTC()
	a.broadcastLocked()
	a.mu.Unlock()
}

func (a *Arbiter) fail(to Mode, err error) {
	a.opts.logger.Printf("arbiter: switch to %s failed: %v", to, err)
	a.mu.Lock()
	a.mode = ModeIdle
	a.target = to
	a.phase = PhaseFailed
	a.switching = false
	a.progress = 0
	a.message = "switch failed"
	a.lastError = err.Error()
	a.since = time.Now().UTC()
	a.broadcastLocked()
	a.mu.Unlock()
}

func (a *Arbiter) update(fn func(*State)) {
	a.mu.Lock()
	st := a.snapshotLocked()
	fn(&st)
	a.mode = st.Mode
	a.target = st.Target
	a.phase = st.Phase
	a.progress = st.Progress
	a.message = st.Message
	a.since = time.Now().UTC()
	a.broadcastLocked()
	a.mu.Unlock()
}

func (a *Arbiter) progressLoop(ctx context.Context, from, to int) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.Lock()
			if a.progress < to {
				a.progress += 3
				if a.progress > to {
					a.progress = to
				}
				if a.progress > from {
					a.message = "waiting for workers to become ready"
				}
				a.broadcastLocked()
			}
			a.mu.Unlock()
		}
	}
}

// State returns a consistent snapshot.
func (a *Arbiter) State() State {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.snapshotLocked()
}

func (a *Arbiter) snapshotLocked() State {
	available := make([]Mode, len(a.available))
	copy(available, a.available)
	inflight := map[string]int64{
		string(ModeLLM): a.inFlight[ModeLLM].Load(),
		string(ModeML):  a.inFlight[ModeML].Load(),
	}
	return State{
		Mode:       a.mode,
		Target:     a.target,
		Phase:      a.phase,
		Switching:  a.switching,
		Progress:   a.progress,
		Message:    a.message,
		LastError:  a.lastError,
		Available:  available,
		InFlight:   inflight,
		Attempt:    a.attempt,
		Exclusive:  true,
		ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// Enabled reports whether a mode can ever be activated.
func (a *Arbiter) Enabled(mode Mode) bool {
	_, ok := a.subs[mode]
	return ok
}

// InFlight reports the number of guarded requests for a mode.
func (a *Arbiter) InFlight(mode Mode) int64 {
	if c := a.inFlight[mode]; c != nil {
		return c.Load()
	}
	return 0
}

// acquire admits a request into a mode or returns an HTTP error. A non-nil
// release must be called when the request completes; it feeds the drain step.
func (a *Arbiter) acquire(mode Mode) (release func(), st State, status int, errType, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st = a.snapshotLocked()
	if a.closed {
		return nil, st, 503, "service_disabled", "server is shutting down"
	}
	if _, ok := a.subs[mode]; !ok {
		return nil, st, 409, "mode_unavailable", fmt.Sprintf("%s mode is not enabled on this server", mode)
	}
	if a.phase == PhaseFailed && !a.switching {
		msg := "mode switch failed"
		if a.lastError != "" {
			msg += ": " + a.lastError
		}
		return nil, st, 503, "switch_failed", msg
	}
	if a.switching || a.phase != PhaseActive {
		if a.switching && a.target != ModeIdle {
			return nil, st, 503, "switching", fmt.Sprintf("switching to %s (%s, %d%%)", a.target, a.phase, a.progress)
		}
		return nil, st, 503, "starting", fmt.Sprintf("server is starting (%s, %d%%)", a.phase, a.progress)
	}
	if a.mode != mode {
		return nil, st, 409, "mode_conflict", fmt.Sprintf("server is in %s mode; switch to %s first", a.mode, mode)
	}
	counter := a.inFlight[mode]
	if counter == nil {
		return nil, st, 503, "service_disabled", fmt.Sprintf("%s mode is not enabled", mode)
	}
	counter.Add(1)
	return func() { counter.Add(-1) }, st, 0, "", ""
}

func (a *Arbiter) broadcastLocked() {
	a.eventSeq++
	ev := Event{Type: "state", Seq: a.eventSeq, State: a.snapshotLocked()}
	for _, ch := range a.subscribers {
		select {
		case ch <- ev:
		default:
			// A slow UI must never block a mode transition.
		}
	}
}

// Subscribe returns a channel of state events and an unsubscribe function.
func (a *Arbiter) Subscribe() (<-chan Event, func()) {
	a.mu.Lock()
	id := a.subSeq
	a.subSeq++
	ch := make(chan Event, 16)
	a.subscribers[id] = ch
	a.mu.Unlock()
	return ch, func() {
		a.mu.Lock()
		if c, ok := a.subscribers[id]; ok {
			delete(a.subscribers, id)
			close(c)
		}
		a.mu.Unlock()
	}
}

// Close cancels any in-progress switch and stops the active subsystem.
func (a *Arbiter) Close(ctx context.Context) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	cancel := a.activation
	done := a.done
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
		}
	}

	a.mu.Lock()
	mode := a.mode
	sub := a.subs[mode]
	a.mu.Unlock()
	if mode == ModeIdle || sub == nil {
		return nil
	}
	return sub.Stop(ctx)
}
