# Cluster protocol

Wire protocol between a Meridian leader and its workers. Spec: `docs/spec-leader-worker-cluster.md` §5.

## Transports

| Transport | Port flag | Default | Used for |
|---|---|---|---|
| UDP broadcast / unicast | `--discovery-port` | 9999 | `DISCOVER_LEADER`, `LEADER_FOUND` only |
| TCP NDJSON | `--control-port` | 9000 | Registration, heartbeats, replica updates |
| HTTP | `--port` | 8080 | Public API (leader). `/healthz` on workers. |

UDP carries nothing else. Control messages never travel over HTTP.

## Signing and framing

- Every discovery and control message carries `sig`: HMAC-SHA256 with `--cluster-secret`, hex encoded.
- The signed bytes are the canonical JSON of the message with `sig` removed. Canonical means keys sorted, no whitespace.
- TCP is NDJSON: one JSON object per line, terminated by `\n`. A line over 1 MiB closes the connection.
- The leader checks `protocol_version == 1`, the signature, and that `timestamp` is within ±30 s. A `message_id` seen in the last 1000 messages from the same node is rejected.
- Discovery datagrams are at most 1 KiB. Larger ones are dropped.

## Discovery (UDP)

Worker probe, sent to `255.255.255.255:<discovery-port>`:

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

Leader reply, unicast to the sender. `control_port` is the TCP control port, not the HTTP port:

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

A leader ignores a probe from its own node ID. A worker ignores a `LEADER_FOUND` whose signature is wrong. A worker that hears two leaders uses the first and logs a warning.

## Control envelope (TCP)

```json
{
  "type": "REGISTER",
  "protocol_version": 1,
  "message_id": "…",
  "timestamp": "2026-11-23T10:00:00Z",
  "node_id": "node-01",
  "payload": {},
  "sig": "hex-hmac-sha256"
}
```

## Message types

| Type | Direction | Purpose |
|---|---|---|
| `REGISTER` | worker → leader | Node info and full replica list. Always the complete current state. |
| `REGISTER_ACK` | leader → worker | Accepted. `lease_seconds` is the node timeout. |
| `REGISTER_REJECT` | leader → worker | Refused, then the connection closes. `reason` is one of `bad_signature`, `protocol_mismatch`, `invalid_payload`, `duplicate_node`. |
| `HEARTBEAT` | worker → leader | Node stats and each replica's live numbers. |
| `HEARTBEAT_ACK` | leader → worker | Liveness confirmation. |
| `REPLICA_UPDATE` | worker → leader | One replica changed state, sent without waiting for a heartbeat. |
| `DEREGISTER` | worker → leader | Graceful shutdown. The node goes offline at once. |
| `ERROR` | either | Protocol error. `code` and `message`. The connection closes after it. |

`REGISTER_REJECT` details:

- `duplicate_node`: the same `node_id` is already online over another live connection. An offline node with a stale connection is replaced, not rejected.
- A stale timestamp or a replayed `message_id` on `REGISTER` is reported as `invalid_payload`, with the cause in `detail`.

## Payloads

`REGISTER`:

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

`HEARTBEAT`:

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

Replica `state` values: `loading`, `ready`, `draining`, `unhealthy`, `stopped`. The leader adds `unavailable` when the node is offline.

Validation on the leader rejects a `REGISTER` with: an unknown field, a non-IP `address`, a port outside 1–65535, a replica `id` or `model` that is empty or longer than 64 characters, an `endpoint` that is not `http://` with a host, an unknown `state`, or a duplicate replica `id`.

## Worker state machine

```
DISCOVERING --(LEADER_FOUND verified)--> CONNECTING --> REGISTERING --(ACK)--> REGISTERED
     ^                                                                            |
     +------------------- TCP error, missing heartbeat ack, REJECT ---------------+
```

- Probes are sent with backoff starting at 1 s, with ±50% jitter, capped at `--reconnect-max-backoff`.
- Probing stops once registered. It starts again only after the state returns to DISCOVERING.
- With `--leader-addr`, the worker skips UDP and goes straight to CONNECTING.
- A worker whose heartbeat ack is missing for the lease length (from `REGISTER_ACK`) reconnects.

## Leader state

- Node and replica records are separate (`internal/registry`).
- A node is `offline` after `--node-timeout` with no heartbeat, or when its connection closes. Its replicas become `unavailable` and keep their last state.
- A node silent for three node timeouts is removed by the registry sweep, along with its replicas.
- A returning node with the same `node_id` replaces its old session. It does not create a second entry.
