# Handoff: running real LLMs and real ML models on this host

Status: **not implemented in the current tree**. Everything below was either
verified on this machine or written and compiled during the investigation, then
reverted at the user's request. Use this document to reapply the work.

Target commit: `6415a55` (`Add exclusive LLM/ML serving mode and React web UI`).

---

## 1. Executive summary

Meridian already has the right control plane: exclusive-mode lease, drain/stop/
start/warm-up state machine, model catalog, policy picker, chat UI and ML Studio.
What is missing is **real backends** and **a way to serve one downloaded model
instead of the hard-coded three-model worker set**.

Three gaps block real models today:

1. `internal/balancer/workers.go` hard-codes 3 GGUF files × 2 workers = 6
   `llama-server` processes. On this 14 GB host that is roughly 9–10 GB of model
   weights and is not practical.
2. Real OCR (`onnxruntime`) and native `stable-diffusion.cpp` are recognised but
   intentionally return “unavailable in this build”.
3. `stable-diffusion-server` expects an already-running `sd-server`. If that
   process is started outside Meridian it stays resident while the LLM is active,
   violating the exclusive-mode guarantee.

The fix is:

- Add `--llm-model`, `--llm-model-name`, `--llm-workers` for a single GGUF.
- Add a `tesseract` OCR engine (Tesseract 5.5.4 is already installed).
- Let the ML worker **spawn and stop `sd-server` itself** via `server_command` /
  `server_args`, so the native process lives and dies with the ML lease.
- Add `configs/ml-real.yaml` and a run script.

Verified during investigation:

- `/usr/local/lib/ollama/llama-server` is a standard llama.cpp server
  (`6f3a9f3de`) and works with Meridian's `-m/--alias/--host/--port/-t/-c/-np`
  arguments.
- `qwen2.5-1.5b-instruct-q4_k_m.gguf` served through Meridian at **~45 tok/s**
  on CPU.
- `sd-server` built successfully from `leejet/stable-diffusion.cpp`
  (`master-1091-b9d7176`), 43 MB binary.
- Tesseract 5.5.4 with `eng` data is installed at `/usr/bin/tesseract`.
- The SD-Turbo download was cancelled during revert; it was not tested
  end-to-end.

---

## 2. Host facts

| Item | Value |
|---|---|
| OS | Fedora Linux 45 Workstation (hostname `asteroid-destroyer`) |
| CPU | 16 threads |
| RAM | 14 GiB total, 27 GiB swap; typically 3–4 GiB free with browsers open |
| GPU | AMD Radeon 610M integrated (no CUDA, no `nvidia-smi`) |
| Disk | 215 GB free on `/` |
| llama.cpp | `/usr/local/lib/ollama/llama-server` (from Ollama) |
| Tesseract | `/usr/bin/tesseract` 5.5.4, language `eng` only |
| Node/npm | `/usr/bin/node`, `/usr/bin/npm` (if a shell says “npm not found”, fix PATH; the embedded UI means npm is only needed for UI changes) |

Implications:

- Use **CPU inference**. `--ngl` / GPU offload flags are irrelevant.
- Prefer small quantised LLMs (1.5B–3B Q4) and SD-Turbo (1–4 steps).
- Image generation will be slow (tens of seconds to a few minutes per 512×512).
- The exclusive lease is essential: LLM + SD weights do not fit comfortably at
  the same time.

---

## 3. Target architecture

```
meridian-server
  ├─ exclusive arbiter (one lease)
  │    ├─ LLM mode: 1 × llama-server (Qwen2.5-1.5B Q4_K_M)
  │    └─ ML mode:
  │         ├─ ml-worker OCR  → tesseract CLI
  │         └─ ml-worker image → sd-server child (SD-Turbo, CPU)
  └─ embedded React UI at /
```

`sd-server` must be a **child of the ML worker**, not an independently running
service. When the arbiter stops the ML subsystem, the worker exits, its deferred
`Close()` sends SIGTERM to `sd-server`, and memory is released before the LLM
starts.

---

## 4. Code changes

### 4.1 Single-model LLM workers

#### `internal/balancer/workers.go`

Add `fmt` and a constructor:

