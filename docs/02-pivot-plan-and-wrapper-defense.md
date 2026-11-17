# Guide 2: Step-by-Step Pivot Plan & Surviving the "Ollama Wrapper" Accusation

This guide gives you:
1. **A concrete 4-step execution plan** to slim the repo and complete the pivot without getting lost in vibe-coded complexity.
2. **Word-for-word engineering counters** to destroy criticisms like *"Isn't this just an Ollama / LM Studio wrapper?"* or *"It's just a wrapper over a wrapper."*

---

## Part 1: The 4-Phase Execution Plan

Do these phases **in strict order**. Run `go test ./...` at the end of every single phase so you never break a working build.

### Phase 1: Total Fat Reduction (~1–2 Hours)
Goal: Strip everything you don't need so the codebase is lean, fast, and 100% explainable.

1. **Remove `internal/arbiter`:**
   - Delete `internal/arbiter/`.
   - In `cmd/meridian-server/main.go`, remove `--serving-mode` and `--initial-mode`.
   - In `internal/balancer/supervisor.go`, remove `arbiter` imports/middleware and always start enabled subsystems directly (what the `else` branch at line 143 already does!).
2. **Remove Image Generation & Dead Engine Stubs:**
   - Delete `configs/ml-sd-server.yaml`.
   - In `internal/ml/config.go`, delete `ImageGenConfig`, `EngineONNXRuntime`, `EngineStableDiffusionCPP`, and `EngineStableDiffusionServer`. Keep `OCRConfig` with `EngineFake` and `EngineTesseract`.
   - In `internal/ml/backend.go`, delete `ImageGenerationBackend`, `fakeImageGen`, `sdServerImageGen`, `startSDServer`, and `backendUnavailableReason`.
   - In `internal/ml/handler.go`, `worker.go`, `service.go`, and `inference.go`, delete the `/v1/images/generations` and `/v1/artifacts/{id}` paths.
3. **Simplify the Web UI (`web/src/`):**
   - Delete `ModeSwitch.tsx` and `TransitionScreen.tsx`.
   - Turn `MLStudio.tsx` into a focused **"Document Analyzer (OCR + LLM)"** view: upload a PNG/JPG of an invoice/report/notes $\rightarrow$ extract text via `/v1/ocr` $\rightarrow$ send extracted text + user prompt to `/v1/chat/completions` (or `/v1/document/analyze`) and display which worker node processed it (`X-Meridian-Worker`).
   - Run `make build && go test ./...`.

---

### Phase 2: Make It Genuinely Distributed Across LAN (~2 Hours)
Goal: Prove in front of your examiner that workers run across **multiple physical laptops/PCs**, not just `127.0.0.1`.

1. **Extend `WorkerSpec` in `internal/balancer/workers.go`:**
   ```go
   type WorkerSpec struct {
       ID     string `yaml:"id"`
       Model  string `yaml:"model"`
       Host   string `yaml:"host"`   // e.g. "192.168.1.24" or "127.0.0.1"
       Port   int    `yaml:"port"`
       File   string `yaml:"file,omitempty"`   // only needed if Meridian spawns it locally
       Remote bool   `yaml:"remote,omitempty"` // true = already running on another LAN machine
   }
   ```
2. **Update `NewPool` (`internal/balancer/pool.go:121`):**
   - If `s.Host == ""` default to `"127.0.0.1"`, then build `Host: fmt.Sprintf("%s:%d", s.Host, s.Port)`.
   - Expose `Host` in `api.WorkerStatus` (`GET /status`) so your dashboard and `curl localhost:8080/status` visibly display `192.168.1.10:8081`, `192.168.1.15:8081`, etc.
3. **Skip Local Spawning for `Remote: true` Workers (`supervisor.go`):**
   - In `preflight()` and `startChildren()`, only check local GGUF files and `exec.Command` for workers where `!s.Remote`. Remote LAN workers are monitored automatically by your existing `StartHealthChecks(ctx)` loop!
