# Server-Client Setup — React UI + Fault Tolerance Testing

> Run Meridian server on one machine, access React UI from another client machine, then test fault tolerance

---

## Architecture

```
┌──────────────────────────────────┐
│   CLIENT MACHINE (Laptop)        │
│                                  │
│  • Browser (http://server:8080)  │
│  • React UI Dashboard            │
│  • Send chat/OCR requests        │
│  • Monitor status in real-time   │
└──────────────────────────────────┘
          ↑ HTTP ↓
    (network connection)
          ↑ HTTP ↓
┌──────────────────────────────────┐
│   SERVER MACHINE (Desktop/Lab)   │
│                                  │
│  • meridian-server               │
│  • 2x llama-server workers       │
│  • Log output to stdout/file     │
│  • Process management            │
└──────────────────────────────────┘
```

---

## Part 1: Server Setup (On Server Machine)

### Step 1: Build on Server

```bash
cd /home/rudra/meridian

# Update code
git pull origin main

# Build
make server

# Verify binaries exist
ls -lh bin/meridian-server bin/meridian-stress
```

### Step 2: Configure Network Access

By default, meridian-server listens on `127.0.0.1:8080` (localhost only).
To access from another machine, we need to bind to all interfaces.

**Create a config file** (optional but recommended):

```bash
cat > config-server.yaml << 'EOF'
# Meridian Server Config - Network Accessible
server:
  bind_addr: "0.0.0.0"  # Listen on all interfaces
  port: 8080
  enable_llm: true
  enable_ml: false
  
llm:
  model: "qwen2.5-1.5b-instruct-q4_k_m.gguf"
  workers: 2
  llama_bin: "/usr/local/lib/ollama/llama-server"
  models_dir: "/home/user/models"
  threads: 8
  ctx: 4096
EOF
```

However, the current server uses CLI flags. We'll pass them directly.

### Step 3: Find Server Machine's IP Address

```bash
# Get the server's IP address (for client to connect to)
hostname -I
# or
ip addr | grep "inet " | grep -v 127.0.0.1

# Example output: 192.168.1.100
# Remember this for the client
```

Save this IP address - you'll give it to the client machine.

### Step 4: Start Server (Bound to All Interfaces)

**Option A: Direct Command (Recommended for Testing)**

```bash
cd /home/rudra/meridian

# Start server (already listens on 0.0.0.0:8080 by default)
./bin/meridian-server serve \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --models-dir ~/models \
  --threads 8 \
  --ctx 4096

# Server is now listening on 0.0.0.0:8080 (all interfaces)
# Expected output:
# 2026/11/19 20:15:00 Starting workers...
# 2026/11/19 20:15:01 worker w1 (...) is now starting
# 2026/11/19 20:15:01 worker w2 (...) is now starting
# 2026/11/19 20:15:10 worker w1 (...) is now healthy
# 2026/11/19 20:15:10 worker w2 (...) is now healthy
# Keep this terminal open!
```

**Option B: Detached (Run in Background)**

```bash
cd /home/rudra/meridian

# Run in background with output to file
nohup ./bin/meridian-server serve \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --models-dir ~/models \
  --threads 8 \
  --ctx 4096 \
  > server.log 2>&1 &

# Get the PID
SPID=$!
echo "Server started with PID: $SPID"

# Verify it's running
sleep 3
ps aux | grep meridian-server | grep -v grep

# Watch logs
tail -f server.log
```

### Step 5: Verify Server is Ready

From the server machine:

```bash
# Check if server is responding
curl http://localhost:8080/healthz | jq .
# Expected: {"status": "ok"}

# Check status (workers should be healthy)
curl http://localhost:8080/status | jq .workers[].state
# Expected: "healthy" and "healthy"

# Check available models
curl http://localhost:8080/v1/models | jq .data[].id
# Expected: qwen2.5-1.5b-instruct-q4_k_m.gguf
```

**Keep this server running. Move to the client machine.**

---

## Part 2: Client Setup (On Client Machine)

### Step 1: Get Server Address from Client

From the server machine (Terminal/SSH), get the IP:

```bash
hostname -I
# Example: 192.168.1.100 or 10.0.0.50
```

