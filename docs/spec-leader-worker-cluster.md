# Spec: Leader and Worker Modes for `meridian-server`

**Status:** ready for implementation, stage by stage.
**Audience:** an implementation agent working in this repository.
**Read first:** `CONTEXT.md` (§2 architecture, §6.1 leader, §6.6 heartbeats), `TODO.md`.

---

## 0. How to use this document

1. Implement the stages in order: **Stage 1 → Stage 2 → Stage 3**. Do not start a stage until the previous one passes its acceptance tests.
2. Run `go test -race ./... && go vet ./...` after every stage. Never move on while red.
3. Keep single-machine mode working. `meridian-server serve` with no role flag must behave exactly as it does today.
4. Where this spec leaves a choice open, use the **Default** in §11 and record the decision in the commit message.
5. Stop and ask if a requirement conflicts with existing code. Do not silently change behavior.

---

## 1. Goal

Run Meridian across 2–3 machines on one LAN:

- One machine starts as the **leader**. It finds workers, keeps a registry, watches their health, and is the only entry point for clients.
- Other machines start as **workers**. They find the leader, register their local models, send heartbeats, and serve inference when asked.
- The leader can also serve its own local models.

**Out of scope for this spec:** model sharding, leader election, TLS, and mid-stream resume. Routing and the React dashboard are covered in later stages (§10).

---

## 2. Existing code to reuse

| Need | Existing location | Rule |
|---|---|---|
| Process supervision of `llama-server` (spawn, restart with backoff) | `internal/balancer/supervisor.go` | Reuse. Do not rewrite. |
| Worker pool, health FSM (`Starting`/`Healthy`/`Unhealthy`), 2 s `/health` probe | `internal/balancer/pool.go` | Reuse for local replicas. Add locking for dynamic membership. |
| Routing policies | `internal/balancer/routing.go`, `least_conn.go`, `token_aware.go`, `random.go` | Reuse. Later stages add policies here. |
| Flags and subcommand dispatch | `cmd/meridian-server/main.go` | Refactor as described in Stage 1. |
| OpenAI-compatible types | `internal/api/types.go` | Extend. Keep existing names working. |

Do not change the behavior of `balancer` for single-machine mode while adding cluster code.

---

## 3. Command line

### 3.1 Subcommands and roles

```bash
meridian-server serve --leader  [flags]     # leader: discovery, registry, public API, local models
meridian-server serve --worker  [flags]     # worker: discovery client, registration, heartbeat, local models
meridian-server serve           [flags]     # standalone: exactly today's behavior (no cluster)
```

- `--leader` and `--worker` are mutually exclusive. Passing both is an error with exit code 2.
- Every node, leader or worker, runs local model replicas from `--llm` with identical rules (§4.5). A node's role decides only what it does for the cluster, not what it can serve.
- Only one leader per LAN is allowed (§6.4).

### 3.2 Flags

Every value below has a flag and a default. No address or port may be hardcoded.

**Cluster role and identity**

| Flag | Default | Applies to | Meaning |
|---|---|---|---|
| `--leader` | false | serve | Run as leader. |
| `--worker` | false | serve | Run as worker. |
| `--node-id` | from state file | both | Stable node ID. If unset, generate once and store it (§5.4). |
| `--state-dir` | `~/.meridian` | both | Where `node-id` and other state are stored. |
| `--cluster-secret` | env `MERIDIAN_CLUSTER_SECRET` | both | Shared secret for signing discovery and control messages. Required in cluster modes. Never logged. |

**Network**

| Flag | Default | Applies to | Meaning |
|---|---|---|---|
| `--discovery-port` | 9999 | both | UDP port for `DISCOVER_LEADER` / `LEADER_FOUND`. |
| `--control-port` | 9000 | both | TCP port for registration, heartbeats, control. Must differ from `--port`. |
| `--host` | 0.0.0.0 | both | Bind address for the HTTP API. |
| `--port` | 8080 | both | HTTP API port. Leader serves the public API here. |
| `--advertise-ip` | auto-detect | both | IP other nodes should use to reach this node. Auto-detect picks the first non-loopback IPv4 and logs it. |
| `--leader-addr` | empty | worker | `host:port` of the control plane. Skips UDP discovery. Fallback for networks without broadcast. |
| `--discovery-timeout` | 60s | worker | Give up one discovery round after this long. The worker then retries with backoff. |

