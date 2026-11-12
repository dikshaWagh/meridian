package ml

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSDServerBackendProxiesRequest(t *testing.T) {
	pngBytes := makePNG(t, 24, 16)
	var got sdTxt2ImgRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdapi/v1/txt2img" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(sdTxt2ImgResponse{
			Images: []string{base64.StdEncoding.EncodeToString(pngBytes)},
		})
	}))
	defer srv.Close()

	backend, err := newImageGenerationBackend(WorkerConfig{
		Kind: KindImageGeneration, ModelID: "sd", Engine: EngineStableDiffusionServer,
		ServerURL: srv.URL, Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := backend.Generate(context.Background(), ImageGenerationRequest{
		Prompt: "a lake", NegativePrompt: "blurry", Width: 24, Height: 16, Steps: 5, Seed: 99,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompt != "a lake" || got.NegativePrompt != "blurry" || got.Width != 24 || got.Height != 16 || got.Steps != 5 || got.Seed != 99 {
		t.Fatalf("unexpected proxied request: %+v", got)
	}
	if out.Width != 24 || out.Height != 16 || out.Seed != 99 {
		t.Fatalf("unexpected result: %+v", out)
	}
	if _, err := png.Decode(bytes.NewReader(out.PNG)); err != nil {
		t.Fatalf("result is not a PNG: %v", err)
	}
}

func TestSDServerBackendConvertsJPEGToPNG(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(sdTxt2ImgResponse{
			Images: []string{"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpegBuf.Bytes())},
		})
	}))
	defer srv.Close()

	backend, err := newImageGenerationBackend(WorkerConfig{Engine: EngineStableDiffusionServer, ServerURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	out, err := backend.Generate(context.Background(), ImageGenerationRequest{Prompt: "x", Width: 8, Height: 8, Steps: 1, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(out.PNG)); err != nil {
		t.Fatalf("result was not re-encoded as PNG: %v", err)
	}
}

func TestSDServerBackendErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not loaded", http.StatusInternalServerError)
	}))
	defer srv.Close()

	backend, err := newImageGenerationBackend(WorkerConfig{Engine: EngineStableDiffusionServer, ServerURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = backend.Generate(context.Background(), ImageGenerationRequest{Prompt: "x", Width: 8, Height: 8, Steps: 1})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected upstream status error, got %v", err)
	}

	if _, err := newImageGenerationBackend(WorkerConfig{Engine: EngineStableDiffusionServer}); err == nil {
		t.Fatal("expected server_url requirement")
	}
}
