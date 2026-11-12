package ml

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testConfig(t *testing.T) *Config {
	t.Helper()
	cfg := &Config{
		ListenHost:     "127.0.0.1",
		ResourcePolicy: "protect-llm",
		Resources:      ResourcesConfig{GPUProbe: "off"},
		OCR: &OCRConfig{
			Enabled:               true,
			ModelID:               "ocr-test",
			Engine:                EngineFake,
			Device:                "cpu",
			Concurrency:           2,
			Port:                  18091,
			MaxUploadMB:           10,
			StartupTimeoutSeconds: 10,
		},
		ImageGeneration: &ImageGenConfig{
			Enabled:               true,
			ModelID:               "img-test",
			Engine:                EngineFake,
			Device:                "cpu",
			Concurrency:           1,
			Port:                  18092,
			OutputDir:             t.TempDir(),
			MaxWidth:              768,
			MaxHeight:             768,
			MaxSteps:              40,
			DefaultSteps:          25,
			MaxPromptChars:        2000,
			TimeoutSeconds:        30,
			StartupTimeoutSeconds: 10,
		},
	}
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		t.Fatalf("test config invalid: %v", err)
	}
	return cfg
}

func testWorkerServer(t *testing.T, cfg WorkerConfig) *httptest.Server {
	t.Helper()
	handler, closeBackend, err := newWorkerHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(func() {
		srv.Close()
		_ = closeBackend()
	})
	return srv
}

// newTestService wires a Service to real fake-backend worker HTTP servers,
// bypassing child-process supervision for API tests.
func newTestService(t *testing.T) *Service {
	t.Helper()
	cfg := testConfig(t)
	ocrSrv := testWorkerServer(t, WorkerConfig{
		Kind: KindOCR, ModelID: cfg.OCR.ModelID, Engine: EngineFake, Concurrency: 2, MaxUploadMB: 10,
	})
	imgSrv := testWorkerServer(t, WorkerConfig{
		Kind: KindImageGeneration, ModelID: cfg.ImageGeneration.ModelID, Engine: EngineFake, Concurrency: 1,
		MaxWidth: 768, MaxHeight: 768, MaxSteps: 40, DefaultSteps: 25, MaxPromptChars: 2000,
	})
	s := &Service{cfg: cfg, artifacts: cfg.ImageGeneration.OutputDir}
	for _, h := range []*WorkerHandle{
		{ID: "ml-ocr-1", Kind: KindOCR, ModelID: cfg.OCR.ModelID, Concurrency: 2, baseURL: ocrSrv.URL, client: &http.Client{}},
		{ID: "ml-image-1", Kind: KindImageGeneration, ModelID: cfg.ImageGeneration.ModelID, Concurrency: 1, baseURL: imgSrv.URL, client: &http.Client{}},
	} {
		h.setState(StateReady)
		s.workers = append(s.workers, h)
	}
	return s
}

func doOCR(t *testing.T, baseURL string, filename string, data []byte, model string) (*http.Response, OCRResponse) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if model != "" {
		_ = mw.WriteField("model", model)
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/ocr", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	var out OCRResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return resp, out
}

func doImageGen(t *testing.T, baseURL, body string) (*http.Response, ImageGenerationResponse) {
	t.Helper()
	resp, err := http.Post(baseURL+"/v1/images/generations", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	var out ImageGenerationResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return resp, out
}

func errorType(t *testing.T, resp *http.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode error body %q: %v", raw, err)
	}
	return body.Error.Type
}

func TestOCRValidRequest(t *testing.T) {
	srv := httptest.NewServer(NewHandler(newTestService(t)))
	defer srv.Close()

	resp, out := doOCR(t, srv.URL, "test.png", makePNG(t, 12, 9), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, errorType(t, resp))
	}
	if out.Model != "ocr-test" {
		t.Fatalf("model = %q", out.Model)
	}
	if !strings.Contains(out.Text, "12x9") || len(out.Lines) != 2 {
		t.Fatalf("unexpected OCR result: %+v", out)
	}
	if out.RequestID == "" {
		t.Fatal("request_id is empty")
	}
}