**Timing**

| Flag | Default | Applies to | Meaning |
|---|---|---|---|
| `--heartbeat-interval` | 5s | worker | How often the worker sends `HEARTBEAT` over TCP. |
| `--node-timeout` | 15s | leader | No heartbeat for this long marks a node offline (3 missed heartbeats). |
| `--health-interval` | 2s | leader | Readiness and liveness probe of each replica (`/health`). Reuses the existing probe. |
| `--discovery-interval` | 20s | leader | Full registry sweep: re-check every node and replica list, and expire stale entries. |
| `--reconnect-max-backoff` | 30s | worker | Upper bound for the exponential backoff with jitter. |

**Model runtime (same meaning as today)**

`--llm`, `--llama-bin`, `--models-dir`, `--threads`, `--ctx`, `--parallel`, `--ngl`, `--gpu-workers`, `--backend-lib`, `--gpu-device`, `--ram-headroom-mb`, `--restart-workers`, `--max-restarts`, `--routing-policy`, `--enable-ml`, `--ml-config`. Behavior is unchanged. See `CONTEXT.md` §7 for examples.

**Deprecated:** `--llm-model` and `--llm-workers` keep working for one release with a warning, as today.

### 3.3 Example commands

Leader with two local models:

```bash
export MERIDIAN_CLUSTER_SECRET='change-me'
./bin/meridian-server serve --leader \
  --llm qwen2.5-0.5b-instruct-q4_k_m.gguf:2 \
  --llm qwen2.5-3b-instruct-q4_k_m.gguf:1 \
  --llama-bin /usr/local/bin/llama-server --models-dir ~/meridian/models \
  --threads 8 --ctx 4096 --parallel 2 \
  --host 0.0.0.0 --port 8080 --control-port 9000 --discovery-port 9999 \
  --advertise-ip 192.168.1.10
```

Worker on another machine:

```bash
export MERIDIAN_CLUSTER_SECRET='change-me'
./bin/meridian-server serve --worker \
  --llm llama-3-8b-instruct-q4_k_m.gguf:1 \
  --llama-bin /usr/local/bin/llama-server --models-dir ~/meridian/models \
  --threads 4 --ctx 4096 --parallel 2 \
  --advertise-ip 192.168.1.11
```

Worker without broadcast (fallback):

```bash
./bin/meridian-server serve --worker --leader-addr 192.168.1.10:9000 ...
```

---

## 4. Process behavior

### 4.1 Leader startup

1. Parse and validate flags (§3). Refuse to start if the secret is missing.
2. Load or create `node-id`.
3. Start the registry (empty).
4. Start local replicas from `--llm` through the existing supervisor and pool. Each local replica is also a registry entry, owned by the leader's node ID.
5. Start the UDP discovery responder on `--discovery-port`.
6. Start the TCP control listener on `--control-port`.
7. Start the HTTP API on `--host:--port`.
8. Start the background loops: replica health probes (`--health-interval`), node lease expiry (`--node-timeout`), and the registry sweep (`--discovery-interval`).
9. Log: node ID, advertised IP, all three ports, and the number of local replicas.

### 4.2 Worker startup

1. Parse and validate flags. Refuse to start if the secret is missing.
2. Load or create `node-id`.
3. Start local replicas from `--llm`. Replicas report `Starting` until `/health` returns 200.
4. Run the worker state machine (§4.3).

### 4.3 Worker state machine

```
          +--------------------------------------------+
          |                                            |
          v                                            |
  DISCOVERING --(LEADER_FOUND valid)--> CONNECTING     |
      ^   |                                  |         |
      |   | (no leader; backoff+jitter)      | TCP ok  |
      |   +----------------------------------+         |
      |                                       v        |
      |                                  REGISTERING   |
      |                                       |        |
      |                          REGISTER_ACK ok        |
      |                                       v        |
      |                                  REGISTERED ---+ (heartbeat ack missing
      |                                       |          for --node-timeout,
      +------- connection lost / ack missing --+          or TCP error)
```

