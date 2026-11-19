# Phase 0 — Trim The Fat (Review + Delete)

**Paste this whole file into DeepSeek as your first prompt. Do not start Phase 1 until all Acceptance Criteria pass.**

## Objective
Remove every feature you cannot explain on a whiteboard in 2 minutes and that contradicts
"fault-tolerant distributed inference". Target: ~40% fewer lines, green build, LLM + OCR only.

## Prerequisites
- You can run `make server && go test ./...` on current tree.
- Keep a git commit before starting: `git add -A && git commit -m "before trim"`

## DELETE (Exact)

1. **Exclusive arbiter (core fat):**
   - Delete dir `internal/arbiter/`
   - Remove `--serving-mode`, `--initial-mode` flags in `cmd/meridian-server/main.go`
   - Remove `arbiter` import, `arbiter.New()`, `arb.Register()`, `arb.Middleware()` in `internal/balancer/supervisor.go`. Always start enabled subsystems directly (the existing `else` branch).
   - Delete `web/src/components/ModeSwitch.tsx`, `web/src/components/TransitionScreen.tsx`
   - Remove `GET /v1/system/*` routes. Keep `GET /v1/catalog`, `GET /status`, `GET /healthz`.

2. **Stable Diffusion / image generation:**
   - Delete `configs/ml-sd-server.yaml`
   - In `internal/ml/config.go`: delete `ImageGenConfig`, `EngineStableDiffusionCPP`, `EngineStableDiffusionServer`, `EngineONNXRuntime`
   - In `internal/ml/backend.go`: delete `ImageGenerationBackend`, `fakeImageGen`, `sdServerImageGen`, `startSDServer`, `backendUnavailableReason`
   - In `internal/ml/handler.go`, `worker.go`, `service.go`, `inference.go`: delete `/v1/images/generations`, `/v1/artifacts/{id}`, `KindImageGeneration` handling. Keep OCR only.
   - In `web/src/components/MLStudio.tsx`: replace image form with Document Analyzer (upload -> `/v1/ocr` -> show text). No txt2img UI.

3. **Dead stubs:**
   - `internal/ml/config.go`: keep only `EngineFake`, `EngineTesseract`. Unknown engines -> error.
   - Delete `docs/ml-serving-implementation.md` claims about SD (or archive to `docs/archive/`).

4. **Optional (recommended):** Delete `cmd/meridian-tui/`, `internal/tui/`. You have Web UI + stress tool.

## KEEP (Do Not Touch Except Wiring)

- `internal/tokens/estimate.go`, `internal/metrics/requests.go`, `cmd/meridian-stress/main.go`
- `internal/balancer/{pool,session,routing,handler,supervisor}.go` (minus arbiter wiring)
- `internal/ml/{config,resource,service,worker,handler,backend}.go` (OCR-only slim version)
- `web/src/{App,api,ChatView,MLStudio}.tsx` (minus mode switch)
- `configs/laptop-real.yaml` (set `image_generation.enabled: false` or delete block), `configs/ml.yaml`

Also update `README.md` + `CONTEXT.md`: remove arbiter/SD sections, update "Run against real LLM workers" to `--llm-model` single-GGUF usage.

## Steps For DeepSeek (Tell It This)

1. Perform deletions above, fix imports/compilation errors.
2. `configs/ml.yaml`: keep OCR `engine: fake` for tests.
3. Run: `go test ./...`, `go vet ./...`, `go test -race ./internal/balancer ./internal/ml`, `make server`.
4. Manual check: start `--enable-llm=true --enable-ml=true --ml-config configs/ml.yaml`, curl `/v1/chat/completions`, `/v1/ocr`, `/status`, `/v1/catalog` all 200 (fake OCR ok).

## Acceptance Criteria (Screenshot/Commit These)

- [ ] `go test ./...` passes, `make server` builds
- [ ] `grep -r arbiter internal/ cmd/` returns 0 hits (except docs/archive)
- [ ] `grep -r sd-server\|StableDiffusion\|image_generation internal/ml/` returns 0 hits
- [ ] `curl /v1/ocr` works, `/v1/images/generations` returns 404
- [ ] No `409 mode_conflict` / `503 switching` anywhere in code
- [ ] `docs/00-overview-and-roadmap.md` claim matches code (LLM+OCR only)

## Non-Goals
No new features in this phase. No router changes. If DeepSeek wants to "improve" routing, say: *Phase 2, not now.*