**From client machine**, test connectivity:

```bash
# Replace with actual server IP
SERVER_IP="192.168.1.100"

# Test if server is reachable
ping $SERVER_IP
# Should see replies

# Test if port 8080 is open
nc -zv $SERVER_IP 8080
# Expected: Connection successful

# Test API
curl http://$SERVER_IP:8080/healthz
# Expected: {"status": "ok"}
```

### Step 2: Build React UI on Client (Optional)

If you want to rebuild the UI:

```bash
cd /home/rudra/meridian

# Build web UI
npm install --prefix web
npm run build --prefix web

# Creates: web/dist/

# Or just use prebuilt version
ls -la web/dist/
```

### Step 3: Open React Dashboard in Browser

From the client machine:

**Option A: If Running Server Locally (Dev Mode)**

```bash
# Terminal 1: Start Vite dev server (with live reload)
cd /home/rudra/meridian/web
npm install
npm run dev

# Output will show:
#   VITE v5.0.0 ready in XXX ms
#   ➜ Local:   http://localhost:5173/

# Open browser to http://localhost:5173/
```

**Option B: Access Via Server (Production Mode)**

```bash
# Just open browser and navigate to:
http://192.168.1.100:8080/

# This serves the built React UI from the server's /webui endpoint
# Static files are embedded in meridian-server binary
```

### Step 4: Explore React UI

Once browser opens, you should see:

```
┌─────────────────────────────────────┐
│  Meridian AI Dashboard              │
├─────────────────────────────────────┤
│ Chat Interface    │  Status Monitor │
│                   │  ├─ w1: healthy │
│ • Model selector  │  ├─ w2: healthy │
│ • Conversation ID │  ├─ tok_per_sec │
│ • Message input   │  └─ sessions    │
│                   │                 │
│ OCR Interface     │  Request Log    │
│ • Image upload    │  (recent requests)
│ • Extract text    │
└─────────────────────────────────────┘
```

**Key UI Elements:**

1. **Model Selector** (top left)
   - Should show: `qwen2.5-1.5b-instruct-q4_k_m.gguf`
   - Select to choose which model

2. **Worker Status** (right panel)
   - w1: healthy (green)
   - w2: healthy (green)
   - `tok_per_sec`: ~20-50 initially

3. **Chat Interface**
   - Type a message in the input box
   - Check "Conversation ID" to persist history
   - Click Send

4. **Status Monitor** (bottom right)
   - Refreshes every ~2 seconds
   - Shows in-flight requests
   - Shows sessions per worker

---

## Part 3: Test React UI (Before Fault Testing)

### Test 1: Send a Simple Chat

**In React UI:**

1. **Verify Server Connection**
   - Check Worker Status box (right)
   - Both w1 and w2 should show "healthy"
   - If red/unhealthy, server not responding

2. **Select Conversation**
   - Check the "Conversation ID" checkbox
   - It auto-generates one (e.g., `conv-abc123`)
   - This persists history across requests

3. **Send First Message**
   - Type: `Hello, what is 2+2?`
   - Click Send
   - Watch Status Monitor on right
   - Should see `in_flight: 1` momentarily
   - Response appears in chat

4. **Verify Telemetry**
   - Status Monitor should show:
     - `tok_per_sec: 35.2` (or similar, not 20.0 anymore)
     - `sessions: 1` (on one of the workers)
     - `served: 1` (count increased)

5. **Send Follow-up Message**
   - Type: `What about 5+5?`
   - Click Send
   - Watch for same worker (session sticky)
   - Response should refer to previous context (if model supports it)

### Test 2: Multi-Turn Conversation

**In React UI:**

1. Keep same Conversation ID
2. Send 3-4 messages in sequence
3. Watch Status Monitor:
   - `sessions` should stay on same worker
   - `tok_per_sec` should increase (averaging actual throughput)
   - `served` count increases

4. Verify history in chat UI

### Test 3: Parallel Conversations

**In React UI:**

1. **Open Two Browser Tabs**
   - Tab 1: Keep Conversation ID `conv-first`
   - Tab 2: Uncheck Conversation ID (or generate different one)

