# Meridian: Project Description

Meridian is a self-hosted AI serving platform written in Go. It runs
open-weight language models (GGUF files served by `llama.cpp`'s
`llama-server`) across one or more machines on a local network, and presents
them to clients through a single OpenAI-compatible HTTP API.

It is built for small offices, labs and homes that have several ordinary
computers, some with GPUs, and want private AI without a cloud account or a
dedicated GPU server. Meridian ships as one binary with an embedded web UI, and
has no runtime dependency on Python, Kubernetes or a database.

This document covers the LLM serving system and the cluster that runs it. OCR
(the document subsystem) is out of scope here.

---

## 1. The problem

A private AI service on ordinary hardware runs into four problems that the
usual tools do not handle well together.

1. **Heterogeneous machines.** One machine has a GPU, another has eight CPU
   cores and 8 GB RAM, a third is a laptop that sleeps. Their speed and memory
   differ by an order of magnitude.
2. **Load-blind balancing.** A generic balancer such as NGINX's round-robin or
   least-connections sees each request as one unit. A 20-token question and a
   4,000-token document look the same, and so does a fast GPU and a slow CPU.
   The result is long tail latency: a 35-second median can coexist with a
   234-second p95.
3. **Node failure.** A lab machine can drop off the network mid-conversation.
   Without health tracking and failover, the client sees an error.
4. **Operational cost.** Cloud routers such as LiteLLM optimise for dollars per
   token. On owned hardware the real cost is RAM and seconds of machine time.
   Distributed serving frameworks such as Ray Serve need Python and an
   orchestrator, which is more than a three-machine office should run.

Meridian addresses these with a single Go binary, measured throughput for
routing, health tracking with failover, and resource admission control.

---

## 2. What it does

- Serves **many models at once** from one gateway. Each model can have several
  **replicas** (independent `llama-server` processes), and replicas can run on
  different machines.
- Exposes an **OpenAI-compatible** `POST /v1/chat/completions` (streaming and
  non-streaming), `GET /v1/models`, plus its own status, policy and catalog
  endpoints.
- Chooses a replica for every request using a **pluggable routing policy**.
  Policies can be changed at runtime, per request, or by the admin.
- Keeps **conversations sticky**: a conversation stays on the replica that
  holds its context, while that replica is healthy.
- Detects unhealthy replicas and dead machines, takes them out of rotation,
  and retries a failed request once on another replica when no bytes have yet
  reached the client.
- Supervises each `llama-server` process: starts it, restarts it with
  exponential backoff, and gives up after a configurable number of failures.
- Checks **memory before starting**, so a machine never loads more model
  weights than it can hold alongside the operating system.
- Provides a **web UI** (chat, model and policy pickers, cluster status) and an
  **admin console** (live routing events, per-node view), both embedded in the
  binary.

---

## 3. Architecture

Meridian has three ways to run. They use the same code.

### 3.1 Standalone

One machine, one `meridian-server serve` process. The process starts the
`llama-server` replicas for each `--llm` entry, and a balancer in front of them.
Clients talk to port 8080.

```
client --> meridian-server :8080 --> balancer --> llama-server :8081
                                       |        --> llama-server :8082
                                       +------> llama-server :8083 ...
```

### 3.2 Cluster: leader and workers

Several machines run the same binary. One is the **leader**; the rest are
**workers**. The leader is the only public entry point and is the only node
that makes routing decisions.

```
client --> LEADER :8080 (gateway, routing policy, registry, admin console)
              |  local replicas: llama-server (its own models)
              |
              |  control channel (TCP :9000, signed NDJSON)
              |  registration, heartbeats, state changes
              v
           WORKER A :8080 (replica endpoint, /healthz)
              llama-server x N           <- leader forwards requests here
           WORKER B :8080
              llama-server x M
```

Key points:

- **Workers hold no routing logic.** They report their replicas and
  accept requests that the leader has already routed to them, signed so that
  only the leader can send them.
- **Every node can serve models.** A node's role decides only its place in the
  cluster, not what it may serve.
