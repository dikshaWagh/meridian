# Meridian — Project Context (Current Pivot)

**Meridian is an efficient local-AI product for office/home use on low-end hardware.**
Single-binary Go gateway + embedded React UI + `meridian-stress` profiler that pools
**heterogeneous, unreliable commodity machines over a LAN** (1 GPU laptop + CPU desktops/laptops
that sleep/crash) into one private AI cluster serving **LLM chat/extraction (`llama-server`)**
and **Document OCR (`tesseract`)**.

**Thesis to prove:** *Intelligent workload allocation between small and premium models
can reduce deployment cost (RAM, latency, energy) without unacceptable quality loss.*
**Build order:** single-machine fault tolerance first, then single-leader distributed cluster.

Roadmap & DeepSeek build files: `docs/00-overview-and-roadmap.md` + `docs/01-` to `docs/05-`
(see §3). Superseded audits and implementation notes live in `docs/archive/`.

---

## 1. Real-World Problem & Thesis

Colleges, clinics, small offices, home labs need private document + LLM processing:
no cloud leakage, no $2,000/mo GPU, often air-gapped. They already own idle mismatched PCs.

Standard tools fail:
1. **Workload/hardware blindness:** NGINX `round-robin`/`least-connections` treat 50-token `hi`
   and 4,000-token document as 1 connection, and 8 tok/s CPU = 55 tok/s GPU.
   Baseline `stress-results.json`: 35s p50 vs **234s p95**.
2. **Node failure:** Lab PCs disconnect/crash mid-chat. Without health breakers, replay,
   session migration, workflows break.
3. **Cost blindness:** LiteLLM optimizes cloud $ (`tokens x price`) — locally $0.
   Ray Serve needs Python/K8s. Ollama is 1 host. Meridian optimizes **RAM-seconds + latency**.

**Meridian (zero Python, single Go binary):**
- **Token-aware routing (TW-LEL):** each request goes to the worker with the lowest predicted
  finish time `C_w = (F_w·(I+Ō) + I + O) / R_w`, using measured tok/s, in-flight load, and the
  request's estimated input/output tokens. The small-vs-premium classifier (§3) is still planned.
- **Fault tolerance:** 3-state health FSM, atomic reservations, session migration, 1x retry pre-headers,
  automatic worker restart with backoff.
- **Compound pipeline:** OCR + LLM extraction with `/proc/meminfo` RAM admission, zero-PII JSONL logs.

---

## 2. Target Architecture

```text
Leader = Meridian Gateway (1x Go binary, :8080)
  Token Estimator -> Classifier (simple/complex) -> Cost-Aware Router
  SessionStore | Health FSM | Atomic Reservations | /v1/catalog + /status
        |                                  |
  Local workers (spawned)          Remote LAN sub-nodes (already running)
  llama-server xN (small/premium)  llama-server xN (small/premium, various models)
  tesseract OCR                    tesseract OCR (remote, 0.0.0.0 on sub-node)

Leader = control plane only. Workers stateless.
Inference 35s, gateway <1ms => leader never bottleneck.
```

- **Node:** may serve various models (catalog), multiple instances of same small model for
  throughput/isolation (see §6.2). Large models: 1 instance + `-np` slots.
- **Leader failover:** static leader + standby gateway, manual start, client replays history.
  No Raft/gossip (overkill for 2-5 node LAN, state this in viva).

---

## 3. Build Order (Phases — Do Not Skip)

| Phase | File | Goal | Done When |
|---|---|---|---|
| 0 Trim | `docs/01-phase-0-trim-the-fat.md` | Delete arbiter/SD/stubs, LLM+OCR only, green build | `grep arbiter/SD` = 0, `go test ./...` green |
| 1 Single-machine FT | `docs/02-phase-1-single-machine-fault-tolerance.md` | Kill w2 on 1 host, chats continue | Kill test >=95% success, `session moved`, `tok_per_sec` in `/status` |
| 2 Small vs Premium | `docs/03-phase-2-intelligent-workload-allocation.md` | `complexity.go` + `cost_aware.go`, cost vs quality proof | p95 lower than all-premium, quality drop <=0.5/5 |
| 3 Leader + Sub-nodes | `docs/04-phase-3-leader-multi-node-distribution.md` | `configs/cluster.yaml`, remote `Host/Remote`, leader+standby note | Remote kill migrates, `/status` shows LAN hosts |
| 4 Evidence | `docs/05-phase-4-test-pitch-and-report.md` | 4 experiments, graphs, 5-slide pitch, report ch. | `docs/phase4-results/results.md` + charts |

