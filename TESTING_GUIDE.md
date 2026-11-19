# Phase 1 Testing Guide — Real Machine Setup

> **Goal:** Test fault tolerance by killing a worker and observing session migration + failover

---

## Prerequisites

You need:
- Go 1.21+ (`go version`)
- `llama-server` binary (from Ollama or built from llama.cpp)
- A Qwen model file (~2-3GB, e.g., `qwen2.5-1.5b-instruct-q4_k_m.gguf`)
- `jq` for JSON parsing (optional but helpful)
- Multiple CPU cores recommended (workers run in parallel)

### Quick Check

```bash
go version                    # Should show Go 1.21+
which llama-server            # Should show path or "not found"
ls ~/models/*.gguf 2>/dev/null | head -1  # Look for a model file
```

If you don't have `llama-server`, install Ollama from https://ollama.ai

---

## Step 1: Clone/Update Repository

```bash
# Clone (if you don't have it)
git clone https://github.com/dikshaWagh/meridian.git
cd meridian

# Or pull latest changes
git fetch origin
git pull origin main

# Verify Phase 1 files exist
ls -la docs/phase1-results.md PHASE1_IMPLEMENTATION.md
echo "✓ Phase 1 files present"
```

---

## Step 2: Build the Project

```bash
# Clean build
make clean 2>/dev/null || true

# Build both meridian-server and meridian-stress
make server

# Verify binaries exist
ls -lh bin/meridian-server bin/meridian-stress

echo "Build complete ✓"
```

**Expected output:**
```
go build -o bin/meridian-server ./cmd/meridian-server
go build -o bin/meridian-stress ./cmd/meridian-stress
```

---

## Step 3: Prepare Model & Config

### Find or Download a Model

```bash
# Check if you have a model
MODEL_PATH=$(ls ~/models/*.gguf 2>/dev/null | head -1)
if [ -z "$MODEL_PATH" ]; then
  echo "No models found. Download one:"
  echo "  ollama pull qwen2.5:1.5b-instruct-q4"
  exit 1
fi

echo "Using model: $MODEL_PATH"
MODEL_FILE=$(basename "$MODEL_PATH")
echo "Model file: $MODEL_FILE"
```

### Find llama-server

```bash
# Try common locations
for path in /usr/local/lib/ollama/llama-server \
            ~/.ollama/ollama/llama-server \
            /opt/homebrew/opt/ollama/libexec/llama-server \
            $(which llama-server); do
  if [ -x "$path" 2>/dev/null ]; then
    echo "Found llama-server at: $path"
    LLAMA_BIN="$path"
    break
  fi
done

if [ -z "$LLAMA_BIN" ]; then
  echo "llama-server not found. Install Ollama or set LLAMA_BIN"
  exit 1
fi
```

---

## Step 4: Quick Sanity Test (Optional)

Before the full test, verify everything works:

```bash
# Terminal 1: Start server with 1 worker
cd /home/rudra/meridian
./bin/meridian-server serve \
  --enable-llm=true \
  --enable-ml=false \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-workers 1 \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --models-dir ~/models \
  --threads 4 \
  --ctx 2048

# Terminal 2: Send a test request
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen2.5-1.5b-instruct-q4_k_m.gguf",
    "messages": [{"role": "user", "content": "Say hello"}],
    "max_tokens": 50
  }' | jq .

# Terminal 2: Check status
curl http://127.0.0.1:8080/status | jq '.workers[0] | {id, state, tok_per_sec}'
```

If you see a response with `tok_per_sec`, you're ready! Press Ctrl+C to stop.

---

## Step 5: Full Fault Tolerance Test

### Terminal 1: Start Server with 2 Workers

```bash
cd /home/rudra/meridian

./bin/meridian-server serve \
  --enable-llm=true \
  --enable-ml=false \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --models-dir ~/models \
  --threads 8 \
  --ctx 4096

# Watch logs for worker startup
# You should see:
# - "Starting workers..."
# - "worker w1 (...) is now starting"
# - "worker w2 (...) is now starting"
# - After ~10s: "worker w1 (...) is now healthy"
# - After ~10s: "worker w2 (...) is now healthy"

# Keep this terminal open and watch it during the test
```

### Terminal 2: Get Worker PIDs and Check Status