- **Discovery is automatic on a LAN.** A worker broadcasts a signed UDP probe
  and the leader answers. A worker that cannot broadcast can be pointed at the
  leader with `--leader-addr`.
- **One leader per LAN.** A new leader refuses to start if it finds another.

### 3.3 The main parts

| Part | Responsibility |
|---|---|
| `cmd/meridian-server` | Command line: `serve` (standalone, `--leader`, `--worker`), `version`. |
| `internal/balancer` | Standalone LLM balancer: worker pool, health state machine, routing policies, sessions, supervisor, HTTP handler. |
| `internal/cluster` | Leader gateway (`/v1/chat/completions`, `/v1/status`, admin API), worker role, replica proxy. |
| `internal/registry` | In-memory record of nodes and replicas, with atomic in-flight reservation. The leader's only scheduling state. |
| `internal/control` | Signed TCP control protocol between leader and workers: registration, heartbeats, state changes. |
| `internal/discovery` | Signed UDP discovery of the leader on the LAN. |
| `internal/health` | Leader's background loops: local replica sync, node lease expiry, registry sweep. |
| `internal/config` | Flag parsing and validation, node ID, and memory admission. |
| `internal/tokens` | Rough token estimate (about 4 characters per token) used for routing. |
| `internal/metrics` | JSON-lines request log. It records no message content. |
| `internal/webui` | Embedded production build of the React UI. |
| `web/` | React 19 + TypeScript + Vite source for the UI. |
| `cmd/meridian-stress` | Multi-turn load generator with latency percentiles. |
| `cmd/meridian-bench` | Routing-policy benchmark: sweeps policies and concurrency, writes an HTML report. |

---

## 4. Request life cycle

For a chat request on the leader:

1. **Validate.** Reject bodies over 1 MiB, invalid JSON, a missing model, or a
   model no replica serves (`404 model_not_found`).
2. **Estimate size.** `tokens.ForRequest` estimates prompt and output tokens.
3. **Filter.** Keep only replicas that are ready, healthy, on an online node,
   and below their parallel-slot limit. Drop replicas whose per-slot context
   (`ctx / parallel`) is too small for the estimated request. If none fit,
   return `413 context_too_long`.
4. **Choose.** The system policy (or the per-request policy in standalone mode)
   picks one candidate from the filtered set.
5. **Reserve.** The registry atomically increments that replica's in-flight
   count if it is still eligible. If another request filled it first, the
   pick is redone (up to five times). This prevents two concurrent requests
   from overbooking a slot.
6. **Forward.** For a remote replica the leader sends the request to the
   worker's replica endpoint, signed with the cluster secret. For a local
   replica it sends straight to `127.0.0.1`. The reply is relayed to the
   client as it arrives.
7. **Retry once.** If the replica fails before any byte reaches the client
   (connection error or 5xx), the leader releases the reservation and tries
   one other replica. After the first byte, no retry is possible and the
   client sees the error.
8. **Record.** The response carries `X-Meridian-Worker` (which replica served
   it), `X-Meridian-Model`, `X-Meridian-Routing-Policy`, and
   `X-Meridian-Failover` if a retry happened. Counters update and a route event
   is published to the admin stream.

Conversations use `X-Conversation-ID`. A conversation stays on its replica
while that replica is healthy. A second request for the same conversation while
one is still running gets `409 conversation_busy`.

---

## 5. Routing policies

A policy is a function from the set of eligible replicas (with their load and
measured speed) to one chosen replica. Every policy is available in standalone
and cluster mode.

| Policy | Rule | Good for | Weakness |
|---|---|---|---|
| `round-robin` (default) | Rotate through eligible replicas in order, separately per model. | Identical replicas, even traffic. | Ignores load and speed. Sends work to a slow replica as often as a fast one. |
| `random` | Pick uniformly at random. | Simple baseline; avoids herding. | Uneven by chance. |
| `least-connections` | Pick the replica with the fewest requests in flight. | Mixed request sizes; keeps busy replicas from piling up. | Counts requests, not work. A tiny question and a long document weigh the same. |
| `token-aware` (TW-LEL) | Estimate each replica's finish time for the new request: `C = (F·(I+Ō) + I + O) / R`, where F is queued requests, I and O are estimated input and output tokens, Ō the model's average output length, and R the replica's measured tokens per second. Pick the lowest. | Heterogeneous hardware and mixed request sizes. | A heuristic, not an exact queue model. Depends on a good tokens-per-second signal. |