Rules:
- In `DISCOVERING`, send `DISCOVER_LEADER` by UDP broadcast. Retry with exponential backoff (start 1 s) plus jitter (±50%), capped at `--reconnect-max-backoff`.
- Stop sending `DISCOVER_LEADER` once `REGISTERED`. Do not broadcast again until the state returns to `DISCOVERING`.
- In `REGISTERED`, send `HEARTBEAT` every `--heartbeat-interval` on the same TCP connection.
- On any TCP error, go to `DISCOVERING` (or straight to `CONNECTING` if `--leader-addr` is set). Re-register the full current state. Do not keep stale replica lists.

### 4.4 Leader behavior when a worker is absent

- No heartbeat for `--node-timeout`: mark the node `offline`. Mark all of its replicas `unavailable`. Do not delete them, so their history stays visible.
- A node returning with the same `node-id` replaces its old session. Do not create a duplicate entry.
- A node that never heartbeats is removed after 3 × `--node-timeout`.

### 4.5 Local replicas (every node)

Leader and worker follow the same rules for their own models, so a node can serve as many models and instances as its memory allows, just like single-machine Meridian (`CONTEXT.md` §6.2).

- **Instances.** `--llm <file>:N` creates N replicas. Each replica is its own `llama-server` process with its own supervisor.
- **Replica IDs.** `<model>-<node-id[:6]>-<i>`, where `i` runs from 1 to N. Unique per node.
- **Ports.** Each replica binds `127.0.0.1` on a port taken from `--replica-port-base` (default 8100), using the next free port. Replica ports are never exposed on the LAN.
- **Admission.** Before starting, a node checks that the sum of its replica model sizes plus `--ram-headroom-mb` fits in its own `MemAvailable`. If it doesn't, startup is refused and the error lists the replicas that don't fit. Each node checks its own memory, not the leader's.
- **Failure isolation.** A crashed replica is restarted by its own supervisor (`--restart-workers`, `--max-restarts`) and is marked `unhealthy` on its own. The node stays `online` (§6.3).
- **LAN access.** Replicas are reached only through the node's HTTP port at `/replicas/<replica-id>/...` (decision D1). The node does no load balancing. It maps a replica ID to its local port and nothing more.
- **Leader's own replicas.** They use the same registry records and the same pool, and are reached in-process, without the HTTP hop.

---

## 5. Protocols

### 5.1 UDP discovery (leader discovery only)

Worker to LAN broadcast (`255.255.255.255:<discovery-port>`, sent with `SO_BROADCAST`):

```json
{
  "type": "DISCOVER_LEADER",
  "protocol_version": 1,
  "message_id": "b7c1f0e2-6a8d-4a2e-9d3c-1f7a2b9e0c11",
  "node_id": "node-01",
  "timestamp": "2026-11-23T10:00:00Z",
  "sig": "hex-hmac-sha256"
}
```

Leader reply, unicast to the sender's address and port:

```json
{
  "type": "LEADER_FOUND",
  "protocol_version": 1,
  "message_id": "…",
  "leader_id": "leader-01",
  "address": "192.168.1.10",
  "control_port": 9000,
  "timestamp": "2026-11-23T10:00:00Z",
  "sig": "hex-hmac-sha256"
}
```

**Note on ports:** the example in the earlier design used `tcp_port: 8080`. That is the HTTP port. This spec uses a separate `control_port` (default 9000) so the control plane and the public API never share a listener.

Rules:
- The worker verifies the HMAC on `LEADER_FOUND`. A reply with a bad signature is ignored. This stops a rogue responder from capturing registrations.
- The leader ignores `DISCOVER_LEADER` with a bad signature, and does not reply.
- Discovery payloads are at most 1 KiB. Larger datagrams are dropped.
- UDP is used **only** for these two messages. No heartbeats or inference over UDP.

### 5.2 TCP control channel

- Newline-delimited JSON (NDJSON). One JSON object per line, terminated by `\n`. TCP is a byte stream, so framing is required.
- Maximum line length: 1 MiB. Longer lines close the connection.
- Every message uses this envelope:

```json
{
  "type": "REGISTER",
  "protocol_version": 1,
  "message_id": "…",
  "timestamp": "2026-11-23T10:00:00Z",
  "node_id": "node-01",
  "payload": { },
  "sig": "hex-hmac-sha256"
}
```

