# Multi-Model Quick Start Guide

## The Goal ✨

Serve multiple different LLM models from a single Meridian node.

## The New Syntax 🚀

```bash
./bin/meridian-server serve \
  --llm qwen.gguf:2 \
  --llm llama.gguf:3 \
  --llm mistral.gguf:1 \
  --models-dir ~/models
```

This command:
- Loads `qwen.gguf` with **2 workers** on ports 8081, 8082
- Loads `llama.gguf` with **3 workers** on ports 8083, 8084, 8085
- Loads `mistral.gguf` with **1 worker** on port 8086
- Uses `~/models` as the model directory for relative paths

## Format Breakdown 📝

`--llm <filename>:<worker_count>`

| Part | Example | Notes |
|------|---------|-------|
| `<filename>` | `qwen.gguf` | Relative to `--models-dir` or absolute path |
| `:<worker_count>` | `:2` | Optional; defaults to 1 if omitted |

## Examples 📚

### Single Model
```bash
./bin/meridian-server serve --llm qwen.gguf:2
```

### Multiple Models with Different Sizes
```bash
./bin/meridian-server serve \
  --llm qwen2.5-7b.gguf:2 \
  --llm llama-13b.gguf:1 \
  --llm mistral-7b.gguf:3
```

### Absolute Paths
```bash
./bin/meridian-server serve \
  --llm /data/qwen.gguf:2 \
  --llm /data/llama.gguf:3
```

### Mixed Absolute and Relative
```bash
./bin/meridian-server serve \
  --llm qwen.gguf:2 \
  --llm /absolute/path/llama.gguf:3 \
  --models-dir ~/models
```

### Default Worker Count
```bash
# qwen gets 1 worker, llama gets 2
./bin/meridian-server serve \
  --llm qwen.gguf \
  --llm llama.gguf:2
```

## API Usage 🔌

Clients just specify the `model` field:

```bash
# Request to qwen model
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen",
    "messages": [{"role": "user", "content": "Hello"}]
  }'

# Request to llama model
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama",
    "messages": [{"role": "user", "content": "Hello"}]
  }'

# Request to mistral model
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mistral",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

## Check Available Models 📋

```bash
# List available models
curl http://localhost:8080/v1/models

# Get detailed catalog (includes worker counts and health)
curl http://localhost:8080/v1/catalog

# Get worker status
curl http://localhost:8080/status
```

Example response from `/v1/models`:
```json
{
  "object": "list",
  "data": [
    {"id": "llama", "object": "model"},
    {"id": "mistral", "object": "model"},
    {"id": "qwen", "object": "model"}
  ]
}
```

Example response from `/v1/catalog`:
```json
{
  "llm": [
    {
      "id": "llama",
      "workers": 3,
      "healthy_workers": 3,
      "state": "ready",
      ...
    },
    {
      "id": "mistral",
      "workers": 1,
      "healthy_workers": 1,
      "state": "ready",
      ...
    },
    {
      "id": "qwen",
      "workers": 2,
      "healthy_workers": 2,
      "state": "ready",
      ...
    }
  ]
}
```

## Model Names 🏷️

Model names are derived from filenames:
- `qwen.gguf` → model name: `qwen`
- `llama-3.2-3b.gguf` → model name: `llama-3.2-3b`
- `mistral_7b_instruct.gguf` → model name: `mistral_7b_instruct`
- `/path/to/model.gguf` → model name: `model` (basename without extension)

## Backwards Compatibility 🔄

Old flags still work:
```bash
# Old way (still supported)
./bin/meridian-server serve \
  --llm-model qwen.gguf \
  --llm-workers 2

# Is equivalent to:
./bin/meridian-server serve \
  --llm qwen.gguf:2
```

## Worker Port Assignment 🔌

Ports are assigned sequentially starting at 8081:
```
Model config:
  qwen.gguf:2
  llama.gguf:3

Port assignment:
  w1: qwen → 8081
  w2: qwen → 8082
  w3: llama → 8083
  w4: llama → 8084
  w5: llama → 8085
```

## Session Affinity 📌

Conversations stay on the same worker:
1. First request for a conversation ID picks a worker
2. Subsequent requests stay on that worker
3. If worker dies, conversation migrates to a healthy worker in same model
4. If requesting different model, error is returned (model mismatch)

## Health Checks ✅

Each worker has independent health status:
```bash
# Check all workers
curl http://localhost:8080/status | jq '.workers'

# Output shows workers grouped by model
[
  {"id": "w1", "model": "qwen", "state": "healthy", ...},
  {"id": "w2", "model": "qwen", "state": "healthy", ...},
  {"id": "w3", "model": "llama", "state": "healthy", ...},
  ...
]
```

## Error Handling 🚨

| Error | Cause | Solution |
|-------|-------|----------|
| `model_not_found` | Unknown model name | Check `/v1/models` endpoint |
| `no_healthy_worker` | All workers unhealthy | Check `/status` endpoint |
| `workers_starting` | Workers still initializing | Wait a few seconds |
| `model_mismatch` | Conversation bound to different model | Don't reuse conversation ID for different models |

## Configuration Tips 💡

### CPU-Only Setup
```bash
./bin/meridian-server serve \
  --llm qwen.gguf:2 \
  --llm llama.gguf:1 \
  --threads 8 \
  --ngl 0
```

### GPU Setup
```bash
./bin/meridian-server serve \
  --llm qwen.gguf:2 \
  --llm llama.gguf:1 \
  --ngl 30 \
  --gpu-workers 3 \
  --backend-lib /usr/local/lib/libggml-vulkan.so
```

### High Context Window
```bash
./bin/meridian-server serve \
  --llm qwen.gguf:1 \
  --ctx 32768 \
  --parallel 4
```

## Testing 🧪

```bash
# Build
go build -o bin/meridian-server ./cmd/meridian-server

# Test: Run with multiple models
./bin/meridian-server serve \
  --llm qwen.gguf:2 \
  --llm llama.gguf:3 \
  --enable-llm=false  # Don't start workers, just test parsing

# Check all tests pass
go test ./internal/balancer -v
```

## Troubleshooting 🔧

**Q: How do I know which models are available?**
```bash
curl http://localhost:8080/v1/models
```

**Q: Why is my request returning "model_not_found"?**
Make sure the model name matches. Model names are derived from filenames:
- Use `qwen` not `qwen.gguf`
- Use `llama-3.2-3b` not `llama-3.2-3b.gguf`

**Q: Can I change models without restarting?**
Not yet. Models are loaded at startup. Stop and restart the server to change.

**Q: What's the port for each model?**
Check `/status` endpoint to see which worker is on which port.

**Q: Can I use the same model file twice?**
Yes:
```bash
./bin/meridian-server serve \
  --llm qwen.gguf:2 \
  --llm qwen.gguf:3
```
This would load qwen with 5 workers total. Note they all share the same model name.

**Q: How do I check worker health?**
```bash
curl http://localhost:8080/status | jq '.workers[] | {id, model, state}'
```

---

Ready to run? Just execute:
```bash
./bin/meridian-server serve --llm model1.gguf:2 --llm model2.gguf:3
```
