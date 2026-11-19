package ml

import (
	"bytes"
	"fmt"
	"net"
	"os"

	"gopkg.in/yaml.v3"
)

// Engine names understood by the OCR worker. Only "fake" is compiled in for
// every platform; "tesseract" shells out to the tesseract CLI. Unknown engines
// are rejected at configuration time.
const (
	EngineFake      = "fake"
	EngineTesseract = "tesseract"
)

// Config is the top-level ML serving configuration (configs/ml.yaml).
type Config struct {
	ListenHost           string          `yaml:"listen_host"`
	ResourcePolicy       string          `yaml:"resource_policy"`
	Resources            ResourcesConfig `yaml:"resources"`
	OCR                  *OCRConfig      `yaml:"ocr"`
	ShutdownGraceSeconds int             `yaml:"shutdown_grace_seconds"`
}

// ResourcesConfig holds admission-control budgets and safety margins.
type ResourcesConfig struct {
	RAMHeadroomMB      int64  `yaml:"ram_headroom_mb"`
	VRAMHeadroomMB     int64  `yaml:"vram_headroom_mb"`
	ProtectedLLMVRAMMB int64  `yaml:"protected_llm_vram_mb"`
	GPUProbe           string `yaml:"gpu_probe"` // auto | off
}

type OCRConfig struct {
	Enabled               bool   `yaml:"enabled"`
	ModelID               string `yaml:"model_id"`
	Engine                string `yaml:"engine"`
	Device                string `yaml:"device"`
	ModelDir              string `yaml:"model_dir"`
	Language              string `yaml:"language"`
	ExpectedRAMMB         int64  `yaml:"expected_ram_mb"`
	ExpectedVRAMMB        int64  `yaml:"expected_vram_mb"`
	Concurrency           int    `yaml:"concurrency"`
	Port                  int    `yaml:"port"`
	MaxUploadMB           int64  `yaml:"max_upload_mb"`
	StartupTimeoutSeconds int    `yaml:"startup_timeout_seconds"`
}

// LoadConfig reads, normalises and validates an ML YAML configuration.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read ml config %q: %w", path, err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse ml config %q: %w", path, err)
	}
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid ml config %q: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) normalize() {
	if c.ListenHost == "" {
		c.ListenHost = "127.0.0.1"
	}
	if c.ResourcePolicy == "" {
		c.ResourcePolicy = "protect-llm"
	}
	if c.Resources.GPUProbe == "" {
		c.Resources.GPUProbe = "auto"
	}
	if c.ShutdownGraceSeconds <= 0 {
		c.ShutdownGraceSeconds = 10
	}
	if c.OCR != nil && c.OCR.Enabled {
		o := c.OCR
		if o.Engine == "" {
			o.Engine = EngineFake
		}
		if o.Device == "" {
			o.Device = "cpu"
		}
		if o.Concurrency <= 0 {
			o.Concurrency = 2
		}
		if o.Port == 0 {
			o.Port = 8091
		}
		if o.MaxUploadMB <= 0 {
			o.MaxUploadMB = 10
		}
		if o.Language == "" {
			o.Language = "eng"
		}
		if o.StartupTimeoutSeconds <= 0 {
			o.StartupTimeoutSeconds = 60
		}
	}
}

func (c *Config) validate() error {
	if !isLoopbackHost(c.ListenHost) {
		return fmt.Errorf("listen_host %q must be a loopback address", c.ListenHost)
	}
	if c.ResourcePolicy != "protect-llm" {
		return fmt.Errorf("resource_policy %q is not supported (only protect-llm)", c.ResourcePolicy)
	}
	if c.Resources.GPUProbe != "auto" && c.Resources.GPUProbe != "off" {
		return fmt.Errorf("resources.gpu_probe %q must be auto or off", c.Resources.GPUProbe)
	}
	if c.Resources.RAMHeadroomMB < 0 || c.Resources.VRAMHeadroomMB < 0 || c.Resources.ProtectedLLMVRAMMB < 0 {
		return fmt.Errorf("resource headroom and protected VRAM must not be negative")
	}
	if c.ShutdownGraceSeconds < 1 {
		return fmt.Errorf("shutdown_grace_seconds must be at least 1")
	}

	if c.OCR == nil || !c.OCR.Enabled {
		return fmt.Errorf("ML serving is enabled but no ML model is enabled")
	}
	o := c.OCR
	if o.ModelID == "" {
		return fmt.Errorf("ocr.model_id is required")
	}
	if o.Engine != EngineFake && o.Engine != EngineTesseract {
		return fmt.Errorf("ocr.engine %q is not recognised (want %s or %s)", o.Engine, EngineFake, EngineTesseract)
	}
	if o.Concurrency < 1 {
		return fmt.Errorf("ocr.concurrency must be at least 1")
	}
	if err := validPort(o.Port); err != nil {
		return fmt.Errorf("ocr.port: %w", err)
	}
	if o.ExpectedRAMMB < 0 || o.ExpectedVRAMMB < 0 {
		return fmt.Errorf("ocr resource budgets must not be negative")
	}
	if o.ExpectedVRAMMB > 0 && o.Device == "cpu" {
		return fmt.Errorf("ocr.device cpu cannot use expected_vram_mb > 0")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is out of range", port)
	}
	return nil
}

// defaultModelID returns the default model ID for a kind, if that kind is enabled.
func (c *Config) defaultModelID(kind Kind) string {
	if kind == KindOCR && c.OCR != nil && c.OCR.Enabled {
		return c.OCR.ModelID
	}
	return ""
}

func (c *Config) enabled(kind Kind) bool {
	return kind == KindOCR && c.OCR != nil && c.OCR.Enabled
}