4. **Add `--cluster-config configs/cluster.yaml`:**
   - Load a simple YAML list of local + remote LAN workers. Now any classmate's laptop running `llama-server --host 0.0.0.0 --port 8081 -m model.gguf` joins your cluster in 10 seconds.

---

### Phase 3: Write the Two Core Differentiators *Yourself* (~3 Hours)
Goal: Hand-write the exact scheduling & fault-tolerance logic you will present on the whiteboard.

1. **Rolling Worker Speed Telemetry + `CostAwarePolicy` (`internal/balancer/cost_aware.go`):**
   - On `Worker`, store an atomic/mutex-protected `tokPerSec float64` initialized to a sensible baseline (e.g., `20.0`).
   - In `handler.go` when a request finishes and `usage.ok && elapsed > 0`, update an Exponentially Weighted Moving Average (EWMA):
     ```go
     measured := float64(usage.out) / elapsed.Seconds()
     w.UpdateSpeed(measured) // newSpeed = 0.7*oldSpeed + 0.3*measured
     ```
   - In `CostAwarePolicy.Pick(model string, healthy []*Worker)`:
     Compute `waitScore = float64(w.EstTokensInFlight.Load()) / w.SpeedTokPerSec()` (plus tie-breaking on `w.Load()`) and pick the worker with the lowest `waitScore`.
   - Pass request token estimate into routing (or include active reserved load) so a slow 8 tok/s CPU node carrying one 1,500-token job (`~187s` drain time) is skipped in favor of a fast 50 tok/s GPU node carrying two 200-token jobs (`~8s` drain time)!
2. **Transparent Single-Retry Failover (`internal/balancer/handler.go`):**
   - For non-streaming JSON requests (or pre-header connection drops), if `worker.Proxy` encounters a dial/connection reset error before writing bytes to the client:
     1. Call `worker.recordFailure(err)` + mark unhealthy if connection refused.
     2. Re-call `pool.PickForSessionWithPolicyReserved(...)` (which automatically migrates the `Session` to a healthy node and logs `session moved w2 -> w1`!).
     3. Replay the buffered request body to the new worker transparently.
   - Result: Pulling the plug on Laptop #2 during a batch causes **zero client-visible errors**.
3. **One-Step Real-World Endpoint: `POST /v1/document/analyze`:**
   - Accepts multipart `file` (scanned image) + `prompt` (e.g., `"Extract patient name, date, and diagnosis as JSON"`) + `model`.
   - Step 1: Calls `mlSvc.Recognize(...)` (OCR).
   - Step 2: Injects recognized text into a chat request and dispatches it through the fault-tolerant LLM balancer pool, returning `{ocr_text, analysis, ocr_ms, llm_ms, worker_id}`.

---

### Phase 4: Collect Empirical Proof with `meridian-stress` (~1 Hour)
Run 3 quick tests using `./bin/meridian-stress` (add mixed-length prompts to `scripts` in `cmd/meridian-stress/main.go`—some 15-word prompts, some 600-word document prompts) and save the JSON outputs:

1. **Experiment 1 (Routing Benchmark on Asymmetric Nodes):**
   Run against 1 fast worker (8 threads or GPU) + 1 throttled/slow worker (2 threads on CPU):
   - Run A: `round-robin`
   - Run B: `least-connections`
   - Run C: `cost-aware`
   - *Expected result:* `cost-aware` slashes **p95 and p99 tail latency** dramatically because big prompts stop clogging the slow worker.
2. **Experiment 2 (Chaos / Fault Injection):**
   Start a 60-conversation run across 2–3 workers. At conversation #20, kill (`kill -9` or disconnect Wi-Fi) Worker #2. Show:
   - Gateway detects failure, logs `session <id> moved w2 -> w1`, and completes remaining turns with conversation history preserved.

---

## Part 2: Viva Defense Playbook — Destroying the "Just a Wrapper" Label

Examiners love testing whether BTech students just glued APIs together. Use these calm, precise engineering answers.

