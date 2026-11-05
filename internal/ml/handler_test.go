package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeWorker is an in-process ML worker implementing the worker contract.
type fakeWorker struct {
	srv      *httptest.Server
	delay    atomic.Int64 // milliseconds
	status   atomic.Int64
	requests atomic.Int64
	bodies   chan []byte
}

func newFakeWorker(t *testing.T, model string) *fakeWorker {
	t.Helper()
	f := &fakeWorker{bodies: make(chan []byte, 256)}
	f.status.Store(http.StatusOK)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		api := map[string]any{"status": "ok", "model": model, "model_loaded": true}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api)
	})
	mux.HandleFunc("POST /predict", func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		select {
		case f.bodies <- buf.Bytes():
		default:
		}
		if d := time.Duration(f.delay.Load()) * time.Millisecond; d > 0 {
			select {
			case <-time.After(d):
			case <-r.Context().Done():
				return
			}
		}
		code := int(f.status.Load())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		conf := 0.97
		_ = json.NewEncoder(w).Encode(PredictResponse{Prediction: "cat", Confidence: &conf, Model: model})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type harness struct {
	sub    *Subsystem
	server *httptest.Server
	pool   *Pool
}

func newHarness(t *testing.T, specs []WorkerSpec, opts ...func(*Options)) *harness {
	t.Helper()
	o := Options{Workers: specs, Weights: DefaultWeights, Logger: quietLogger(), RequestTimeout: 5 * time.Second}
	for _, fn := range opts {
		fn(&o)
	}
	sub, err := New(o)
	if err != nil {
		t.Fatalf("ml.New: %v", err)
	}
	srv := httptest.NewServer(sub.Handler)
	t.Cleanup(srv.Close)
	return &harness{sub: sub, server: srv, pool: sub.Pool}
}

func (h *harness) predict(t *testing.T, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(h.server.URL+"/v1/predict", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /v1/predict: %v", err)
	}
	return resp
}

func drain(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}

func TestPredictProxiesToWorker(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: f.srv.URL, Enabled: true, GPUEnabled: true}})
	h.sub.Health.CheckAll(context.Background())

	resp := h.predict(t, `{"model":"resnet50","input":{"image":"base64..."}}`)
	body := drain(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"prediction"`) || !strings.Contains(body, `"confidence"`) {
		t.Fatalf("worker body not passed through: %s", body)
	}
	if got := resp.Header.Get("X-Meridian-ML-Worker"); got != "W1" {
		t.Fatalf("worker header = %q", got)
	}
	if got := resp.Header.Get("X-Meridian-ML-Model"); got != "resnet50" {
		t.Fatalf("model header = %q", got)
	}

	// The request body reaches the worker verbatim, including the model name.
	select {
	case raw := <-f.bodies:
		var got PredictRequest
		if json.Unmarshal(raw, &got) != nil {
			t.Fatalf("worker received invalid JSON: %s", raw)
		}
		if got.Model != "resnet50" || !strings.Contains(string(got.Input), "base64...") {
			t.Fatalf("worker received %s", raw)
		}
	default:
		t.Fatal("worker never saw the request")
	}

	w := worker(t, h.pool, "W1")
	if w.Served() != 1 || w.Success() != 1 || w.Failures() != 0 {
		t.Fatalf("counters: served=%d success=%d failures=%d", w.Served(), w.Success(), w.Failures())
	}
	if w.Latency().Samples != 1 {
		t.Fatal("latency was not measured")
	}
	if w.InFlight() != 0 {
		t.Fatalf("in-flight = %d, want 0", w.InFlight())
	}
}

func TestPredictBadRequests(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: f.srv.URL, Enabled: true}})
	h.sub.Health.CheckAll(context.Background())

	cases := map[string]string{
		"not json":    `{nope`,
		"no model":    `{"input":1}`,
		"empty model": `{"model":"","input":1}`,
		"no input":    `{"model":"resnet50"}`,
	}
	for name, body := range cases {
		resp := h.predict(t, body)
		got := drain(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: status = %d body = %s", name, resp.StatusCode, got)
		}
	}
}

func TestPredictUnknownModelIs404(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: f.srv.URL, Enabled: true}})
	h.sub.Health.CheckAll(context.Background())

	resp := h.predict(t, `{"model":"mystery-net","input":1}`)
	body := drain(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "model_not_found") {
		t.Fatalf("body = %s", body)
	}
	if f.requests.Load() != 0 {
		t.Fatal("request was forwarded to a worker of another model")
	}
}

func TestPredictNoHealthyWorkerIs503(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: f.srv.URL, Enabled: true}})
	// Never health checked: the worker is still STARTING, so not a candidate.
	resp := h.predict(t, `{"model":"resnet50","input":1}`)
	body := drain(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "no_healthy_worker") {
		t.Fatalf("body = %s", body)
	}
	if f.requests.Load() != 0 {
		t.Fatal("request reached a worker that was not a candidate")
	}
}

func TestPredictWorkerErrorPassesThrough(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	f.status.Store(http.StatusInternalServerError)
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: f.srv.URL, Enabled: true}})
	h.sub.Health.CheckAll(context.Background())

	resp := h.predict(t, `{"model":"resnet50","input":1}`)
	drain(t, resp)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want the worker's 500 passed through", resp.StatusCode)
	}
	w := worker(t, h.pool, "W1")
	if w.Served() != 1 || w.Failures() != 1 || w.Success() != 0 {
		t.Fatalf("served=%d success=%d failures=%d", w.Served(), w.Success(), w.Failures())
	}
	if !w.IsHealthy() {
		t.Fatal("a 5xx response must not change the health state")
	}
}

func TestPredictConnectionFailureIs502(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	url := f.srv.URL
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: url, Enabled: true}})
	h.sub.Health.CheckAll(context.Background())
	f.srv.Close() // worker disappears after it was marked healthy

	resp := h.predict(t, `{"model":"resnet50","input":1}`)
	body := drain(t, resp)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	w := worker(t, h.pool, "W1")
	if w.Failures() != 1 {
		t.Fatalf("failures = %d", w.Failures())
	}
	if w.consecutiveFailures.Load() != 1 {
		t.Fatalf("transport failures should feed the health streak, got %d", w.consecutiveFailures.Load())
	}
}

func TestPredictTimeoutIs504(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	f.delay.Store(2000)
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: f.srv.URL, Enabled: true}},
		func(o *Options) { o.RequestTimeout = 100 * time.Millisecond })
	h.sub.Health.CheckAll(context.Background())

	resp := h.predict(t, `{"model":"resnet50","input":1}`)
	body := drain(t, resp)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "worker_timeout") {
		t.Fatalf("body = %s", body)
	}
	if w := worker(t, h.pool, "W1"); w.InFlight() != 0 {
		t.Fatalf("in-flight = %d after timeout", w.InFlight())
	}
}

func TestStatusEndpointExposesDecisions(t *testing.T) {
	f1 := newFakeWorker(t, "resnet50")
	f2 := newFakeWorker(t, "resnet50")
	h := newHarness(t, []WorkerSpec{
		{ID: "W1", Model: "resnet50", URL: f1.srv.URL, Enabled: true, GPUEnabled: true},
		{ID: "W2", Model: "resnet50", URL: f2.srv.URL, Enabled: true, GPUEnabled: true},
	})
	h.sub.Health.CheckAll(context.Background())
	drain(t, h.predict(t, `{"model":"resnet50","input":1}`))

	resp, err := http.Get(h.server.URL + "/ml/status")
	if err != nil {
		t.Fatalf("GET /ml/status: %v", err)
	}
	defer resp.Body.Close()
	var st StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Weights != DefaultWeights {
		t.Fatalf("weights = %+v", st.Weights)
	}
	if len(st.Workers) != 2 || len(st.Models) != 1 {
		t.Fatalf("workers = %d models = %d", len(st.Workers), len(st.Models))
	}
	if st.Totals.Served != 1 || st.Totals.Success != 1 {
		t.Fatalf("totals = %+v", st.Totals)
	}
	if len(st.Recent) != 1 {
		t.Fatalf("recent events = %d", len(st.Recent))
	}
	ev := st.Recent[0]
	if ev.Model != "resnet50" || ev.Status != 200 || ev.Selected == "" {
		t.Fatalf("event = %+v", ev)
	}
	if len(ev.Candidates) != 2 {
		t.Fatalf("candidates = %+v", ev.Candidates)
	}
	if ev.LatencyMS <= 0 {
		t.Fatalf("latency was not measured: %v", ev.LatencyMS)
	}
	if ev.Reason == "" {
		t.Fatal("decision reason is empty")
	}
}

func TestRejectedRequestsAreLogged(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: f.srv.URL, Enabled: true}})
	h.sub.Health.CheckAll(context.Background())
	drain(t, h.predict(t, `{"model":"nope","input":1}`))

	events := h.sub.Telemetry.Events()
	if len(events) != 1 || events[0].Status != http.StatusNotFound {
		t.Fatalf("events = %+v", events)
	}
}

func TestMLHealthzAndModels(t *testing.T) {
	f := newFakeWorker(t, "resnet50")
	h := newHarness(t, []WorkerSpec{{ID: "W1", Model: "resnet50", URL: f.srv.URL, Enabled: true}})

	resp, err := http.Get(h.server.URL + "/ml/healthz")
	if err != nil {
		t.Fatalf("GET /ml/healthz: %v", err)
	}
	if body := drain(t, resp); resp.StatusCode != 200 || !strings.Contains(body, `"ml"`) {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	resp, err = http.Get(h.server.URL + "/ml/models")
	if err != nil {
		t.Fatalf("GET /ml/models: %v", err)
	}
	if body := drain(t, resp); resp.StatusCode != 200 || !strings.Contains(body, "resnet50") {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
}

// §13 concurrency: 10 / 50 / 100 parallel requests. InFlight returns to 0 and
// Success + Failures == Served.
func TestConcurrentPredicts(t *testing.T) {
	for _, n := range []int{10, 50, 100} {
		t.Run(fmt.Sprintf("%d", n), func(t *testing.T) {
			f1 := newFakeWorker(t, "resnet50")
			f2 := newFakeWorker(t, "resnet50")
			f3 := newFakeWorker(t, "resnet50")
			f1.delay.Store(2)
			f2.delay.Store(4)
			f3.delay.Store(6)
			h := newHarness(t, []WorkerSpec{
				{ID: "W1", Model: "resnet50", URL: f1.srv.URL, Enabled: true, GPUEnabled: true},
				{ID: "W2", Model: "resnet50", URL: f2.srv.URL, Enabled: true, GPUEnabled: true},
				{ID: "W3", Model: "resnet50", URL: f3.srv.URL, Enabled: true, GPUEnabled: true},
			})
			h.sub.Health.CheckAll(context.Background())
			go h.sub.Telemetry.Sample(context.Background())

			var wg sync.WaitGroup
			var ok, bad atomic.Int64
			client := &http.Client{Timeout: 20 * time.Second}
			for i := 0; i < n; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					body := fmt.Sprintf(`{"model":"resnet50","input":{"n":%d}}`, i)
					resp, err := client.Post(h.server.URL+"/v1/predict", "application/json", strings.NewReader(body))
					if err != nil {
						bad.Add(1)
						return
					}
					defer resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						ok.Add(1)
					} else {
						bad.Add(1)
					}
				}(i)
			}
			wg.Wait()

			if ok.Load() != int64(n) {
				t.Fatalf("%d/%d requests succeeded (%d failed)", ok.Load(), n, bad.Load())
			}
			var served, success, failures, inFlight int64
			for _, w := range h.pool.Workers() {
				served += w.Served()
				success += w.Success()
				failures += w.Failures()
				inFlight += w.InFlight()
			}
			if inFlight != 0 {
				t.Fatalf("in-flight = %d, want 0", inFlight)
			}
			if served != int64(n) {
				t.Fatalf("served = %d, want %d", served, n)
			}
			if success+failures != served {
				t.Fatalf("success(%d) + failures(%d) != served(%d)", success, failures, served)
			}
			if total := h.pool.Totals(); total.Served != int64(n) || total.InFlight != 0 {
				t.Fatalf("totals = %+v", total)
			}
			// Work must have been spread across instances by scoring.
			used := 0
			for _, f := range []*fakeWorker{f1, f2, f3} {
				if f.requests.Load() > 0 {
					used++
				}
			}
			if n >= 50 && used < 2 {
				t.Fatalf("only %d of 3 workers received traffic", used)
			}
		})
	}
}

// Routing state and telemetry must stay consistent under concurrent reads.
func TestConcurrentRoutingAndStatus(t *testing.T) {
	f1 := newFakeWorker(t, "resnet50")
	f2 := newFakeWorker(t, "yolov8")
	h := newHarness(t, []WorkerSpec{
		{ID: "W1", Model: "resnet50", URL: f1.srv.URL, Enabled: true, GPUEnabled: true},
		{ID: "Y1", Model: "yolov8", URL: f2.srv.URL, Enabled: true, GPUEnabled: true},
	})
	h.sub.Health.CheckAll(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			model := "resnet50"
			if i%2 == 1 {
				model = "yolov8"
			}
			for j := 0; j < 20; j++ {
				resp, err := http.Post(h.server.URL+"/v1/predict", "application/json",
					strings.NewReader(`{"model":"`+model+`","input":1}`))
				if err != nil {
					t.Error(err)
					return
				}
				resp.Body.Close()
			}
		}(i)
	}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 40; j++ {
				resp, err := http.Get(h.server.URL + "/ml/status")
				if err != nil {
					t.Error(err)
					return
				}
				resp.Body.Close()
				h.sub.Telemetry.Sample(ctx)
				h.sub.Health.CheckAll(ctx)
			}
		}()
	}
	wg.Wait()

	// Each model's workers only ever saw their own model's traffic.
	if f1.requests.Load() != 80 || f2.requests.Load() != 80 {
		t.Fatalf("resnet50 worker got %d, yolov8 worker got %d (want 80 each)", f1.requests.Load(), f2.requests.Load())
	}
}
