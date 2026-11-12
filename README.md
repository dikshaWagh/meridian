# Meridian

Meridian is a self-hosted AI serving platform written in Go. One public HTTP
server exposes two independent serving subsystems:

- the **LLM subsystem**: an OpenAI-compatible chat API in front of supervised
  `llama-server` workers, with session affinity and pluggable L7 routing;
- the **ML subsystem**: OCR and Stable Diffusion image generation, with their
  own registry, router, process supervisor, concurrency limits and resource
  admission control.

Clients (React, TUI, `curl`, the stress tool) only ever talk to Meridian. Worker
listeners are loopback-only.

```text
client -> meridian-server :8080
             |
             +-- LLM subsystem ---> llama-server workers :8081..8086
             |     handler / pool / routing / sessions / supervisor
             |
             +-- ML subsystem  ---> Go ML workers :8091 (OCR), :8092 (image)
                   handler / registry / router / supervisor / resource manager
```

The two subsystems share the public listener and nothing else. The ML package
does not import the LLM pool, and the default `protect-llm` resource policy has
no ability to stop, restart or evict an LLM worker.

## Layout

```text
cmd/
  meridian-server/    server CLI: LLM balancer + ML control plane + workers
  meridian-tui/       interactive LLM chat client
  meridian-stress/    LLM load generator
internal/
  api/                shared request/response JSON types
  balancer/           LLM handler, pool, routing, sessions, supervisor, workers
  ml/                 independent ML config, registry, router, supervisor,
                      resource admission, OCR/image backends and public API
  mockworker/         fake LLM worker used by --mock
  metrics/            JSONL request metrics (LLM and ML)
  tokens/             rough token estimator
  tui/                terminal chat UI
configs/ml.yaml       sample ML configuration (fake engines, runnable anywhere)
```

## Build

```bash
go build ./...
go build -o bin/meridian-server ./cmd/meridian-server
go build -o bin/meridian-tui    ./cmd/meridian-tui
go build -o bin/meridian-stress ./cmd/meridian-stress
```

Go 1.22+ is required. The only added module is `gopkg.in/yaml.v3` for the ML
configuration.

## Deployment modes

The existing LLM-first behavior is the default; ML serving is opt-in.

```bash
# LLM only (previous behavior)
./bin/meridian-server serve --enable-llm=true --enable-ml=false

# ML only
./bin/meridian-server serve --enable-llm=false --enable-ml=true \
  --ml-config ./configs/ml.yaml

# Both behind one listener
./bin/meridian-server serve --enable-llm=true --enable-ml=true \
  --ml-config ./configs/ml.yaml
```

Disabling both fails startup with a configuration error. `--enable-llm=false`
does not run preflight checks or spawn LLM workers; `--enable-ml=false` does not
load ML configuration or spawn ML workers.

### Try it without models (mock LLM + fake ML)

```bash
./bin/meridian-server serve --mock --enable-ml=true --ml-config ./configs/ml.yaml

# in another shell
./bin/meridian-tui --addr http://localhost:8080

curl -F file=@photo.png http://localhost:8080/v1/ocr
curl -X POST http://localhost:8080/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"a quiet mountain lake at sunrise","width":512,"height":512,"steps":25,"seed":42}'
```

## LLM subsystem (unchanged contract)

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/chat/completions` | OpenAI-style chat completion |
| `GET` | `/v1/models` | LLM models with at least one healthy worker |
| `GET` | `/status` | Per-worker LLM status |
| `GET` | `/policies` | Routing policies |
| `GET` | `/healthz` | Liveness |

Routing policies are `round-robin` (default), `random` and `least-connections`.
Sessions are sticky: the policy runs only when a worker is chosen (new
conversation or failover). `X-Conversation-ID` provides per-conversation
affinity and `409 conversation_busy` enforces one in-flight request per
conversation. See the existing flags (`--models-dir`, `--llama-bin`, `--threads`,
`--ctx`, `--parallel`, `--ngl`, `--gpu-workers`, `--backend-lib`, `--gpu-device`,
`--routing-policy`, `--mock`); their meaning is unchanged.

## ML subsystem

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
shutdown_grace_seconds: 10      # drain in-flight ML requests before SIGTERM

ocr:
  enabled: true
  model_id: pp-ocrv5-mobile-en
  engine: fake                  # fake | onnxruntime
  device: cpu
  model_dir: ./models/ocr/pp-ocrv5-mobile-en
  expected_ram_mb: 256
  expected_vram_mb: 0
  concurrency: 2
  port: 8091
  max_upload_mb: 10

image_generation:
  enabled: true
  model_id: stable-diffusion-v1-5
  engine: fake                  # fake | stable-diffusion.cpp
  device: cpu
  model_path: ./models/stable-diffusion/model.safetensors
  expected_ram_mb: 512
  expected_vram_mb: 0
  request_overhead_vram_mb: 0
  concurrency: 1
  port: 8092
  output_dir: ./data/generated
  max_width: 768
  max_height: 768
  max_steps: 40
  default_steps: 25
  timeout_seconds: 300
```

The sample runs on any machine because it selects the deterministic `fake`
engines. Memory budgets are declared estimates used only for admission; they are
not inferred from model file size. Configuration is validated at startup
(unknown engines, duplicate ports, non-loopback bind addresses, negative
budgets, invalid limits and unknown fields are rejected). A missing model file or
missing native library blocks only that ML worker and never crashes an LLM-only
deployment.

