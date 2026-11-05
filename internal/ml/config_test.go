package ml

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "meridian.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	path := writeConfig(t, `
ml:
  listen: "127.0.0.1:9999"
  health_interval: 3s
  failure_threshold: 5
  weights: {latency: 0.5, queue: 0.2, gpu: 0.2, cpu: 0.1}
  workers:
    - {id: resnet-1, model: resnet50, url: "http://127.0.0.1:9001"}
    - {id: resnet-2, model: resnet50, url: "http://127.0.0.1:9002", gpu: false}
    - {id: yolov8-1, model: yolov8,   url: "http://127.0.0.1:9003", ready_path: /ready}
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Listen != "127.0.0.1:9999" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	if time.Duration(cfg.HealthInterval) != 3*time.Second {
		t.Fatalf("health interval = %v", time.Duration(cfg.HealthInterval))
	}
	if cfg.FailureThreshold != 5 {
		t.Fatalf("failure threshold = %d", cfg.FailureThreshold)
	}
	if cfg.Weights == nil || cfg.Weights.Latency != 0.5 {
		t.Fatalf("weights = %+v", cfg.Weights)
	}

	specs := cfg.Specs()
	if len(specs) != 3 {
		t.Fatalf("specs = %d", len(specs))
	}
	if specs[0].HealthPath != DefaultHealthPath || specs[0].PredictPath != DefaultPredictPath {
		t.Fatalf("defaults not applied: %+v", specs[0])
	}
	if !specs[0].GPUEnabled || specs[1].GPUEnabled {
		t.Fatalf("gpu flags = %v %v", specs[0].GPUEnabled, specs[1].GPUEnabled)
	}
	if specs[2].ReadyPath != "/ready" {
		t.Fatalf("ready path = %q", specs[2].ReadyPath)
	}
	for _, s := range specs {
		if !s.Enabled {
			t.Fatalf("%s should be enabled by default", s.ID)
		}
	}
}

func TestLoadConfigRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"no workers":    "ml:\n  workers: []\n",
		"missing model": "ml:\n  workers:\n    - {id: a, url: \"http://127.0.0.1:1\"}\n",
		"missing url":   "ml:\n  workers:\n    - {id: a, model: m}\n",
		"bad url":       "ml:\n  workers:\n    - {id: a, model: m, url: nope}\n",
		"duplicate id":  "ml:\n  workers:\n    - {id: a, model: m, url: \"http://127.0.0.1:1\"}\n    - {id: a, model: m, url: \"http://127.0.0.1:2\"}\n",
		"unknown field": "ml:\n  nope: 1\n  workers:\n    - {id: a, model: m, url: \"http://127.0.0.1:1\"}\n",
		"bad duration":  "ml:\n  health_interval: soon\n  workers:\n    - {id: a, model: m, url: \"http://127.0.0.1:1\"}\n",
	}
	for name, body := range cases {
		if _, err := LoadConfig(writeConfig(t, body)); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

func TestSpecListFlag(t *testing.T) {
	var l SpecList
	if err := l.Set("id=resnet-1,model=resnet50,url=http://127.0.0.1:9001/"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := l.Set("id=cpu-1,model=resnet50,url=http://127.0.0.1:9002,gpu=false,gpu_index=1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(l) != 2 {
		t.Fatalf("len = %d", len(l))
	}
	if l[0].URL != "http://127.0.0.1:9001" {
		t.Fatalf("trailing slash not trimmed: %q", l[0].URL)
	}
	if l[0].HealthPath != DefaultHealthPath || l[0].PredictPath != DefaultPredictPath || !l[0].Enabled {
		t.Fatalf("defaults = %+v", l[0])
	}
	if l[1].GPUEnabled || l[1].GPUIndex != 1 {
		t.Fatalf("gpu fields = %+v", l[1])
	}
	for _, bad := range []string{"", "model=m,url=http://x:1", "id=a,model=m", "id=a,model=m,url=nope", "id=a,model=m,url=http://x:1,what=1", "nonsense"} {
		var v SpecList
		if err := v.Set(bad); err == nil {
			t.Fatalf("Set(%q): expected an error", bad)
		}
	}
}

func TestWeightsValidate(t *testing.T) {
	if err := (Weights{}).Validate(); err == nil {
		t.Fatal("all-zero weights should be rejected")
	}
	if err := (Weights{Latency: -1, Queue: 1}).Validate(); err == nil {
		t.Fatal("negative weights should be rejected")
	}
	if err := (Weights{Latency: 1}).Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
