# Meridian : Project Context (Pivoted Architecture)

**Meridian** is a single-binary Go **Fault-Tolerant Distributed ML & LLM Inference Gateway** with an embedded React operator/client UI and a multi-turn load profiler (`meridian-stress`).

It pools **heterogeneous, unreliable commodity machines over a LAN** (e.g., 1 lab PC with a modest GPU + 2 CPU-only office desktops + laptops that disconnect/crash) into a unified, private on-prem AI cluster serving **LLM chat/extraction (`llama-server`)** and **Document OCR (`tesseract`)**.

---

## 1. Real-World Problem & Core Thesis

Small colleges, clinics, law offices, and startups need private document + LLM processing (no cloud data leakage, no \$2,000/mo cloud GPU bills), and already own idle, mismatched PCs on a local network.

Standard HTTP load balancers (NGINX/HAProxy) and single-node tools (Ollama/LM Studio) fail in this environment for two reasons:
1. **AI Workload & Hardware Blindness:** NGINX `round-robin` and `least-connections` treat a 50-token greeting and a 4,000-token OCR document as "1 HTTP connection," and treat an 8 tok/s CPU desktop identically to a 55 tok/s GPU PC—causing severe head-of-line blocking (evidenced by our baseline `stress-results.json`: 35s p50 vs **234s p95**).
2. **Mid-Session Node Failures:** Commodity lab/office machines sleep, disconnect from Wi-Fi, or crash. Without gateway-managed health circuit breakers, request replay, and stateful session migration, user workflows break.

**Meridian solves both** in pure Go (zero Python runtime):
- **Token- & Speed-Aware L7 Scheduling:** Routes using pre-inference token estimation (`EstTokensInFlight`) divided by each worker's measured rolling `tok/s`.
- **Circuit-Breaker Health Probes & Stateful Session Failover:** Detects dead LAN/local workers, migrates pinned multi-turn conversations (`SessionStore`) to surviving nodes, and retries failed pre-header upstream dispatches transparently.
- **Compound Private Document Workflow:** Chains lightweight Document OCR (`/v1/ocr`) + distributed LLM extraction (`/v1/chat/completions` / `/v1/document/analyze`) with host RAM headroom admission (`/proc/meminfo`) and zero-PII JSONL audit telemetry.

---

## 2. Repository Layout (Current vs. Pivot Target)

```text
cmd/
  meridian-server/   Gateway server + local worker supervisor (serve | ml-worker)
  meridian-stress/   Concurrent multi-turn benchmark & fault-injection load generator
  meridian-tui/      Optional terminal client (can be dropped during fat trim)
internal/
  api/               Shared OpenAI-compatible + status JSON contracts
  balancer/          Worker pool, health FSM, session store, atomic reservations,
                     L7 routing policies (round-robin, random, least-conn, +cost-aware)
  tokens/            Model-independent O(n) prompt/completion token estimator
  ml/                OCR worker supervisor, concurrency semaphores, /proc/meminfo RAM admission
  metrics/           Privacy-safe JSONL request telemetry (latency, tokens, worker, status)
  webui/             go:embed all:dist single-binary static UI handler
  arbiter/           [DEPRECATED / TO TRIM] Single-host exclusive LLM<->ML mode switcher
web/                 React + TypeScript + Vite UI (chat, OCR document upload, cluster status)
configs/             Cluster & ML worker YAML configs
docs/                Architecture notes, fat-trim audit, execution & viva defense guides
```

---

## 3. Current State vs. Target Pivot Delta

### Already Working in Tree
- OpenAI-compatible `POST /v1/chat/completions` (streaming SSE & JSON) + `X-Conversation-ID` sticky session routing (`internal/balancer/handler.go`, `session.go`).
- Atomic two-phase slot selection (`WorkerReservation`: `Reserve -> Commit / Release` in `internal/balancer/pool.go`) preventing concurrent burst race conditions.
- 3-state worker health FSM (`Starting -> Healthy -> Unhealthy` after 3 failed `/health` checks or process exit) with automatic conversation migration (`session <id> moved w1 -> w2`).
- Token estimator (`internal/tokens/estimate.go`) + per-worker `EstTokensInFlight` atomic tracking + actual token usage capture (`captureUsage`).
- Real `tesseract` OCR worker (`POST /v1/ocr`) with multipart bounds, concurrency semaphores, bounded restarts, and `/proc/meminfo` RAM admission (`internal/ml/`).
- `meridian-stress` multi-turn p50/p95/p99 & tok/s benchmark generator (`cmd/meridian-stress/`).