- `sig` is HMAC-SHA256 with `--cluster-secret`, over the canonical JSON of the envelope with `sig` removed. Canonical means keys sorted and no extra whitespace.
- The leader rejects a message if `protocol_version` is not 1, the signature is wrong, or `timestamp` is more than ±30 s from its clock. This limits replay.
- Each `message_id` is unique. The leader remembers the last 1000 IDs from each node and rejects duplicates.

### 5.3 Message types

| Type | Direction | Purpose |
|---|---|---|
| `REGISTER` | worker → leader | Node info and replica list (§5.5). |
| `REGISTER_ACK` | leader → worker | Accepted. Includes `lease_seconds`. |
| `REGISTER_REJECT` | leader → worker | Refused. Includes `reason` (`bad_signature`, `protocol_mismatch`, `invalid_payload`, `duplicate_node`). The worker goes back to `DISCOVERING` after a backoff. |
| `HEARTBEAT` | worker → leader | Node stats, replica states, in-flight counts. |
| `HEARTBEAT_ACK` | leader → worker | Liveness confirmation. |
| `REPLICA_UPDATE` | worker → leader | Immediate update when a replica changes state (for example `Starting` → `Ready`, or crash). |
| `DEREGISTER` | worker → leader | Graceful shutdown. The leader marks the node offline immediately. |
| `ERROR` | either | Protocol error. Connection is closed after it is sent. |

### 5.4 Node identity

- `node-id` is stored in `<state-dir>/node-id`, created once with 128 random bits, hex encoded.
- The IP address is not part of the identity. It can change under DHCP.
- If `--node-id` is given, it overrides the file.

### 5.5 Payloads

`REGISTER` payload:

```json
{
  "address": "192.168.1.11",
  "http_port": 8080,
  "hardware": { "cpu_cores": 8, "ram_total_mb": 16384, "gpus": [] },
  "replicas": [
    {
      "id": "llama-8b-01",
      "model": "llama-8b",
      "instance": 1,
      "model_version": "q4_k_m",
      "kind": "llm",
      "runtime": "llama.cpp",
      "endpoint": "http://192.168.1.11:8080/replicas/llama-8b-01",
      "context_window": 4096,
      "max_concurrent": 2,
      "state": "ready"
    }
  ]
}
```

`HEARTBEAT` payload:

```json
{
  "cpu_percent": 37.5,
  "ram_used_mb": 9120,
  "replicas": [
    {
      "id": "llama-8b-01",
      "state": "ready",
      "in_flight": 1,
      "queue_depth": 0,
      "served": 412,
      "est_tokens_in_flight": 980,
      "tok_per_sec": 21.4,
      "last_error": ""
    }
  ]
}
```

Replica `state` values: `loading`, `ready`, `draining`, `unhealthy`, `stopped`.

Validation (leader): every field is type-checked. `id` and `model` are non-empty and at most 64 characters. `endpoint` must be a valid `http://` URL. A registration with duplicate replica IDs is rejected.

---

## 6. Registry

### 6.1 Entities

Two separate record types. Do not merge them.

**NodeRecord**

- `NodeID`, `Address`, `HTTPPort`, `ControlConn` (nil if local), `State` (`online` | `offline`), `LastHeartbeat`, `Hardware`, `CPUPercent`, `RAMUsedMB`, `ReplicaIDs []string`, `RegisteredAt`.

**ReplicaRecord**

- `ReplicaID`, `NodeID`, `Model`, `Instance` (1..N within its `--llm` entry), `ModelVersion`, `Kind` (`llm` | `ocr`), `Runtime`, `Endpoint`, `ContextWindow`, `MaxConcurrent`, `State` (`loading` | `ready` | `draining` | `unhealthy` | `stopped` | `unavailable`), `Healthy`, `InFlight`, `QueueDepth`, `Served`, `EstTokensInFlight`, `TokPerSec`, `LastCheck`, `LastError`.

A replica is `unavailable` when its node is offline. Its last reported state is kept for display.

### 6.2 Interface