```bash
cd /home/rudra/meridian

# Get current status
curl http://127.0.0.1:8080/status | jq .

# Save it for reference
STATUS=$(curl -s http://127.0.0.1:8080/status)
W1_PID=$(echo "$STATUS" | jq '.workers[0].pid')
W2_PID=$(echo "$STATUS" | jq '.workers[1].pid')

echo "Worker 1 PID: $W1_PID"
echo "Worker 2 PID: $W2_PID"

# Watch tok_per_sec (should be ~20 initially)
curl http://127.0.0.1:8080/status | jq '.workers[] | {id, state, tok_per_sec, in_flight}'
```

### Terminal 3: Start Stress Test (Concurrent Conversations)

```bash
cd /home/rudra/meridian

# Start stress test with 4 concurrent connections, 10 conversations each
./bin/meridian-stress \
  --addr http://127.0.0.1:8080 \
  --concurrency 4 \
  --conversations-per-model 10 &

STRESS_PID=$!
echo "Stress test started (PID: $STRESS_PID)"

# Let it run for ~30 seconds to build up conversations
sleep 30

# Check how many are active
jobs -l
```

**Expected output:**
```
model=qwen2.5-1.5b-instruct-q4_k_m.gguf worker=w1 status=200 time=...
model=qwen2.5-1.5b-instruct-q4_k_m.gguf worker=w1 status=200 time=...
model=qwen2.5-1.5b-instruct-q4_k_m.gguf worker=w2 status=200 time=...
```

### Terminal 2: Monitor Status Before Kill

```bash
# Watch workers and sessions before kill
watch -n 1 'curl -s http://127.0.0.1:8080/status | jq ".workers[] | {id, state, in_flight, tok_per_sec, sessions}"'

# In another Terminal 2 window, tail logs
tail -f logs/requests.jsonl | grep -E "session|worker"
```

### Terminal 2: Kill Worker 2 (The Critical Moment)

```bash
# Get the PID of worker 2 (from earlier output)
# Example: if W2_PID=12345, then:
kill -9 12345

echo "Killed worker 2 (PID: 12345)"

# Watch Terminal 1 for this log:
# "worker w2 (...) is now unhealthy"
```

### Terminal 2: Observe Recovery (Key Tests)

```bash
# Immediately check status (w2 should be unhealthy)
curl http://127.0.0.1:8080/status | jq .

# Expected:
# {
#   "workers": [
#     {"id": "w1", "state": "healthy", "sessions": 10, "tok_per_sec": 45.2},
#     {"id": "w2", "state": "unhealthy", "sessions": 0, "tok_per_sec": 0}
#   ],
#   "sessionCount": 10
# }

# Watch for session migrations in logs
sleep 2
tail -n 20 logs/requests.jsonl | grep "session moved"

# Expected output:
# session conv-abc123 moved w2 -> w1 (reason: worker process exited)
# session conv-def456 moved w2 -> w1 (reason: worker process exited)
```

### Terminal 3: Check Stress Test Status

```bash
# Is the stress test still running?
jobs -l

# If still running, let it complete or wait for it
wait $STRESS_PID 2>/dev/null

# Get final results
echo "Stress test completed"
```

### Terminal 2: Analyze Results

```bash
# Count successful vs failed requests
echo "=== Results ==="

# Total requests
TOTAL=$(grep -c "status=200" logs/requests.jsonl)
echo "Total successful: $TOTAL"

# Session migrations
MIGRATED=$(grep -c "session moved" logs/requests.jsonl)
echo "Sessions migrated: $MIGRATED"

# Failures (if any)
FAILURES=$(grep "status=[45][0-9][0-9]" logs/requests.jsonl | grep -v "status=409" | wc -l)
echo "Real failures: $FAILURES"

# Success rate (non-streaming)
if [ $TOTAL -gt 0 ]; then
  SUCCESS_RATE=$((100 * (TOTAL - FAILURES) / TOTAL))
  echo "Success rate: $SUCCESS_RATE%"
  
  if [ $SUCCESS_RATE -ge 95 ]; then
    echo "✓ PASS: Success rate >= 95%"
  else
    echo "✗ FAIL: Success rate < 95%"
  fi
fi

# Check tok_per_sec increased
echo ""
echo "=== Token/s Telemetry ==="
curl http://127.0.0.1:8080/status | jq '.workers[] | {id, state, tok_per_sec, served}'
```

---

## Step 6: Verify Expected Behaviors

### Expected Results

