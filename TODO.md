# Meridian — Distributed TODO

Goal: run Meridian **as it is today** (LLM via `llama-server`, OCR via `tesseract`) across several
machines on a LAN, with a **fixed leader** (first node in `cluster.yaml`), heartbeats, and load
balancing on the leader. No election, no Raft, no sharding.

Reference: `CONTEXT.md` §2, §3, §6.1, §6.6. Phase doc: `docs/04-phase-3-leader-multi-node-distribution.md`.

Legend: `[ ]` todo · `[x]` done · `(blocked)` waiting on something

---

## 0. Prerequisites (verify before starting)

- [ ] `go test -race ./internal/balancer ./internal/ml && go vet ./...` is green on the current tree
* [ ] Phase 0 trim is complete (no `arbiter`, `tui`, or image code left)
* [x] Phase 1 single-machine fault tolerance works: kill a worker, chat continues
- [ ] `llama-server` binary and the same GGUF files are installable on every target machine
- [ ] At least 3 machines available on one LAN (leader + 2 workers), with fixed IPs or DHCP reservations
- [ ] Decide: is Phase 2 (`complexity.go`, `cost_aware.go`) in scope? **Default: no, deferred.**

---

## 1. Cluster config

- [ ] Define `configs/cluster.yaml` schema:
  - `leader` implied as the **first** entry in `nodes`
  - per node: `id`, `host`, `api_port`, `role` (`leader` | `worker`)
  - per worker: `id`, `node`, `model`, `file`, `port`, `ctx`, `parallel`, `mode` (`local` | `remote`)
- [ ] Parser in Go with validation:
  - unique node and worker IDs
  - no duplicate `host:port` pairs
  - `mode: local` only allowed on the machine that runs the config
  - `ctx` and `parallel` present for every worker
  - reject a config where the first node's `host` is not this machine
- [ ] Unit tests for parsing and every validation rule
- [ ] Example `configs/cluster.example.yaml` with a 3-machine layout

---

## 2. Remote worker model

- [ ] Add `Host` / `Remote` to the worker spec used by `internal/balancer`
- [ ] Build the worker URL from `host:port` for remote entries
- [ ] Spawn local workers only for `mode: local`; remote workers are **probe-only** (no process spawn, no restart)
- [ ] Remote worker exit is not restartable by the leader. It is marked unhealthy via probes only.
* [ ] Sub-node startup doc: `llama-server --host 0.0.0.0 --port  -m <file> --alias <model> -c <ctx> -np <parallel>`
- [ ] Test: a remote worker entry is probed and routed to, using two local `llama-server` processes on different ports

---

## 3. Heartbeats (worker level — already partly exists)

- [ ] Confirm `Pool.StartHealthChecks` probes remote URLs with the same 2 s interval and 2 s timeout
- [ ] Make the probe timeout configurable (default 2 s) for slow LAN links
- [ ] Log state transitions with `host` and `worker` fields
* [ ] Test: remote worker unplugged -> unhealthy within \~6 s -> traffic moves to other workers
- [ ] Test: remote worker back -> healthy after one successful probe, and receives traffic again

---

## 4. Node-level heartbeat (new)

Needed so `/status` can tell "machine off" from "llama-server crashed". See `CONTEXT.md` §6.6.

- [ ] Decide the check: **TCP connect to the node's `api_port`** (simplest, no new daemon). Record this decision.
- [ ] Add a node state: `up` | `down`, with the same 3-failure rule as workers
- [ ] A node is `down` -> all its workers are shown as `unreachable` in `/status`
- [ ] `/status` includes per-node state and last-seen age
- [ ] Test: stop a whole sub-node -> node shows `down`, its workers show `unreachable`, not `crashed`

---

## 5. Load balancing across hosts

- [ ] Routing policies consider remote workers identically to local ones (no special-casing)
- [ ] Per-worker context limit comes from the worker's own config, not the leader's (remote `ctx` may differ)
- [ ] Token-aware policy: `R_w` (tok/s) is measured per remote worker, same EWMA as local
- [ ] Optional: `locality` policy prefers local workers on tie (only if time allows)
- [ ] Tests with fake remote workers covering:
  - least-connections picks the least loaded host
  - token-aware skips a worker whose `ctx/parallel` is too small
  - unhealthy remote worker is never picked

---

## 6. Sessions and failover across hosts

- [ ] Session pinning works when the pinned worker is on another host
- [ ] When the pinned remote worker goes unhealthy, the session moves and history is preserved (`session moved` logged)
- [ ] Pre-headers retry (1x) can go to a worker on a different host
- [ ] Streaming mid-token loss on a remote worker: returns a clean error, next turn reroutes (documented limitation)
- [ ] Test: kill a remote worker mid-conversation -> next turn succeeds on another host, session moved
- [ ] Test: kill the leader -> documented behavior: requests fail until restart, sessions gone, models still up on workers

