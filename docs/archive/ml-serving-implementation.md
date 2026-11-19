# ML Serving Implementation Note

This note records the backend and toolchain decisions required by the ML serving
specification. It complements the user-facing README.

## What was implemented

Two independent subsystems behind the existing `meridian-server` listener:

- The LLM subsystem is unchanged: same pool, routing policies, sessions,
  reservations, supervisor and endpoints.
- A new `internal/ml` package provides configuration, a per-kind model registry,
  an exact model-ID router, per-worker concurrency limits, a child-process
  supervisor with bounded restarts, resource admission, ML metrics, and the
  public OCR/image/artifact endpoints.

ML workers are the same executable in a new mode:

```text
meridian-server ml-worker --kind ocr --config <generated-json> --listen 127.0.0.1:8091
meridian-server ml-worker --kind image-generation --config <generated-json> --listen 127.0.0.1:8092
```

The parent writes a small per-worker JSON config into a temporary directory and
re-executes itself. The child loads one backend, exposes `/health`, `/ready`,
`POST /v1/ocr` (raw image bytes) and `POST /v1/images/generations` (typed JSON),
and exits non-zero if initialisation fails. All worker listeners are loopback.

## Toolchain and dependency gate

- Baseline stays Go 1.22. No toolchain upgrade was made.
- Added dependency: `gopkg.in/yaml.v3 v3.0.1` (configuration only; pure Go,
  BSD-style licence, stable, compatible with Go 1.22). It is the only new module.
- No Python runtime, model server, script or virtual environment is used.

## Native backend status (explicit blocker)

The following engines are recognised by configuration validation but **not
compiled into this build**:

| Kind | Engine | Status |
|---|---|---|
| OCR | `onnxruntime` | blocked: requires a validated Go binding + ONNX Runtime shared library |
| Image | `stable-diffusion.cpp` | blocked: requires a validated Go binding + stable-diffusion.cpp shared library |
| Image | `stable-diffusion-server` | **working**: HTTP proxy to a local `sd-server` (`/sdapi/v1/txt2img`), no cgo |
| both | `fake` | fully implemented, deterministic, no native code |

Candidate bindings were reviewed at implementation time:

- `github.com/microsoft/onnxruntime/go/onnxruntime` — native runtime layout and
  Go/toolchain compatibility must be verified per platform.
- `github.com/Allod-Solutions/go-ocr` — described upstream as active development
  and untagged; not safe as a silent dependency.
- `github.com/Pendra-Cloud/stable-diffusion-go`,
  `github.com/ardanlabs/malina` — native shared libraries and a documented
  stable-diffusion.cpp compatibility matrix are required.

None were validated on this host (no native libraries, no GPU probe). Adding any
of them would have required either a Go toolchain upgrade or an unverified,
possibly untagged dependency. Per the specification, the adapter interfaces and a
deterministic test backend were implemented first, and the blocker is reported
instead of introducing Python.

When a binding is validated, only `internal/ml/backend.go` needs to change:
`newOCRBackend` / `newImageGenerationBackend` already receive the worker
configuration (model path/dir, device) and return the shared interfaces. The
control plane, supervisor, resource admission and HTTP contract stay the same.

## Resource policy

`protect-llm` is the only policy. Admission:

1. takes a RAM snapshot from `/proc/meminfo` and optional `nvidia-smi` VRAM
   snapshot (2 second cache), both behind the `Probe` interface;
2. subtracts configured headroom and `protected_llm_vram_mb`;
3. subtracts reservations already granted to other ML workers;
4. rejects with `insufficient_resources` or `resource_probe_unavailable` when
   the request does not fit.

Unknown VRAM is never treated as unlimited. The manager has no process-control
API, so it cannot stop or evict an LLM worker. `internal/ml` does not import
`internal/balancer`.

## Failure behavior

- Missing model/native library/port conflict ⇒ worker `exited` or
  `resource_blocked`; no restart loop.
- Crash after readiness ⇒ at most two restarts with backoff, then `exited`.
- ML initialization or resource failure never prevents LLM workers from serving.
- Shutdown stops new ML work, sends SIGTERM, waits up to the grace period, then
  force-kills; reservations are released and the existing LLM shutdown sequence
  is preserved.

## Verification performed

- `go build ./...`
- `go test ./...`
- `go vet ./...`
- `go test -race ./internal/balancer ./internal/ml`
- Manual end-to-end runs of LLM-only, ML-only and combined modes, including OCR,
  image generation, artifact download, path-traversal rejection, disabled
  endpoints, and resource-blocked behavior on a host without a GPU.
