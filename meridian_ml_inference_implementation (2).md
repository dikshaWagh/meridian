# Meridian — ML Inference Load Balancing Spec

## 1. Objective

Add a **separate, isolated ML inference load-balancing subsystem** to Meridian, alongside the existing LLM balancer.

**Hard rule: do NOT modify, refactor, or rename existing LLM code** (routing, worker lifecycle, health semantics, metrics, telemetry, TUI, config semantics). LLM behavior must stay identical.

```text
Meridian
 ├── LLM LB (existing, unchanged) → llama-server workers
 └── ML LB  (new, isolated)       → ML inference workers
```

Share only genuinely generic infrastructure, and only if it provably changes no LLM behavior. Prefer small duplicated structs over risky abstraction. Do not merge LLM/ML worker state.

---

## 2. API

`POST /v1/predict`

```json
{ "model": "resnet50", "input": <any JSON: image data | text | object | array> }
```

Meridian is an L7 reverse proxy: it never runs inference and does not interpret `input`.

**Worker contract:** `POST /predict` → `{"prediction": ..., "confidence": ...}`; `GET /health`; optional `GET /ready`. Forward the model name only if the worker needs it.

---

## 3. Two-Stage Routing (mandatory)

1. **Model routing** — `request.model` selects eligible workers. Only workers serving that model are candidates; never compare across models.
2. **Worker routing** — pick the best instance among healthy candidates.

Maintain an explicit `model → []worker` map.

---

## 4. Worker

Fields: ID, model, URL, optional process info (only if Meridian spawns workers), state, in-flight, served, success, failures, latency stats, CPU %, GPU %, GPU mem %, last health check, last error.

Config-driven (follow the repo's existing config conventions; don't touch LLM config):

```yaml
ml:
  workers:
    - {id: resnet-1, model: resnet50, url: "http://127.0.0.1:9001"}
    - {id: yolov8-1, model: yolov8,   url: "http://127.0.0.1:9003"}
```

---

## 5. Health vs Routing (keep separate)

- **Health:** "Can this worker serve requests?" Inputs: reachability, `/health` or `/ready`, model loaded, consecutive failures, startup state.
- **Routing:** "Which healthy worker is best?" Inputs: latency, in-flight, CPU, GPU.

States: `STARTING | HEALTHY | UNHEALTHY | STOPPED`. Track `last_check`, `consecutive_failures`, `last_error`. Repeated failures → remove from candidates; recovery → rejoin.

High load (e.g. GPU 95%) ≠ unhealthy. Failure rate ≠ health. Do not reuse LLM health assumptions.

---

## 6. Routing Algorithm

1. Read model from request
2. Get workers for that model
3. Drop non-HEALTHY workers (**always before scoring**)
4. Collect metrics
5. Normalize
6. Weighted score
7. Pick **lowest** score
8. Proxy request
9. Record result

**Normalization** (all ∈ [0,1], 0 best; guard zero denominators; keep replaceable by rolling baselines/thresholds later):

```text
latency_score = latency / max_latency      (among candidates)
queue_score   = in_flight / max_in_flight  (among candidates)
cpu_score     = cpu% / 100
gpu_score     = gpu% / 100                 (CPU-only worker: no GPU data → gpu_score = 0 and flag "n/a")
```

**Score:**

```text
score = 0.40·latency + 0.25·queue + 0.20·gpu + 0.15·cpu
```

Weights must be configurable constants. No round robin as the primary ML algorithm.

---

## 7. Metrics (measured only — never fabricated)

| Metric | Source |
|---|---|
| Latency | `time.Since(start)` around the real proxy call; t0 received → t4 returned (optionally routing_time, worker_latency) |
| In-flight | `atomic.AddInt64` ±1 around each request. Used as the load signal; **no queue time is reported unless Meridian implements a real queue** |
| Total / success / failure | 2xx = success; 4xx/5xx/error = failure; derive failure rate |
| CPU | Host telemetry (Go system-metrics lib), never from HTTP |
| GPU | NVML or equivalent, optional; **must not break CPU-only builds/workers**. Also GPU memory if available |

Optional (only if reliably measurable): P50/P95/P99, req/s, queue length.

---

## 8. Concurrency