Policies can be selected three ways:

- **System default:** `PUT /v1/policies/default` (admin token required in
  cluster mode). This is what the leader uses for all traffic unless overridden.
- **Per request:** the `X-Meridian-Routing-Policy` header (honoured in
  standalone mode; the cluster leader uses its system default).
- **Web UI:** a policy picker per conversation.

### Measured throughput

`token-aware` needs a tokens-per-second figure for each replica. In standalone
mode the figure is refreshed from each completed request (an exponentially
weighted average). In the cluster, the current gateway reads it from the
registry, which is updated by heartbeats. When that figure is flat across
replicas, `token-aware` behaves like `least-connections`. The benchmark tool
reports this condition explicitly so it is not mistaken for a result.

---

## 6. Health, failure and recovery

### Replicas (single machine and workers)

Each replica moves through three states:

- **Starting:** not routed to until the first successful `/health` probe
  (probes every 2 s, within a readiness grace period).
- **Healthy:** routed to.
- **Unhealthy:** after three consecutive failed probes, or immediately when the
  process exits. Routed to again after one successful probe.

A process that exits is restarted by its supervisor with exponential backoff
from 1 s to 30 s. After `--max-restarts` consecutive failures (default 5) it is
left down. A run lasting at least a minute resets the count. Shutdown never
restarts.

### Nodes (cluster)

- Each worker sends a heartbeat every `--heartbeat-interval` (default 5 s) with
  CPU, RAM and the live numbers of each replica.
- A node with no heartbeat for `--node-timeout` (default 15 s) is marked
  offline, and its replicas become unavailable.
- An offline node that stays silent for three timeouts is removed from the
  registry. It re-registers itself when it comes back.
- A worker that loses the leader reconnects with jittered exponential backoff
  (capped by `--reconnect-max-backoff`).

Failover in practice: a replica or node failure is detected within roughly
one to three health intervals, traffic moves to remaining replicas, and a
request in flight at the moment of failure is retried once if no output has
been sent. Streamed output that has already started cannot be resumed; the
client receives an error and the next turn is routed normally.

### Known limits

- **Leader is a single point of failure.** If it stops, the cluster stops
  accepting requests until it is restarted. Workers keep their models loaded.
  There is no election.
- **Sessions are in memory** and expire after 30 minutes. A leader restart
  loses conversation affinity, and clients must resend history.
- **No node-level heartbeat separate from replicas.** The cluster cannot
  always tell "machine off" from "llama-server crashed" without looking at the
  node state.
- **No authentication or TLS.** Intended for a trusted LAN. The cluster secret
  signs control traffic; the public API is open unless fronted by a proxy.
- **Token counts are estimates** (about 4 characters per token) for routing.
  Reported usage comes from `llama-server` when it provides it.

---

## 7. Memory admission

Each machine checks its own memory before starting models. The check reads
available RAM from the operating system and refuses to start if the planned
set of model weights plus the configured headroom (`--ram-headroom-mb`) does
not fit. The same check runs in every role, so a worker with too many models
is rejected at startup with a clear message rather than swapping or crashing
later.

Model size is taken from the file on disk, so admission reflects what will
actually be loaded.

---

## 8. Interfaces

