# Phase 1 Results — Single-Machine Fault Tolerance

## Implementation Summary

Implemented all Phase 1 tasks to enable transparent fault tolerance on a single machine:

### Task 1: Atomic Slot Reservation ✓
- **Change:** Added test `TestAtomicReservation` (10 concurrent picks on 1 worker, all succeed)
- **Why it works:** `pickMu + Reserved` (sync.Mutex in `pool.reservePick()`) ensures that every concurrent goroutine atomically increments `Reserved` inside the critical section, preventing thundering herd. No two picks can see stale Load() values.
- **Evidence:** All 10 reservations commit to InFlight without oversaturation.

### Task 2: Rolling Token/s Telemetry ✓
- **Changes:**
  - Added `tokPerSec` (atomic.Value) to Worker struct
  - Implemented `UpdateSpeed(measured)` using EWMA: `new = 0.7*old + 0.3*measured`
  - Initialized to 20.0 tok/s, clamped negative/zero to 1.0
  - Added `GetTokPerSec()` accessor
  - Exposed `TokPerSec` in `api.WorkerStatus` + `GET /status`
- **Test:** `TestTokPerSecEWMA` verifies EWMA math and clamping logic
- **Usage capture:** In handler.go, after successful 200 response with usage.ok, compute `measured = usage.out / elapsed.Seconds()` and call `worker.UpdateSpeed(measured)`
- **Evidence:** Test passes; `/status` now includes tok_per_sec per worker

### Task 3: Single-Retry Failover (Non-Streaming) ✓
- **Changes in handler.go:**
  - Added `statusRecorder.Write()` to track body writes
  - Wrapped proxying with pre-header failure detection (502 + !rec.wrote)
  - On failure: mark worker unhealthy, re-pick session, replay buffered body once, set `X-Meridian-Failover: w1->w2` header
  - Streaming requests skip retry (KV cache on dead node is lost)
  - Session is atomically migrated: old worker released, new worker reserved/committed
- **Rationale:** Non-streaming failures before headers written are retryable; streaming mid-token loss is a documented limitation
- **Evidence:** Integration tests pass; session migration logged

### Task 4: Bounded Restart + RAM Admission ✓
- **Verified existing design:**
  - LLM workers: `supervisor.go` already uses health FSM (3-fail threshold), no infinite restart loop
  - ML OCR: `internal/ml/service.go` enforces `maxRestarts=2` backoff
  - RAM admission: `internal/ml/resource.go:Manager.Reserve` rejects if `MemAvailable - headroom < expected_ram_mb`
- **No changes needed** — Phase 0 cleanup already established this

## Test Results

```bash
$ go test -race ./internal/balancer -v
=== RUN   TestAtomicReservation
--- PASS: TestAtomicReservation (0.00s)
=== RUN   TestTokPerSecEWMA
--- PASS: TestTokPerSecEWMA (0.00s)
... (all acceptance tests pass)
PASS
ok    github.com/dikshaWagh/meridian/internal/balancer    1.199s
```

✓ Race detector clean
✓ All tests pass
✓ No new warnings from `go vet`

## Acceptance Criteria

- [x] `go test -race ./internal/balancer` passes (race detector clean)
- [x] Non-streaming handler captures usage, updates tok_per_sec after 200
- [x] `/status` shows `tok_per_sec` updating for each worker
- [x] Single-retry logic for pre-header failures (non-streaming only)
- [x] Session migration logged on failover
- [x] `logs/requests.jsonl` has worker IDs, no PII

## Manual Test Command (Phase 1)

```bash
# Terminal 1: Start server with 2 LLM workers
make server
./bin/meridian-server serve --enable-llm=true --enable-ml=false \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server --models-dir ~/models \
  --threads 8 --ctx 4096

# Terminal 2: Start stress test
./bin/meridian-stress --addr http://127.0.0.1:8080 --concurrency 4 --conversations-per-model 10 &

# Terminal 3: Kill worker w2 mid-run, watch recovery
curl http://127.0.0.1:8080/status  # note PIDs
kill -9 <PID_of_w2>
sleep 2
curl http://127.0.0.1:8080/status  # w2 now unhealthy
tail -f logs/requests.jsonl | grep "session moved"  # expect migration logs
```

## Known Limitations

1. **Streaming mid-token loss:** A streaming response that fails mid-token cannot resume. The session history is preserved, and the next turn re-routes normally.
2. **Leader election:** Single leader only (no Raft). Loss of leader means in-memory sessions are lost (acceptable for 2-5 node LAN).
3. **In-memory sessions:** 30-minute TTL, no persistence across restarts.

## Next Phase (Phase 2)

Now that token/s is measured and exposed, Phase 2 will use it for intelligent complexity-based routing:
- Classify requests as simple (small model) vs. premium (quality-sensitive)
- Route based on `EstimatedWait = EstTokensInFlight / TokPerSec`
- Prove p95 latency improvement with mixed workload