```go
type Registry interface {
    UpsertNode(n NodeRecord) error
    RemoveNode(nodeID string) error
    MarkNodeOffline(nodeID string, at time.Time)
    UpdateNodeStats(nodeID string, stats NodeStats) error

    UpsertReplica(r ReplicaRecord) error
    UpdateReplicaState(replicaID string, s ReplicaState) error
    UpdateReplicaStats(replicaID string, stats ReplicaStats) error

    Nodes() []NodeRecord
    Replicas() []ReplicaRecord
    ReplicasForModel(model string) []ReplicaRecord
    ReplicasOnNode(nodeID string) []ReplicaRecord
    EligibleReplicas(model string) []ReplicaRecord // healthy, ready, under max_concurrent
    CountHealthy(model string) int
}
```

Implementation: in-memory, guarded by `sync.RWMutex`. Reads return copies, never internal pointers. Put behind this interface so a persistent store can be added later.

### 6.3 Rules

- The registry is the source of truth for scheduling. The router reads only from it.
- An unhealthy or non-ready replica is excluded from `EligibleReplicas` immediately.
- Failing one replica does not mark its node offline.
- Node offline does not delete replicas.

### 6.4 Single leader guard

- On startup, the leader listens for `LEADER_FOUND` messages from other leaders for 3 s (`--discovery-interval` is not used here). If one is seen, the leader exits with a clear error.
- A worker that receives `LEADER_FOUND` from two leaders uses the first one and logs a warning.

---

## 7. Health and heartbeats

| Check | Who | Interval | Rule |
|---|---|---|---|
| Replica readiness and liveness | leader probes each replica `GET /health` | `--health-interval` (2 s) | 200 → `ready`. 503 → `loading`. 3 consecutive failures → `unhealthy`. One success → back to `ready`. |
| Node heartbeat | worker sends `HEARTBEAT` | `--heartbeat-interval` (5 s) | Refreshes the node lease. |
| Node lease | leader | checked every `--health-interval` | No heartbeat for `--node-timeout` (15 s) → node `offline`. |
| Registry sweep | leader | `--discovery-interval` (20 s) | Re-check node list, remove nodes gone for 3 × `--node-timeout`, log counts. |

Liveness is "process is running". Readiness is "model loaded and can serve". A replica in `loading` must not receive requests.

A worker reports replica state changes immediately with `REPLICA_UPDATE`, without waiting for the next heartbeat.

---

## 8. HTTP API (leader, public)

Keep the existing API shapes from `internal/api/types.go`.

| Endpoint | Stage | Notes |
|---|---|---|
| `GET /healthz` | 1 | Leader process liveness. |
| `GET /v1/status` | 2 | Returns nodes and replicas (`NodeStatus`, `ReplicaStatus`). The existing `StatusResponse` stays as an alias for one release. |
| `GET /v1/models` | 3 | Models with at least one ready replica. |
| `GET /v1/catalog` | 3 | All configured models with replica counts. |
| `POST /v1/chat/completions` | 4 | Routing and proxying (later stage). |
| `GET /v1/policies`, `PUT /v1/policies/default` | 4 | Existing. |

Workers expose no public API. Their HTTP port serves only `/healthz` and the `/replicas/<replica-id>/...` path used by the leader (§4.5, decision D1).

Internal control messages go only over the TCP control port, never over HTTP.

### 8.1 Request flow (leader only)

The React UI is hosted by the leader. The browser talks only to the leader's HTTP API, and load balancing happens only on the leader, using the registry.

```text
Browser (React UI, served by the leader)
     |  POST /v1/chat/completions
     v
Leader HTTP API
  1. Read the requested model from the body.
  2. registry.EligibleReplicas(model)        <- the only source of truth
  3. Routing policy picks ONE replica        <- local replicas included
  4. Reserve a slot on that replica (atomic)
  5a. Local replica:  call its 127.0.0.1 endpoint in-process
  5b. Remote replica: proxy to http://<node-ip>:<http-port>/replicas/<replica-id>/v1/chat/completions
  6. Stream the reply back to the browser as it arrives
  7. Release the slot, update the replica's stats, log the request
```

