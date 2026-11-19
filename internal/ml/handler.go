package ml

import (
	"bytes"
	"errors"
	"image"
	"io"
	"net/http"
	"time"

	"github.com/dikshaWagh/meridian/internal/api"
	"github.com/dikshaWagh/meridian/internal/metrics"
)

// Register mounts the ML public API on an existing mux. A nil service keeps
// the endpoints registered but returns service_disabled, which gives clients a
// stable contract in LLM-only deployments.
func Register(mux *http.ServeMux, svc *Service) {
	RegisterFunc(mux, func() *Service { return svc })
}

// RegisterFunc mounts the ML API with a service provider, consulted per request.
func RegisterFunc(mux *http.ServeMux, get func() *Service) {
	h := &Handler{get: get}
	mux.HandleFunc("POST /v1/ocr", h.handleOCR)
	mux.HandleFunc("GET /v1/ml/models", h.handleModels)
	mux.HandleFunc("GET /ml/status", h.handleStatus)
}

// Handler serves Meridian's ML endpoints.
type Handler struct {
	get func() *Service
}

func (h *Handler) service() *Service {
	if h == nil || h.get == nil {
		return nil
	}
	return h.get()
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

	svc := h.service()
	if svc == nil || svc.cfg == nil || svc.cfg.OCR == nil || !svc.cfg.OCR.Enabled {
		errType = "service_disabled"
		api.WriteError(w, http.StatusServiceUnavailable, errType, "ML serving is disabled (start with --enable-ml and an ml config)")
		return
	}
	cfg := svc.cfg.OCR
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

	resp, err := svc.Recognize(r.Context(), modelID, raw, contentType, requestID)
	if err != nil {
		errType = h.writeServiceError(w, KindOCR, modelID, err)
		return
	}
	modelID = resp.Model
	api.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	list := ModelList{Object: "list", Data: []ModelStatus{}}
	if svc := h.service(); svc != nil {
		list.Data = svc.Models()
	}
	api.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	svc := h.service()
	if svc == nil {
		api.WriteJSON(w, http.StatusOK, StatusResponse{
			Enabled:   false,
			Models:    []ModelStatus{},
			Workers:   []WorkerStatus{},
			Resources: ResourceStatus{Policy: "protect-llm", Reservations: []ReservationStatus{}},
		})
		return
	}
	api.WriteJSON(w, http.StatusOK, svc.Snapshot())
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
		if svc := h.service(); svc != nil {
			if status, errType, msg, ok := svc.blockedReason(kind, modelID); ok {
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