```go
func SingleModelWorkers(file, name string, n int) []WorkerSpec {
    if n < 1 {
        n = 1
    }
    if name == "" {
        name = file
    }
    out := make([]WorkerSpec, 0, n)
    for i := 0; i < n; i++ {
        out = append(out, WorkerSpec{
            ID:    fmt.Sprintf("w%d", i+1),
            Model: name,
            Port:  8081 + i,
            File:  file,
        })
    }
    return out
}
```

#### `internal/balancer/supervisor.go`

Add options:

```go
// LLMModel serves one GGUF file instead of the built-in worker set.
// Relative paths are resolved under ModelsDir.
LLMModel     string
LLMModelName string
LLMWorkers   int
```

Add helpers:

```go
func workerSpecs(o Options) []WorkerSpec {
    file := strings.TrimSpace(o.LLMModel)
    if file == "" {
        return DefaultWorkers()
    }
    name := strings.TrimSpace(o.LLMModelName)
    if name == "" {
        base := filepath.Base(file)
        name = strings.TrimSuffix(base, filepath.Ext(base))
    }
    return SingleModelWorkers(file, name, o.LLMWorkers)
}

func modelPath(dir, file string) string {
    if filepath.IsAbs(file) {
        return file
    }
    return filepath.Join(dir, file)
}
```

Change `preflight` to accept the selected specs and use `modelPath`:

```go
func preflight(o Options, specs []WorkerSpec) error {
    // ...
    for _, w := range specs {
        file := modelPath(o.ModelsDir, w.File)
        // stat file
    }
}
```

In `Run`:

```go
specs := workerSpecs(o)
if o.EnableLLM {
    if o.ModelsDir, _ = expandHome(o.ModelsDir); !o.Mock {
        if err := preflight(o, specs); err != nil {
            return err
        }
    }
}
// ...
pool = NewPool(specs)
```

In `startChildren`, replace `filepath.Join(o.ModelsDir, s.File)` with
`modelPath(o.ModelsDir, s.File)`.

#### `cmd/meridian-server/main.go`

```go
fs.StringVar(&o.LLMModel, "llm-model", "",
    "serve one GGUF model file instead of the built-in worker set (relative paths use --models-dir)")
fs.StringVar(&o.LLMModelName, "llm-model-name", "",
    "public model id for --llm-model (defaults to the file name)")
fs.IntVar(&o.LLMWorkers, "llm-workers", 1,
    "number of workers for --llm-model")
```

This alone makes real LLM chat work.

---

### 4.2 Real OCR with Tesseract

Tesseract is already installed; no cgo or ONNX runtime is needed.

#### `internal/ml/config.go`

```go
const (
    EngineFake      = "fake"
    EngineTesseract = "tesseract"
    // ...
)

type OCRConfig struct {
    // ...
    ModelDir string `yaml:"model_dir"`
    Language string `yaml:"language"`
    // ...
}
```

In `normalize`, default `o.Language = "eng"`.

In `validate`, allow `EngineTesseract` and only require `model_dir` for
`onnxruntime`:

```go
if o.Engine != EngineFake && o.Engine != EngineTesseract && o.Engine != EngineONNXRuntime {
    return fmt.Errorf("ocr.engine %q is not recognised (want %s, %s or %s)",
        o.Engine, EngineFake, EngineTesseract, EngineONNXRuntime)
}
if o.Engine == EngineONNXRuntime && o.ModelDir == "" {
    return fmt.Errorf("ocr.model_dir is required for engine %s", o.Engine)
}
```

#### `internal/ml/worker.go`

```go
Language string `json:"language,omitempty"`
```

#### `internal/ml/service.go`

Pass `Language: o.Language` into the OCR `WorkerConfig`.

#### `internal/ml/backend.go`

```go
case EngineTesseract:
    return &tesseractOCR{language: cfg.Language}, nil
```