****Current status:** Phase 1 is implemented. Token-aware routing (`token-aware`, TW-LEL) is implemented
and tested, and it is the predictive policy the UI visualises. `complexity.go` and `cost_aware.go` from
Phase 2 are not built yet. Phase 2 benchmarks are still to be run.

How to use with DeepSeek (free tier):** paste ONE phase file at a time, run
`go test ./... && go vet ./...`, commit, then next. Never move on red.

---

## 4. Repository Layout (Post-Trim Target)

```text
cmd/
  meridian-server/   Gateway (serve | ml-worker) — leader
  meridian-stress/   Multi-turn p50/p95/p99 + tok/s + fault injection
internal/
  api/               OpenAI-compatible + WorkerStatus/Catalog contracts
  balancer/          pool, session, routing, handler, supervisor, runtime
                     token_aware.go (TW-LEL policy), stream_usage.go (SSE usage + reply capture)
                     + NEW cost_aware.go, complexity.go (Phase 2, not built)
  tokens/            O(n) estimator (~4 chars/token, template overhead)
  ml/                OCR supervisor only (fake/tesseract), /proc/meminfo admission
  metrics/           JSONL telemetry: latency, tokens, worker, status (no PII)
  webui/             go:embed all:dist
web/                 React+TS+Vite: Chat + Document OCR, cluster status with routing policy
                     and predicted finish times per worker
configs/
  ml.yaml            fake OCR (tests)
  laptop-real.yaml   real tesseract OCR
  cluster.yaml       [NEW Phase 3] leader worker list: id/model/host/port/file/remote
docs/
  00-overview-and-roadmap.md     Product claim + phase order (read first)
  01-phase-0-trim-the-fat.md     Phase 0 deletions + acceptance
  02-phase-1-single-machine-fault-tolerance.md
  03-phase-2-intelligent-workload-allocation.md
  04-phase-3-leader-multi-node-distribution.md
  05-phase-4-test-pitch-and-report.md
  archive/                       superseded audits and implementation notes
```

Phase 0 is complete: `internal/arbiter`, `internal/tui`, `cmd/meridian-tui`,
exclusive mode switching, and image generation (`/v1/images/generations`,
`/v1/artifacts/{id}`) are gone. LLM and OCR run concurrently.

---

## 5. Core HTTP API (Post-Trim + Phase 2/3)

### Gateway (`internal/balancer`)
- `POST /v1/chat/completions` — `X-Conversation-ID`, `X-Meridian-Routing-Policy`, `X-Meridian-Failover`
- `GET /v1/models`, `GET /status` (`in_flight`, `est_tokens_in_flight`, `tok_per_sec`, `host`, `sessions`)
- `GET /policies`, `PUT /v1/policies/default` (`round-robin|random|least-connections|token-aware`;
  `cost-aware` planned in Phase 2)
- `GET /healthz`, `GET /v1/catalog`

### OCR + Compound (`internal/ml`)
- `POST /v1/ocr` (multipart PNG/JPEG) -> text + lines + `processing_ms`
- `POST /v1/document/analyze` (Phase 3 if built): image + prompt -> OCR -> premium/small LLM JSON
- `GET /ml/status` — OCR state + RAM headroom/admission

---

## 6. Key Design Decisions (Viva-Ready)

### 6.1 Leader Arbitration
Single leader for deterministic routing + session stickiness. Workers stateless, so leader
loss loses only in-memory sessions (200, 30m TTL), not models. Standby takes `:8080`.
Gateway cost << inference, so no bottleneck. Raft omitted deliberately for explainability.

### 6.2 Multiple Instances, Same Model, Same Node
- Small Q4 (0.5-1.5B, ~0.4-1.1GB): 2 instances allowed under RAM admission.
  Each gets full `-c` ctx, crash isolation, independent OS scheduling. 1 instance `-np 4`
  shares KV pool (`ctx/parallel` per conv) and dies as one.
