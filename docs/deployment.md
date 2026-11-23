# Deployment

## Single machine (unchanged)

```bash
./bin/meridian-server serve --llm qwen2.5-0.5b-instruct-q4_k_m.gguf:2 --models-dir ~/meridian/models
```

Without `--leader` or `--worker`, the server behaves exactly as before. Cluster flags are rejected in this mode.

## Two or three machines on one LAN

Use the same secret on every machine. It signs all cluster messages, so each node must be given it. Do not put it in shell history on a shared machine; `MERIDIAN_CLUSTER_SECRET` is the usual choice.

Every machine needs:

- the same `llama-server` build and the same GGUF files under `--models-dir`
- the same `--cluster-secret` (or `MERIDIAN_CLUSTER_SECRET`)
- a fixed IP or a DHCP reservation, passed as `--advertise-ip` if auto-detect picks the wrong interface

### Leader (machine A, 192.168.1.10)

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

The leader starts after a 3 s check. If another leader answers, it exits with an error. It logs its node ID, its three ports, and its local replica count.

### Worker (machine B, 192.168.1.11)

```bash
export MERIDIAN_CLUSTER_SECRET='change-me'
./bin/meridian-server serve --worker \
  --llm llama-3-8b-instruct-q4_k_m.gguf:1 \
  --llama-bin /usr/local/bin/llama-server --models-dir ~/meridian/models \
  --threads 4 --ctx 4096 --parallel 2 \
  --port 8080 --control-port 9000 --discovery-port 9999 \
  --advertise-ip 192.168.1.11
```

The worker finds the leader by UDP broadcast, registers, and sends heartbeats. It starts its replicas first, so a replica reports `loading` until `/health` returns 200.

### Worker without broadcast

Some networks block UDP broadcast. Give the leader's control address instead:

```bash
./bin/meridian-server serve --worker --leader-addr 192.168.1.10:9000 --llm ... 
```

With `--leader-addr`, the worker skips discovery.

### Notes

- Start order does not matter. A worker that starts first keeps probing until a leader appears.
- Each node checks its own free RAM at startup, against the sum of its model files plus `--ram-headroom-mb`. A node that is too full refuses to start, and the error lists the replicas that do not fit.
- Two workers on one machine need different `--port` and `--replica-port-base` values, and different `--node-id` values (or separate `--state-dir`s).
- `--node-id` is stored in `<state-dir>/node-id`. Keep that file when a machine is reimaged if you want the same identity.
- Replica ports are bound to 127.0.0.1 only. Only `--port` is reachable from the LAN.

### Checking the cluster

```bash
curl http://192.168.1.10:8080/v1/status
```

This lists every node and replica, with its state, its endpoint, and its last error. The secret is never shown.

### Failure behaviour

| Event | What happens |
|---|---|
| Worker process killed | Leader marks the node offline at once (DEREGISTER or connection close). Its replicas become `unavailable`. |
| Worker network unplugged | Leader marks the node offline after `--node-timeout` (15 s). Worker reconnects on its own when the network returns. |
| One replica crashes | That replica becomes `unhealthy` and its supervisor restarts it. The node stays `online`. |
| Leader restarted | Workers return to discovery, find the leader, and re-register with their full state. No worker restart is needed. |

### Known limits

- Routing and the public chat endpoint across machines are not in this release (Stage 4). `/v1/status` shows the cluster; chat still goes to a single-machine server.
- No TLS. Prompts and responses are plain text on the LAN.
- The leader is still a single point of failure. Restart it by hand.