func TestOCRRejectsBadInput(t *testing.T) {
	srv := httptest.NewServer(NewHandler(newTestService(t)))
	defer srv.Close()

	t.Run("non-image mime", func(t *testing.T) {
		resp, _ := doOCR(t, srv.URL, "a.txt", []byte("hello"), "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || errorType(t, resp) != "invalid_request" {
			t.Fatalf("status=%d type=%s", resp.StatusCode, errorType(t, resp))
		}
	})
	t.Run("malformed image", func(t *testing.T) {
		raw := append([]byte("\x89PNG\r\n\x1a\n"), []byte("not a real png")...)
		resp, _ := doOCR(t, srv.URL, "a.png", raw, "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})
	t.Run("unknown model", func(t *testing.T) {
		resp, _ := doOCR(t, srv.URL, "a.png", makePNG(t, 4, 4), "nope")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || errorType(t, resp) != "model_not_found" {
			t.Fatalf("status=%d type=%s", resp.StatusCode, errorType(t, resp))
		}
	})
}

func TestOCROversizedUpload(t *testing.T) {
	s := newTestService(t)
	s.cfg.OCR.MaxUploadMB = 1
	srv := httptest.NewServer(NewHandler(s))
	defer srv.Close()

	resp, _ := doOCR(t, srv.URL, "big.png", bytes.Repeat([]byte{0}, 2<<20), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestImageGenerationAndArtifact(t *testing.T) {
	s := newTestService(t)
	srv := httptest.NewServer(NewHandler(s))
	defer srv.Close()

	resp, out := doImageGen(t, srv.URL, `{"prompt":"a quiet mountain lake","width":64,"height":64,"steps":4,"seed":42}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s)", resp.StatusCode, errorType(t, resp))
	}
	if out.ID == "" || out.URL != "/v1/artifacts/"+out.ID {
		t.Fatalf("bad artifact response: %+v", out)
	}
	if out.Width != 64 || out.Height != 64 || out.Seed != 42 {
		t.Fatalf("unexpected image metadata: %+v", out)
	}

	art, err := http.Get(srv.URL + out.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer art.Body.Close()
	if art.StatusCode != http.StatusOK || art.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("artifact status=%d content-type=%q", art.StatusCode, art.Header.Get("Content-Type"))
	}
	cfg, err := png.DecodeConfig(art.Body)
	if err != nil || cfg.Width != 64 || cfg.Height != 64 {
		t.Fatalf("artifact not a 64x64 PNG: cfg=%+v err=%v", cfg, err)
	}

	t.Run("deterministic", func(t *testing.T) {
		_, again := doImageGen(t, srv.URL, `{"prompt":"a quiet mountain lake","width":64,"height":64,"steps":4,"seed":42}`)
		if again.ID == out.ID {
			t.Fatal("artifact IDs should be unique per request")
		}
	})

	t.Run("missing artifact", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/v1/artifacts/" + strings.Repeat("0", 32))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
	t.Run("path traversal", func(t *testing.T) {
		for _, id := range []string{"..%2f..%2fetc%2fpasswd", "../etc/passwd", "abc/../def"} {
			resp, err := http.Get(srv.URL + "/v1/artifacts/" + id)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Fatalf("path traversal %q was served", id)
			}
		}
	})
}

func TestImageGenerationRejectsInvalidInput(t *testing.T) {
	srv := httptest.NewServer(NewHandler(newTestService(t)))
	defer srv.Close()

	cases := map[string]string{
		"missing prompt":     `{"width":64,"height":64}`,
		"non-multiple dims":  `{"prompt":"x","width":65,"height":64}`,
		"too wide":           `{"prompt":"x","width":776,"height":64}`,
		"too many steps":     `{"prompt":"x","steps":41}`,
		"n greater than one": `{"prompt":"x","n":2}`,
		"unknown model":      `{"model":"other","prompt":"x"}`,
		"bad json":           `{`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp, _ := doImageGen(t, srv.URL, body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 400/404", resp.StatusCode)
			}
			if name == "unknown model" && errorType(t, resp) != "model_not_found" {
				t.Fatalf("expected model_not_found")
			}
		})
	}
}

func TestDisabledMLHandler(t *testing.T) {
	srv := httptest.NewServer(NewHandler(nil))
	defer srv.Close()

	resp, _ := doOCR(t, srv.URL, "a.png", makePNG(t, 4, 4), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || errorType(t, resp) != "service_disabled" {
		t.Fatalf("status=%d type=%s", resp.StatusCode, errorType(t, resp))
	}

	resp2, _ := doImageGen(t, srv.URL, `{"prompt":"x"}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("image status = %d", resp2.StatusCode)
	}

	mresp, err := http.Get(srv.URL + "/v1/ml/models")
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	var list ModelList
	if err := json.NewDecoder(mresp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) != 0 || list.Object != "list" {
		t.Fatalf("unexpected disabled model list: %+v", list)
	}

	sresp, err := http.Get(srv.URL + "/ml/status")
	if err != nil {
		t.Fatal(err)
	}
	defer sresp.Body.Close()
	var status StatusResponse
	if err := json.NewDecoder(sresp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Enabled {
		t.Fatal("disabled status reports enabled=true")
	}
}

func TestResourceBlockedError(t *testing.T) {
	s := newTestService(t)
	for _, h := range s.workers {
		if h.Kind == KindOCR {
			h.setState(StateResourceBlocked)
			h.setError("insufficient_resources: requested 4096 MB RAM, available 256 MB")
		}
	}
	srv := httptest.NewServer(NewHandler(s))
	defer srv.Close()

	resp, _ := doOCR(t, srv.URL, "a.png", makePNG(t, 4, 4), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || errorType(t, resp) != "insufficient_resources" {
		t.Fatalf("status=%d type=%s", resp.StatusCode, errorType(t, resp))
	}
}

func TestBusyWorkerRejectedPromptly(t *testing.T) {
	s := newTestService(t)
	var ocr *WorkerHandle
	for _, h := range s.workers {
		if h.Kind == KindOCR {
			ocr = h
		}
	}
	if !ocr.tryAcquire() || !ocr.tryAcquire() {
		t.Fatal("failed to fill worker slots")
	}
	srv := httptest.NewServer(NewHandler(s))
	defer srv.Close()

	resp, _ := doOCR(t, srv.URL, "a.png", makePNG(t, 4, 4), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || errorType(t, resp) != "no_healthy_worker" {
		t.Fatalf("status=%d type=%s", resp.StatusCode, errorType(t, resp))
	}
}

func TestArtifactPathStaysInsideDirectory(t *testing.T) {
	dir := t.TempDir()
	s := &Service{artifacts: dir}
	id := strings.Repeat("a", 32)
	if err := os.WriteFile(filepath.Join(dir, id+".png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ArtifactPath(id); err != nil {
		t.Fatalf("valid artifact rejected: %v", err)
	}
	for _, bad := range []string{"../x", "..", id + "/../x", "not-hex", strings.Repeat("z", 32)} {
		if _, err := s.ArtifactPath(bad); err == nil {
			t.Fatalf("invalid artifact id %q accepted", bad)
		}
	}
}
