package ml

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration accepts Go duration strings ("2s", "500ms") in YAML.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) orDefault(def time.Duration) time.Duration {
	if d == 0 {
		return def
	}
	return time.Duration(d)
}

// Config is the on-disk ML configuration. Only the `ml:` key is read; LLM
// configuration is untouched and keeps using its existing flags.
type Config struct {
	ML MLConfig `yaml:"ml"`
}

type MLConfig struct {
	Listen            string         `yaml:"listen"`
	HealthInterval    Duration       `yaml:"health_interval"`
	TelemetryInterval Duration       `yaml:"telemetry_interval"`
	RequestTimeout    Duration       `yaml:"request_timeout"`
	StartupGrace      Duration       `yaml:"startup_grace"`
	FailureThreshold  int            `yaml:"failure_threshold"`
	Weights           *Weights       `yaml:"weights"`
	Workers           []WorkerConfig `yaml:"workers"`
}

type WorkerConfig struct {
	ID          string `yaml:"id"`
	Model       string `yaml:"model"`
	URL         string `yaml:"url"`
	HealthPath  string `yaml:"health_path"`
	ReadyPath   string `yaml:"ready_path"`
	PredictPath string `yaml:"predict_path"`
	GPU         *bool  `yaml:"gpu"`
	GPUIndex    int    `yaml:"gpu_index"`
	Enabled     *bool  `yaml:"enabled"`
}

// Default paths of the ML worker contract.
const (
	DefaultHealthPath  = "/health"
	DefaultPredictPath = "/predict"
)

// LoadConfig reads and validates an ML config file.
func LoadConfig(path string) (MLConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return MLConfig{}, err
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return MLConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.ML.Validate(); err != nil {
		return MLConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	return c.ML, nil
}

func (c MLConfig) Validate() error {
	if len(c.Workers) == 0 {
		return fmt.Errorf("ml.workers is empty")
	}
	seen := map[string]bool{}
	for i, w := range c.Workers {
		if w.ID == "" {
			return fmt.Errorf("ml.workers[%d]: id is required", i)
		}
		if seen[w.ID] {
			return fmt.Errorf("ml.workers[%d]: duplicate id %q", i, w.ID)
		}
		seen[w.ID] = true
		if w.Model == "" {
			return fmt.Errorf("ml.workers[%d] (%s): model is required", i, w.ID)
		}
		if w.URL == "" {
			return fmt.Errorf("ml.workers[%d] (%s): url is required", i, w.ID)
		}
		u, err := url.Parse(w.URL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("ml.workers[%d] (%s): invalid url %q", i, w.ID, w.URL)
		}
	}
	if c.Weights != nil {
		if err := c.Weights.Validate(); err != nil {
			return fmt.Errorf("ml.weights: %w", err)
		}
	}
	if c.FailureThreshold < 0 {
		return fmt.Errorf("ml.failure_threshold must be >= 0")
	}
	return nil
}

// SpecList is a repeatable CLI flag of inline worker specs, for quick demos and
// tests without a config file:
//
//	--ml-worker id=resnet-1,model=resnet50,url=http://127.0.0.1:9001
type SpecList []WorkerSpec

func (s *SpecList) String() string {
	ids := make([]string, 0, len(*s))
	for _, w := range *s {
		ids = append(ids, w.ID)
	}
	return strings.Join(ids, ",")
}

func (s *SpecList) Set(v string) error {
	spec := WorkerSpec{HealthPath: DefaultHealthPath, PredictPath: DefaultPredictPath, GPUEnabled: true, Enabled: true}
	for _, field := range strings.Split(v, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key, val, ok := strings.Cut(field, "=")
		if !ok {
			return fmt.Errorf("expected key=value, got %q", field)
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "id":
			spec.ID = val
		case "model":
			spec.Model = val
		case "url":
			spec.URL = strings.TrimRight(val, "/")
		case "health_path":
			spec.HealthPath = val
		case "ready_path":
			spec.ReadyPath = val
		case "predict_path":
			spec.PredictPath = val
		case "gpu":
			spec.GPUEnabled = val == "true" || val == "1" || val == "yes"
		case "gpu_index":
			n, err := strconv.Atoi(val)
			if err != nil {
				return fmt.Errorf("invalid gpu_index %q", val)
			}
			spec.GPUIndex = n
		default:
			return fmt.Errorf("unknown field %q", key)
		}
	}
	if spec.ID == "" || spec.Model == "" || spec.URL == "" {
		return fmt.Errorf("id, model and url are required")
	}
	if u, err := url.Parse(spec.URL); err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid url %q", spec.URL)
	}
	*s = append(*s, spec)
	return nil
}

// Specs converts the config workers into runtime specs, applying contract
// defaults.
func (c MLConfig) Specs() []WorkerSpec {
	out := make([]WorkerSpec, 0, len(c.Workers))
	for _, w := range c.Workers {
		s := WorkerSpec{
			ID:          w.ID,
			Model:       w.Model,
			URL:         strings.TrimRight(w.URL, "/"),
			HealthPath:  w.HealthPath,
			ReadyPath:   w.ReadyPath,
			PredictPath: w.PredictPath,
			GPUIndex:    w.GPUIndex,
			GPUEnabled:  true,
			Enabled:     true,
		}
		if s.HealthPath == "" {
			s.HealthPath = DefaultHealthPath
		}
		if s.PredictPath == "" {
			s.PredictPath = DefaultPredictPath
		}
		if w.GPU != nil {
			s.GPUEnabled = *w.GPU
		}
		if w.Enabled != nil {
			s.Enabled = *w.Enabled
		}
		out = append(out, s)
	}
	return out
}
