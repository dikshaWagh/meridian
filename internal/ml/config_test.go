package ml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ml.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSampleConfig(t *testing.T) {
	cfg, err := LoadConfig("../../configs/ml.yaml")
	if err != nil {
		t.Fatalf("sample config must be valid: %v", err)
	}
	if cfg.OCR == nil || !cfg.OCR.Enabled || cfg.ImageGeneration == nil || !cfg.ImageGeneration.Enabled {
		t.Fatalf("sample config should enable both ML models")
	}
	if cfg.ResourcePolicy != "protect-llm" {
		t.Fatalf("sample config policy = %q", cfg.ResourcePolicy)
	}
}

func TestLoadSDServerSampleConfig(t *testing.T) {
	cfg, err := LoadConfig("../../configs/ml-sd-server.yaml")
	if err != nil {
		t.Fatalf("sd-server sample config must be valid: %v", err)
	}
	if cfg.ImageGeneration == nil || cfg.ImageGeneration.Engine != EngineStableDiffusionServer {
		t.Fatalf("unexpected image config: %+v", cfg.ImageGeneration)
	}
	if cfg.OCR != nil && cfg.OCR.Enabled {
		t.Fatal("sd-server sample should not enable the fake OCR worker")
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	path := writeTempConfig(t, `
listen_host: 127.0.0.1
resources:
  ram_headroom_mb: 128
ocr:
  enabled: true
  model_id: ocr-1
  engine: fake
image_generation:
  enabled: true
  model_id: img-1
  engine: fake
  output_dir: ./testdata/out
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.OCR.Concurrency != 2 || cfg.OCR.Port != 8091 || cfg.OCR.MaxUploadMB != 10 {
		t.Fatalf("OCR defaults not applied: %+v", cfg.OCR)
	}
	if cfg.ImageGeneration.Concurrency != 1 || cfg.ImageGeneration.MaxWidth != 768 || cfg.ImageGeneration.MaxSteps != 40 {
		t.Fatalf("image defaults not applied: %+v", cfg.ImageGeneration)
	}
}

func TestConfigRejectsBadValues(t *testing.T) {
	cases := map[string]string{
		"unknown engine": `
ocr:
  enabled: true
  model_id: ocr-1
  engine: python-paddle
image_generation:
  enabled: true
  model_id: img-1
  engine: fake
  output_dir: ./out
`,
		"no models": `
ocr:
  enabled: false
image_generation:
  enabled: false
`,
		"non-loopback listen": `
listen_host: 0.0.0.0
ocr:
  enabled: true
  model_id: ocr-1
  engine: fake
image_generation:
  enabled: false
`,
		"negative budget": `
resources:
  ram_headroom_mb: -1
ocr:
  enabled: true
  model_id: ocr-1
  engine: fake
image_generation:
  enabled: false
`,
		"duplicate port": `
ocr:
  enabled: true
  model_id: ocr-1
  engine: fake
  port: 8091
image_generation:
  enabled: true
  model_id: img-1
  engine: fake
  port: 8091
  output_dir: ./out
`,
		"bad image limits": `
ocr:
  enabled: false
image_generation:
  enabled: true
  model_id: img-1
  engine: fake
  output_dir: ./out
  max_width: 100
`,
		"server engine without url": `
ocr:
  enabled: false
image_generation:
  enabled: true
  model_id: img-1
  engine: stable-diffusion-server
  output_dir: ./out
`,
		"server engine bad url": `
ocr:
  enabled: false
image_generation:
  enabled: true
  model_id: img-1
  engine: stable-diffusion-server
  server_url: 127.0.0.1:1234
  output_dir: ./out
`,
		"unknown resource policy": `
resource_policy: kill-llm
ocr:
  enabled: true
  model_id: ocr-1
  engine: fake
image_generation:
  enabled: false
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig(writeTempConfig(t, body)); err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
		})
	}
}

func TestConfigKnownFieldsRejected(t *testing.T) {
	path := writeTempConfig(t, `
ocr:
  enabled: true
  model_id: ocr-1
  engine: fake
  typo_field: 1
image_generation:
  enabled: false
`)
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "typo_field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}