```go
type tesseractOCR struct {
    language string
}

func (t *tesseractOCR) Recognize(ctx context.Context, imageBytes []byte) (*OCRResult, error) {
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    f, err := os.CreateTemp("", "meridian-ocr-*")
    if err != nil {
        return nil, fmt.Errorf("create OCR temp file: %w", err)
    }
    path := f.Name()
    defer os.Remove(path)
    if _, err := f.Write(imageBytes); err != nil {
        f.Close()
        return nil, fmt.Errorf("write OCR temp file: %w", err)
    }
    if err := f.Close(); err != nil {
        return nil, fmt.Errorf("close OCR temp file: %w", err)
    }

    lang := strings.TrimSpace(t.language)
    if lang == "" {
        lang = "eng"
    }
    cmd := exec.CommandContext(ctx, "tesseract", path, "stdout", "-l", lang, "--psm", "6")
    var stderr bytes.Buffer
    cmd.Stderr = &stderr
    out, err := cmd.Output()
    if err != nil {
        return nil, fmt.Errorf("tesseract: %w: %s", err, strings.TrimSpace(stderr.String()))
    }

    text := strings.TrimSpace(string(out))
    lines := make([]OCRLine, 0, 8)
    for _, line := range strings.Split(text, "\n") {
        line = strings.TrimSpace(line)
        if line != "" {
            lines = append(lines, OCRLine{Text: line})
        }
    }
    return &OCRResult{Text: text, Lines: lines}, nil
}

func (t *tesseractOCR) Close() error { return nil }
```

Add `os`, `os/exec` imports. `bytes` is already imported.

Optional: add a unit test that skips when `exec.LookPath("tesseract")` fails.

---

### 4.3 Real image generation with managed `sd-server`

The current `stable-diffusion-server` engine only proxies to an existing process.
Add optional process management so the child dies with the ML lease.

#### `internal/ml/config.go`

```go
type ImageGenConfig struct {
    // ...
    ServerURL     string   `yaml:"server_url"`
    ServerCommand string   `yaml:"server_command"`
    ServerArgs    []string `yaml:"server_args"`
    // ...
    CFGScale float64 `yaml:"cfg_scale"`
    // ...
}
```

Validation:

```go
if g.CFGScale < 0 {
    return fmt.Errorf("image_generation.cfg_scale must not be negative")
}
if g.ServerCommand != "" && g.Engine != EngineStableDiffusionServer {
    return fmt.Errorf("image_generation.server_command is only supported for engine %s",
        EngineStableDiffusionServer)
}
```

#### `internal/ml/worker.go`

```go
ServerCommand string   `json:"server_command,omitempty"`
ServerArgs    []string `json:"server_args,omitempty"`
CFGScale      float64  `json:"cfg_scale,omitempty"`
```

#### `internal/ml/service.go`

Pass `ServerCommand`, `ServerArgs`, `CFGScale` into the image `WorkerConfig`.

#### `internal/ml/backend.go`

Add imports: `os`, `os/exec`, `syscall`, `time`.

Extend the backend:

```go
type sdServerImageGen struct {
    baseURL  string
    client   *http.Client
    cfgScale float64
    cmd      *exec.Cmd
}
```

In `newImageGenerationBackend`:

```go
backend := &sdServerImageGen{
    baseURL:  base,
    client:   &http.Client{},
    cfgScale: cfg.CFGScale,
}
if strings.TrimSpace(cfg.ServerCommand) != "" {
    cmd, err := startSDServer(base, cfg)
    if err != nil {
        return nil, err
    }
    backend.cmd = cmd
}
return backend, nil
```

Start helper:

```go
func startSDServer(baseURL string, cfg WorkerConfig) (*exec.Cmd, error) {
    cmd := exec.Command(cfg.ServerCommand, cfg.ServerArgs...)
    cmd.Stdout = os.Stderr
    cmd.Stderr = os.Stderr
    if err := cmd.Start(); err != nil {
        return nil, fmt.Errorf("start sd-server %q: %w", cfg.ServerCommand, err)
    }

    client := &http.Client{Timeout: 2 * time.Second}
    deadline := time.Now().Add(3 * time.Minute)
    for time.Now().Before(deadline) {
        if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
            _ = cmd.Wait()
            return nil, fmt.Errorf("sd-server exited before becoming ready")
        }
        resp, err := client.Get(baseURL + "/sdapi/v1/options")
        if err == nil {
            resp.Body.Close()
            if resp.StatusCode < 500 {
                return cmd, nil
            }
        }
        time.Sleep(500 * time.Millisecond)
    }
    _ = cmd.Process.Kill()
    _ = cmd.Wait()
    return nil, fmt.Errorf("sd-server at %s did not become ready within 3 minutes", baseURL)
}
```

