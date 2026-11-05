package ml

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func singleWorkerPool(t *testing.T, spec WorkerSpec, grace time.Duration, threshold int) (*Pool, *Worker) {
	t.Helper()
	p, err := NewPool([]WorkerSpec{spec}, grace, threshold)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	return p, p.Workers()[0]
}

func TestHealthStateMachine(t *testing.T) {
	_, w := singleWorkerPool(t, WorkerSpec{ID: "w", Model: "m", URL: "http://127.0.0.1:1", Enabled: true}, time.Hour, DefaultFailureThreshold)
	now := time.Now()
	if w.State() != StateStarting {
		t.Fatalf("initial state = %s", w.State())
	}
	w.applyProbe(probeSuccess, "", now)
	if !w.IsHealthy() {
		t.Fatal("successful probe did not make the worker healthy")
	}
	w.applyProbe(probeFailure, "boom", now)
	w.applyProbe(probeFailure, "boom", now)
	if !w.IsHealthy() {
		t.Fatalf("worker left HEALTHY after %d failures", 2)
	}
	w.applyProbe(probeFailure, "boom", now)
	if w.State() != StateUnhealthy {
		t.Fatalf("state after %d failures = %s", DefaultFailureThreshold, w.State())
	}
	w.applyProbe(probeSuccess, "", now)
	if !w.IsHealthy() {
		t.Fatal("worker did not rejoin the candidate set after recovery")
	}
	if w.consecutiveFailures.Load() != 0 {
		t.Fatal("failure streak not reset on recovery")
	}
}

func TestStartingGracePeriod(t *testing.T) {
	_, w := singleWorkerPool(t, WorkerSpec{ID: "w", Model: "m", URL: "http://127.0.0.1:1", Enabled: true}, time.Second, DefaultFailureThreshold)
	start := w.startedAt
	w.applyProbe(probeNotReady, "loading", start.Add(500*time.Millisecond))
	if w.State() != StateStarting {
		t.Fatalf("worker left STARTING during grace: %s", w.State())
	}
	w.applyProbe(probeNotReady, "loading", start.Add(2*time.Second))
	if w.State() != StateUnhealthy {
		t.Fatalf("worker stayed STARTING past grace: %s", w.State())
	}
}

func TestStoppedWorkerIsNeverRoutable(t *testing.T) {
	p, w := singleWorkerPool(t, WorkerSpec{ID: "w", Model: "m", URL: "http://127.0.0.1:1", Enabled: true}, time.Hour, DefaultFailureThreshold)
	w.applyProbe(probeSuccess, "", time.Now())
	w.Stop("shutting down")
	if w.State() != StateStopped {
		t.Fatalf("state = %s", w.State())
	}
	w.applyProbe(probeSuccess, "", time.Now())
	if w.State() != StateStopped {
		t.Fatal("a STOPPED worker must not be revived by a probe")
	}
	if _, _, err := NewRouter(p, DefaultWeights).Route("m"); err != ErrNoHealthyWorker {
		t.Fatalf("err = %v", err)
	}
}

func TestDisabledWorkerStartsStopped(t *testing.T) {
	_, w := singleWorkerPool(t, WorkerSpec{ID: "w", Model: "m", URL: "http://127.0.0.1:1", Enabled: false}, time.Hour, DefaultFailureThreshold)
	if w.State() != StateStopped {
		t.Fatalf("state = %s", w.State())
	}
}

func TestProbeHealthyWorker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok","model":"resnet50","model_loaded":true}`))
		case "/ready":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, w := singleWorkerPool(t, WorkerSpec{ID: "w", Model: "resnet50", URL: srv.URL, HealthPath: "/health", ReadyPath: "/ready", Enabled: true}, time.Hour, DefaultFailureThreshold)
	hc := NewHealthChecker(p, time.Second, quietLogger())
	hc.CheckAll(context.Background())
	if !w.IsHealthy() {
		t.Fatalf("worker not healthy: %s", w.Status().LastError)
	}
}

func TestProbeNotReadyAndModelMismatch(t *testing.T) {
	ready := false
	model := "resnet50"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"loading","model_loaded":false}`))
			return
		}
		w.Write([]byte(`{"status":"ok","model":"` + model + `","model_loaded":true}`))
	}))
	defer srv.Close()

	p, w := singleWorkerPool(t, WorkerSpec{ID: "w", Model: "resnet50", URL: srv.URL, HealthPath: "/health", Enabled: true}, time.Hour, DefaultFailureThreshold)
	hc := NewHealthChecker(p, time.Second, quietLogger())

	hc.CheckAll(context.Background())
	if w.State() != StateStarting {
		t.Fatalf("a not-ready worker inside grace should stay STARTING, got %s", w.State())
	}

	ready = true
	hc.CheckAll(context.Background())
	if !w.IsHealthy() {
		t.Fatalf("worker should be healthy, last error: %s", w.Status().LastError)
	}

	// The worker now reports a different model: it cannot serve our requests.
	model = "yolov8"
	for i := 0; i < DefaultFailureThreshold; i++ {
		hc.CheckAll(context.Background())
	}
	if w.State() != StateUnhealthy {
		t.Fatalf("model mismatch should end in UNHEALTHY, got %s", w.State())
	}
}

func TestProbeUnreachableWorker(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	p, w := singleWorkerPool(t, WorkerSpec{ID: "w", Model: "m", URL: url, HealthPath: "/health", Enabled: true}, time.Millisecond, DefaultFailureThreshold)
	hc := NewHealthChecker(p, time.Second, quietLogger())
	hc.CheckAll(context.Background())
	if w.State() != StateUnhealthy {
		t.Fatalf("unreachable worker state = %s", w.State())
	}
	if w.Status().LastError == "" {
		t.Fatal("last error should be recorded")
	}
}
