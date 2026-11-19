# Phase 1: Single-Machine Fault Tolerance — Quick Start

## What You Get

✓ **Atomic Request Reservations** — No thundering herd  
✓ **Rolling Token/s Telemetry** — Measure actual throughput per worker  
✓ **Single-Retry Failover** — Non-streaming requests auto-recover on worker death  
✓ **Session Migration** — Conversations move to healthy workers transparently  

## Build & Test

```bash
# Build
make server

# Run all balancer tests (race-detector enabled)
go test -race ./internal/balancer -v

# Verify no issues
go vet ./...
```

## Check Token/s Telemetry

```bash
# Start server with 2 workers
./bin/meridian-server serve --enable-llm=true --enable-ml=false \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server --models-dir ~/models \
  --threads 8 --ctx 4096

# Poll /status in another terminal
curl http://127.0.0.1:8080/status | jq '.workers[] | {id, state, in_flight, tok_per_sec}'

# Example output:
# {
#   "id": "w1",
#   "state": "healthy",
#   "in_flight": 0,
#   "tok_per_sec": 45.3
# }
```

## Observe Session Migration

```bash
# Terminal 1: Start server (note worker PIDs)
./bin/meridian-server serve --enable-llm=true --enable-ml=false \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server --models-dir ~/models

# Terminal 2: Start stress test with conversations
./bin/meridian-stress --addr http://127.0.0.1:8080 --concurrency 4 --conversations-per-model 10 &

# Terminal 3: Kill one worker
PIDS=$(pgrep -f llama-server | head -2)
kill -9 $(echo $PIDS | awk '{print $2}')  # Kill w2

# Terminal 1: Watch logs for migration messages
tail -f | grep "session.*moved"
# Expected: "session conv-XXX moved w2 -> w1 (reason: worker process exited)"

# Terminal 3: Check that stress test continues
# ~95% of non-streaming requests should succeed
```

## API Changes for Phase 1

### `/status` Response (New Field)

```json
{
  "workers": [
    {
      "id": "w1",
      "model": "qwen2.5-1.5b",
      "state": "healthy",
      "in_flight": 2,
      "est_tokens_in_flight": 340,
      "tok_per_sec": 45.3,
      "sessions": 3
    }
  ],
  "sessionCount": 5
}
```

### Response Header (Failover)

When a non-streaming request retries due to worker failure:
```
X-Meridian-Failover: w1->w2
```

## Key Code Locations

- **Atomic reservation:** `internal/balancer/pool.go` — `reservePick()`, `WorkerReservation`
- **Token/s tracking:** `internal/balancer/pool.go` — `UpdateSpeed()`, `GetTokPerSec()`
- **Failover logic:** `internal/balancer/handler.go` — `handleChat()` failover block
- **Health FSM:** `internal/balancer/pool.go` — `applyProbe()`, state machine tests
- **Session migration:** `internal/balancer/pool.go` — `pickForSession()` lines 512-533

## Important Notes

⚠️ **Streaming Mid-Token Loss:** Streaming requests that fail mid-response are NOT retried (KV cache is not synchronized). History is preserved; next turn re-routes normally. This is a documented Phase 1 limitation.

⚠️ **In-Memory Sessions:** All session state is in memory with a 30-minute TTL. Loss of the leader gateway means loss of in-memory sessions. This is acceptable for 2-5 node LANs; Phase 3 will address persistence if needed.

⚠️ **Single Retry Only:** We only retry once per request. Multiple retries risk infinite loops. Clients should implement exponential backoff at a higher level.

## Troubleshooting

### tok_per_sec shows 20.0 (not updating)

**Possible causes:**
1. No requests have completed successfully yet (initial value)
2. Worker is not capturing usage (check that `usage.ok` is true)
3. Not sending `Content-Type: application/json` in responses

**Fix:** Send a few test requests; tok_per_sec will update to actual throughput after first success.

### Failover header not appearing

**Possible causes:**
1. Request is streaming (`stream: true`)
2. Headers already written before failure
3. Worker did not actually fail (check logs)

**Expected behavior:** Only non-streaming, pre-header failures trigger failover.

### Sessions not migrating

**Check:**
1. Worker is actually unhealthy: `curl http://127.0.0.1:WORKER_PORT/health`
2. Health FSM updated: `grep "worker.*now" logs/*` (or server stdout)
3. Conversation has an X-Conversation-ID header

**Fix:** Ensure X-Conversation-ID is sent with every request in a conversation.

## Next: Phase 2 Preview

Phase 2 will use this tok_per_sec telemetry to implement:
- Request classification (simple vs. premium)
- Cost-aware routing minimizing latency
- Proof of p95 improvement with mixed workload