Close:

```go
func (s *sdServerImageGen) Close() error {
    if s.cmd == nil || s.cmd.Process == nil {
        return nil
    }
    _ = s.cmd.Process.Signal(syscall.SIGTERM)
    done := make(chan struct{})
    go func() {
        _ = s.cmd.Wait()
        close(done)
    }()
    select {
    case <-done:
    case <-time.After(15 * time.Second):
        _ = s.cmd.Process.Kill()
        <-done
    }
    return nil
}
```

Send CFG scale in the request:

```go
body, err := json.Marshal(sdTxt2ImgRequest{
    // ...
    CFGScale: s.cfgScale,
})
```

`sdTxt2ImgRequest.CFGScale` already exists with `omitempty`, so `1.0` is sent
and `0` is omitted.

#### `web/src/components/MLStudio.tsx`

SD-Turbo is distilled for 1–4 steps:

```ts
const [steps, setSteps] = useState(4)
// ...
min={1}
max={40}
```

This is a generic UI default. If you switch to SD 1.5 later, users must raise the
slider (20–30 steps).

---

## 5. Real config

Create `configs/ml-real.yaml`:

```yaml
listen_host: 127.0.0.1
resource_policy: protect-llm

resources:
  ram_headroom_mb: 512
  vram_headroom_mb: 0
  protected_llm_vram_mb: 0
  gpu_probe: off

shutdown_grace_seconds: 10

ocr:
  enabled: true
  model_id: tesseract-eng
  engine: tesseract
  device: cpu
  language: eng
  expected_ram_mb: 256
  expected_vram_mb: 0
  concurrency: 1
  port: 8091
  max_upload_mb: 10
  startup_timeout_seconds: 30

image_generation:
  enabled: true
  model_id: sd-turbo
  engine: stable-diffusion-server
  device: cpu
  server_url: http://127.0.0.1:1234
  # The ML worker owns this process; it is stopped when ML mode ends.
  server_command: /home/rudra/src/stable-diffusion.cpp/build/bin/sd-server
  server_args:
    - "-m"
    - "/home/rudra/models/sd_turbo.safetensors"
    - "--type"
    - "f16"
    - "--listen-ip"
    - "127.0.0.1"
    - "--listen-port"
    - "1234"
    - "-t"
    - "8"
  cfg_scale: 1.0
  default_steps: 4
  expected_ram_mb: 4096
  expected_vram_mb: 0
  request_overhead_vram_mb: 0
  concurrency: 1
  port: 8092
  output_dir: ./data/generated
  max_width: 768
  max_height: 768
  max_steps: 40
  max_prompt_chars: 2000
  timeout_seconds: 900
  startup_timeout_seconds: 180
```

Notes:

- `expected_ram_mb` is an admission estimate, not measured. SD-Turbo f16 weights
  are ~2.6 GB; peak RAM is higher because of CLIP/VAE/decoding. 4096 is a
  reasonable starting point on this host.
- If admission rejects the worker (`insufficient_resources`), close browser
  tabs first, then lower `ram_headroom_mb` or `expected_ram_mb`. Do not set the
  estimate below real usage.
- `protected_llm_vram_mb: 0` is correct for exclusive mode because the LLM is
  stopped before ML starts. In `concurrent` mode keep a real protection budget.

---

## 6. Build and download steps on this host

### 6.1 LLM binary

Already available and verified:

```bash
/usr/local/lib/ollama/llama-server --version
```

For reproducibility, either pin this binary or build upstream llama.cpp:

```bash
git clone --depth 1 https://github.com/ggml-org/llama.cpp ~/src/llama.cpp
cmake -S ~/src/llama.cpp -B ~/src/llama.cpp/build -DCMAKE_BUILD_TYPE=Release
cmake --build ~/src/llama.cpp/build -j 12 --target llama-server
```

Then use `--llama-bin ~/src/llama.cpp/build/bin/llama-server`.

