# Meridian Pivot — Roadmap (Read This First, Then Use Phases 1-5 One By One)

## Product Claim (Use This Everywhere)

> **Meridian is an efficient local-AI product for office/home use on low-end hardware.**
> It proves that **intelligent workload allocation between small and premium models**
> can cut deployment cost (RAM, latency, energy) without unacceptable quality loss,
> starting from **single-machine fault tolerance** and then extending to a
> **single-leader distributed cluster (leader + sub-nodes)**.

Target hardware: 10-16 GB RAM laptop/office PCs, CPU or iGPU, no cloud, no Kubernetes.
Target user: college lab, clinic, small office, home lab — private docs + chat, offline.

## Architecture (Final, After Trim)

```text
                    Meridian Gateway (Leader, 1x Go binary)
        Token Estimator -> Cost-Aware Router -> SessionStore
                  | Health FSM + Atomic Reservations
        +---------+----------+
        |                      |
  Local node workers     Remote LAN sub-nodes (Phase 4)
  llama-server xN        llama-server xN (different models)
  tesseract OCR          tesseract OCR
```

- Leader = gateway + scheduler + session store. **Inference is slow (30-200s), gateway is <1ms, so leader is never the bottleneck.**
- Sub-nodes = stateless workers. If one dies, leader reroutes + migrates session.
- Small vs premium routing is a *router policy*, not a new model. Explainable rule, measured with `meridian-stress`.

## Phase Order (Do Not Skip)

| Phase | File | Goal | DeepSeek Prompt Size |
|---|---|---|---|
| 0 | `01-phase-0-trim-the-fat.md` | Delete arbiter/SD/stubs, green build | Small |
| 1 | `02-phase-1-single-machine-fault-tolerance.md` | Kill-one-worker survival on 1 machine | Medium |
| 2 | `03-phase-2-intelligent-workload-allocation.md` | Small vs premium router, cost vs quality proof | Medium |
| 3 | `04-phase-3-leader-multi-node-distribution.md` | 1 leader + LAN sub-nodes, various models | Medium |
| 4 | `05-phase-4-test-pitch-and-report.md` | Benchmarks, graphs, viva pitch | Small |

**How to use with DeepSeek (FREE tier):**
1. Paste ONE phase file at a time. Do not paste all 6.
2. Run `go test ./... && go vet ./...` after each phase. Commit.
3. If DeepSeek breaks a test, paste the error + that phase file back. Do not move on with red tests.
4. Each phase ends with Acceptance Criteria — screenshot/log them for your report.

## What Is Out Of Scope (Say This In Viva)

Fine-tuning, image generation, cloud APIs, multi-GPU clusters, Raft consensus, auth/TLS, persistent DB.
Streams cannot resume mid-token if a node dies — we preserve history and reroute the *next* turn.

## Repo Pointers (Know These Cold)

- `internal/tokens/estimate.go` — token cost before inference
- `internal/balancer/pool.go` — health FSM + `WorkerReservation` atomicity + `SessionStore` migration
- `internal/balancer/routing.go` + `cost_aware.go` (new) — explainable scheduling formula
- `internal/ml/resource.go` — `/proc/meminfo` RAM admission
- `cmd/meridian-stress/main.go` — p50/p95/p99 + tok/s proof
