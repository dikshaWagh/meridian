# Guide 1: How to Trim the Fat & Explain Every Surviving File

> **The Golden Rule for Your BTech Viva:**
> Every file in your repository is fair game for your examiner to open on screen and ask: *"Walk me through lines 40–90—why does this exist and how does the concurrency work?"*
> If a feature **contradicts distributed serving**, **cannot run fast on laptop hardware**, or **was vibe-coded with complex state machines you don't need**, cutting it *raises* your grade.

---

## Part 1: What to Cut Immediately (The Fat List)

Cutting the items below removes **~1,800+ lines of high-risk code** while making your architecture cleaner, faster, and 100% aligned with **"Fault-Tolerant Distributed ML Inference Platform."**

### 1. Cut `internal/arbiter/` + Exclusive Mode Switching
- **Files to delete:**
  - `internal/arbiter/arbiter.go`
  - `internal/arbiter/http.go`
  - `internal/arbiter/arbiter_test.go`
  - `web/src/components/ModeSwitch.tsx`
  - `web/src/components/TransitionScreen.tsx`
- **Flags/wiring to remove:**
  - `--serving-mode` and `--initial-mode` in `cmd/meridian-server/main.go` and `internal/balancer/supervisor.go`.
- **Why this hurts you if you keep it:**
  1. `arbiter` was only invented because Stable Diffusion ate 5+ GB of RAM on one laptop. Once Stable Diffusion is gone, `tesseract` OCR only uses ~50–300 MB of RAM and runs happily alongside LLM workers.
  2. Shutting down all LLM chat workers globally whenever someone runs ML makes **zero sense** in a multi-user distributed platform.
  3. `arbiter.go` contains subtle asynchronous phase transitions (`draining -> stopping -> starting -> warming`) and SSE broadcast channels that are painful to trace under viva pressure.

### 2. Cut Stable Diffusion / Image Generation Completely
- **Code/files to remove:**
  - `configs/ml-sd-server.yaml`
  - `ImageGenConfig` and image validation in `internal/ml/config.go`
  - `ImageGenerationBackend`, `fakeImageGen`, `sdServerImageGen`, `startSDServer` in `internal/ml/backend.go`
  - `POST /v1/images/generations` and `GET /v1/artifacts/{id}` handlers in `internal/ml/handler.go` and `internal/ml/worker.go`
  - The text-to-image form inside `web/src/components/MLStudio.tsx` (repurpose `MLStudio.tsx` into a clean **Document OCR + AI Analyzer** tab!).
- **Why this hurts you if you keep it:**
  - Generating a 512×512 image on CPU takes minutes, bloats memory, orphans `sd-server` processes on `SIGKILL`, and solves no real campus/enterprise infrastructure problem.
  - Keeping **OCR (`tesseract`) + LLM (`llama-server`)** gives you a fast (<2 second OCR), believable, privacy-critical real-world use case: **automated analysis of scanned invoices, lab reports, and forms**.

### 3. Cut Unimplemented "Blocked" Engine Stubs
- **Code to remove in `internal/ml/config.go` & `internal/ml/backend.go`:**
  - `EngineONNXRuntime` (`"onnxruntime"`)
  - `EngineStableDiffusionCPP` (`"stable-diffusion.cpp"`)
  - `backendUnavailableReason(...)`
- **Why this hurts you if you keep it:**
  - Nothing screams *"AI generated this boilerplate"* louder to an examiner than config options that exist solely to return `"unavailable in this build"`. Keep only `tesseract` (production) and `fake` (for deterministic unit tests).

### 4. Cut (or Archive) `cmd/meridian-tui/` & `internal/tui/` (Optional but Recommended)
- **Files:** `cmd/meridian-tui/main.go`, `internal/tui/chat.go`, `internal/tui/color.go`
- **Why:** You already have a React Web UI, `curl`, and `cmd/meridian-stress`. Maintaining a separate terminal ANSI chat client gives you zero extra marks in a distributed-systems evaluation while adding 3 more files you could be quizzed on.

---

## Part 2: The Lean Core You Keep — Your File-by-File Viva Cheat Sheet

Once the fat is trimmed, your entire backend fits into **6 clean, small packages**. Here is what every remaining component does and **how to explain it to your teacher in plain Computer Science terms**:

### 1. `internal/tokens/estimate.go` (~37 lines)
- **What it does:** Estimates prompt + max completion tokens in $O(n)$ rune count (`~4 chars/token` + per-message chat template overhead) *before* forwarding the request.
- **How to explain it:**
  > *"Unlike normal web APIs where every JSON payload takes 5ms, LLM compute time scales linearly with input prefill tokens plus generated output tokens. We run a fast heuristic estimator at the gateway so our router knows a request's compute weight in microseconds without loading a heavy tokenizer."*

