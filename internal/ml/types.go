// Package ml implements Meridian's independent ML serving subsystem (OCR and
// image generation). It deliberately does not import internal/balancer: the ML
// control plane shares only the public HTTP listener with the LLM subsystem.
package ml

import "errors"

type Kind string

const (
	KindOCR             Kind = "ocr"
	KindImageGeneration Kind = "image-generation"
)

// Worker/model lifecycle states.
type State string

const (
	StateDisabled        State = "disabled"
	StateStarting        State = "starting"
	StateReady           State = "ready"
	StateUnhealthy       State = "unhealthy"
	StateResourceBlocked State = "resource_blocked"
	StateStopping        State = "stopping"
	StateExited          State = "exited"
)

var (
	ErrServiceDisabled  = errors.New("ml serving is disabled")
	ErrInvalidRequest   = errors.New("invalid ml request")
	ErrModelNotFound    = errors.New("ml model not found")
	ErrNoHealthyWorker  = errors.New("no healthy ml worker")
	ErrAllWorkersBusy   = errors.New("all ml workers are busy")
	ErrInferenceTimeout = errors.New("ml inference timed out")
	ErrArtifactNotFound = errors.New("artifact not found")
)

// BBox is a text bounding box in image pixel coordinates.
type BBox struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// OCRLine is one recognised line. Confidence and BBox are omitted when the
// backend does not provide them; they are never fabricated.
type OCRLine struct {
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence,omitempty"`
	BBox       *BBox    `json:"bbox,omitempty"`
}

// OCRResult is returned by OCR backends and the worker endpoint.
type OCRResult struct {
	Model string    `json:"model"`
	Text  string    `json:"text"`
	Lines []OCRLine `json:"lines"`
}

// OCRResponse is the public /v1/ocr response.
type OCRResponse struct {
	RequestID    string    `json:"request_id"`
	Model        string    `json:"model"`
	Text         string    `json:"text"`
	Lines        []OCRLine `json:"lines"`
	ProcessingMS int64     `json:"processing_ms"`
}

// ImageGenerationRequest is accepted by the public API and forwarded to a worker.
type ImageGenerationRequest struct {
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt"`
	NegativePrompt string `json:"negative_prompt,omitempty"`
	Width          int    `json:"width,omitempty"`
	Height         int    `json:"height,omitempty"`
	Steps          int    `json:"steps,omitempty"`
	Seed           int64  `json:"seed,omitempty"`
	N              int    `json:"n,omitempty"`
}

// GeneratedImage is produced by an image-generation backend.
type GeneratedImage struct {
	Model  string
	PNG    []byte
	Width  int
	Height int
	Steps  int
	Seed   int64
}

// WorkerImageResponse is the worker -> parent transport shape.
type WorkerImageResponse struct {
	Model     string `json:"model"`
	PNGBase64 string `json:"png_base64"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Steps     int    `json:"steps"`
	Seed      int64  `json:"seed"`
}

// ImageGenerationResponse is the public /v1/images/generations response.
type ImageGenerationResponse struct {
	ID           string `json:"id"`
	Model        string `json:"model"`
	URL          string `json:"url"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Steps        int    `json:"steps"`
	Seed         int64  `json:"seed"`
	Created      int64  `json:"created"`
	ProcessingMS int64  `json:"processing_ms"`
}

// ModelStatus is one entry in GET /v1/ml/models.
type ModelStatus struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Kind    Kind   `json:"kind"`
	State   State  `json:"state"`
	Healthy bool   `json:"healthy"`
	Worker  string `json:"worker_id,omitempty"`
}

type ModelList struct {
	Object string        `json:"object"`
	Data   []ModelStatus `json:"data"`
}

// WorkerStatus describes one supervised ML worker.
type WorkerStatus struct {
	ID              string `json:"id"`
	Kind            Kind   `json:"kind"`
	ModelID         string `json:"model_id"`
	Engine          string `json:"engine"`
	Device          string `json:"device"`
	PID             int    `json:"pid"`
	Port            int    `json:"port"`
	State           State  `json:"state"`
	Healthy         bool   `json:"healthy"`
	InFlight        int64  `json:"in_flight"`
	Concurrency     int64  `json:"concurrency"`
	Served          int64  `json:"served"`
	Failed          int64  `json:"failed"`
	Restarts        int64  `json:"restarts"`
	LastCheck       string `json:"last_check,omitempty"`
	LastError       string `json:"last_error,omitempty"`
	LastInferenceMS int64  `json:"last_inference_ms"`
}

// ReservationStatus reports one admission-control reservation.
type ReservationStatus struct {
	ModelID  string `json:"model_id"`
	Kind     Kind   `json:"kind"`
	RAMMB    int64  `json:"ram_mb"`
	VRAMMB   int64  `json:"vram_mb"`
	Overhead int64  `json:"overhead_vram_mb"`
}

// ResourceStatus exposes admission-control state. Memory values are estimates
// and may be stale between probe refreshes.
type ResourceStatus struct {
	Policy             string              `json:"policy"`
	RAMKnown           bool                `json:"ram_known"`
	RAMAvailableMB     int64               `json:"ram_available_mb"`
	RAMTotalMB         int64               `json:"ram_total_mb"`
	RAMReservedMB      int64               `json:"ram_reserved_mb"`
	RAMHeadroomMB      int64               `json:"ram_headroom_mb"`
	VRAMKnown          bool                `json:"vram_known"`
	VRAMAvailableMB    int64               `json:"vram_available_mb"`
	VRAMTotalMB        int64               `json:"vram_total_mb"`
	VRAMReservedMB     int64               `json:"vram_reserved_mb"`
	VRAMHeadroomMB     int64               `json:"vram_headroom_mb"`
	ProtectedLLMVRAMMB int64               `json:"protected_llm_vram_mb"`
	GPUProbe           string              `json:"gpu_probe"`
	ProbeError         string              `json:"probe_error,omitempty"`
	Reservations       []ReservationStatus `json:"reservations"`
}

// StatusResponse is GET /ml/status.
type StatusResponse struct {
	Enabled   bool           `json:"enabled"`
	Models    []ModelStatus  `json:"models"`
	Workers   []WorkerStatus `json:"workers"`
	Resources ResourceStatus `json:"resources"`
}