### 6.2 LLM model

```bash
mkdir -p ~/models
curl -L --retry 3 -C - \
  -o ~/models/qwen2.5-1.5b-instruct-q4_k_m.gguf \
  'https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf'
```

Size: ~1.1 GB. Verified to serve through Meridian at ~45 tok/s.

### 6.3 `sd-server`

The first configure attempt failed because the `ggml` submodule was missing.
Always initialise submodules:

```bash
git clone --depth 1 https://github.com/leejet/stable-diffusion.cpp ~/src/stable-diffusion.cpp
cd ~/src/stable-diffusion.cpp
git submodule update --init --recursive
cmake -B build -DCMAKE_BUILD_TYPE=Release -DSD_BUILD_EXAMPLES=ON
cmake --build build -j 12 --target sd-server
```

Result: `~/src/stable-diffusion.cpp/build/bin/sd-server` (verified, 43 MB).

`pnpm` is not required; the CMake warning about the server frontend is harmless
because Meridian only uses the WebUI API.

### 6.4 SD model

```bash
curl -L --retry 3 -C - \
  -o ~/models/sd_turbo.safetensors \
  'https://huggingface.co/stabilityai/sd-turbo/resolve/main/sd_turbo.safetensors'
```

Size: ~5.2 GB. Check the license before redistribution.

### 6.5 UI (only if `web/src` changed)

The committed `internal/webui/dist` is embedded in the binary, so `make build`
is not required for backend-only changes. If the UI default changed:

```bash
/usr/bin/npm --prefix web install --no-audit --no-fund
/usr/bin/npm --prefix web run build
go build -o bin/meridian-server ./cmd/meridian-server
```

If `npm` is missing in a shell, use `/usr/bin/npm` or fix `PATH`. Do not run
`make build` on a host without Node; it will fail at the web step even though
the embedded UI is already present.

---

## 7. Run

```bash
cd ~/meridian

pkill -x meridian-server 2>/dev/null || true

setsid ./bin/meridian-server serve \
  --enable-llm=true \
  --enable-ml=true \
  --serving-mode exclusive \
  --initial-mode llm \
  --ml-config ./configs/ml-real.yaml \
  --llama-bin /usr/local/lib/ollama/llama-server \
  --llm-model qwen2.5-1.5b-instruct-q4_k_m.gguf \
  --llm-model-name qwen2.5-1.5b \
  --llm-workers 1 \
  --models-dir ~/models \
  --threads 8 \
  --ctx 4096 \
  > ~/meridian.log 2>&1 < /dev/null &
```

Open the UI:

```bash
xdg-open http://127.0.0.1:8080
```

The server starts in LLM mode. Click **ML Studio** (or POST a mode switch) to
drain the LLM, stop it, start the ML worker, launch `sd-server`, and warm up.

---

## 8. Verification

### 8.1 State and catalog

```bash
curl -s http://127.0.0.1:8080/v1/system/state | jq .
curl -s http://127.0.0.1:8080/v1/catalog | jq .
```

Expect `mode: "llm"`, `phase: "active"`, and a catalog containing
`qwen2.5-1.5b` plus `tesseract-eng` / `sd-turbo`.

### 8.2 Real chat

```bash
curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"qwen2.5-1.5b","messages":[{"role":"user","content":"Say hello in one sentence."}]}' | jq .
```

This was verified working. The response includes llama.cpp `timings`.

### 8.3 Switch to ML

```bash
curl -s -X POST http://127.0.0.1:8080/v1/system/mode \
  -H 'Content-Type: application/json' \
  -d '{"mode":"ml"}' | jq .

curl -s http://127.0.0.1:8080/ml/status | jq .
```

While ML is active:

```bash
pgrep -ax sd-server     # must exist
pgrep -af llama-server  # must be empty
```

After switching back to LLM:

```bash
curl -s -X POST http://127.0.0.1:8080/v1/system/mode \
  -H 'Content-Type: application/json' -d '{"mode":"llm"}' | jq .

pgrep -ax sd-server     # must be empty
```

### 8.4 Real OCR

```bash
curl -F file=@document.png http://127.0.0.1:8080/v1/ocr | jq .
```

