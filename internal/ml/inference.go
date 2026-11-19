package ml

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