Rules:
- **Only the leader chooses.** The leader picks a replica, and the node is implied by that replica's record. A worker never chooses between its own replicas for a cluster request. It only maps the replica ID to the local port.
- **The browser never contacts a worker.** The UI's model list comes from `/v1/catalog` and its status from `/v1/status`, both on the leader.
- **Workers accept `/replicas/...` only from the leader.** Each forwarded request carries a leader-signed header (HMAC with `--cluster-secret`, plus a timestamp). Any other caller gets `403`.
- **Conversation stickiness.** `X-Conversation-ID` pins a conversation to its replica while that replica is eligible. If the replica is no longer eligible, the leader moves the conversation (existing session logic, `CONTEXT.md` §6.5).
- **Retry.** A failed forward may be retried once on another eligible replica, only before any response bytes have reached the browser. Never retry after streaming has started.
- **Streaming.** The leader passes each chunk through as it arrives. It does not buffer the full response.
- **Errors.** No eligible replica for the model: `503 no_healthy_worker`. Context too large for every eligible replica: `413 context_too_long`. Unknown model: `404 model_not_found`.

---

## 9. Package layout

```text
cmd/meridian-server/main.go        subcommand and role dispatch only (no logic)
internal/config/config.go          flag struct, validation, defaults, env fallbacks
internal/discovery/protocol.go     DISCOVER_LEADER / LEADER_FOUND types and HMAC
internal/discovery/udp.go          leader responder, worker broadcaster
internal/control/protocol.go       NDJSON envelope, canonical JSON, HMAC, validation
internal/control/leader.go         TCP listener, per-node session, ACK/REJECT, replay guard
internal/control/worker.go         worker state machine, reconnect, heartbeat sender
internal/registry/registry.go      Registry interface and in-memory implementation
internal/registry/node.go          NodeRecord
internal/registry/replica.go       ReplicaRecord
internal/health/monitor.go         replica probes, node lease expiry, sweep
internal/balancer/…                existing pool, one supervisor per replica, routing (reused)
internal/api/types.go              shared JSON types (extended)
```

`main.go` must stay thin. Each role is a function that takes a config and runs until the context is cancelled.

Use `context.Context` for shutdown. On SIGINT or SIGTERM: the worker sends `DEREGISTER`, the leader stops listeners, and both wait for goroutines to finish before exiting. Timeout 10 s.

---

## 10. Stages

### Stage 1: CLI and mode dispatch (`cmd/meridian-server`, `internal/config`)

Work:
- Add `serve --leader`, `serve --worker`, and the flags in §3.2.
- Add `internal/config` with validation: mutual exclusion, required secret in cluster modes, port collision check (`--control-port` ≠ `--port` ≠ `--discovery-port`), and `--leader-addr` format.
- Print the resolved configuration at startup, with the secret redacted.
- Keep the existing single-machine path unchanged.

Acceptance:
- `serve --help` lists the new flags.
- `serve --leader --worker` exits with code 2 and a clear message.
- `serve --leader` without a secret exits non-zero.
- `serve --leader --port 9000 --control-port 9000` is rejected.
- `serve` with no role flag passes all existing tests unchanged.
- Startup refuses to run when the node's replicas exceed its own `MemAvailable` minus `--ram-headroom-mb`. The error lists the replicas that don't fit.
- Config unit tests cover every validation rule.

### Stage 2: Leader (`internal/discovery`, `internal/control/leader.go`, `internal/registry`, `internal/health`)

Work:
- UDP responder: verify `DISCOVER_LEADER` signature, reply with a signed `LEADER_FOUND`.
- TCP listener: accept connections, read NDJSON, validate envelope, verify HMAC, check timestamp window and replay cache.
- Handle `REGISTER` → validate → upsert node and replicas → `REGISTER_ACK`. Handle `REGISTER_REJECT` with reasons.
- Handle `HEARTBEAT` → update node stats and replica stats → `HEARTBEAT_ACK`.
- Handle `REPLICA_UPDATE` and `DEREGISTER`.
- Registry implementation and tests.
- Lease expiry and sweep loop.
- Local replicas registered from `--llm` on startup.
- `GET /v1/status` returns the registry.
- Single-leader guard (§6.4).