2. **Send in Both Tabs**
   - Tab 1 message, then Tab 2 message
   - Watch Status Monitor:
     - `in_flight: 2` (briefly)
     - `sessions: 1` on w1, `sessions: 1` on w2 (distributed)

3. **Verify Load Distribution**
   - `tok_per_sec` on both workers should increase

---

## Part 4: Fault Tolerance Test (The Main Event)

### Prerequisites

- Server is running on remote machine (healthy, both workers)
- React UI is open in browser on client machine
- Stress tool will run on server machine (or create workload via React UI)

### Step 1: Baseline Before Kill

**In React UI:**

```bash
Status should show:
  w1: healthy, tok_per_sec: 35.2, sessions: 0
  w2: healthy, tok_per_sec: 41.5, sessions: 0
```

Record this screenshot or note the numbers.

### Step 2: Create Load (Generate Conversations)

**Option A: Via React UI (Manual)**

```bash
# In browser, on a single Conversation ID:
1. Type message: "Write a 50-word story about a robot"
2. Click Send
3. While waiting, open new tab with different Conversation ID
4. Repeat steps 1-2 in new tab
5. Switch between tabs, create more conversations
```

**Option B: Via Server Stress Tool (Automated)**

From the **server machine** (in a new terminal while server runs):

```bash
cd /home/rudra/meridian

# Run stress test in background
./bin/meridian-stress \
  --addr http://127.0.0.1:8080 \
  --concurrency 4 \
  --conversations-per-model 10 &

# This creates 10 conversations with 4 concurrent requests
# Watch React UI on client to see load

# Let it run for ~30 seconds to build up conversations
sleep 30
```

### Step 3: Monitor on React UI While Loaded

**In React UI (client browser):**

```
Expected to see (Status Monitor updates every ~2s):
  w1: healthy, tok_per_sec: 42.1, sessions: 5
  w2: healthy, tok_per_sec: 39.8, sessions: 5
  
  Request Log shows recent requests:
    [timestamp] model=... worker=w1 status=200 time=2500ms
    [timestamp] model=... worker=w2 status=200 time=2400ms
```

### Step 4: Kill Worker 2 (Server Machine)

**On server machine** (in new terminal):

```bash
# Find meridian-server process
ps aux | grep meridian-server

# Note: meridian-server spawns child llama-server processes
# We want to kill ONE of the llama-server worker processes (not the main gateway)

# List all processes
ps aux | grep llama-server

# Example output:
#   user 12345 ... llama-server -m ... (w1 on port 8001)
#   user 12346 ... llama-server -m ... (w2 on port 8002)

# Kill w2 (second llama-server)
kill -9 12346

echo "✓ Killed w2 (PID 12346)"
```

**Alternative: Kill by Port**

```bash
# Find which process is listening on port 8002 (w2)
lsof -i :8002

# Kill it
kill -9 <PID>
```

### Step 5: Watch React UI for Recovery

**Immediately in React UI (client browser):**

Watch the Status Monitor (should update in real-time):

**Before kill:**
```
w1: healthy, tok_per_sec: 42.1, sessions: 5
w2: healthy, tok_per_sec: 39.8, sessions: 5
```

**After kill (~2 seconds):**
```
w1: healthy, tok_per_sec: 45.2, sessions: 10  ← Load shifted!
w2: unhealthy, tok_per_sec: 0, sessions: 0     ← Now unhealthy
```

**Expected UI Changes:**

1. w2 worker box turns RED ❌
2. w2 shows `state: unhealthy`
3. w1 shows increased `sessions` count (absorbed all sessions)
4. w1's `tok_per_sec` increases (more load)
5. Any in-flight requests on w2 were retried on w1

### Step 6: Verify Session Migration in React UI

**In React UI:**

1. **Look at Request Log**
   - Should show recent requests are all on w1
   - Any requests that were on w2 are now on w1

2. **Try Sending New Message**
   - Use a conversation that was on w2
   - Should still work (routed to w1)
   - Message appears in UI

3. **Check Status**
   - Requests continue to be processed
   - No error messages in browser console

### Step 7: Complete the Test

**On server machine:**