### 2. `internal/balancer/pool.go` (~450 lines — **Read Every Line**)
- **Key mechanisms inside:**
  1. **`Worker` health Finite State Machine (`applyProbe`):** `Starting -> Healthy -> Unhealthy`. A worker starts in `Starting` (with a warm-up grace window). Health polling hits `/health`; 1 pass marks it `Healthy`; 3 consecutive failures (or process exit) trip the circuit breaker to `Unhealthy` so traffic stops immediately.
  2. **`WorkerReservation` (`reservePick`, `Commit`, `Release`):** Fixes a classic concurrency race condition. If 10 requests arrive at the exact same millisecond, `pickMu` locks selection + increments `Reserved` atomically (`Load() = InFlight + Reserved`) using `sync/atomic` and `sync.Once` so all 10 requests don't pile onto Worker 1 before HTTP proxying begins.
- **How to explain it:**
  > *"This is our distributed node pool and circuit breaker. It solves the thundering-herd selection race using atomic two-phase slot reservations, and isolates dead or warming nodes automatically via periodic health probes."*

### 3. `internal/balancer/session.go` (~127 lines)
- **What it does:** Thread-safe in-memory map (`SessionStore`) with LRU-style capacity eviction (`MaxSessions = 200`, skipping `inFlight` conversations) and TTL sweeping (`30m`). Stores conversation `Messages`, bound `Model`, and pinned `WorkerID`.
- **How to explain it:**
  > *"Inference nodes (`llama-server`) are kept completely stateless so any node can die at any moment. The gateway maintains lightweight session affinity (`X-Conversation-ID`) to reuse warm KV-caches on the same worker during normal operation, and if that worker dies (`pool.go:511`), the gateway seamlessly re-pins the session to a surviving node and replays the conversation history."*

### 4. `internal/balancer/routing.go`, `least_conn.go`, `random.go` (+ your new `cost_aware.go`)
- **What it does:** Strategy pattern (`RoutingPolicy` interface with `Pick(model, healthyWorkers)`). Supports hot-swapping policies at runtime via `PUT /v1/policies/default` or per-request header `X-Meridian-Routing-Policy`.
- **How to explain it:**
  > *"We used the Strategy design pattern so we can benchmark classical L7 policies (`round-robin`, `least-connections`) directly against our AI-specific `cost-aware` policy under identical workloads using `meridian-stress`."*

### 5. `internal/ml/resource.go`, `service.go`, `worker.go`, `backend.go` (Slimmed to OCR only)
- **What it does:** Supervises local OCR worker child processes with concurrency semaphores (`tryAcquire`), bounded crash auto-restarts (`maxRestarts = 2`), and host memory admission (`resource.go` reads Linux `/proc/meminfo` `MemAvailable` and subtracts OS safety headroom before admitting workers).
- **How to explain it:**
  > *"Before spawning auxiliary ML workers like Document OCR on an edge/lab machine, Meridian checks `/proc/meminfo` to enforce memory admission control (`ram_headroom_mb`), ensuring OCR spikes never starve the OS or crash co-located LLM processes via Linux OOM-killer."*

### 6. `internal/metrics/requests.go` & `cmd/meridian-stress/main.go`
- **What they do:** `metrics` appends structured JSONL audit logs (`latency_ms`, `est_total_tokens`, `actual_completion_tokens`, `worker_id`, `status_code`) with zero user PII. `meridian-stress` fires N concurrent multi-turn conversations and computes **p50, p95, p99 tail latencies** and **tokens/sec**.
- **How to explain it:**
  > *"In distributed systems, average latency hides systemic failures. We built a reproducible multi-turn stress generator that measures p50/p95/p99 tail latency across conversation turns so we can mathematically prove fault recovery and routing improvements."*

---

## Self-Test Checklist Before Viva

Before walking into your presentation, verify you can draw these **3 diagrams** from memory on a blank sheet of paper:
1. **Request Lifecycle:** `Client -> Handler (Token Estimate) -> Pool (pickMu + WorkerReservation + Session Affinity) -> ReverseProxy to LAN Node -> Capture Usage & Update Rolling tok/s -> JSONL Audit Log`.
2. **Health Circuit Breaker FSM:** `Starting --(/health 200)--> Healthy --(3x timeout or connection error)--> Unhealthy --(/health 200)--> Healthy`.
3. **Cost-Aware Scheduling Equation:** Why `EstTokensInFlight / WorkerTokPerSec` beats `LeastConnections` when Node A is a GPU laptop and Node B is an old CPU desktop.
