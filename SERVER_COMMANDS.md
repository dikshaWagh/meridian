# Server Commands — Network-Accessible Setup

> Copy-paste commands to run Meridian server accessible from client machines

---

## 🖥️ SERVER MACHINE SETUP

### Step 1: Build (One Time)

```bash
cd /home/rudra/meridian
git pull origin main
make server

# Verify
ls -lh bin/meridian-server
```

### Step 2: Get Server IP (Give This to Client)

```bash
# Your server's IP address (clients will use this)
hostname -I

# Example output: 192.168.1.100 or 10.0.0.50
# Save this!
```

### Step 3: Start Server (Network Accessible)

**Option A: Foreground (Watch Logs in Real-Time)**

```bash
cd /home/rudra/meridian

./bin/meridian-server serve \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --models-dir ~/models \
  --threads 8 \
  --ctx 4096

# Watch for:
# 2026/11/19 20:15:00 Starting workers...
# 2026/11/19 20:15:10 worker w1 (...) is now healthy
# 2026/11/19 20:15:10 worker w2 (...) is now healthy
#
# Then server is READY
# Keep this terminal open!
```

**Option B: Background (With Log File)**

```bash
cd /home/rudra/meridian

# Start in background
nohup ./bin/meridian-server serve \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --models-dir ~/models \
  --threads 8 \
  --ctx 4096 \
  > server.log 2>&1 &

# Save PID
SERVER_PID=$!
echo "Server started with PID: $SERVER_PID"

# Watch logs
tail -f server.log

# Or check status
sleep 5 && curl http://127.0.0.1:8080/status | jq '.workers[].state'
```

### Step 4: Verify Server is Ready (From Server Machine)

```bash
# Check health
curl http://localhost:8080/healthz | jq .

# Check workers
curl http://localhost:8080/status | jq '.workers[] | {id, state}'

# Should show:
# {"id": "w1", "state": "healthy"}
# {"id": "w2", "state": "healthy"}
```

---

## 💻 CLIENT MACHINE SETUP

### Step 1: From Client, Verify Connectivity

Replace `SERVER_IP` with the IP you got from server (e.g., `192.168.1.100`):

```bash
# Verify server is reachable
ping <SERVER_IP>

# Test API
curl http://<SERVER_IP>:8080/healthz | jq .

# Check workers
curl http://<SERVER_IP>:8080/status | jq '.workers[] | {id, state}'
```

### Step 2: Open React UI in Browser

**Option A: Static UI (Embedded in Server)**

```bash
# Just open browser and go to:
http://<SERVER_IP>:8080/

# Example:
# http://192.168.1.100:8080/
```

**Option B: Dev Server with Live Reload (if on same machine as code)**

```bash
cd /home/rudra/meridian/web

npm install
npm run dev

# Opens: http://localhost:5173/

# But you'll need to configure API proxy to point to server
# Edit: web/vite.config.ts
# Change: proxy target to http://<SERVER_IP>:8080
```

---

## 🔥 FAULT TOLERANCE TEST

### Terminal on Server: Create Load

```bash
cd /home/rudra/meridian

# Run stress test in background
./bin/meridian-stress \
  --addr http://127.0.0.1:8080 \
  --concurrency 4 \
  --conversations-per-model 10 &

echo "Stress test started (PID: $!)"

# Let it run for ~30 seconds to build conversations
# Watch React UI on client for load distribution
sleep 30
```

### Browser on Client: Watch React UI

```
Expected to see in Status Monitor (right panel):
  w1: healthy, tok_per_sec: 42.3, sessions: 5
  w2: healthy, tok_per_sec: 39.7, sessions: 5
  
Recent requests shown in log below
```

### Terminal on Server: Kill Worker 2

```bash
# Find llama-server processes (the workers)
ps aux | grep llama-server | grep -v grep

# You'll see output like:
#   user 12345 ... llama-server ... (this is w1)
#   user 12346 ... llama-server ... (this is w2)

# Kill the second one (w2)
kill -9 12346

echo "✓ Killed worker 2"
```

### Browser on Client: Watch Recovery

**Status Monitor should update to show:**

```
Before kill:
  w1: healthy, tok_per_sec: 42.3, sessions: 5
  w2: healthy, tok_per_sec: 39.7, sessions: 5

After kill (~2 seconds):
  w1: healthy, tok_per_sec: 45.8, sessions: 10 ← increased!
  w2: unhealthy, tok_per_sec: 0, sessions: 0    ← now RED
```

**What to verify:**

- ✅ w2 turns RED (unhealthy)
- ✅ w1 sessions increase (5 → 10)
- ✅ w1 tok_per_sec increases (more load)
- ✅ Any messages you send still work
- ✅ No error messages in browser console

### Terminal on Server: Check Logs

```bash
# Stop stress test if still running
killall meridian-stress 2>/dev/null || true

# Check migration logs
tail -20 logs/requests.jsonl | grep "session moved"

# Expected output:
# session conv-abc123 moved w2 -> w1 (reason: worker process exited)
# session conv-def456 moved w2 -> w1 (reason: worker process exited)

# Count success rate
TOTAL=$(grep -c "status=200" logs/requests.jsonl)
FAILURES=$(grep "status=[45][0-9][0-9]" logs/requests.jsonl | grep -v "status=409" | wc -l)
echo "Success rate: $((100 * (TOTAL - FAILURES) / TOTAL))%"
# Should be >= 95%
```

