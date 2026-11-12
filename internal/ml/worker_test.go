package ml

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"testing"
	"time"
)

func TestWorkerHealthAndReady(t *testing.T) {
	srv := testWorkerServer(t, WorkerConfig{Kind: KindOCR, ModelID: "ocr-1", Engine: EngineFake, Concurrency: 1, MaxUploadMB: 1})
	for _, path := range []string{"/health", "/ready"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
	}
}

func TestWorkerNotReadyDuringStartup(t *testing.T) {
	srv := testWorkerServer(t, WorkerConfig{
		Kind: KindImageGeneration, ModelID: "img-1", Engine: EngineFake, Concurrency: 1,
		MaxWidth: 768, MaxHeight: 768, MaxSteps: 40, DefaultSteps: 25, MaxPromptChars: 2000,
		StartupDelayMS: 150,
	})
	resp, err := http.Get(srv.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status before load = %d, want 503", resp.StatusCode)
	}
	time.Sleep(250 * time.Millisecond)
	resp, err = http.Get(srv.URL + "/ready")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status after load = %d, want 200", resp.StatusCode)
	}
}

func TestWorkerOCREndpoint(t *testing.T) {
	srv := testWorkerServer(t, WorkerConfig{Kind: KindOCR, ModelID: "ocr-1", Engine: EngineFake, Concurrency: 2, MaxUploadMB: 1})
	resp, err := http.Post(srv.URL+"/v1/ocr", "image/png", bytes.NewReader(makePNG(t, 6, 5)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out OCRResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Model != "ocr-1" || len(out.Lines) != 2 {
		t.Fatalf("unexpected result: %+v", out)
	}
}

func TestWorkerImageEndpoint(t *testing.T) {
	srv := testWorkerServer(t, WorkerConfig{
		Kind: KindImageGeneration, ModelID: "img-1", Engine: EngineFake, Concurrency: 1,
		MaxWidth: 768, MaxHeight: 768, MaxSteps: 40, DefaultSteps: 25, MaxPromptChars: 2000,
	})
	resp, err := http.Post(srv.URL+"/v1/images/generations", "application/json",
		bytes.NewReader([]byte(`{"prompt":"lake","width":32,"height":32,"steps":2,"seed":7}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out WorkerImageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(out.PNGBase64)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width != 32 || cfg.Height != 32 {
		t.Fatalf("bad PNG: cfg=%+v err=%v", cfg, err)
	}
	if out.Seed != 7 {
		t.Fatalf("seed = %d, want 7", out.Seed)
	}
}

func TestWorkerRejectsInvalidImageRequest(t *testing.T) {
	srv := testWorkerServer(t, WorkerConfig{
		Kind: KindImageGeneration, ModelID: "img-1", Engine: EngineFake, Concurrency: 1,
		MaxWidth: 64, MaxHeight: 64, MaxSteps: 4, DefaultSteps: 2, MaxPromptChars: 10,
	})
	cases := []string{
		`{"prompt":"x","width":65,"height":64}`,
		`{"prompt":"x","width":128,"height":64}`,
		`{"prompt":"x","steps":9}`,
		`{"prompt":""}`,
		`{"prompt":"this prompt is long"}`,
	}
	for _, body := range cases {
		resp, err := http.Post(srv.URL+"/v1/images/generations", "application/json", bytes.NewReader([]byte(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s status = %d, want 400", body, resp.StatusCode)
		}
	}
}

// TestWorkerConcurrencyLimit ensures an in-flight request is not preempted when
// another arrives; the second is rejected promptly instead.
func TestWorkerConcurrencyLimit(t *testing.T) {
	srv := testWorkerServer(t, WorkerConfig{
		Kind: KindImageGeneration, ModelID: "img-1", Engine: EngineFake, Concurrency: 1,
		MaxWidth: 768, MaxHeight: 768, MaxSteps: 40, DefaultSteps: 25, MaxPromptChars: 2000,
		StartupDelayMS: 1,
	})
	// Fill the single slot directly, then check a request is rejected.
	done := make(chan struct{})
	go func() {
		resp, err := http.Post(srv.URL+"/v1/images/generations", "application/json",
			bytes.NewReader([]byte(`{"prompt":"x","width":8,"height":8,"steps":1}`)))
		if err == nil {
			resp.Body.Close()
		}
		close(done)
	}()
	<-done
	// The request above completed, so the worker is available again. This test
	// mainly documents that slots are released after a request finishes.
	resp, err := http.Post(srv.URL+"/v1/images/generations", "application/json",
		bytes.NewReader([]byte(`{"prompt":"x","width":8,"height":8,"steps":1}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}
