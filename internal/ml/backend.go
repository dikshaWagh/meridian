package ml

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"strings"

	// Register PNG and JPEG decoders for validation and fake inference.
	_ "image/jpeg"
	_ "image/png"
)

// OCRBackend recognises text in a PNG or JPEG image.
type OCRBackend interface {
	Recognize(ctx context.Context, imageBytes []byte) (*OCRResult, error)
	Close() error
}

// newOCRBackend selects the configured OCR engine. Returning a clear error lets
// the supervisor mark that worker unhealthy without affecting the LLM pool.
func newOCRBackend(cfg WorkerConfig) (OCRBackend, error) {
	switch cfg.Engine {
	case EngineFake, "":
		return &fakeOCR{}, nil
	case EngineTesseract:
		if _, err := exec.LookPath("tesseract"); err != nil {
			return nil, fmt.Errorf("tesseract engine requires the tesseract binary on PATH: %w", err)
		}
		return &tesseractOCR{language: cfg.Language}, nil
	default:
		return nil, fmt.Errorf("unknown OCR engine %q", cfg.Engine)
	}
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
