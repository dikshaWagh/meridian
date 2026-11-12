package ml

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const ocrTimeout = 60 * time.Second

// Recognize sends one image to the OCR worker and returns its result.
func (s *Service) Recognize(ctx context.Context, modelID string, imageBytes []byte, contentType, requestID string) (*OCRResponse, error) {
	start := time.Now()
	h, err := s.pick(KindOCR, modelID)
	if err != nil {
		return nil, err
	}
	defer h.releaseSlot()

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, ocrTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/ocr", bytes.NewReader(imageBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := h.client.Do(req)
	if err != nil {
		h.failed.Add(1)
		if ctx.Err() != nil {
			return nil, ErrInferenceTimeout
		}
		return nil, fmt.Errorf("ocr worker: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		h.failed.Add(1)
		return nil, ErrAllWorkersBusy
	}
	if resp.StatusCode != http.StatusOK {
		h.failed.Add(1)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("ocr worker returned %d: %s", resp.StatusCode, string(body))
	}

	var result OCRResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		h.failed.Add(1)
		return nil, fmt.Errorf("decode ocr result: %w", err)
	}
	h.served.Add(1)
	ms := time.Since(start).Milliseconds()
	h.lastInferenceMS.Store(ms)
	return &OCRResponse{
		RequestID:    requestID,
		Model:        result.Model,
		Text:         result.Text,
		Lines:        result.Lines,
		ProcessingMS: ms,
	}, nil
}

// GenerateImage validates the request, renders it on the image worker and
// persists the PNG under the configured artifact directory.
func (s *Service) GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResponse, error) {
	if s == nil || s.cfg == nil || s.cfg.ImageGeneration == nil || !s.cfg.ImageGeneration.Enabled {
		return nil, ErrServiceDisabled
	}
	cfg := s.cfg.ImageGeneration
	if req.Model == "" {
		req.Model = cfg.ModelID
	}
	if req.Model != cfg.ModelID {
		return nil, ErrModelNotFound
	}
	if req.N > 1 {
		return nil, fmt.Errorf("%w: n greater than 1 is not supported", ErrInvalidRequest)
	}
	workerCfg := WorkerConfig{
		Kind:           KindImageGeneration,
		MaxWidth:       cfg.MaxWidth,
		MaxHeight:      cfg.MaxHeight,
		MaxSteps:       cfg.MaxSteps,
		DefaultSteps:   cfg.DefaultSteps,
		MaxPromptChars: cfg.MaxPromptChars,
	}
	// Reject invalid input before touching a worker.
	if err := normalizeImageRequest(&req, workerCfg); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}

	start := time.Now()
	h, err := s.pick(KindImageGeneration, req.Model)
	if err != nil {
		return nil, err
	}
	defer h.releaseSlot()

	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutSeconds)*time.Second)
	defer cancel()

	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/v1/images/generations", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(httpReq)
	if err != nil {
		h.failed.Add(1)
		if ctx.Err() != nil {
			return nil, ErrInferenceTimeout
		}
		return nil, fmt.Errorf("image worker: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusServiceUnavailable {
		h.failed.Add(1)
		return nil, ErrAllWorkersBusy
	}
	if resp.StatusCode != http.StatusOK {
		h.failed.Add(1)
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("image worker returned %d: %s", resp.StatusCode, string(body))
	}

	var workerResp WorkerImageResponse
	if err := json.NewDecoder(resp.Body).Decode(&workerResp); err != nil {
		h.failed.Add(1)
		return nil, fmt.Errorf("decode image result: %w", err)
	}
	pngBytes, err := base64.StdEncoding.DecodeString(workerResp.PNGBase64)
	if err != nil {
		h.failed.Add(1)
		return nil, fmt.Errorf("decode generated png: %w", err)
	}
	if _, err := png.DecodeConfig(bytes.NewReader(pngBytes)); err != nil {
		h.failed.Add(1)
		return nil, fmt.Errorf("generated image is not a valid PNG: %w", err)
	}
	id, err := s.saveArtifact(pngBytes)
	if err != nil {
		h.failed.Add(1)
		return nil, err
	}
	h.served.Add(1)
	ms := time.Since(start).Milliseconds()
	h.lastInferenceMS.Store(ms)
	return &ImageGenerationResponse{
		ID:           id,
		Model:        cfg.ModelID,
		URL:          "/v1/artifacts/" + id,
		Width:        workerResp.Width,
		Height:       workerResp.Height,
		Steps:        workerResp.Steps,
		Seed:         workerResp.Seed,
		Created:      time.Now().Unix(),
		ProcessingMS: ms,
	}, nil
}

// saveArtifact writes PNG bytes under an opaque random ID.
func (s *Service) saveArtifact(pngBytes []byte) (string, error) {
	if s.artifacts == "" {
		return "", fmt.Errorf("artifact directory is not configured")
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	final := filepath.Join(s.artifacts, id+".png")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, pngBytes, 0o644); err != nil {
		return "", fmt.Errorf("write artifact: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("commit artifact: %w", err)
	}
	return id, nil
}

// ArtifactPath resolves an opaque artifact ID to a file inside the artifact
// directory. Path traversal is rejected by the ID pattern and prefix check.
func (s *Service) ArtifactPath(id string) (string, error) {
	if s == nil || s.artifacts == "" {
		return "", ErrArtifactNotFound
	}
	if !validArtifactID(id) {
		return "", ErrArtifactNotFound
	}
	path := filepath.Join(s.artifacts, id+".png")
	rel, err := filepath.Rel(s.artifacts, path)
	if err != nil || rel == "." || rel == ".." || len(rel) >= 2 && rel[:2] == ".." {
		return "", ErrArtifactNotFound
	}
	if _, err := os.Stat(path); err != nil {
		return "", ErrArtifactNotFound
	}
	return path, nil
}