### Planned Pivot Changes (See `docs/01-trim-the-fat-audit.md` & `docs/02-pivot-plan-and-wrapper-defense.md`)
1. **Trim Bloat:** Remove `internal/arbiter` (manual exclusive mode switching), remove Stable Diffusion image generation (`sd-server` / `fakeImageGen` / `/v1/images/generations`), and remove uncompiled `onnxruntime`/`stable-diffusion.cpp` config stubs.
2. **True LAN Distribution:** Extend `WorkerSpec` (`internal/balancer/workers.go`) to accept remote `Host`/`URL` entries from a `configs/cluster.yaml` (or CLI flags) instead of hardcoding `127.0.0.1`, enabling multi-laptop LAN clusters alongside locally supervised workers.
3. **Cost-Aware Heterogeneous Router (`internal/balancer/cost_aware.go`):** Route to minimize estimated drain time:
   `EstimatedWait(w) = (w.EstTokensInFlight + reqEstTokens) / w.RollingTokPerSec`
4. **Transparent Upstream Failover + Document Analyze Pipeline:** Retry once on dial/reset failure before response headers are written, and expose `POST /v1/document/analyze` (`OCR -> LLM`) as a single compound endpoint.

---

## 4. Core HTTP API (Post-Trim Target)

### Distributed LLM Gateway (`internal/balancer`)
- `POST /v1/chat/completions` — OpenAI-compatible chat/extraction (`X-Conversation-ID`, `X-Meridian-Routing-Policy`)
- `GET /v1/models` — Healthy models available across the cluster
- `GET /status` — Per-node worker state, host/port, `in_flight`, `est_tokens_in_flight`, rolling `tok/s`, pinned sessions
- `GET /policies` & `PUT /v1/policies/default` — Inspect or hot-swap active L7 routing policy (`round-robin | random | least-connections | cost-aware`)
- `GET /healthz` — Gateway liveness

### Document OCR & Compound Pipeline (`internal/ml` + pipeline)
- `POST /v1/ocr` — Multipart PNG/JPEG upload $\rightarrow$ recognized text + lines + processing latency
- `POST /v1/document/analyze` — Multipart document image + prompt $\rightarrow$ OCR text $\rightarrow$ fault-tolerant LLM structured extraction
- `GET /ml/status` — OCR worker state + `/proc/meminfo` RAM headroom & admission status

---

## 5. Build, Verify & Benchmark

```bash
# Build Go binaries (uses committed internal/webui/dist; no Node required unless editing web/src)
make server
go test -race ./...
go vet ./...

# Rebuild embedded React UI (only after editing web/src)
make build

# Run multi-turn load & tail-latency benchmark against cluster
./bin/meridian-stress --addr http://127.0.0.1:8080 --concurrency 6 --conversations-per-model 50
```

## 6. Architectural Invariants&#x20;

1. **Gateway owns session state; workers stay stateless:** If any remote `llama-server` node crashes or disconnects, no conversation history is lost because `SessionStore` replays prior turns onto the newly selected healthy node.
2. **Atomic Pick + Reserve (`pickMu` + `WorkerReservation`):** Selecting the least-loaded worker and incrementing its `Reserved` counter happen inside one critical section so 10 simultaneous requests never stampede the same idle node.
3. **Never trust HTTP connection count as AI load:** Routing accounts for both **prompt/output token volume** (`EstTokensInFlight`) and **node hardware speed** (rolling `tok/s`), preventing slow CPU nodes from becoming tail-latency bottlenecks.
4. **Zero-PII Telemetry:** `logs/requests.jsonl` and `logs/ml-requests.jsonl` record request IDs, worker IDs, token counts, status codes, and latency—never raw user documents or prompts.
