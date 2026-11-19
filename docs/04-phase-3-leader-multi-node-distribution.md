# Phase 3 — Single Leader + Multiple Sub-Nodes (Various Models)

**Paste into DeepSeek ONLY after Phase 2 passes. This is the distributed extension.**

## Objective
Prove: **one leader gateway arbitrates load across LAN sub-nodes, each serving various models
(small/premium/OCR), with the same fault tolerance as Phase 1.**

## Architecture To Implement

```text
Leader (Laptop A): meridian-server :8080 (gateway, SessionStore, scheduler)
  |
  +-- Local worker: llama-server :8081 (premium, spawned by leader)
  +-- Sub-node B:   llama-server :8081 (small, remote, already running)
  +-- Sub-node C:   llama-server :8081 (premium, remote) + tesseract :8091 (OCR remote)
```

Leader is **control plane only**. Inference never runs on leader logic — it proxies to sub-nodes.
Gateway load is <1ms vs inference 30-200s, so leader is not bottleneck (state this in report).

## Task 1: WorkerSpec Remote Support
- `internal/balancer/workers.go`: add `Host string`, `Remote bool` to `WorkerSpec`.
- `internal/balancer/pool.go:NewPool`: dial `Host:Port` (default `127.0.0.1`). Add `Host` to `api.WorkerStatus`.
- `internal/balancer/supervisor.go`: `preflight()` + `startChildren()`: skip file check/spawn if `Remote==true`. Health checks (`StartHealthChecks`) must probe remote `/health` same as local.
- `internal/ml/config.go`: allow `listen_host: 0.0.0.0` for OCR worker on sub-node (keep gateway loopback check for local mode; document firewall warning).

## Task 2: `configs/cluster.yaml` (New)
```yaml
workers:
  - id: w1-premium-local
    model: qwen2.5-1.5b
    host: 127.0.0.1
    port: 8081
    file: qwen2.5-1.5b-instruct-q4_k_m.gguf
    remote: false
  - id: w2-small-remote
    model: qwen2.5-0.5b
    host: 192.168.1.20   # Laptop B
    port: 8081
    remote: true
  - id: w3-premium-remote
    model: qwen2.5-1.5b
    host: 192.168.1.21   # Laptop C
    port: 8081
    remote: true
```
- Add `--cluster-config` flag in `cmd/meridian-server/main.go`. If set, `workerSpecs()` loads YAML instead of `--llm-model` / defaults.
- Validate: duplicate IDs/ports rejected, remote must have host+port, local must have file.

## Task 3: Leader + Sub-Node Runbook
- Sub-node (B/C): run `llama-server` directly (not Meridian):
  `llama-server --host 0.0.0.0 --port 8081 -m model.gguf --alias MODEL -t 4 -c 4096 -np 2`
  Or run `meridian-server ml-worker` for OCR on that node.
- Leader (A): `meridian-server serve --cluster-config configs/cluster.yaml --llama-bin llama-server --models-dir ~/models`
- Firewall: `0.0.0.0` bind + no auth is LAN-only. Document: "Use on trusted LAN/hotspot only, front with reverse proxy if needed." (Known limitation in CONTEXT.md).

## Task 4: Leader Failover Note (No Raft)
- Document in `docs/phase3-leader-failover.md`: leader is SPOF for *sessions*, not models. Workers are stateless. Standby = second gateway with same `cluster.yaml`, manual start. Client reconnects, history replayed from client. No split-brain because only one gateway binds `:8080` at a time (OS enforces).
- Add `/healthz` to leader for standby polling. Do NOT attempt Raft/gossip.

## Manual Test (2-3 Laptops, Same Hotspot)
```bash
# B: llama small
llama-server --host 0.0.0.0 --port 8081 -m qwen2.5-0.5b.gguf --alias qwen2.5-0.5b -t 4 -c 4096 -np 2
# A: leader
./bin/meridian-server serve --cluster-config configs/cluster.yaml
curl http://127.0.0.1:8080/status  # expect 3 workers, 1 local + 2 remote healthy, tok_per_sec per node
./bin/meridian-stress --addr http://127.0.0.1:8080 --concurrency 6 --conversations-per-model 20
# Kill B Wi-Fi mid-run -> expect session moved, cost-aware shifts traffic to A/C
```

## Acceptance Criteria
- [ ] `go test ./...` passes with `cluster.yaml` validation tests
- [ ] `/status` shows remote hosts, health flips to `unhealthy` within 6s of sub-node death
- [ ] Kill remote sub-node mid-stress: non-streaming success >=95%, sessions migrate
- [ ] `docs/phase3-leader-failover.md` (1 page) + updated `CONTEXT.md` architecture diagram
- [ ] Photo/video of 2+ laptops demo saved for report

## Non-Goals
No auto-discovery (mDNS), no Kubernetes, no Raft, no cross-subnet routing, no TLS/auth.
