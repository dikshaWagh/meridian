# Phase 2 — Intelligent Workload Allocation (Small vs Premium Models)

**Paste into DeepSeek ONLY after Phase 1 passes. This is your product thesis.**

## Objective
Prove: **routing simple work to a small cheap model and complex work to a premium model
cuts cost/latency/RAM with no unacceptable quality loss on low-end hardware.**

## Model Tiers (Single Machine First)
- **Small (fast/cheap):** `qwen2.5-0.5b-instruct-q4_k_m.gguf` (~0.4 GB, ~60-80 tok/s CPU)
- **Premium (quality):** `qwen2.5-1.5b-instruct-q4_k_m.gguf` (~1.1 GB, ~40-45 tok/s) or `qwen2.5-3b` if RAM allows
- Config via `--llm-model` single-file mode + multi-model `cluster.yaml` (Phase 3 will distribute these). For Phase 2, run 2 workers: w1=small, w2=premium on same host.

## Task 1: Explainable Complexity Classifier (Rule-Based, No ML Training)
Create `internal/balancer/complexity.go`:
```go
type Workload struct { EstTokens int; Complexity string } // "simple" | "complex"
func Classify(req api.ChatRequest) Workload
```
Rules (document each in code comment — viva must be able to explain):
- `EstTokens > 1500` OR `len(messages) > 6` -> complex
- Contains keywords (`summarize document`, `extract`, `code`, `debug`, `math`, `translate formal`, `invoice`, `diagnosis`) -> complex
- Short factual (`"hi"`, `"what is X"`, `"define"`) + EstTokens < 500 -> simple
- Default: simple (fail-open to cheap, escalate on failure)
Unit test with 10 prompts (5 simple, 5 complex). No LLM call needed for classifier.

## Task 2: Cost-Aware Router Policy
Create `internal/balancer/cost_aware.go` implementing `RoutingPolicy` in `routing.go`:
```go
// Score = EstimatedWait + Lambda * ModelCostPenalty
// EstimatedWait(w) = (w.EstTokensInFlight + reqEstTokens) / w.TokPerSec()
// ModelCostPenalty = 0 for small, 1 for premium (tuned: latency vs quality)
// Pick model tier from Classify(), then pick worker minimizing Score within tier.
```
- Register as `"cost-aware"` in `newRoutingPolicies()`, expose via `GET /policies`, `PUT /v1/policies/default`, header `X-Meridian-Routing-Policy`.
- Log decision: `model=... tier=simple picked=w1 score=...` for demo.
- **Escalation:** If small worker returns `413 context_too_long` or empty, retry once on premium (reuse Phase 1 retry path).

## Task 3: Cost Model (Define +"Write Down For Report")
Define deployment cost = `RAM-GB * hours + latency_penalty` (cloud $0, so cost = RAM + seconds).
Document in `docs/phase2-cost-model.md`:
- Cost_small = 0.4 GB * duration, Cost_premium = 1.1 GB * duration
- Quality proxy: manual 1-5 rating on 20 fixed prompts (same prompts for both tiers) + output length + JSON validity for extraction prompts.
- Acceptable loss threshold: quality drop <= 0.5/5 on average, latency p95 improvement >= 30% on simple traffic.

## Task 4: Benchmark Script
- Extend `cmd/meridian-stress/main.go` `scripts`: add 5 simple (`"define X"`, `"hi"`) + 5 complex (`"summarize this 800-word invoice..."`, `"write Go code to..."`).
- Run 3 configs on same host: (a) all-premium, (b) all-small, (c) cost-aware.
- Save to `docs/phase2-results.json`: per-config p50/p95, tok/s, avg RAM (from `/status`), quality scores.

## Manual Test
```bash
./bin/meridian-stress --addr http://127.0.0.1:8080 --concurrency 4 --conversations-per-model 20
curl -H "X-Meridian-Routing-Policy: cost-aware" -X POST /v1/chat/completions ...
curl http://127.0.0.1:8080/status  # check tok_per_sec, in-flight per tier
```

## Acceptance Criteria
- [ ] `Classify` unit tests pass, 10/10 expected tiers
- [ ] `cost-aware` policy selectable at runtime, logs tier decision
- [ ] Graph: cost-aware p95 < all-premium p95, quality drop within threshold (fill `phase2-results.json`)
- [ ] 1-page `docs/phase2-cost-model.md` you can explain without notes

## Non-Goals
No neural classifier, no learned router, no auto-tuning Lambda. Keep rule-based + one tunable constant.
