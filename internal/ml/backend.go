package ml

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	// Register PNG and JPEG decoders for validation and fake inference.
	_ "image/jpeg"
	_ "image/png"
)

// OCRBackend recognises text in a PNG or JPEG image.
type OCRBackend interface {
	Recognize(ctx context.Context, imageBytes []byte) (*OCRResult, error)
	Close() error
}

// ImageGenerationBackend renders a text prompt into a PNG image.
type ImageGenerationBackend interface {
	Generate(ctx context.Context, req ImageGenerationRequest) (*GeneratedImage, error)
	Close() error
}

// newOCRBackend selects the configured OCR engine. Native engines are
// recognised but not compiled in; returning a clear error lets the supervisor
// mark that worker unhealthy without affecting other models or the LLM pool.
func newOCRBackend(cfg WorkerConfig) (OCRBackend, error) {
	switch cfg.Engine {
	case EngineFake, "":
		return &fakeOCR{}, nil
	case EngineTesseract:
		if _, err := exec.LookPath("tesseract"); err != nil {
			return nil, fmt.Errorf("tesseract engine requires the tesseract binary on PATH: %w", err)
		}
		return &tesseractOCR{language: cfg.Language}, nil
	case EngineONNXRuntime:
		return nil, fmt.Errorf("native OCR backend unavailable in this build: onnxruntime requires a validated Go binding and the ONNX Runtime shared library; use engine: fake or see README (native backend not compiled in)")
	default:
		return nil, fmt.Errorf("unknown OCR engine %q", cfg.Engine)
	}
}

func newImageGenerationBackend(cfg WorkerConfig) (ImageGenerationBackend, error) {
	switch cfg.Engine {
	case EngineFake, "":
		return &fakeImageGen{}, nil
	case EngineStableDiffusionServer:
		base := strings.TrimRight(strings.TrimSpace(cfg.ServerURL), "/")
		if base == "" {
			return nil, fmt.Errorf("stable-diffusion-server engine requires server_url")
		}
		backend := &sdServerImageGen{baseURL: base, client: &http.Client{}, cfgScale: cfg.CFGScale}
		if strings.TrimSpace(cfg.ServerCommand) != "" {
			cmd, err := startSDServer(base, cfg)
			if err != nil {
				return nil, err
			}
			backend.cmd = cmd
		}
		return backend, nil
	case EngineStableDiffusionCPP:
		return nil, fmt.Errorf("native image-generation backend unavailable in this build: stable-diffusion.cpp requires a validated Go binding and native shared library; use engine: fake or engine: stable-diffusion-server")
	default:
		return nil, fmt.Errorf("unknown image-generation engine %q", cfg.Engine)
	}
}

// backendUnavailableReason reports native engines that this build recognises but
// cannot execute, so the parent can block the worker with a precise error
// instead of spawning a child that is guaranteed to fail.
func backendUnavailableReason(cfg WorkerConfig) string {
	switch cfg.Kind {
	case KindOCR:
		if cfg.Engine == EngineONNXRuntime {
			return "worker_start_failed: native OCR backend unavailable in this build: onnxruntime requires a validated Go binding and the ONNX Runtime shared library"
		}
	case KindImageGeneration:
		if cfg.Engine == EngineStableDiffusionCPP {
			return "worker_start_failed: native image-generation backend unavailable in this build: stable-diffusion.cpp requires a validated Go binding and native shared library"
		}
	}
	return ""
}

// fakeOCR decodes the image (validating it) and returns deterministic text.
// It reports no confidence or bounding boxes because it does not compute them.
type fakeOCR struct{}

func (f *fakeOCR) Recognize(ctx context.Context, imageBytes []byte) (*OCRResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	img, format, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	b := img.Bounds()
	return &OCRResult{
		Text: fmt.Sprintf("Meridian fake OCR\nimage %dx%d %s", b.Dx(), b.Dy(), format),
		Lines: []OCRLine{
			{Text: "Meridian fake OCR"},
			{Text: fmt.Sprintf("image %dx%d %s", b.Dx(), b.Dy(), format)},
		},
	}, nil
}

func (f *fakeOCR) Close() error { return nil }

// fakeImageGen renders a deterministic colourful gradient derived from the
// request. Same request -> same image; different seed/prompt -> different image.
type fakeImageGen struct{}