The text should come from Tesseract, not “Meridian fake OCR”.

### 8.5 Real image generation

```bash
curl -s -X POST http://127.0.0.1:8080/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"a quiet mountain lake at sunrise, cinematic light","width":512,"height":512,"steps":4,"seed":42}' | jq .
```

Fetch the returned `url`:

```bash
curl -s http://127.0.0.1:8080/v1/artifacts/<id> -o out.png
```

Expect a real SD-Turbo image. On CPU this can take tens of seconds to a few
minutes. The UI steps default is 4 after the change.

### 8.6 Tests

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./internal/arbiter ./internal/balancer ./internal/ml
```

Add tests for:

- `workerSpecs` / `SingleModelWorkers` port and name mapping.
- `modelPath` absolute vs relative.
- Tesseract backend (skip if `exec.LookPath("tesseract")` fails).
- `startSDServer` failure path (missing command) and `Close` no-op.
- Config validation for `tesseract`, `server_command`, negative `cfg_scale`.

---

## 9. Memory and performance expectations

| Process | Approx. resident memory |
|---|---|
| `meridian-server` + UI | 50–150 MB |
| 1 × `llama-server` Qwen2.5-1.5B Q4_K_M | ~1.5 GB |
| `sd-server` SD-Turbo f16 | ~3–5 GB peak |
| `tesseract` (per request) | ~100–300 MB |

Exclusive mode is what makes this fit on 14 GB. Do not run `sd-server`
independently; that defeats the lease and can OOM the host.

CPU notes:

- Qwen2.5-1.5B Q4: ~45 tok/s with `-t 8` on this CPU.
- SD-Turbo 512×512, 4 steps: expect tens of seconds to a few minutes.
- `--type f16` halves weight memory versus f32. Do not use `q4` unless you
  verify quality and sd.cpp support for the checkpoint.

---

## 10. Risks and follow-ups

1. **Ollama binary dependency.** `/usr/local/lib/ollama/llama-server` is an
   Ollama-managed artifact and may change or disappear on upgrade. For a
   production handoff, build upstream llama.cpp and use that binary.
2. **Orphaned `sd-server`.** `Close()` handles SIGTERM, but if the ML worker is
   SIGKILLed the child can survive. Consider starting `sd-server` in its own
   process group and killing the group, or use `cmd.WaitDelay` plus a PID file.
3. **Admission estimates.** `expected_ram_mb` is declared, not measured. Watch
   `free -h` and `~/meridian.log`; adjust the config rather than overcommitting.
4. **OCR language data.** Only `eng` is installed. Install
   `tesseract-langpack-<lang>` (Fedora) and set `ocr.language`.
5. **SD model license.** SD-Turbo has its own license; review before
   redistribution or commercial use.
6. **No auth/TLS.** Keep `--host 127.0.0.1` for local use. If exposing on the
   LAN, put a reverse proxy with auth/TLS in front.
7. **UI steps default.** Changing the default to 4 is correct for SD-Turbo but
   poor for SD 1.5. A future improvement is to expose per-model defaults in the
   catalog and let the UI initialise from it.
8. **Real OCR model semantics.** Tesseract is a real OCR engine, but it is not
   the PaddleOCR model the config names (`pp-ocrv5-mobile-en`). Use a distinct
   `model_id` such as `tesseract-eng` so clients are not misled.
9. **Concurrent mode.** If someone runs `--serving-mode concurrent`, the
   `protect-llm` admission policy and `protected_llm_vram_mb` matter again.
   The sample real config is tuned for exclusive mode.

---

## 11. Rollback

The implementation is additive and flag-gated:

- Without `--llm-model`, `DefaultWorkers()` is used exactly as before.
- Without `engine: tesseract`, OCR behaves as before.
- Without `server_command`, `sd-server` is still treated as external.
- `configs/ml.yaml` and `configs/ml-sd-server.yaml` are untouched.

To roll back, remove the new flags/config fields and restore
`internal/balancer/workers.go`, `supervisor.go`, `cmd/meridian-server/main.go`,
`internal/ml/{config,worker,service,backend}.go` and
`web/src/components/MLStudio.tsx` from `6415a55`.