```bash
# ✓ PASS if ALL these are true:

1. Worker transitions to unhealthy
   curl http://127.0.0.1:8080/status | jq '.workers[] | select(.id=="w2") | .state'
   # Should output: "unhealthy"

2. Sessions migrate to healthy worker
   grep "session moved" logs/requests.jsonl | wc -l
   # Should be > 0

3. tok_per_sec increases on w1 (load shifted)
   curl http://127.0.0.1:8080/status | jq '.workers[0].tok_per_sec'
   # Should increase from stress load

4. Non-streaming success >= 95%
   # From earlier calculation

5. Failover header appears (optional)
   grep "X-Meridian-Failover" logs/requests.jsonl | head -1
   # Should show: w2->w1 or similar
```

---

## Step 7: Run Unit Tests

```bash
cd /home/rudra/meridian

# Run all balancer tests with race detector
go test -race ./internal/balancer -v

# Expected output:
# === RUN   TestAtomicReservation
# --- PASS: TestAtomicReservation (0.00s)
# === RUN   TestTokPerSecEWMA
# --- PASS: TestTokPerSecEWMA (0.00s)
# === RUN   TestAcceptance*
# --- PASS: TestAcceptance* (...)
# PASS
# ok    github.com/dikshaWagh/meridian/internal/balancer    1.199s
```

### If Tests Fail

```bash
# Run with verbose output
go test -race ./internal/balancer -v -run TestAtomicReservation

# Check for race detector warnings (should be none)
go test -race ./internal/balancer 2>&1 | grep "DATA RACE"

# Run linter
go vet ./internal/balancer
```

---

## Complete Test Script (Copy-Paste Ready)

Save this as `test-phase1.sh`:

```bash
#!/bin/bash
set -e

cd /home/rudra/meridian

echo "╔════════════════════════════════════════════════════════════════╗"
echo "║          PHASE 1 TESTING — FAULT TOLERANCE                    ║"
echo "╚════════════════════════════════════════════════════════════════╝"
echo ""

# Step 1: Build
echo "[1/4] Building..."
make server > /dev/null 2>&1
echo "✓ Build complete"

# Step 2: Find model and llama-server
echo "[2/4] Finding model and llama-server..."
MODEL=$(ls ~/models/*.gguf 2>/dev/null | head -1)
if [ -z "$MODEL" ]; then
  echo "✗ No model found in ~/models/"
  exit 1
fi
MODEL_FILE=$(basename "$MODEL")
echo "✓ Model: $MODEL_FILE"

LLAMA_BIN="/usr/local/lib/ollama/llama-server"
if [ ! -x "$LLAMA_BIN" ]; then
  LLAMA_BIN=$(which llama-server 2>/dev/null || true)
  if [ -z "$LLAMA_BIN" ]; then
    echo "✗ llama-server not found"
    exit 1
  fi
fi
echo "✓ llama-server: $LLAMA_BIN"

# Step 3: Run unit tests
echo "[3/4] Running unit tests..."
go test -race ./internal/balancer -v > /tmp/test-output.txt 2>&1
TEST_PASS=$(grep -c "PASS" /tmp/test-output.txt || echo 0)
TEST_FAIL=$(grep -c "FAIL" /tmp/test-output.txt || echo 0)
echo "✓ Tests: $TEST_PASS pass, $TEST_FAIL fail"
if [ $TEST_FAIL -gt 0 ]; then
  cat /tmp/test-output.txt
  exit 1
fi

# Step 4: Instructions for manual testing
echo "[4/4] Manual testing instructions..."
echo ""
echo "Run these in separate terminals:"
echo ""
echo "Terminal 1 (server):"
echo "  ./bin/meridian-server serve \\"
echo "    --enable-llm=true --enable-ml=false \\"
echo "    --llm-model $MODEL_FILE \\"
echo "    --llm-workers 2 \\"
echo "    --llama-bin $LLAMA_BIN \\"
echo "    --models-dir ~/models \\"
echo "    --threads 8 --ctx 4096"
echo ""
echo "Terminal 2 (monitor):"
echo "  watch -n 1 'curl -s http://127.0.0.1:8080/status | jq .'"
echo ""
echo "Terminal 3 (stress):"
echo "  ./bin/meridian-stress --addr http://127.0.0.1:8080 --concurrency 4 --conversations-per-model 10 &"
echo ""
echo "Terminal 2 (kill worker):"
echo "  kill -9 <PID_of_w2>"
echo "  sleep 2"
echo "  tail logs/requests.jsonl | grep 'session moved'"
echo ""
echo "✓ All checks passed! Ready for manual testing."
```