Acceptance:
- A leader started with `--leader` answers a signed discovery probe and ignores a badly signed one.
- A test client that sends a valid `REGISTER` gets `REGISTER_ACK`. A bad signature gets `REGISTER_REJECT`.
- After `REGISTER`, `GET /v1/status` lists the node and its replicas.
- Stopping heartbeats for `--node-timeout` marks the node offline and its replicas `unavailable`.
- A duplicate `message_id` is rejected.
- Local `--llm` replicas appear in `/v1/status` under the leader's node ID.
- `--llm qwen.gguf:2` on the leader produces two replicas with distinct IDs and distinct loopback ports, both listed under the leader's node ID.

### Stage 3: Worker (`internal/discovery`, `internal/control/worker.go`)

Work:
- Worker state machine (§4.3) with backoff and jitter.
- UDP broadcaster: send `DISCOVER_LEADER` until `LEADER_FOUND` is verified. Stop after registration.
- `--leader-addr` path skips UDP.
- TCP client: connect, send `REGISTER`, wait for `REGISTER_ACK`, then heartbeats.
- Reconnect and re-register. Re-send full state.
- `REPLICA_UPDATE` on replica state change.
- `DEREGISTER` on shutdown.
- Local `--llm` replicas start and report `loading` → `ready`.

Acceptance:
- Worker finds the leader on the loopback test setup (§12) and reaches `REGISTERED`.
- Worker stops sending `DISCOVER_LEADER` once registered (checked by counting packets in a test).
- Killing the leader process: worker returns to `DISCOVERING` and re-registers after the leader restarts, without restarting the worker.
- Killing the worker process: leader marks its node `offline` within `--node-timeout` + one check interval.
- A replica that is still loading is shown as `loading` and is not `Healthy`.
- Two workers on one machine with different `--node-id` values both appear in the registry.
- A worker started with `--llm a.gguf:2 --llm b.gguf:1` reports three replicas. Killing one `a` replica marks only that replica `unhealthy`. The node stays `online`, and the other two replicas keep serving.
- A worker whose replicas exceed its own memory refuses to start, even when the leader has room.

### Stage 4: Routing and proxy (leader only)

Work:
- Route `POST /v1/chat/completions` through the registry (§8.1). Routing policies are reused from `internal/balancer/routing.go` and work on `[]ReplicaRecord`.
- Reserve a slot atomically before forwarding. Release it when the request ends or fails.
- Local replicas: in-process call to the loopback endpoint.
- Remote replicas: HTTP proxy to `/replicas/<id>/v1/chat/completions` with the leader-signed header.
- Worker side: a `/replicas/<id>/` handler that verifies the leader signature and forwards to the local loopback port. It performs no selection.
- SSE pass-through with flushing per chunk.
- Retry once before headers, on a different replica.
- `GET /v1/models`, `GET /v1/catalog`, `GET /v1/status`, and the existing policy endpoints, all on the leader.
- Serve the React UI from the leader only.

Acceptance:
- A model served by a leader replica and a worker replica receives requests from both, under round-robin.
- A request for a model with no ready replica returns `503 no_healthy_worker`. An unknown model returns `404 model_not_found`.
- A forwarded request without a valid leader signature to a worker's `/replicas/...` returns `403`.
- The first streamed token reaches the browser before the full generation finishes (checked by timing a streamed reply through the leader).
- Killing a worker before its response starts causes one retry on another replica. Killing it after the first token returns a clean error and does not retry.
- The browser, given only the leader's address, can list models, view status, and chat. No request from the browser goes to a worker address.

---

## 11. Decisions and defaults

These are open. Use the default unless the user has said otherwise.