func (f *fakeImageGen) Generate(ctx context.Context, req ImageGenerationRequest) (*GeneratedImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Width <= 0 || req.Height <= 0 {
		return nil, fmt.Errorf("width and height must be positive")
	}
	seed := req.Seed
	if seed == 0 {
		seed = int64(len(req.Prompt) + req.Width*31 + req.Height*17 + req.Steps*7)
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d|%d|%d", req.Prompt, req.NegativePrompt, req.Width, req.Height, req.Steps, seed)))

	img := image.NewRGBA(image.Rect(0, 0, req.Width, req.Height))
	for y := 0; y < req.Height; y++ {
		for x := 0; x < req.Width; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(int(key[0]) + x*255/req.Width),
				G: uint8(int(key[1]) + y*255/req.Height),
				B: uint8(int(key[2]) + (x+y)*127/(req.Width+req.Height)),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return &GeneratedImage{PNG: buf.Bytes(), Width: req.Width, Height: req.Height, Steps: req.Steps, Seed: seed}, nil
}

func (f *fakeImageGen) Close() error { return nil }

// sdServerImageGen proxies image generation to a stable-diffusion.cpp
// sd-server instance using its Stable Diffusion WebUI compatible endpoint.
// This keeps Meridian a pure Go process: sd-server owns model loading and the
// native inference runtime.
type sdServerImageGen struct {
	baseURL  string
	client   *http.Client
	cfgScale float64
	cmd      *exec.Cmd
}

// startSDServer launches sd-server as a child of this worker, so it is stopped
// when the ML lease ends and its memory is released before the LLM starts.
func startSDServer(baseURL string, cfg WorkerConfig) (*exec.Cmd, error) {
	cmd := exec.Command(cfg.ServerCommand, cfg.ServerArgs...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start sd-server %q: %w", cfg.ServerCommand, err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			_ = cmd.Wait()
			return nil, fmt.Errorf("sd-server exited before becoming ready")
		}
		resp, err := client.Get(baseURL + "/sdapi/v1/options")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return cmd, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return nil, fmt.Errorf("sd-server at %s did not become ready within 3 minutes", baseURL)
}

type sdTxt2ImgRequest struct {
	Prompt         string  `json:"prompt"`
	NegativePrompt string  `json:"negative_prompt,omitempty"`
	Width          int     `json:"width"`
	Height         int     `json:"height"`
	Steps          int     `json:"steps"`
	Seed           int64   `json:"seed"`
	CFGScale       float64 `json:"cfg_scale,omitempty"`
}

type sdTxt2ImgResponse struct {
	Images []string `json:"images"`
	Info   string   `json:"info,omitempty"`
}

func (s *sdServerImageGen) Generate(ctx context.Context, req ImageGenerationRequest) (*GeneratedImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Width <= 0 || req.Height <= 0 {
		return nil, fmt.Errorf("width and height must be positive")
	}
	seed := req.Seed
	if seed == 0 {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("generate seed: %w", err)
		}
		seed = int64(binary.LittleEndian.Uint64(b[:]) & 0x7fffffffffffffff)
	}

	body, err := json.Marshal(sdTxt2ImgRequest{
		Prompt:         req.Prompt,
		NegativePrompt: req.NegativePrompt,
		Width:          req.Width,
		Height:         req.Height,
		Steps:          req.Steps,
		Seed:           seed,
		CFGScale:       s.cfgScale,
	})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/sdapi/v1/txt2img", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("stable-diffusion server %s: %w", s.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("stable-diffusion server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out sdTxt2ImgResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode stable-diffusion response: %w", err)
	}
	if len(out.Images) == 0 || out.Images[0] == "" {
		return nil, fmt.Errorf("stable-diffusion server returned no images")
	}

	encoded := out.Images[0]
	if i := strings.Index(encoded, ","); strings.HasPrefix(encoded, "data:") && i > 0 {
		encoded = encoded[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode generated image: %w", err)
	}

	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("generated image is not a valid image: %w", err)
	}
	if format != "png" {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode generated image as PNG: %w", err)
		}
		raw = buf.Bytes()
	}
	b := img.Bounds()
	return &GeneratedImage{
		PNG:    raw,
		Width:  b.Dx(),
		Height: b.Dy(),
		Steps:  req.Steps,
		Seed:   seed,
	}, nil
}

func (s *sdServerImageGen) Close() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = s.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		_ = s.cmd.Process.Kill()
		<-done
	}
	return nil
}

// tesseractOCR runs the tesseract CLI on each image. Tesseract is a real OCR
// engine; it is not the PaddleOCR model named in the config.
type tesseractOCR struct {
	language string
}

func (t *tesseractOCR) Recognize(ctx context.Context, imageBytes []byte) (*OCRResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", "meridian-ocr-*")
	if err != nil {
		return nil, fmt.Errorf("create OCR temp file: %w", err)
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(imageBytes); err != nil {
		f.Close()
		return nil, fmt.Errorf("write OCR temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close OCR temp file: %w", err)
	}

	lang := strings.TrimSpace(t.language)
	if lang == "" {
		lang = "eng"
	}
	cmd := exec.CommandContext(ctx, "tesseract", path, "stdout", "-l", lang, "--psm", "6")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("tesseract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	text := strings.TrimSpace(string(out))
	lines := make([]OCRLine, 0, 8)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, OCRLine{Text: line})
		}
	}
	return &OCRResult{Text: text, Lines: lines}, nil
}

func (t *tesseractOCR) Close() error { return nil }
