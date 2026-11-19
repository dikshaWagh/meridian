# Meridian

Meridian is a self-hosted AI serving platform written in Go. One public HTTP
server exposes two independent serving subsystems:

- the **LLM subsystem**: an OpenAI-compatible chat API in front of supervised
  `llama-server` workers, with session affinity and pluggable L7 routing;
- the **ML subsystem**: OCR, with its own registry, router, process supervisor,
  concurrency limits and resource admission control.

The two subsystems run concurrently. They share the public listener and nothing
else: the ML package does not import the LLM pool, and the `protect-llm`
resource policy has no ability to stop, restart or evict an LLM worker. There is
no exclusive mode, no lease and no mode switching.

A bundled React UI exposes chat, routing-policy selection, model choice and the
Document Analyzer (OCR).

Clients (React, `curl`, the stress tool) only ever talk to Meridian. Worker
listeners are loopback-only.

```text
client -> meridian-server :8080
             |
             +-- LLM subsystem ---> llama-server workers :8081..
             |     handler / pool / routing / sessions / supervisor
             |
             +-- ML subsystem  ---> Go OCR worker :8091
                   handler / registry / router / supervisor / resource manager
```

## Layout

```text
cmd/
  meridian-server/    server CLI: LLM balancer + ML control plane + workers
  meridian-stress/    LLM load generator
internal/
  api/                shared request/response JSON types
  balancer/           LLM handler, pool, routing, sessions, supervisor, workers
  ml/                 independent ML config, registry, router, supervisor,
                      resource admission, OCR backend and public API
  metrics/            JSONL request metrics (LLM and ML)
  tokens/             rough token estimator
  webui/              embedded production build of the React app
web/                  React 19 + TypeScript + Vite source for the web UI
configs/ml.yaml       sample OCR configuration (fake engine, runnable anywhere)
configs/laptop-real.yaml  OCR configuration using the tesseract CLI
```

## Build

```bash
# production web assets (embedded into the server binary)
npm --prefix web install
npm --prefix web run build

go build ./...
go build -o bin/meridian-server ./cmd/meridian-server
go build -o bin/meridian-stress ./cmd/meridian-stress
```

Or `make build`, which builds the web app first. The committed
`internal/webui/dist` lets `go build ./...` work without Node; rebuild it after
changing anything under `web/src`.

Go 1.22+ is required. The only added Go module is `gopkg.in/yaml.v3` for the ML
configuration. The web app uses Node 18+.

### Web UI development

```bash
# terminal 1
./bin/meridian-server serve --enable-ml=true --ml-config ./configs/ml.yaml \
  --models-dir ~/models --llama-bin llama-server

# terminal 2: Vite dev server on :5173, proxying /v1 and /ml to :8080
npm --prefix web run dev
```

`npm --prefix web run typecheck` runs `tsc --noEmit`. Production builds are
written directly to `internal/webui/dist` and served from `/` by the Go server;
`/v1/*` and `/ml/*` are never shadowed by the SPA fallback.

## Deployment modes

LLM serving is on by default; ML (OCR) serving is opt-in. Both can run at the
same time, limited only by the configured resource budgets.

```bash
# LLM only (default)
./bin/meridian-server serve --enable-llm=true --enable-ml=false

# OCR only
./bin/meridian-server serve --enable-llm=false --enable-ml=true \
  --ml-config ./configs/ml.yaml

# Both, resident concurrently
./bin/meridian-server serve --enable-llm=true --enable-ml=true \
  --ml-config ./configs/ml.yaml
```

Disabling both fails startup with a configuration error. `--enable-llm=false`
does not run preflight checks or spawn LLM workers; `--enable-ml=false` does not
load ML configuration or spawn ML workers. In an LLM-only deployment the ML
endpoints stay mounted and return `503 service_disabled`.

### Run against real LLM workers

Meridian has no simulated LLM worker. Point it at a `llama-server` binary and a
directory of GGUF files (the six files named in `internal/balancer/workers.go`),
or serve a single file with `--llm-model`:

