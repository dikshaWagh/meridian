# Phase 1 — Fault Tolerance On A Single Machine

**Paste into DeepSeek ONLY after Phase 0 passes. This is your viva core.**

## Objective
Prove: **kill one worker process on the same machine, chats continue, history preserved, no client error for non-streaming requests.**

## Current State (After Phase 0)
- `internal/balancer/pool.go`: health FSM `Starting->Healthy->Unhealthy` (3 fails), `WorkerReservation` (`Reserve->Commit/Release` under `pickMu`), `SessionStore` migration (`session moved w1->w2`).
- `internal/balancer/handler.go`: forwards via `ReverseProxy`, captures usage, logs `logs/requests.jsonl`.
- Missing: transparent retry on dial failure. Today a dead worker returns `502 upstream_error`.

## Task 1: Atomic Slot Reservation Audit (No Logic Change, Add Test)
- Read `pool.go:reservePick`, `handler.go` reservation lifecycle.
- Add test: 10 concurrent `PickForSessionWithPolicyReserved` on 1 idle worker must never exceed `Load()` accounting. Existing `pool_test.go` style.
- Explain in comment: why `pickMu + Reserved` prevents thundering herd.

## Task 2: Rolling tok/s Telemetry
- On `Worker` in `pool.go`, add `tokPerSec float64 + mu` with `UpdateSpeed(measured)` using EWMA: `new = 0.7*old + 0.3*measured`, init `20.0`.
- In `handler.go` after successful `200` and `usage.ok`, compute `measured = usage.out / elapsed.Seconds()`, call `worker.UpdateSpeed(measured)`.
- Expose `tok_per_sec` in `api.WorkerStatus` + `GET /status` + `Snapshot()`.
- Test: fake worker speed updates, clamps negative/zero.

## Task 3: Single-Retry Failover (Non-Streaming First)
- In `handler.go:handleChat`, buffer request body (`raw` already read, max 1 MiB).
- Wrap `worker.Proxy.ServeHTTP` with a `statusRecorder` that detects pre-header dial/reset failure (`upstream_error` 502, client bytes not yet written `!rec.wrote`).
- On failure: `worker.recordFailure(err)`, mark unhealthy if connection refused, re-call `pool.PickForSessionWithPolicyReserved(model, convID, policy)` (this auto-migrates session + logs move), replay buffered body **once** to new worker.
- `stream: true` requests: do NOT retry mid-stream. Return error and document why (KV cache on dead node lost, tokens non-deterministic).
- Add header `X-Meridian-Failover: w2->w1` on retried response for demo visibility.

## Task 4: Bounded Restart + RAM Admission (Single Machine)
- LLM workers: in `supervisor.go:startChildren`, on `cmd.Wait()` exit, rely on existing health FSM (no infinite restart loop). Log clearly.
- ML OCR: verify `internal/ml/service.go` `maxRestarts=2` backoff still works after trim.
- Verify `internal/ml/resource.go:Manager.Reserve` still rejects OCR if `MemAvailable - headroom < expected_ram_mb`. Log `insufficient_resources`.

## Manual Test Script (Run + Save Logs)
```bash
make server
./bin/meridian-server serve --enable-llm=true --enable-ml=false \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server --models-dir ~/models \
  --threads 8 --ctx 4096
# Terminal 2: start 20-chat stress in background
./bin/meridian-stress --addr http://127.0.0.1:8080 --concurrency 4 --conversations-per-model 10 &
# Terminal 3: kill worker w2 mid-run
curl http://127.0.0.1:8080/status  # note PIDs
kill -9 <PID_of_w2>
curl http://127.0.0.1:8080/status  # w2 unhealthy, w1 healthy
# Expect: stress failures ~0 for non-streaming, log shows session moved
```

## Acceptance Criteria
- [ ] `go test -race ./internal/balancer` passes (race detector clean)
- [ ] Kill w2 mid-stress: non-streaming success rate >= 95%, log has `session moved`
- [ ] `/status` shows `tok_per_sec` updating after requests
- [ ] `logs/requests.jsonl` has worker IDs, no prompts/PII
- [ ] Write 5-line note in `docs/phase1-results.md`: p50/p95 before/after kill, failure count

## Non-Goals
No multi-node yet. No cost-aware routing yet (Phase 2 uses tok/s you just built). Do not add Raft/leader election.