### Public API

| Method | Path | Purpose |
|---|---|---|
| `POST` | `/v1/ocr` | OCR one PNG/JPEG image |
| `POST` | `/v1/images/generations` | Text-to-image, PNG artifact |
| `GET` | `/v1/artifacts/{id}` | Fetch a generated PNG |
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

#### Image generation

```bash
curl -X POST http://localhost:8080/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"a quiet mountain lake","negative_prompt":"blurry",
       "width":512,"height":512,"steps":25,"seed":42}'
```

```json
{
  "id": "8f0c…",
  "model": "stable-diffusion-v1-5",
  "url": "/v1/artifacts/8f0c…",
  "width": 512,
  "height": 512,
  "steps": 25,
  "seed": 42,
  "created": 1794511201,
  "processing_ms": 2500
}
```

Validation: model ID, non-empty prompt (bounded length), width/height multiples
of 8 within configured maxima, steps in range, `n` limited to 1. Artifacts are
written under `output_dir` using opaque random IDs and served by Meridian; the
artifact handler validates the ID and keeps the resolved path inside the
artifact directory. No prompt text is used in file names and no prompt, image or
OCR text is written to metrics.

#### Error contract

`invalid_request` (400), `model_not_found` (404), `request_too_large` (413),
`service_disabled` / `no_healthy_worker` / `insufficient_resources` /
`worker_start_failed` (503), `inference_timeout` (504), `upstream_error` (502).

### Worker lifecycle and supervision

`meridian-server ml-worker --kind ocr|image-generation --config <json>` runs as a
child process of the server (the same self re-exec pattern as `--mock`). Each
worker:

- loads exactly one model/backend before reporting ready;
- exposes private `/health`, `/ready`, and typed inference endpoints on
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

### Native inference backends

This implementation ships the complete Go control plane plus deterministic fake
backends, and isolates real inference behind small interfaces:

```go
type OCRBackend interface {
    Recognize(ctx context.Context, imageBytes []byte) (*OCRResult, error)
    Close() error
}

type ImageGenerationBackend interface {
    Generate(ctx context.Context, req ImageGenerationRequest) (*GeneratedImage, error)
    Close() error
}
```

`configs/ml.yaml` therefore defaults to `engine: fake`. Selecting
`engine: onnxruntime` or `engine: stable-diffusion.cpp` is recognised and
validated, but this build reports a clear `worker_start_failed: … unavailable in
this build` error and blocks only that worker. No Python runtime is introduced.

For real image generation without cgo, use `engine: stable-diffusion-server`.
Meridian proxies to a locally running
[stable-diffusion.cpp](https://github.com/leejet/stable-diffusion.cpp)
`sd-server` over its Stable Diffusion WebUI API (`/sdapi/v1/txt2img`), while
`sd-server` owns the model weights and native runtime. A ready-to-run sample is
[`configs/ml-sd-server.yaml`](configs/ml-sd-server.yaml):

```bash
# terminal 1: native runtime
sd-server -m ~/models/stable-diffusion/model.safetensors \
  --listen-ip 127.0.0.1 --listen-port 1234

# terminal 2: Meridian
./bin/meridian-server serve --enable-llm=false --enable-ml=true \
  --ml-config ./configs/ml-sd-server.yaml

# terminal 3: real PNG
curl -X POST http://127.0.0.1:8080/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"a quiet mountain lake","width":512,"height":512,"steps":20,"seed":42}'
```

CPU inference can take minutes per image; `timeout_seconds` defaults to 900 in
that sample. `sd-server` must already be running when a request arrives, but
Meridian does not need it at startup.

Why the native engines are not wired in yet: the candidate Go bindings
(`microsoft/onnxruntime-go`, `Allod-Solutions/go-ocr`,
`Pendra-Cloud/stable-diffusion-go`, `ardanlabs/malina`) require
platform-specific native shared libraries and, in several cases, a Go toolchain
newer than the project baseline. They were not validated on this host, so adding
them would have silently upgraded the toolchain or shipped an unverified
dependency. The adapters are ready for a validated binding; see
[`docs/ml-serving-implementation.md`](docs/ml-serving-implementation.md).

## Metrics

- LLM: `logs/requests.jsonl` (unchanged format).
- ML: `logs/ml-requests.jsonl` — `request_id`, `kind`, `model`, `status_code`,
  `latency_ms`, optional `error_type`, and a `service: "meridian-ml"` namespace.
  Prompts, images and OCR text are never logged.

## Tests

```bash
go build ./...
go test ./...
go vet ./...
go test -race ./internal/balancer ./internal/ml
```

The ML suite covers configuration validation, OCR/image APIs, invalid MIME,
malformed and oversized uploads, artifact retrieval and path-traversal
rejection, disabled-service behavior, worker readiness/health, initialisation
failure without restart loops, port conflicts, concurrency limits, resource
admission (including "unknown VRAM is not unlimited"), and the protect-llm
invariant. No test needs model weights or a GPU.

## Current limitations

- Static LLM worker list; one ML worker per configured model.
- No streaming/SSE for LLM; no streaming image previews.
- No authentication or TLS.
- In-memory LLM sessions (lost on restart); OCR/image requests are stateless.
- Approximate LLM token counting.
- Metrics are file-based only.
- Native OCR/image backends are adapters only in this build (fake engines are
  fully functional).
