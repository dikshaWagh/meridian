package ml

import (
	"io"
	"log"
	"reflect"
	"testing"
	"time"
)

func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func TestPoolModelMap(t *testing.T) {
	p, err := NewPool(testSpecs(), DefaultStartupGrace, DefaultFailureThreshold)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if got, want := p.Models(), []string{"resnet50", "yolov8"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Models() = %v, want %v", got, want)
	}
	if got := len(p.ForModel("resnet50")); got != 3 {
		t.Fatalf("resnet50 workers = %d, want 3", got)
	}
	if got := len(p.ForModel("yolov8")); got != 1 {
		t.Fatalf("yolov8 workers = %d, want 1", got)
	}
	if len(p.ForModel("nope")) != 0 || p.HasModel("nope") {
		t.Fatal("unknown model should have no workers")
	}
	for _, w := range p.ForModel("resnet50") {
		if w.Spec.Model != "resnet50" {
			t.Fatalf("model map leaked %s into resnet50", w.Spec.Model)
		}
	}
}

func TestPoolRejectsBadSpecs(t *testing.T) {
	cases := map[string][]WorkerSpec{
		"missing model": {{ID: "a", URL: "http://127.0.0.1:1"}},
		"missing id":    {{Model: "m", URL: "http://127.0.0.1:1"}},
		"bad url":       {{ID: "a", Model: "m", URL: "not-a-url"}},
		"duplicate id": {
			{ID: "a", Model: "m", URL: "http://127.0.0.1:1"},
			{ID: "a", Model: "m", URL: "http://127.0.0.1:2"},
		},
	}
	for name, specs := range cases {
		if _, err := NewPool(specs, DefaultStartupGrace, DefaultFailureThreshold); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestPoolDefaultsPaths(t *testing.T) {
	p, err := NewPool([]WorkerSpec{{ID: "a", Model: "m", URL: "http://127.0.0.1:1", Enabled: true}}, 0, 0)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	w := p.Workers()[0]
	if w.Spec.HealthPath != DefaultHealthPath || w.Spec.PredictPath != DefaultPredictPath {
		t.Fatalf("paths = %q %q", w.Spec.HealthPath, w.Spec.PredictPath)
	}
}

func TestPoolTotalsAndStatus(t *testing.T) {
	p, err := NewPool(testSpecs(), DefaultStartupGrace, DefaultFailureThreshold)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	allHealthy(p)
	w1 := worker(t, p, "W1")
	w1.recordSuccess(10 * time.Millisecond)
	w1.recordSuccess(30 * time.Millisecond)
	w1.recordRequestFailure(20*time.Millisecond, "worker returned 500")

	st := w1.Status()
	if st.Served != 3 || st.Success != 2 || st.Failures != 1 {
		t.Fatalf("worker status = %+v", st)
	}
	if st.Latency.Samples != 3 || st.Latency.AvgMS != 20 {
		t.Fatalf("latency = %+v", st.Latency)
	}
	if st.SuccessRate < 0.66 || st.SuccessRate > 0.67 {
		t.Fatalf("success rate = %v", st.SuccessRate)
	}

	tot := p.Totals()
	if tot.Served != 3 || tot.Success != 2 || tot.Failures != 1 {
		t.Fatalf("totals = %+v", tot)
	}
	if tot.AvgLatencyMS != 20 {
		t.Fatalf("avg latency = %v", tot.AvgLatencyMS)
	}

	ms := p.ModelStatus()
	if len(ms) != 2 || ms[0].Model != "resnet50" || ms[0].Workers != 3 || ms[0].Healthy != 3 {
		t.Fatalf("model status = %+v", ms)
	}
}

func TestLatencyWindowPercentiles(t *testing.T) {
	l := newLatencyWindow(8)
	for i := 1; i <= 8; i++ {
		l.add(time.Duration(i) * time.Millisecond)
	}
	s := l.stats()
	if s.Samples != 8 {
		t.Fatalf("samples = %d", s.Samples)
	}
	if s.AvgMS != 4.5 {
		t.Fatalf("avg = %v, want 4.5", s.AvgMS)
	}
	if s.LastMS != 8 {
		t.Fatalf("last = %v", s.LastMS)
	}
	if s.P50MS != 4 || s.P95MS != 7 || s.P99MS != 7 {
		t.Fatalf("percentiles = %v/%v/%v", s.P50MS, s.P95MS, s.P99MS)
	}
	// The window is fixed size: older samples fall out.
	for i := 0; i < 8; i++ {
		l.add(100 * time.Millisecond)
	}
	if s := l.stats(); s.Samples != 8 || s.AvgMS != 100 {
		t.Fatalf("rolled window = %+v", s)
	}
}

func TestEmptyLatencyWindow(t *testing.T) {
	l := newLatencyWindow(4)
	if s := l.stats(); s != (LatencyStats{}) {
		t.Fatalf("stats = %+v, want zero", s)
	}
}