- Large (3B+): 1 instance + `-np` slots only. `N * model_size < MemAvailable - headroom` enforced.
- Old 6-worker hardcode (9-10GB) removed for violating admission.

### 6.3 Small vs Premium Routing (Phase 2)
- Tiers: small `qwen2.5-0.5b` (fast/cheap) vs premium `qwen2.5-1.5b/3b` (quality).
- `Classify()`: tokens>1500 or keywords (`summarize/extract/code/invoice`) -> premium,
  else small. Default small, escalate on `413`/empty.
- Score: `EstimatedWait + Lambda*premiumPenalty`. One tunable, whiteboard-explainable.
- Cost = `RAM-GB * seconds`. Quality proxy: 1-5 manual rating on 20 fixed prompts.

### 6.4 Fault Tolerance (Single Machine First)
Health `Starting->Healthy->Unhealthy` (3 fails), `pickMu + WorkerReservation` atomicity,
`SessionStore` migration, 1x retry before headers (non-streaming). Streaming mid-token
loss is documented limitation — next turn reroutes, history preserved.

**Worker restart:** when a llama-server process exits, its supervisor restarts it with exponential
backoff (1s → 30s). It gives up after `--max-restarts` (default 5) consecutive failed restarts. A run that
lasts at least a minute resets the counter. `--restart-workers=false` disables this. Shutdown never restarts.

### 6.5 Token-Aware Routing (TW-LEL)
- Score per healthy worker: `C_w = (F_w·(I+Ō) + I + O) / R_w`. Lowest score wins.
  `F_w` = in-flight plus reserved requests; `I` = estimated input (includes stored session history);
  `O` = `max_tokens` or 128; `Ō` = per-model mean of completed output tokens (128 before any data);
  `R_w` = measured tok/s (workers with zero or invalid throughput are skipped).
- Eligibility: a worker is excluded if `I+O` exceeds its per-slot context (`ctx / parallel`).
  If no worker fits, the server returns `413 context_too_long`.
- Pinned session requests stay on their worker. The policy applies to new conversations and moves.
- **Limitation:** a heuristic, not an exact queueing model. Assumes queued requests are as large as the
  new one, and that input and output tokens cost the same. Continuous batching means throughput does not
  fall linearly with load. Improving this would require fitting the estimate to observed latency.
- Throughput measurement: non-streaming replies use JSON usage. Streamed replies request
  `stream_options.include_usage` and measure tok/s from the first token to the end. This needs
  a llama-server build that supports `include_usage`.

---

## 7. Build, Run, Test

```bash
make server
go test -race ./internal/balancer ./internal/ml && go vet ./...
make build          # only after web/src edits

# Single machine (Phase 1): repeat --llm per model, count after the colon = instances
./bin/meridian-server serve \
  --llm qwen2.5-0.5b-instruct-q4_k_m.gguf:2 \
  --llm qwen2.5-3b-instruct-q4_k_m.gguf:1 \
  --llama-bin /usr/local/lib/ollama/llama-server --models-dir ~/meridian/models \
  --threads 8 --ctx 4096 --host 0.0.0.0 --port 8080 \
  --ram-headroom-mb 256 --routing-policy token-aware
# Preflight rejects the start if N x model size + headroom exceeds MemAvailable.
# Deprecated --llm-model/--llm-workers conflict with --llm.

# Leader + LAN sub-nodes (Phase 3)
# Sub-node: llama-server --host 0.0.0.0 --port 8081 -m model.gguf --alias MODEL -t 4 -c 4096 -np 2
./bin/meridian-server serve --cluster-config configs/cluster.yaml

# Benchmark (Phases 2-4)
./bin/meridian-stress --addr http://127.0.0.1:8080 --concurrency 6 --conversations-per-model 20
curl http://127.0.0.1:8080/status
```

**Known limitations (say upfront):** no auth/TLS (trusted LAN only), in-memory sessions,
crude token estimate (~4 chars/token), heuristic token-aware score (§6.5), no mid-stream resume,
no image generation/fine-tuning. Frontend chips show the policy in effect: the chat's choice if set,
otherwise the server default.