Make it executable and run:

```bash
chmod +x test-phase1.sh
./test-phase1.sh
```

---

## Interpreting Results

### ✅ PASS Indicators

- Server starts both workers (see "is now healthy")
- Stress test runs continuously (log messages flowing)
- After kill: worker transitions to "unhealthy"
- Sessions appear in logs: "session moved w2 -> w1"
- `tok_per_sec` increases on w1 (load shifted)
- Non-streaming success rate >= 95%

### ❌ FAIL Indicators

- Worker never becomes "healthy" (restart issue)
- Stress test stops after kill (no failover)
- No "session moved" logs (migration broken)
- Success rate < 95% (too many failures)
- tok_per_sec stays at 20.0 (telemetry not working)

---

## Troubleshooting

### llama-server Not Starting

```bash
# Check if port is free
lsof -i :8001 :8002 2>/dev/null | grep LISTEN

# Try manually starting llama-server
/usr/local/lib/ollama/llama-server \
  -m ~/models/qwen2.5-1.5b-instruct-q4_k_m.gguf \
  -ngl 0 \
  -t 4 \
  -c 4096 \
  --host 127.0.0.1 \
  --port 8001 \
  -np 1 \
  -e

# Should show: "Server is listening"
```

### Stress Test Not Running

```bash
# Check if meridian-server is responding
curl http://127.0.0.1:8080/status

# If no response, server may have crashed
# Check logs in Terminal 1
```

### No Sessions Migrating

```bash
# Verify conversation IDs are being sent
tail -f logs/requests.jsonl | jq '.conversation_id' | head -5

# If all null, stress test not sending X-Conversation-ID
# Check stress test implementation
```

### tok_per_sec Not Updating

```bash
# Check if responses are 200 OK
tail -f logs/requests.jsonl | jq '.status_code' | head -5

# Check if usage is captured
tail -f logs/requests.jsonl | jq '.actual_prompt_tokens' | head -5

# If 0 or null, captureUsage() might not be working
```

---

## Performance Expectations

### Typical Numbers (Qwen 1.5B, 4 threads, CPU-only)

| Metric | Expected |
|--------|----------|
| `tok_per_sec` | 20-50 (varies by CPU) |
| Latency per request | 2-5 seconds |
| Stress test progress | 1-2 requests/second |
| Session migration time | <100ms |
| Worker death detection | 1-2 seconds |

### If Performance is Poor

```bash
# Try with fewer threads
--threads 2

# Try with smaller context
--ctx 2048

# Try with smaller model
qwen2.5-0.5b instead of qwen2.5-1.5b

# Use GPU (if available)
# Add --gpu-layers 10 to llama-server command
```

---

## Next Steps After Testing

### If ✅ All Tests Pass

1. Review the documentation
   ```bash
   cat README_PHASE1.md
   ```

2. Prepare for viva
   ```bash
   cat PHASE1_TECHNICAL_DETAILS.md  # Talking points
   ```

3. Move to Phase 2
   ```bash
   cat docs/03-phase-2-intelligent-workload-allocation.md
   ```

### If ❌ Tests Fail

1. Check logs
   ```bash
   tail -n 50 logs/requests.jsonl
   ```

2. Run unit tests with output
   ```bash
   go test -race ./internal/balancer -v 2>&1 | tee test-output.txt
   ```

3. Report issue with:
   - Full test output
   - Hardware specs (CPU cores, RAM)
   - Model used
   - Worker count

---

## Final Checklist

```
Before testing:
  [ ] Git pull latest
  [ ] make server builds
  [ ] Model file exists (~2GB)
  [ ] llama-server binary found
  [ ] Port 8080 is free

During testing:
  [ ] Server starts 2 workers
  [ ] Stress test runs for 30s
  [ ] Kill worker 2
  [ ] Sessions migrate
  [ ] Stress test continues
  [ ] Success rate >= 95%

After testing:
  [ ] Run: go test -race ./internal/balancer (pass)
  [ ] Check: logs/requests.jsonl (has "session moved")
  [ ] Verify: curl status shows tok_per_sec
  [ ] Document: any issues or unexpected behavior
```

---

**You're ready! Start with Terminal 1 and follow the commands above.**