### HTTP API (leader, port 8080)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/chat/completions` | Chat completion, optionally streamed (`"stream": true`). |
| `GET` | `/v1/models` | Models with at least one healthy replica. |
| `GET` | `/v1/catalog` | Models with state, replica counts and context size, plus policies. |
| `GET` | `/v1/status` | Cluster view: nodes and replicas, in-flight, served, tokens per second, last check. |
| `GET` | `/status` | Public per-replica status (no addresses or process IDs). |
| `GET` | `/policies` | Available policies and the current default. |
| `PUT` | `/v1/policies/default` | Change the default policy (admin token in cluster mode). |
| `GET` | `/v1/admin/cluster` | Full admin view with addresses and PIDs (admin token). |
| `GET` | `/v1/admin/events` | Live route events as server-sent events (admin token). |
| `GET` | `/healthz` | Liveness. |

Worker: `GET /healthz`, and a signed `POST /replicas/{id}/...` used only by the
leader.

### Command line

```bash
meridian-server serve                # standalone
meridian-server serve --leader ...   # cluster leader
meridian-server serve --worker ...   # cluster worker
meridian-server version
```

Important flags: `--llm model.gguf:N` (repeat per model; `N` is the number of
replicas), `--llama-bin`, `--models-dir`, `--threads`, `--ctx`, `--parallel`
(slots per replica), `--ngl` and `--gpu-workers` (GPU offload), `--routing-policy`,
`--ram-headroom-mb`, `--admin-token`, `--cluster-secret` (or
`MERIDIAN_CLUSTER_SECRET`), `--advertise-ip`, `--leader-addr`, and the timing
flags for heartbeats and health.

### Web UI

A React single-page app embedded in the binary, served at `/`:

- **Chat:** streamed replies, model and policy pickers, Markdown rendering,
  per-conversation history (sent with each request).
- **Cluster:** node and replica status, and the policy in effect.
- **Admin console** (admin token): live route events, showing the path
  leader → node → replica for each request.

---

## 9. Testing and measurement

- **Unit and integration tests** cover the balancer, routing policies, the
  health state machine, session affinity, the registry's reservation logic,
  the signed control protocol, discovery, and the leader's gateway, including
  failover. Run with `go test ./...`.
- **`meridian-stress`** runs multi-turn conversations at fixed concurrency and
  reports p50, p95 and p99 latency, tokens per second, and failure rates by
  status.
- **`meridian-bench`** is the routing-policy benchmark. It runs the same
  workload under each policy at each concurrency level, polls the leader's
  status during each run, and writes `report.html`, `report.md` and CSV/JSON.
  It reports end-to-end and time-to-first-token latency, goodput, per-replica
  request share, fairness across replicas of one model, and server-side
  in-flight, tokens per second, CPU and RAM. It warns when a result is not
  meaningful (for example, when no model runs on more than one node, so
  cross-node routing was not exercised). See `docs/bench.md`.

---

## 10. Build and run

Requirements: Go 1.22 or later, `llama-server` (from llama.cpp) on each machine,
GGUF model files in one directory. Node 18+ is needed only to rebuild the web UI.

```bash
go build -o bin/meridian-server ./cmd/meridian-server
go build -o bin/meridian-bench  ./cmd/meridian-bench

# standalone
./bin/meridian-server serve --llm qwen2.5-1.5b-instruct-q4_k_m.gguf:2 \
  --models-dir ./models --llama-bin llama-server --threads 4 --parallel 2 --ctx 4096

# cluster
export MERIDIAN_CLUSTER_SECRET='a-long-shared-secret'
./bin/meridian-server serve --leader --admin-token "$MERIDIAN_ADMIN_TOKEN" \
  --llm qwen2.5-0.5b-instruct-q4_k_m.gguf:2 --models-dir ./models --llama-bin llama-server
./bin/meridian-server serve --worker --advertise-ip 192.168.1.103 \
  --llm qwen2.5-1.5b-instruct-q4_k_m.gguf:1 --models-dir ./models --llama-bin llama-server
```

---

## 11. Scope and status

Implemented: standalone serving with multiple models and replicas, four routing
policies, sticky sessions, health tracking and retry, supervised restart,
memory admission, the leader/worker cluster with signed discovery and control
channels, the admin console and web UI, and the benchmark tools.

Not implemented: leader election or standby, model sharding across machines,
TLS, authentication on the public API, persistent sessions, mid-stream resume,
and image generation.