Go handles requests per goroutine. Make safe: InFlight, Served, Success, Failures, routing stats, worker state. Use atomics for counters, mutex/RWMutex for compound state and the pool.

---

## 9. Errors (never crash Meridian)

| Case | Code |
|---|---|
| Malformed request / no model | 400 |
| Unknown model / no worker for model | 404 |
| All workers unhealthy | 503 |
| Worker timeout | 504 |
| Worker connection failure | 502 |
| Worker 4xx/5xx | pass through, count as failure |

---

## 10. Routing Decision Object

```text
RoutingDecision: Model, CandidateWorkers, HealthyWorkers, SelectedWorker,
                 LatencyScore, QueueScore, GPUScore, CPUScore, FinalScore, Timestamp
```

Per-worker scores should be kept so the TUI can show every candidate. Real values only.

---

## 11. TUI

Separate ML view/tab. Do not alter the LLM TUI. Refresh periodically.

Show: model, per-worker table (ID, state, in-flight, latency, CPU, GPU, score), selected worker + reason, request metrics (total, success, failures, success rate, avg latency, in-flight), health, and a recent routing event log:

```text
[12:31:02] model=resnet50 | W1 healthy score=0.31 | W2 healthy score=0.67 | W3 unhealthy
[12:31:02] selected W1 → 200 latency=84ms
```

Per recent request, expose: model, candidates, health, raw metrics, normalized scores, final score, selected worker, status, latency. The TUI should demonstrate the algorithm, not just monitor.

---

## 12. Package Layout

Isolated `internal/ml/` (adapt to the repo's existing structure; today it has `cmd/`, `internal/`, `bin/`):

| File | Role |
|---|---|
| `handler.go` | `/v1/predict`, parse/validate, call router, proxy, respond |
| `pool.go` | thread-safe workers, model→workers lookup |
| `worker.go` | worker struct, counters, state |
| `health.go` | checks, transitions, failure tracking |
| `router.go` | filter, normalize, score, select |
| `metrics.go` | latency, in-flight, success/failure, CPU/GPU |
| `telemetry.go` | periodic updates, TUI snapshots |
| `types.go` | request/response, states, metrics, decision |
| `supervisor.go` | **only** if Meridian spawns ML workers; otherwise omit |

---

## 13. Tests

- **Model matching:** `resnet50` request considers only resnet50 workers
- **Health filtering:** unhealthy worker never selected, even with the lowest score
- **Score:** `0.40(0.2)+0.25(0.4)+0.20(0.5)+0.15(0.3) = 0.325`
- **Lowest wins:** W1=0.2, W2=0.6, W3=0.4 → W1
- **No healthy workers:** 503
- **Unknown model:** 404
- **Concurrency:** 10 / 50 / 100 parallel requests; InFlight returns to 0; Success + Failures = Served
- **LLM regression:** existing LLM tests/behavior unchanged
- **Integration:** LLM and ML run simultaneously without sharing routing state

Run `go test ./...` and `go test -race ./...`.

---

## 14. Implementation Phases

1. Isolated `ml` package (no LLM edits)
2. `/v1/predict`
3. Worker pool + model mapping
4. Health + states
5. Basic routing (model match, health filter)
6. Metrics (latency, in-flight, success/failure, CPU, GPU)
7. Scoring + lowest-score selection
8. Concurrency safety
9. ML TUI
10. Tests (`-race`), then manual LLM + ML run

---

## 15. Demo

3 ResNet workers → send requests → TUI shows live scores → load W1 → W1's score rises and traffic shifts to W2/W3 → stop W3 → marked UNHEALTHY, removed from candidates → requests continue via W1/W2. This contrasts health- and performance-aware routing with round robin.

---

## 16. Do NOT

- Modify LLM routing, health, metrics, worker lifecycle, supervisor, or TUI
- Replace LLM round robin with ML scoring
- Merge LLM and ML worker state
- Treat high CPU/GPU as unhealthy
- Fabricate metrics or report unmeasured queue time
- Choose the model by score, or route to a worker of a different model
- Add GPU dependencies that break CPU-only operation
- Build a giant generic abstraction to avoid a few duplicated structs

**Core goal:** one Meridian process, two independent subsystems; add ML capability with zero LLM regression.