| ID | Question | Default | Reason |
|---|---|---|---|
| D1 | How does the leader reach a replica on a worker? | **Per-replica path on the node's HTTP port**: `/replicas/<replica-id>/...` maps to that replica's loopback port. Replica ports are never exposed on the LAN. | One LAN port per node, however many replicas it runs. Node health and in-flight counts come from the same agent. The node does no balancing, so cluster routing stays on the leader (§4.5). Stage 4. |
| D2 | Discovery transport. | UDP broadcast (`255.255.255.255`), as in the design. mDNS is a later option. | Simple, no dependency. Does not cross subnets, so `--leader-addr` exists. |
| D3 | Join security. | HMAC-SHA256 on every discovery and control message with `--cluster-secret`. Replay window ±30 s and a 1000-ID cache. | Prevents rogue workers and rogue leaders. Still no TLS, so prompts travel in plain text on the LAN. |
| D4 | Liveness probe interval. | 2 s replica probe, 15 s node lease, 20 s sweep. | 20 s alone would take about 60 s to detect a dead worker. The 2 s probe reuses existing behavior. |
| D5 | Node ID persistence. | Random ID in `<state-dir>/node-id`. | Survives restarts and DHCP changes. |
| D6 | Leader serves its own replicas. | Yes, through the existing pool. Local is a tie-breaker in routing, not a special case in the handler. | Matches the requirement that the leader is also a worker. |
| D7 | Config file. | Flags only in this stage. `cluster.yaml` is an optional later addition. | Keeps Stage 1 small. |
| D8 | Replica port allocation. | Loopback only, from `--replica-port-base` (default 8100), next free port. | Avoids port conflicts between replicas on one node, and keeps replica ports off the LAN. |
| D9 | Memory admission. | Each node checks its own `MemAvailable` before starting its replicas. | Each machine has different RAM. A check on the leader says nothing about a worker. |

---

## 12. Testing

### Unit tests (no network)
- Canonical JSON and HMAC: same input → same signature; changed field → different signature; wrong secret → rejected.
- Envelope validation: wrong `protocol_version`, missing `node_id`, oversized line, timestamp outside ±30 s.
- Replay cache: duplicate `message_id` rejected, cache bounded at 1000.
- Registry: concurrent upsert, read, and state update under `-race`. Reads return copies.
- `EligibleReplicas`: excludes `loading`, `unhealthy`, `draining`, `unavailable`, and replicas at `max_concurrent`.
- Worker backoff: sequence grows, is capped, and includes jitter.
- Config: every rule in Stage 1.

### Integration tests (localhost, multiple processes)
- Leader on `--discovery-port 19999 --control-port 19000 --port 18080`.
- Worker on `--discovery-port 19999 --leader-addr 127.0.0.1:19000` (UDP fallback test uses broadcast on loopback, which may be unsupported; document the result).
- Use a fake `llama-server` (a small Go test binary that serves `/health`) so tests do not need real models.
- Scenarios: registration, heartbeat, leader restart, worker kill, node timeout, duplicate node ID, bad secret.

### Manual LAN test (after Stage 3)
- 2 physical machines. Leader and worker on the same subnet.
- Start the worker before the leader, then the other way round.
- Unplug the worker's network. Plug it back in. Confirm re-registration and that no leader restart is needed.

---

## 13. Constraints

1. Go only for backend and cluster code. Standard library first. Add a dependency only if it removes real complexity, and record it.
2. UDP only for `DISCOVER_LEADER` and `LEADER_FOUND`.
3. TCP NDJSON for control. HTTP for inference and the public API.
4. Only the leader load-balances. It picks a replica from the registry for every request. Workers never choose between nodes, and the React UI and other clients talk only to the leader's HTTP API.
5. Node and replica are separate records.
6. Unhealthy or loading replicas never receive new requests.
7. No hardcoded addresses or ports. Every timing value is a flag.
8. No Kubernetes, Kafka, etcd, Consul, or external registry.
9. Secrets are never logged or written to `/status`.
10. `go test -race` and `go vet` pass after each stage.

---

## 14. Non-goals for this spec

- Routing and proxying of `POST /v1/chat/completions` to remote replicas is Stage 4 (§10). The request flow is in §8.1.
- The React dashboard changes (Stage 5).
- Leader election, consensus, or a standby leader.
- TLS or certificate management.
- Model sharding across machines.
- Persistent registry storage.
- Mid-stream resume.

---

## 15. Deliverables for the three stages

1. `cmd/meridian-server/main.go` with role dispatch.
2. `internal/config`, `internal/discovery`, `internal/control`, `internal/registry`, `internal/health` with tests.
3. Updated `internal/api/types.go` with `NodeStatus` and `ReplicaStatus`.
4. `docs/protocol.md` with the message table and examples from §5.
5. A short run guide for a 2-machine cluster appended to `docs/deployment.md`.