Let the stress test finish (or stop it):

```bash
# If still running
killall meridian-stress

# Check logs
tail -50 logs/requests.jsonl
```

**Verify Results:**

```bash
# Count successful requests
TOTAL=$(grep -c "status=200" logs/requests.jsonl)

# Count failures (excluding 409 which is concurrent)
FAILURES=$(grep "status=[45][0-9][0-9]" logs/requests.jsonl | grep -v "status=409" | wc -l)

# Calculate success rate
SUCCESS=$((100 * (TOTAL - FAILURES) / TOTAL))

echo "Total requests: $TOTAL"
echo "Failures: $FAILURES"
echo "Success rate: $SUCCESS%"

# Should be >= 95%

# Check migrations
grep "session moved" logs/requests.jsonl | wc -l
```

---

## Summary: What to Expect

### React UI Before Kill
```
✓ Chat works (messages appear)
✓ Two workers both healthy (green)
✓ Status updates in real-time
✓ tok_per_sec shows actual throughput
✓ Conversations sticky (same worker)
```

### React UI After Killing w2
```
✓ w2 immediately turns red (unhealthy)
✓ w1 sessions count increases (load shifted)
✓ Existing conversations keep working
✓ New messages route to w1
✓ Chat continues seamlessly
✓ No error messages in browser
```

### Server Logs Confirm
```
session conv-abc123 moved w2 -> w1 (reason: worker process exited)
session conv-def456 moved w2 -> w1 (reason: worker process exited)
worker w2 (...) is now unhealthy
```

---

## Troubleshooting

### React UI Won't Connect

```bash
# From client, verify server is reachable
curl http://192.168.1.100:8080/healthz

# If fails:
# 1. Check server is running: ps aux | grep meridian-server
# 2. Check firewall: sudo ufw allow 8080
# 3. Check IP is correct: hostname -I (on server)
```

### Status Monitor Not Updating

```bash
# Check if requests are being sent
tail -f logs/requests.jsonl

# If nothing, server may not be processing requests
# Verify workers are healthy: curl http://server:8080/status | jq
```

### Workers Never Become Healthy

```bash
# On server, check if llama-server is running
ps aux | grep llama-server

# Check logs for startup errors
tail -50 server.log (if using nohup)

# Verify model file exists
ls -lh ~/models/qwen*.gguf
```

### Stress Test Doesn't Generate Load

```bash
# Check if meridian-stress binary exists
./bin/meridian-stress --help

# Try manually sending a request
curl -X POST http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen2.5-1.5b-instruct-q4_k_m.gguf",
    "messages": [{"role": "user", "content": "Hi"}]
  }'
```

---

## Quick Reference: Complete Commands

### Server Machine

```bash
# Terminal 1: Build
cd /home/rudra/meridian
git pull origin main
make server

# Terminal 1: Start server (keep running)
./bin/meridian-server serve \
  --enable-llm=true --enable-ml=false \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-workers 2 \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --models-dir ~/models \
  --threads 8 --ctx 4096 \
  --bind-addr 0.0.0.0

# Get IP address (for client)
hostname -I

# Terminal 2: Start stress test (optional, after ~30s)
./bin/meridian-stress \
  --addr http://127.0.0.1:8080 \
  --concurrency 4 \
  --conversations-per-model 10 &

# Terminal 3: Kill w2 (after ~60s of running)
ps aux | grep "llama-server" | grep -v grep
kill -9 <PID_of_w2>
```

### Client Machine

```bash
# 1. Test connectivity
ping <SERVER_IP>
curl http://<SERVER_IP>:8080/healthz

# 2. Open browser
# URL: http://<SERVER_IP>:8080/

# 3. Use React UI to:
# - Send chat messages
# - Watch status monitor
# - Observe session migration after kill
```

---

## Success Criteria

✅ **Successful Setup If:**
- React UI opens and shows both workers as "healthy"
- Chat messages send and appear in UI
- Status monitor updates in real-time
- After killing w2: UI shows w2 as "unhealthy"
- Sessions migrate (messages still work)
- Success rate >= 95%

---

**You're ready! Start the server machine first, then access from client. 🚀**
