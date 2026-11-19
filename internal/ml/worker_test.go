package ml

import (
	"bytes"
	"encoding/json"
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
		Kind: KindOCR, ModelID: "ocr-1", Engine: EngineFake, Concurrency: 1, MaxUploadMB: 1,
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

func TestWorkerRejectsNonOCRKind(t *testing.T) {
	if _, _, err := newWorkerHandler(WorkerConfig{Kind: Kind("unsupported"), ModelID: "x", Engine: EngineFake}); err == nil {
		t.Fatal("expected unsupported worker kind error")
	}
}
