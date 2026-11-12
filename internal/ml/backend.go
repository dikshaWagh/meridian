package ml

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"

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
	case EngineStableDiffusionCPP:
		return nil, fmt.Errorf("native image-generation backend unavailable in this build: stable-diffusion.cpp requires a validated Go binding and native shared library; use engine: fake or see README (native backend not compiled in)")
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
