package ml

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"regexp"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/metrics"
)

var artifactIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func validArtifactID(id string) bool { return artifactIDPattern.MatchString(id) }

// Register mounts the ML public API on an existing mux. A nil service keeps
// the endpoints registered but returns service_disabled, which gives clients a
// stable contract in LLM-only deployments.
func Register(mux *http.ServeMux, svc *Service) {
	h := &Handler{svc: svc}
	mux.HandleFunc("POST /v1/ocr", h.handleOCR)
	mux.HandleFunc("POST /v1/images/generations", h.handleImageGeneration)
	mux.HandleFunc("GET /v1/ml/models", h.handleModels)
	mux.HandleFunc("GET /ml/status", h.handleStatus)
	mux.HandleFunc("GET /v1/artifacts/{id}", h.handleArtifact)
}

// Handler serves Meridian's ML endpoints.
type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) http.Handler {
	mux := http.NewServeMux()
	Register(mux, svc)
	return mux
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.status = code
	s.wrote = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}

func (h *Handler) handleOCR(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	w = rec
	requestID := metrics.NewRequestID()
	w.Header().Set("X-Request-ID", requestID)
	modelID := ""
	errType := ""
	defer func() {
		metrics.AppendMLRequest(metrics.MLRequestLog{
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
			RequestID:  requestID,
			Service:    "meridian-ml",
			Kind:       string(KindOCR),
			Model:      modelID,
			StatusCode: rec.status,
			LatencyMS:  time.Since(start).Milliseconds(),
			ErrorType:  errType,
		})
	}()

	if h.svc == nil || h.svc.cfg == nil || h.svc.cfg.OCR == nil || !h.svc.cfg.OCR.Enabled {
		errType = "service_disabled"
		api.WriteError(w, http.StatusServiceUnavailable, errType, "ML serving is disabled (start with --enable-llm/--enable-ml and an ml config)")
		return
	}
	cfg := h.svc.cfg.OCR
	maxUpload := cfg.MaxUploadMB << 20
	if maxUpload <= 0 {
		maxUpload = 10 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		errType = "request_too_large"
		api.WriteError(w, http.StatusRequestEntityTooLarge, errType, "multipart upload exceeds the configured limit")
		return
	}
	defer r.MultipartForm.RemoveAll()
	modelID = r.FormValue("model")

	file, header, err := r.FormFile("file")
	if err != nil {
		errType = "invalid_request"
		api.WriteError(w, http.StatusBadRequest, errType, "multipart field \"file\" is required")
		return
	}
	defer file.Close()
	if header.Size > maxUpload {
		errType = "request_too_large"
		api.WriteError(w, http.StatusRequestEntityTooLarge, errType, "image exceeds the configured upload limit")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil || int64(len(raw)) > maxUpload {
		errType = "request_too_large"
		api.WriteError(w, http.StatusRequestEntityTooLarge, errType, "image exceeds the configured upload limit")
		return
	}

	contentType := http.DetectContentType(raw)
	if contentType != "image/png" && contentType != "image/jpeg" {
		errType = "invalid_request"
		api.WriteError(w, http.StatusBadRequest, errType, "only PNG and JPEG images are supported")
		return
	}
	if _, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
		errType = "invalid_request"
		api.WriteError(w, http.StatusBadRequest, errType, "malformed image: "+err.Error())
		return
	}

	resp, err := h.svc.Recognize(r.Context(), modelID, raw, contentType, requestID)
	if err != nil {
		errType = h.writeServiceError(w, KindOCR, modelID, err)
		return
	}
	modelID = resp.Model
	api.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleImageGeneration(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	w = rec
	requestID := metrics.NewRequestID()
	w.Header().Set("X-Request-ID", requestID)
	modelID := ""
	errType := ""
	defer func() {
		metrics.AppendMLRequest(metrics.MLRequestLog{
			Timestamp:  time.Now().UTC().Format(time.RFC3339),
			RequestID:  requestID,
			Service:    "meridian-ml",
			Kind:       string(KindImageGeneration),
			Model:      modelID,
			StatusCode: rec.status,
			LatencyMS:  time.Since(start).Milliseconds(),
			ErrorType:  errType,
		})
	}()

	if h.svc == nil || h.svc.cfg == nil || h.svc.cfg.ImageGeneration == nil || !h.svc.cfg.ImageGeneration.Enabled {
		errType = "service_disabled"
		api.WriteError(w, http.StatusServiceUnavailable, errType, "ML serving is disabled (start with --enable-ml=true and an ml config)")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req ImageGenerationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errType = "invalid_request"
		api.WriteError(w, http.StatusBadRequest, errType, "body is not valid JSON or exceeds 1 MiB")
		return
	}
	modelID = req.Model
	if modelID == "" {
		modelID = h.svc.cfg.ImageGeneration.ModelID
	}

	resp, err := h.svc.GenerateImage(r.Context(), req)
	if err != nil {
		errType = h.writeServiceError(w, KindImageGeneration, modelID, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleArtifact(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		api.WriteError(w, http.StatusServiceUnavailable, "service_disabled", "ML serving is disabled")
		return
	}
	id := r.PathValue("id")
	path, err := h.svc.ArtifactPath(id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "model_not_found", "artifact not found")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "model_not_found", "artifact not found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "model_not_found", "artifact not found")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, id+".png", info.ModTime(), f)
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	list := ModelList{Object: "list", Data: []ModelStatus{}}
	if h.svc != nil {
		list.Data = h.svc.Models()
	}
	api.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	if h.svc == nil {
		api.WriteJSON(w, http.StatusOK, StatusResponse{
			Enabled:   false,
			Models:    []ModelStatus{},
			Workers:   []WorkerStatus{},
			Resources: ResourceStatus{Policy: "protect-llm", Reservations: []ReservationStatus{}},
		})
		return
	}
	api.WriteJSON(w, http.StatusOK, h.svc.Snapshot())
}

// writeServiceError maps stable ML errors to the API error contract and
// returns the error type for metrics.
func (h *Handler) writeServiceError(w http.ResponseWriter, kind Kind, modelID string, err error) string {
	switch {
	case errors.Is(err, ErrServiceDisabled):
		api.WriteError(w, http.StatusServiceUnavailable, "service_disabled", err.Error())
		return "service_disabled"
	case errors.Is(err, ErrInvalidRequest):
		api.WriteError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return "invalid_request"
	case errors.Is(err, ErrModelNotFound):
		api.WriteError(w, http.StatusNotFound, "model_not_found", "unknown ML model "+modelID)
		return "model_not_found"
	case errors.Is(err, ErrInferenceTimeout):
		api.WriteError(w, http.StatusGatewayTimeout, "inference_timeout", "ML inference exceeded its timeout")
		return "inference_timeout"
	case errors.Is(err, ErrAllWorkersBusy):
		api.WriteError(w, http.StatusServiceUnavailable, "no_healthy_worker", "all ML workers for this model are busy")
		return "no_healthy_worker"
	case errors.Is(err, ErrNoHealthyWorker):
		if h.svc != nil {
			if status, errType, msg, ok := h.svc.blockedReason(kind, modelID); ok {
				api.WriteError(w, status, errType, msg)
				return errType
			}
		}
		api.WriteError(w, http.StatusServiceUnavailable, "no_healthy_worker", "no healthy ML worker for this model")
		return "no_healthy_worker"
	default:
		api.WriteError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return "upstream_error"
	}
}