---

## 🔧 TROUBLESHOOTING

### Server Won't Start

```bash
# Check if port 8080 is already in use
lsof -i :8080

# If yes, kill the process
kill -9 <PID>

# Check if model exists
ls -lh ~/models/qwen2.5*.gguf

# Check if llama-server binary exists
which llama-server
```

### Client Can't Connect to Server

```bash
# From client, verify connectivity
ping <SERVER_IP>

# If ping fails: firewall or network issue
# On server, open port 8080:
sudo ufw allow 8080
sudo firewall-cmd --add-port=8080/tcp --permanent

# Test connectivity again
curl http://<SERVER_IP>:8080/healthz
```

### React UI Shows Workers as Unhealthy

```bash
# Workers startup takes ~10 seconds
# Wait 15 seconds after starting server

# If still unhealthy, check server logs:
# Terminal 1 where you started meridian-server

# Should show:
# worker w1 (...) is now healthy
# worker w2 (...) is now healthy
```

### Stress Test Doesn't Create Load

```bash
# Try one manual request
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen2.5-1.5b-instruct-q4_k_m.gguf",
    "messages": [{"role": "user", "content": "Hi"}],
    "max_tokens": 50
  }'

# If that works, stress test should too
```

---

## 📊 MONITORING COMMANDS (While Running)

### From Server Machine

```bash
# Watch status in real-time (updates every 2 seconds)
watch -n 2 'curl -s http://127.0.0.1:8080/status | jq ".workers[] | {id, state, tok_per_sec, sessions}"'

# Watch logs
tail -f logs/requests.jsonl

# Watch system resources
top
# Look for llama-server processes
```

### From Client Machine

```bash
# Check server status
curl http://<SERVER_IP>:8080/status | jq .

# Get just worker states
curl http://<SERVER_IP>:8080/status | jq '.workers[] | {id, state, sessions}'

# Get telemetry
curl http://<SERVER_IP>:8080/status | jq '.workers[] | {id, tok_per_sec, served, in_flight}'
```

---

## 📋 COMPLETE TIMELINE

### T=0: Start Server (Server Machine)

```bash
./bin/meridian-server serve \
  --enable-llm=true --enable-ml=false \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --models-dir ~/models \
  --threads 8 --ctx 4096 \
  --bind-addr 0.0.0.0
```

### T=10s: Get Server IP

```bash
hostname -I  # Save this
```

### T=15s: Open React UI (Client Machine)

```bash
# In browser: http://<SERVER_IP>:8080/
# Verify both workers are healthy (green)
```

### T=20s: Send Test Messages (Client Machine)

```bash
# In React UI:
# 1. Check "Conversation ID" checkbox
# 2. Type: "Hello!"
# 3. Click Send
# 4. Verify response appears and tok_per_sec increases
```

### T=30s: Start Stress Test (Server Machine, Terminal 2)

```bash
./bin/meridian-stress \
  --addr http://127.0.0.1:8080 \
  --concurrency 4 \
  --conversations-per-model 10 &
```

### T=40s: Monitor Load (Client Machine, Browser)

```
Watch Status Monitor on right:
  Sessions increase
  tok_per_sec increases
  Requests flowing in
```

### T=60s: Kill Worker 2 (Server Machine, Terminal 3)

```bash
ps aux | grep llama-server | grep -v grep
kill -9 <PID_of_w2>
```

### T=62s: Observe Recovery (Client Machine, Browser)

```
Status Monitor updates:
  w2 turns RED
  w1 sessions double
  w1 tok_per_sec increases
  Requests continue
```

### T=90s: Check Results (Server Machine)

```bash
killall meridian-stress
grep "session moved" logs/requests.jsonl | wc -l  # Should be > 0
SUCCESS=$((100 * ($(grep -c "status=200" logs/requests.jsonl) - $(grep "status=[45][0-9][0-9]" logs/requests.jsonl | grep -v "status=409" | wc -l)) / $(grep -c "status=200" logs/requests.jsonl)))
echo "Success: $SUCCESS%"  # Should be >= 95%
```

---

## ✅ SUCCESS CHECKLIST

- [ ] Server builds without errors
- [ ] Server starts and both workers become "healthy"
- [ ] Client can reach server: `curl http://<SERVER_IP>:8080/healthz`
- [ ] React UI opens in browser and shows both workers healthy
- [ ] Send message in React UI, response appears
- [ ] Status monitor shows tok_per_sec > 20
- [ ] Stress test runs and creates load
- [ ] Status monitor shows load distributed across workers
- [ ] After kill: w2 shows unhealthy (RED)
- [ ] After kill: w1 sessions increase
- [ ] After kill: existing conversations still work
- [ ] Logs show "session moved" messages
- [ ] Success rate >= 95%

**If all ✓, Phase 1 Fault Tolerance works! 🎉**

---

## Next Steps

1. **Observe the UI** — Send messages, watch real-time updates
2. **Test Failover** — See sessions migrate when worker dies
3. **Review Results** — Check logs for success rate
4. **Move to Phase 2** — Implement cost-aware routing using tok_per_sec

---

**Good luck! Questions? Check SERVER_CLIENT_SETUP.md for detailed explanations.**