```bash
./bin/meridian-server serve --enable-llm=true --enable-ml=false \
  --models-dir ~/models --llama-bin llama-server

# or one model file
./bin/meridian-server serve --llm-model ~/models/Qwen3-4B-Q4_K_M.gguf \
  --llm-model-name qwen3-4b --llm-workers 2
```

Open <http://localhost:8080> for the web UI.

## Web UI

The bundled React app is a single-page client for the whole server:

- **Chat** — streamed OpenAI-style chat with a model picker, routing-policy
  picker, per-conversation history and Markdown rendering. History is sent with
  every request (stateless client).
- **Documents** — the Document Analyzer: drop a PNG/JPEG, send it to `/v1/ocr`,
  and read or copy the extracted text.
- Chat and Documents are local tabs; both subsystems can serve requests at the
  same time.
- Light/dark appearance, glassy panels and an Apple-style blue accent, with no
  UI framework beyond React.

The app is built into `internal/webui/dist` and embedded with
`//go:embed all:dist`, so a single binary serves both the API and the UI.

## LLM subsystem

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI-style chat completion |
| `GET` | `/v1/models` | LLM models with at least one healthy worker |
| `GET` | `/status` | Per-worker LLM status |
| `GET` | `/policies` | Routing policies |
| `PUT` | `/v1/policies/default` | Change the default policy at runtime |
| `GET` | `/healthz` | Liveness |

Routing policies are `round-robin` (default), `random` and `least-connections`.
Sessions are sticky: the policy runs only when a worker is chosen (new
conversation or failover). `X-Conversation-ID` provides per-conversation
affinity and `409 conversation_busy` enforces one in-flight request per
conversation. `X-Meridian-Routing-Policy` pins a single request to a policy, and
`PUT /v1/policies/default` with `{"name":"least-connections"}` changes the
server default. Set `"stream": true` on `/v1/chat/completions` for OpenAI-style
SSE deltas.

See the existing flags (`--models-dir`, `--llama-bin`, `--threads`, `--ctx`,
`--parallel`, `--ngl`, `--gpu-workers`, `--backend-lib`, `--gpu-device`,
`--routing-policy`); their meaning is unchanged. `--llm-model`,
`--llm-model-name` and `--llm-workers` select a single GGUF file instead of the
built-in worker set. Every LLM worker is a real `llama-server` process; there is
no simulated worker.

## ML subsystem (OCR)

### Configuration

Start from [`configs/ml.yaml`](configs/ml.yaml):

```yaml
listen_host: 127.0.0.1          # ML workers are loopback-only
resource_policy: protect-llm    # the only supported policy

resources:
  ram_headroom_mb: 1024
  vram_headroom_mb: 1024
  protected_llm_vram_mb: 1024
  gpu_probe: auto               # auto | off
shutdown_grace_seconds: 10      # drain in-flight OCR requests before SIGTERM

ocr:
  enabled: true
  model_id: pp-ocrv5-mobile-en
  engine: fake                  # fake | tesseract
  device: cpu
  model_dir: ./models/ocr/pp-ocrv5-mobile-en
  expected_ram_mb: 256
  expected_vram_mb: 0
  concurrency: 2
  port: 8091
  max_upload_mb: 10
  startup_timeout_seconds: 60
```

The sample runs on any machine because it selects the deterministic `fake`
engine. `configs/laptop-real.yaml` switches to `engine: tesseract`, which shells
out to the `tesseract` binary on `PATH`. Unknown engines are rejected at
startup. Memory budgets are declared estimates used only for admission; they are
not inferred from model file size. Configuration is validated at startup
(unknown engines, duplicate/invalid ports, non-loopback bind addresses, negative
budgets, invalid limits and unknown fields are rejected). A missing model file
or missing native library blocks only that ML worker and never crashes an
LLM-only deployment.

### Public API

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/ocr` | OCR one PNG/JPEG image |
| `GET` | `/v1/ml/models` | Configured ML models and states |
| `GET` | `/ml/status` | ML workers + resource admission details |

`/v1/models` stays LLM-only for compatibility. In LLM-only deployments the ML
endpoints remain mounted but return `503 service_disabled` (a stable error
contract rather than a bare 404).

#### OCR

```bash
curl -F file=@document.png -F model=pp-ocrv5-mobile-en \
  http://localhost:8080/v1/ocr