---

## 7. Timeouts, deadlines, and backpressure

- [ ] Connect timeout and response-header timeout on every leader -> worker call
- [ ] Propagate the client deadline to the worker request (no work after the client has gone)
- [ ] Per-worker in-flight cap respected across hosts (reservation is still atomic on the leader)
- [ ] When every healthy worker is full: return `503` with `Retry-After`, not a hang
- [ ] Use monotonic time for every latency and timeout measurement

---

## 8. OCR on remote machines

- [ ] Allow an OCR worker entry with `mode: remote` in `cluster.yaml`
- [ ] **RAM admission is per machine.** `/proc/meminfo` on the leader says nothing about a remote box.
  - Option A (simple): remote OCR is allowed without admission; the leader logs that admission is unchecked.
  - Option B: each sub-node reports `MemAvailable` in its probe response. Leader uses that for admission.
  - Decide and record. **Default: Option A for now, Option B as a stretch.**
- [ ] `/v1/ocr` routes to a healthy OCR worker on any host
- [ ] Test: OCR request served by a remote tesseract worker

---

## 9. Web UI and API

- [ ] `/status` shows a host column and per-node state (`up`/`down`)
- [ ] Cluster panel in `web/` lists nodes, their workers, state, tok/s, and in-flight count
- [ ] Predicted-finish-time chips work for remote workers
- [ ] `GET /v1/catalog` lists remote-served models
- [ ] `make build` after any `web/src` change; check the embedded UI loads

---

## 10. Metrics and logs

- [ ] JSONL request log gains `host` and `worker` fields (no PII, same as today)
- [ ] Log per-request: `queue_ms`, `forward_ms`, `ttft_ms`, `total_ms`, `worker`, `host`, `retried`
- [ ] Timestamps only from the leader's clock; never compare clocks across machines

---

## 11. Testing

### Automated
- [ ] Integration test harness: run 3 `meridian-server` / `llama-server` processes on localhost with different ports, simulating 3 hosts
- [ ] Failure tests in the harness: kill a worker, kill a whole "node", kill the leader
- [ ] `go test -race ./...` green after each milestone

### Real LAN
- [ ] 3 physical machines: one leader, two workers
* [ ] Unplug a worker's network cable -> detection within \~6 s, traffic moves
- [ ] Power off a worker -> node shows `down`
- [ ] Power the worker back on and rejoin -> traffic returns without restarting the leader
- [ ] Restart the leader -> documented recovery steps work

### Network faults (optional but valuable)
- [ ] Inject latency and loss with `tc netem` on one worker; measure p95 change
- [ ] Partition one worker with a firewall rule; confirm the leader marks it down and does not hang

---

## 12. Stress and evidence

- [ ] `meridian-stress` runs against the leader address with the same workload for each configuration:
  - 1 machine
  - 3 machines, round-robin
  - 3 machines, token-aware
- [ ] Record p50, p95, p99, tok/s, and success rate for each run
- [ ] Kill-test result: >= 95% success for chats during a remote worker kill (Phase 3 done-when)
- [ ] Save results under `docs/phase4-results/` and link from `docs/05-phase-4-test-pitch-and-report.md`

---

## 13. Documentation

- [ ] Update `docs/04-phase-3-leader-multi-node-distribution.md` to match the fixed-leader design
- [ ] Runbook: `docs/runbook-cluster.md`
  - starting the leader
  - joining a sub-node
  - what to do when a node is down
  - what to do when the leader is down (manual restart, sessions lost)
- [ ] Update `CONTEXT.md` status table when Phase 3 is done
- [ ] Known limitations section updated (no auth/TLS, fixed leader SPOF, no node-level admission for OCR, in-memory sessions)

---

## 14. Out of scope (explicitly deferred)

- [ ] Automatic leader election (Raft, lease, or gossip)
- [ ] Model sharding across machines (layer or tensor splitting)
- [ ] Auth and TLS (trusted LAN only for this phase)
- [ ] Mid-stream resume after a worker dies
- [ ] Phase 2 small-vs-premium routing
- [ ] Ollama integration
- [ ] Windows support for the cluster (Linux first)

---

## Definition of done (whole project)

- [ ] A 3-machine LAN runs chat and OCR through one leader address
- [ ] Killing any single worker keeps chats working with >= 95% success
* [ ] A node going offline shows as `down` in `/status` within \~6 s
- [ ] A recovered worker rejoins without restarting the leader
- [ ] Stress results for 1 machine vs 3 machines are recorded and reproducible
- [ ] Runbook exists and has been followed once by someone other than the author