### Attack 1: *"Isn't this just a wrapper around Ollama or `llama.cpp`, like LM Studio?"*
**Your Counter:**
> *"No, Sir/Ma'am—calling Meridian an Ollama wrapper would be like calling Kubernetes or Envoy a 'Docker wrapper.'*
> *Ollama and LM Studio are **single-machine model runners**—they load weights on one PC and have zero awareness of other machines on the network, zero cross-node load balancing, and if that PC shuts down, everything dies.*
> *`llama-server` is purely our **stateless math execution engine** on each worker node (doing C++ matrix multiplication), because re-writing matrix kernels in Go isn't distributed systems engineering. Meridian is the **distributed control plane and L7 data plane**: it handles multi-node health circuit breaking, atomic slot reservations, token-cost scheduling across heterogeneous hardware, and stateful session migration when physical nodes crash."*

### Attack 2: *"Why build this in Go? Why not just put NGINX in front of 3 `llama-server` machines?"*
**Your Counter (Walk to your benchmark slide):**
> *"We actually tested classical HTTP load balancing (`round-robin` and `least-connections`, which is what NGINX does), and here is our `stress-results.json` data showing p95 latency spiking to 234 seconds.*
> *NGINX operates at HTTP connection level—it assumes every request costs roughly the same and every backend server has identical speed. In ML inference, both assumptions are false:*
> 1. *One 3,000-token document extraction takes **60x more compute** than a 50-token chat turn.*
> 2. *Campus/office clusters are **heterogeneous**—a GPU PC runs at 50 tok/s while a spare CPU PC runs at 8 tok/s.*
> 3. *Multi-turn conversations are **stateful**—if Node 2 crashes mid-chat, NGINX drops the context, whereas Meridian's gateway `SessionStore` migrates the session to Node 1 and replays the conversation history automatically."*

### Attack 3: *"Isn't chaining Tesseract OCR + `llama-server` just a wrapper over a wrapper?"*
**Your Counter:**
> *"Every production inference platform—including NVIDIA Triton and Ray Serve—delegates tensor execution to C/C++ runtimes while solving **resource isolation, admission control, and fault tolerance** in the serving layer.*
> *In Meridian, `internal/ml/resource.go` probes `/proc/meminfo` before admitting OCR workers to guarantee OS/LLM memory headroom, enforces bounded concurrency semaphores so image uploads can't OOM the host, isolates worker crashes with bounded restarts, and strips PII from operational JSONL logs (`internal/metrics/requests.go`). A thin wrapper blindly forwards requests and crashes the host under load; an inference platform enforces capacity invariants."*

### Attack 4: *"What real-world problem does this actually solve? Why wouldn't a company just use the OpenAI API or buy an A100 server?"*
**Your Counter (The Business Executive Answer):**
> *"Two hard constraints in Indian colleges, clinics, CA firms, and small enterprises: **Data Privacy** and **CapEx**.*
> *A clinic or exam cell cannot legally or safely send patient diagnostics or confidential records to external cloud APIs, and they don't have ₹15–20 Lakhs for an enterprise GPU server. What they **already own** is 5 to 10 mismatched desktops and laptops sitting idle at <10% CPU utilization on the same LAN.*
> *Meridian turns existing zero-cost commodity LAN PCs—even unreliable ones that get rebooted or unplugged—into a single fault-tolerant private document & LLM processing cluster."*

### Attack 5: *"Walk me through a tricky concurrency bug your code actually prevents."*
**Your Counter (Instant A+ Credibility — Open `internal/balancer/pool.go:277`):**
> *"In `pool.go`, look at `reservePick` and `WorkerReservation`. Under a concurrent burst, if 8 goroutines inspect `worker.InFlight` at the same time before the HTTP reverse proxy opens the upstream socket, all 8 see `InFlight == 0` on Worker 1 and stampede the exact same node—breaking `least-connections` and `cost-aware` routing.*
> *We solved this with two-phase atomic reservation: inside `pickMu`, selection atomically increments `worker.Reserved` (`Load = InFlight + Reserved`). If request validation fails before proxying, `defer reservation.Release()` decrements `Reserved` via `sync.Once`; once proxying begins, `reservation.Commit()` atomically transfers that count from `Reserved` to `InFlight`."*