```

```json
{
  "request_id": "…",
  "model": "pp-ocrv5-mobile-en",
  "text": "…",
  "lines": [{"text": "…"}],
  "processing_ms": 42
}
```

`confidence` and `bbox` are omitted unless the backend computes them; they are
never fabricated.

#### Error contract

`invalid_request` (400), `model_not_found` (404), `request_too_large` (413),
`service_disabled` / `no_healthy_worker` / `insufficient_resources` /
`worker_start_failed` (503), `inference_timeout` (504), `upstream_error` (502).

### Worker lifecycle and supervision

`meridian-server ml-worker --kind ocr --config <json>` runs as a child process of
the server (a self re-exec pattern; the same binary in worker mode). The worker:

- loads exactly one model/backend before reporting ready;
- exposes private `/health`, `/ready`, and a typed inference endpoint on
  loopback only;
- bounds request bodies, image dimensions and concurrency;
- exits non-zero on initialisation failure.

The parent tracks PID, model, kind, port, state, last check/error, in-flight
requests, served/failed counts, restarts and last inference duration. States are
`starting`, `ready`, `unhealthy`, `resource_blocked`, `stopping`, `exited`.

Restart policy is bounded: a worker that was ready and then crashes is restarted
at most twice with backoff; a worker that fails initialisation (missing model,
missing native library, port conflict) is left `exited` and is never
restart-looped. Health is probed every 2 seconds; three consecutive failures
mark a worker unhealthy.

### Resource admission (`protect-llm`)

Before an ML worker starts, the resource manager takes a host snapshot
(`/proc/meminfo`, optional `nvidia-smi`) and reserves the configured RAM, VRAM
and inference overhead. Admission is serialized so concurrent startups cannot
oversubscribe the same capacity. Reservations are released when a worker stops
or fails.

- Unknown VRAM is **not** treated as unlimited: GPU-backed models stay
  `resource_blocked` until a probe and budget are available.
- A rejected model does not start; other ML models and all LLM workers continue.
- The manager has no kill/eviction API, so the default policy structurally
  cannot stop an LLM worker to admit an ML model.

Memory snapshots are estimates and can become stale; reservations reduce the
risk of oversubscription but cannot guarantee the OS or GPU driver will never
run out of memory.

### OCR backends

```go
type OCRBackend interface {
    Recognize(ctx context.Context, imageBytes []byte) (*OCRResult, error)
    Close() error
}
```

Two engines are compiled in:

- `fake` — deterministic output, no external dependencies; used by
  `configs/ml.yaml`.
- `tesseract` — runs the Tesseract CLI (`tesseract`) over a temporary image
  file; used by `configs/laptop-real.yaml`.

## Metrics

- LLM: `logs/requests.jsonl` (unchanged format).
- ML: `logs/ml-requests.jsonl` — `request_id`, `kind`, `model`, `status_code`,
  `latency_ms`, optional `error_type`, and a `service: "meridian-ml"` namespace.
  Images and OCR text are never logged.

## Tests

```bash
go build ./...
go test ./...
go vet ./...
go test -race ./internal/balancer ./internal/ml
npm --prefix web run typecheck
```

The ML suite covers configuration validation, the OCR API, invalid MIME,
malformed and oversized uploads, disabled-service behavior, worker
readiness/health, initialisation failure without restart loops, port conflicts,
concurrency limits, resource admission (including "unknown VRAM is not
unlimited"), and the protect-llm invariant. No test needs model weights or a
GPU.

## Current limitations

- Static LLM worker list; one ML worker per configured model.
- No authentication or TLS.
- In-memory LLM sessions (lost on restart); OCR requests are stateless.
- Approximate LLM token counting.
- Metrics are file-based only.
- The `fake` engine is fully functional; `tesseract` requires the CLI on
  `PATH`.
