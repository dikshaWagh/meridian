# Phase 1 Testing — Quick Commands

> Copy-paste these commands to test Phase 1 fault tolerance on a real machine

---

## 🚀 Super Quick (2 minutes)

```bash
# Clone and build
git clone https://github.com/dikshaWagh/meridian.git
cd meridian
git pull origin main
make server

# Sanity check (tests only)
go test -race ./internal/balancer -v | tail -5
# Expected: PASS, 31 tests
```

---

## 🔬 Full Test (15 minutes)

### Terminal 1: Start Server

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
```

**Watch for:**
- "worker w1 is now healthy"
- "worker w2 is now healthy"

---

### Terminal 2: Monitor + Kill

```bash
cd /home/rudra/meridian

# Get worker PIDs
STATUS=$(curl -s http://127.0.0.1:8080/status)
W1_PID=$(echo "$STATUS" | jq '.workers[0].pid')
W2_PID=$(echo "$STATUS" | jq '.workers[1].pid')
echo "w1=$W1_PID, w2=$W2_PID"

# Monitor in real-time
watch -n 1 'curl -s http://127.0.0.1:8080/status | jq ".workers[] | {id, state, tok_per_sec, sessions}"'

# (In another window) Kill w2 after ~30 seconds
kill -9 $W2_PID
echo "Killed w2"

# Watch for "unhealthy"
# Expected: w2 transitions from "healthy" to "unhealthy"
```

---

### Terminal 3: Stress Test

```bash
cd /home/rudra/meridian

# Start stress test
./bin/meridian-stress \
  --addr http://127.0.0.1:8080 \
  --concurrency 4 \
  --conversations-per-model 10 &

# Wait 30 seconds, then killer does kill -9
# Wait for test to complete
wait
```

---

### Terminal 2: Check Results

```bash
cd /home/rudra/meridian

# After kill, check immediately
curl http://127.0.0.1:8080/status | jq '.workers[] | {id, state, sessions}'

# Check migrations
tail -f logs/requests.jsonl | grep "session moved" | head -5

# Count successes
echo "Success rate:"
TOTAL=$(grep -c "status=200" logs/requests.jsonl)
FAILED=$(grep "status=[45][0-9][0-9]" logs/requests.jsonl | grep -v "status=409" | wc -l)
echo "Total: $TOTAL, Failed: $FAILED"
[ $((100 * (TOTAL - FAILED) / TOTAL)) -ge 95 ] && echo "✓ PASS" || echo "✗ FAIL"
```

---

## 📊 Expected Results

| Metric | Expected | Command |
|--------|----------|---------|
| Tests pass | 31/31 | `go test -race ./internal/balancer \| grep -c PASS` |
| w2 state after kill | unhealthy | `curl -s http://localhost:8080/status \| jq '.workers[1].state'` |
| Sessions migrated | > 0 | `grep -c "session moved" logs/requests.jsonl` |
| Success rate | ≥ 95% | (from above check) |
| tok_per_sec on w1 | increases | `curl -s http://localhost:8080/status \| jq '.workers[0].tok_per_sec'` |

---

## 🔧 Troubleshooting

### Server won't start
```bash
# Check if llama-server is available
which llama-server
# or
ls -la /usr/local/lib/ollama/llama-server

# Check if port 8080 is free
lsof -i :8080

# Check if model exists
ls -lh ~/models/*.gguf | head -1
```

### Stress test won't run
```bash
# Verify server is responding
curl http://127.0.0.1:8080/status | jq .

# Check log for errors
tail -20 logs/requests.jsonl
```

### No sessions migrating
```bash
# Check if conversations have IDs
tail logs/requests.jsonl | jq '.conversation_id' | head -5

# Should NOT all be null/empty
```

### tok_per_sec not updating
```bash
# Check if responses are 200
tail logs/requests.jsonl | jq '.status_code' | head -5

# Check if tokens are captured
tail logs/requests.jsonl | jq '.actual_completion_tokens' | head -5
```

---

## ⚡ One-Liner Test (Build Only)

```bash
cd /home/rudra/meridian && git pull origin main && make server && go test -race ./internal/balancer -v 2>&1 | tail -5
```

Expected: `PASS ok`

---

## 📝 What to Document

If testing, save:

```bash
# Save logs
mkdir -p ~/meridian-test-results
cp logs/requests.jsonl ~/meridian-test-results/
cp meridian.log ~/meridian-test-results/ 2>/dev/null || true

# Save status
curl http://127.0.0.1:8080/status | jq . > ~/meridian-test-results/final-status.json

# Save test results
go test -race ./internal/balancer -v > ~/meridian-test-results/test-results.txt 2>&1

# Summarize
echo "Test Results:" > ~/meridian-test-results/SUMMARY.txt
echo "Migrations: $(grep -c 'session moved' logs/requests.jsonl)" >> ~/meridian-test-results/SUMMARY.txt
echo "Tests: $(grep -c PASS ~/meridian-test-results/test-results.txt)/31" >> ~/meridian-test-results/SUMMARY.txt
```

---

## 🎯 Success Criteria

- [ ] Build succeeds (`make server`)
- [ ] Tests pass (`go test -race ./internal/balancer` → PASS)
- [ ] Server starts 2 workers (see logs)
- [ ] Stress test runs (see request logs)
- [ ] After kill: w2 becomes "unhealthy"
- [ ] Sessions migrate (see "session moved" in logs)
- [ ] Success rate ≥ 95%

**All boxes checked = Phase 1 works! ✅**

---

## 🚀 Next: Phase 2

Once Phase 1 passes:

```bash
# Read Phase 2 spec
cat docs/03-phase-2-intelligent-workload-allocation.md

# or start implementing:
# - complexity.go (classify simple vs. premium)
# - cost_aware.go (route based on tok_per_sec)
```

---

**Good luck! Report any issues with logs + hardware specs.**
