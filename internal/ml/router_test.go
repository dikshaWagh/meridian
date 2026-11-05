package ml

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func testSpecs() []WorkerSpec {
	return []WorkerSpec{
		{ID: "W1", Model: "resnet50", URL: "http://127.0.0.1:9001", Enabled: true, GPUEnabled: true},
		{ID: "W2", Model: "resnet50", URL: "http://127.0.0.1:9002", Enabled: true, GPUEnabled: true},
		{ID: "W3", Model: "resnet50", URL: "http://127.0.0.1:9003", Enabled: true, GPUEnabled: true},
		{ID: "Y1", Model: "yolov8", URL: "http://127.0.0.1:9004", Enabled: true, GPUEnabled: true},
	}
}

func testRouter(t *testing.T) *Router {
	t.Helper()
	p, err := NewPool(testSpecs(), DefaultStartupGrace, DefaultFailureThreshold)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return NewRouter(p, DefaultWeights)
}

func worker(t *testing.T, p *Pool, id string) *Worker {
	t.Helper()
	for _, w := range p.Workers() {
		if w.Spec.ID == id {
			return w
		}
	}
	t.Fatalf("no worker %s", id)
	return nil
}

func markHealthy(p *Pool, ids ...string) {
	for _, w := range p.Workers() {
		for _, id := range ids {
			if w.Spec.ID == id {
				w.applyProbe(probeSuccess, "", time.Now())
			}
		}
	}
}

func allHealthy(p *Pool) {
	for _, w := range p.Workers() {
		w.applyProbe(probeSuccess, "", time.Now())
	}
}

