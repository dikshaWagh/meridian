package ml

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"os"

	"gopkg.in/yaml.v3"
)

// Engine names understood by the ML worker. Only "fake" is compiled in for
// every platform; the native engines are recognised but intentionally blocked
// with a clear error until a validated Go binding is wired in.
const (
	EngineFake               = "fake"
	EngineTesseract          = "tesseract"
	EngineONNXRuntime        = "onnxruntime"
	EngineStableDiffusionCPP = "stable-diffusion.cpp"
	// EngineStableDiffusionServer proxies to a running stable-diffusion.cpp
	// sd-server over its Stable Diffusion WebUI API (/sdapi/v1/txt2img).
	EngineStableDiffusionServer = "stable-diffusion-server"
)

// Config is the top-level ML serving configuration (configs/ml.yaml).
type Config struct {
	ListenHost           string          `yaml:"listen_host"`
	ResourcePolicy       string          `yaml:"resource_policy"`
	Resources            ResourcesConfig `yaml:"resources"`
	OCR                  *OCRConfig      `yaml:"ocr"`
	ImageGeneration      *ImageGenConfig `yaml:"image_generation"`
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

type ImageGenConfig struct {
	Enabled               bool     `yaml:"enabled"`
	ModelID               string   `yaml:"model_id"`
	Engine                string   `yaml:"engine"`
	Device                string   `yaml:"device"`
	ModelPath             string   `yaml:"model_path"`
	ServerURL             string   `yaml:"server_url"`
	ServerCommand         string   `yaml:"server_command"`
	ServerArgs            []string `yaml:"server_args"`
	CFGScale              float64  `yaml:"cfg_scale"`
	ExpectedRAMMB         int64    `yaml:"expected_ram_mb"`
	ExpectedVRAMMB        int64    `yaml:"expected_vram_mb"`
	RequestOverheadVRAMMB int64    `yaml:"request_overhead_vram_mb"`
	Concurrency           int      `yaml:"concurrency"`
	Port                  int      `yaml:"port"`
	OutputDir             string   `yaml:"output_dir"`
	MaxWidth              int      `yaml:"max_width"`
	MaxHeight             int      `yaml:"max_height"`
	MaxSteps              int      `yaml:"max_steps"`
	DefaultSteps          int      `yaml:"default_steps"`
	MaxPromptChars        int      `yaml:"max_prompt_chars"`
	TimeoutSeconds        int      `yaml:"timeout_seconds"`
	StartupTimeoutSeconds int      `yaml:"startup_timeout_seconds"`
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
	if c.ImageGeneration != nil && c.ImageGeneration.Enabled {
		g := c.ImageGeneration
		if g.Engine == "" {
			g.Engine = EngineFake
		}
		if g.Device == "" {
			g.Device = "cpu"
		}
		if g.Concurrency <= 0 {
			g.Concurrency = 1
		}
		if g.Port == 0 {
			g.Port = 8092
		}
		if g.MaxWidth <= 0 {
			g.MaxWidth = 768
		}
		if g.MaxHeight <= 0 {
			g.MaxHeight = 768
		}
		if g.MaxSteps <= 0 {
			g.MaxSteps = 40
		}
		if g.DefaultSteps <= 0 {
			g.DefaultSteps = 25
		}
		if g.MaxPromptChars <= 0 {
			g.MaxPromptChars = 2000
		}
		if g.TimeoutSeconds <= 0 {
			g.TimeoutSeconds = 300
		}
		if g.StartupTimeoutSeconds <= 0 {
			g.StartupTimeoutSeconds = 60
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

	ocrEnabled := c.OCR != nil && c.OCR.Enabled
	imgEnabled := c.ImageGeneration != nil && c.ImageGeneration.Enabled
	if !ocrEnabled && !imgEnabled {
		return fmt.Errorf("ML serving is enabled but no ML model is enabled")
	}

	ports := map[int]string{}
	if ocrEnabled {
		o := c.OCR
		if o.ModelID == "" {
			return fmt.Errorf("ocr.model_id is required")
		}
		if o.Engine != EngineFake && o.Engine != EngineTesseract && o.Engine != EngineONNXRuntime {
			return fmt.Errorf("ocr.engine %q is not recognised (want %s, %s or %s)", o.Engine, EngineFake, EngineTesseract, EngineONNXRuntime)
		}
		if o.Engine == EngineONNXRuntime && o.ModelDir == "" {
			return fmt.Errorf("ocr.model_dir is required for engine %s", o.Engine)
		}
		if o.Concurrency < 1 {
			return fmt.Errorf("ocr.concurrency must be at least 1")
		}
		if err := validPort(o.Port); err != nil {
			return fmt.Errorf("ocr.port: %w", err)
		}
		ports[o.Port] = "ocr"
		if o.ExpectedRAMMB < 0 || o.ExpectedVRAMMB < 0 {
			return fmt.Errorf("ocr resource budgets must not be negative")
		}
		if o.ExpectedVRAMMB > 0 && o.Device == "cpu" {
			return fmt.Errorf("ocr.device cpu cannot use expected_vram_mb > 0")
		}
	}
	if imgEnabled {
		g := c.ImageGeneration
		if g.ModelID == "" {
			return fmt.Errorf("image_generation.model_id is required")
		}
		switch g.Engine {
		case EngineFake:
		case EngineStableDiffusionCPP:
			if g.ModelPath == "" {
				return fmt.Errorf("image_generation.model_path is required for engine %s", g.Engine)
			}
		case EngineStableDiffusionServer:
			if g.ServerURL == "" {
				return fmt.Errorf("image_generation.server_url is required for engine %s", g.Engine)
			}
			if err := validServerURL(g.ServerURL); err != nil {
				return fmt.Errorf("image_generation.server_url: %w", err)
			}
		default:
			return fmt.Errorf("image_generation.engine %q is not recognised (want %s, %s or %s)", g.Engine, EngineFake, EngineStableDiffusionCPP, EngineStableDiffusionServer)
		}
		if g.CFGScale < 0 {
			return fmt.Errorf("image_generation.cfg_scale must not be negative")
		}
		if g.ServerCommand != "" && g.Engine != EngineStableDiffusionServer {
			return fmt.Errorf("image_generation.server_command is only supported for engine %s", EngineStableDiffusionServer)
		}
		if g.Concurrency < 1 {
			return fmt.Errorf("image_generation.concurrency must be at least 1")
		}
		if err := validPort(g.Port); err != nil {
			return fmt.Errorf("image_generation.port: %w", err)
		}
		if other, ok := ports[g.Port]; ok {
			return fmt.Errorf("port %d is used by both %s and image_generation", g.Port, other)
		}
		ports[g.Port] = "image_generation"
		if g.OutputDir == "" {
			return fmt.Errorf("image_generation.output_dir is required")
		}
		if g.ExpectedRAMMB < 0 || g.ExpectedVRAMMB < 0 || g.RequestOverheadVRAMMB < 0 {
			return fmt.Errorf("image_generation resource budgets must not be negative")
		}
		if g.ExpectedVRAMMB > 0 && g.Device == "cpu" {
			return fmt.Errorf("image_generation.device cpu cannot use expected_vram_mb > 0")
		}
		if g.MaxWidth <= 0 || g.MaxHeight <= 0 || g.MaxSteps < 1 {
			return fmt.Errorf("image_generation max dimensions and max steps must be positive")
		}
		if g.MaxWidth%8 != 0 || g.MaxHeight%8 != 0 {
			return fmt.Errorf("image_generation max_width and max_height must be multiples of 8")
		}
		if g.DefaultSteps < 1 || g.DefaultSteps > g.MaxSteps {
			return fmt.Errorf("image_generation.default_steps must be between 1 and max_steps")
		}
		if g.TimeoutSeconds < 1 {
			return fmt.Errorf("image_generation.timeout_seconds must be at least 1")
		}
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

func validServerURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("must be an http or https URL")
	}
	if u.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

// modelDefaults returns the default model ID for a kind, if that kind is enabled.
func (c *Config) defaultModelID(kind Kind) string {
	switch kind {
	case KindOCR:
		if c.OCR != nil && c.OCR.Enabled {
			return c.OCR.ModelID
		}
	case KindImageGeneration:
		if c.ImageGeneration != nil && c.ImageGeneration.Enabled {
			return c.ImageGeneration.ModelID
		}
	}
	return ""
}

func (c *Config) enabled(kind Kind) bool {
	switch kind {
	case KindOCR:
		return c.OCR != nil && c.OCR.Enabled
	case KindImageGeneration:
		return c.ImageGeneration != nil && c.ImageGeneration.Enabled
	}
	return false
}
