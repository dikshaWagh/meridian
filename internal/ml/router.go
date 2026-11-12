package ml

// workerFor returns the single configured worker for a kind/model, if any.
// The first implementation supports one worker per model kind.
func (s *Service) workerFor(kind Kind, modelID string) *WorkerHandle {
	if s == nil || s.cfg == nil || !s.cfg.enabled(kind) {
		return nil
	}
	if modelID == "" {
		modelID = s.cfg.defaultModelID(kind)
	}
	if modelID != s.cfg.defaultModelID(kind) {
		return nil
	}
	for _, h := range s.workers {
		if h.Kind == kind && h.ModelID == modelID {
			return h
		}
	}
	return nil
}

// pick routes by exact model ID and kind, skipping workers that are not ready,
// and enforces the per-worker concurrency limit without preempting anyone.
func (s *Service) pick(kind Kind, modelID string) (*WorkerHandle, error) {
	if s == nil || s.cfg == nil || !s.cfg.enabled(kind) {
		return nil, ErrServiceDisabled
	}
	if s.shuttingDown.Load() {
		return nil, ErrServiceDisabled
	}
	h := s.workerFor(kind, modelID)
	if h == nil {
		return nil, ErrModelNotFound
	}
	if !h.IsReady() {
		return nil, ErrNoHealthyWorker
	}
	if !h.tryAcquire() {
		return nil, ErrAllWorkersBusy
	}
	return h, nil
}

// blockedReason reports why a configured model cannot currently serve work.
func (s *Service) blockedReason(kind Kind, modelID string) (status int, errType, msg string, ok bool) {
	h := s.workerFor(kind, modelID)
	if h == nil {
		return 0, "", "", false
	}
	if h.IsReady() {
		return 0, "", "", false
	}
	state := h.State()
	lastErr := h.LastError()
	switch state {
	case StateResourceBlocked:
		return 503, "insufficient_resources", lastErr, true
	case StateStarting:
		return 503, "worker_start_failed", "ml worker is starting", true
	case StateUnhealthy:
		return 503, "no_healthy_worker", "ml worker is unhealthy: " + lastErr, true
	default:
		return 503, "worker_start_failed", lastErr, true
	}
}