// setMetrics gives a worker measured-looking inputs so scoring can be asserted.
func setMetrics(w *Worker, latency time.Duration, inFlight int64, cpu, gpu float64) {
	w.observeLatency(latency)
	w.inFlight.Store(inFlight)
	w.setTelemetry(WorkerTelemetry{
		CPUPercent: cpu, CPUAvailable: true,
		GPUPercent: gpu, GPUAvailable: true,
	})
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// §13: score = 0.40(0.2) + 0.25(0.4) + 0.20(0.5) + 0.15(0.3) = 0.325
func TestScoreFormula(t *testing.T) {
	got := DefaultWeights.Score(0.2, 0.4, 0.5, 0.3)
	if !near(got, 0.325) {
		t.Fatalf("score = %v, want 0.325", got)
	}
}

func TestDefaultWeights(t *testing.T) {
	w := DefaultWeights
	if w.Latency != 0.40 || w.Queue != 0.25 || w.GPU != 0.20 || w.CPU != 0.15 {
		t.Fatalf("unexpected default weights: %+v", w)
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

// §13: a resnet50 request considers only resnet50 workers.
func TestModelMatchingOnly(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	// Make the yolov8 worker by far the most attractive instance.
	setMetrics(worker(t, r.Pool(), "Y1"), time.Millisecond, 0, 1, 1)
	for _, id := range []string{"W1", "W2", "W3"} {
		setMetrics(worker(t, r.Pool(), id), 900*time.Millisecond, 20, 99, 99)
	}
	for i := 0; i < 20; i++ {
		got, d, err := r.Route("resnet50")
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		if got.Spec.Model != "resnet50" {
			t.Fatalf("routed to model %s", got.Spec.Model)
		}
		for _, c := range d.Candidates {
			if c.WorkerID == "Y1" {
				t.Fatal("yolov8 worker appeared as a resnet50 candidate")
			}
		}
	}
}

// §13: an unhealthy worker is never selected, even with the lowest score.
func TestHealthFilterBeatsScore(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	best := worker(t, r.Pool(), "W1")
	setMetrics(best, time.Millisecond, 0, 0, 0)
	setMetrics(worker(t, r.Pool(), "W2"), 500*time.Millisecond, 5, 80, 80)
	setMetrics(worker(t, r.Pool(), "W3"), 500*time.Millisecond, 5, 80, 80)

	if got, _, err := r.Route("resnet50"); err != nil || got.Spec.ID != "W1" {
		t.Fatalf("healthy best should win: %v %v", got, err)
	}

	// W1 keeps the lowest raw metrics but goes UNHEALTHY.
	for i := 0; i < DefaultFailureThreshold; i++ {
		best.applyProbe(probeFailure, "boom", time.Now())
	}
	if best.IsHealthy() {
		t.Fatal("W1 should be unhealthy")
	}
	for i := 0; i < 20; i++ {
		got, d, err := r.Route("resnet50")
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		if got.Spec.ID == "W1" {
			t.Fatal("unhealthy worker was selected")
		}
		for _, c := range d.Candidates {
			if c.WorkerID == "W1" && c.Scored {
				t.Fatal("unhealthy worker was scored")
			}
		}
		if len(d.HealthyWorkers) != 2 {
			t.Fatalf("healthy workers = %v", d.HealthyWorkers)
		}
	}
}

// §13: W1=0.2, W2=0.6, W3=0.4 -> W1 wins.
func TestLowestScoreWins(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	// Latency is the only differentiator: latency_score = latency / max_latency,
	// so W1=100/300, W3=200/300, W2=300/300 and the lowest (W1) must win.
	r = NewRouter(r.Pool(), Weights{Latency: 1})
	setMetrics(worker(t, r.Pool(), "W1"), 100*time.Millisecond, 0, 0, 0)
	setMetrics(worker(t, r.Pool(), "W2"), 300*time.Millisecond, 0, 0, 0)
	setMetrics(worker(t, r.Pool(), "W3"), 200*time.Millisecond, 0, 0, 0)

	got, d, err := r.Route("resnet50")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if got.Spec.ID != "W1" {
		t.Fatalf("selected %s, want W1", got.Spec.ID)
	}
	want := map[string]float64{"W1": 100.0 / 300, "W2": 1, "W3": 200.0 / 300}
	for _, c := range d.Candidates {
		if !near(c.FinalScore, want[c.WorkerID]) {
			t.Fatalf("%s score = %v, want %v", c.WorkerID, c.FinalScore, want[c.WorkerID])
		}
	}
	if d.SelectedWorker != "W1" || !near(d.FinalScore, want["W1"]) {
		t.Fatalf("decision = %+v", d)
	}
}

func TestNormalizationGuardsZeroDenominators(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	// No latency samples, no in-flight, no telemetry at all.
	got, d, err := r.Route("resnet50")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if got == nil {
		t.Fatal("no worker selected")
	}
	for _, c := range d.Candidates {
		if c.FinalScore != 0 || c.LatencyScore != 0 || c.QueueScore != 0 {
			t.Fatalf("%s: expected zero scores, got %+v", c.WorkerID, c)
		}
	}
}

// A CPU-only worker reports no GPU data: gpu_score = 0, flagged n/a.
func TestCPUOnlyWorkerScoresGPUZero(t *testing.T) {
	specs := []WorkerSpec{
		{ID: "cpu1", Model: "resnet50", URL: "http://127.0.0.1:9001", Enabled: true, GPUEnabled: false},
	}
	p, err := NewPool(specs, DefaultStartupGrace, DefaultFailureThreshold)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	allHealthy(p)
	w := worker(t, p, "cpu1")
	w.setTelemetry(WorkerTelemetry{CPUPercent: 50, CPUAvailable: true, GPUSource: "n/a (cpu-only worker)"})
	r := NewRouter(p, DefaultWeights)
	_, d, err := r.Route("resnet50")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	c := d.Candidates[0]
	if c.GPUScore != 0 || c.GPUAvailable {
		t.Fatalf("gpu should be n/a and score 0: %+v", c)
	}
	if !near(c.CPUScore, 0.5) {
		t.Fatalf("cpu score = %v, want 0.5", c.CPUScore)
	}
	if !near(c.FinalScore, DefaultWeights.CPU*0.5) {
		t.Fatalf("final score = %v", c.FinalScore)
	}
	if !strings.Contains(d.Reason, "gpu=n/a") {
		t.Fatalf("reason should flag gpu n/a: %s", d.Reason)
	}
}

// §13: unknown model -> ErrModelNotFound (404 at the HTTP layer).
func TestRouteUnknownModel(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	_, d, err := r.Route("does-not-exist")
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("err = %v, want ErrModelNotFound", err)
	}
	if len(d.CandidateWorkers) != 0 {
		t.Fatalf("candidates = %v", d.CandidateWorkers)
	}
}

// §13: no healthy workers -> ErrNoHealthyWorker (503 at the HTTP layer).
func TestRouteNoHealthyWorker(t *testing.T) {
	r := testRouter(t)
	_, d, err := r.Route("resnet50")
	if !errors.Is(err, ErrNoHealthyWorker) {
		t.Fatalf("err = %v, want ErrNoHealthyWorker", err)
	}
	if len(d.CandidateWorkers) != 3 || len(d.HealthyWorkers) != 0 {
		t.Fatalf("decision = %+v", d)
	}
	if len(d.Candidates) != 3 {
		t.Fatalf("every candidate should be reported: %+v", d.Candidates)
	}
	for _, c := range d.Candidates {
		if c.Scored {
			t.Fatalf("%s should not be scored", c.WorkerID)
		}
	}
}

// High load is not a health signal: a saturated worker is still a candidate.
func TestHighLoadIsNotUnhealthy(t *testing.T) {
	r := testRouter(t)
	markHealthy(r.Pool(), "W1")
	w := worker(t, r.Pool(), "W1")
	setMetrics(w, 2*time.Second, 64, 100, 95)
	got, _, err := r.Route("resnet50")
	if err != nil || got.Spec.ID != "W1" {
		t.Fatalf("saturated worker must stay routable: %v %v", got, err)
	}
	if !w.IsHealthy() {
		t.Fatal("load changed health state")
	}
}

// Request failure rate is not a health signal either.
func TestRequestFailuresDoNotChangeHealth(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	w := worker(t, r.Pool(), "W1")
	for i := 0; i < 20; i++ {
		w.recordRequestFailure(10*time.Millisecond, "worker returned 500")
	}
	if !w.IsHealthy() {
		t.Fatal("4xx/5xx responses must not flip health state")
	}
	if w.Failures() != 20 || w.Served() != 20 {
		t.Fatalf("served=%d failures=%d", w.Served(), w.Failures())
	}
}

func TestSelectedWorkerIsFlaggedInDecision(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	setMetrics(worker(t, r.Pool(), "W1"), 10*time.Millisecond, 0, 0, 0)
	setMetrics(worker(t, r.Pool(), "W2"), 100*time.Millisecond, 0, 0, 0)
	setMetrics(worker(t, r.Pool(), "W3"), 100*time.Millisecond, 0, 0, 0)
	_, d, err := r.Route("resnet50")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	selected := 0
	for _, c := range d.Candidates {
		if c.Selected {
			selected++
			if c.WorkerID != d.SelectedWorker {
				t.Fatalf("selected flag on %s but decision says %s", c.WorkerID, d.SelectedWorker)
			}
		}
	}
	if selected != 1 {
		t.Fatalf("%d candidates flagged selected", selected)
	}
}

// No round robin: the best worker keeps winning while it stays best.
func TestRoutingIsNotRoundRobin(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	setMetrics(worker(t, r.Pool(), "W1"), 10*time.Millisecond, 0, 0, 0)
	setMetrics(worker(t, r.Pool(), "W2"), 200*time.Millisecond, 0, 0, 0)
	setMetrics(worker(t, r.Pool(), "W3"), 300*time.Millisecond, 0, 0, 0)
	for i := 0; i < 10; i++ {
		got, _, err := r.Route("resnet50")
		if err != nil {
			t.Fatalf("Route: %v", err)
		}
		if got.Spec.ID != "W1" {
			t.Fatalf("pick %d went to %s, scoring should keep choosing W1", i, got.Spec.ID)
		}
	}
}

// Rising in-flight load shifts traffic away from a worker (the §15 demo).
func TestLoadShiftsTraffic(t *testing.T) {
	r := testRouter(t)
	allHealthy(r.Pool())
	for _, id := range []string{"W1", "W2", "W3"} {
		setMetrics(worker(t, r.Pool(), id), 50*time.Millisecond, 0, 10, 10)
	}
	if got, _, _ := r.Route("resnet50"); got.Spec.ID != "W1" {
		t.Fatalf("tie should break to W1, got %s", got.Spec.ID)
	}
	// Load W1: higher latency and a deeper queue.
	setMetrics(worker(t, r.Pool(), "W1"), 400*time.Millisecond, 8, 10, 10)
	got, d, err := r.Route("resnet50")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if got.Spec.ID == "W1" {
		t.Fatalf("traffic did not shift away from the loaded worker: %+v", d.Candidates)
	}
}
