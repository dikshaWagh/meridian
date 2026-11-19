# Multi-Model Single-Node Architecture Design

## Goal
Enable a single Meridian node to serve multiple different LLM models, each with configurable worker instances.

## Current State
- Only supports: `--llm-model qwen.gguf --llm-workers 2`
- This loads ONE model file with 2 instances

## Desired State
- Support: `--llm qwen.gguf:2 --llm llama.gguf:3 --llm mistral.gguf:1`
- This loads THREE different models with 2, 3, and 1 instances respectively

## Implementation Approach

### 1. New Flag Syntax
```bash
# Old (single model only)
./bin/meridian-server serve \
  --llm-model qwen.gguf \
  --llm-workers 2 \
  --models-dir ~/models

# New (multi-model)
./bin/meridian-server serve \
  --llm qwen.gguf:2 \
  --llm llama.gguf:3 \
  --llm mistral.gguf:1 \
  --models-dir ~/models
```

### 2. Configuration Parsing

Each `--llm` flag accepts format: `filename:worker_count`
- Parse to: `{"filename": "qwen.gguf", "workers": 2}`
- Default workers to 1 if not specified: `--llm qwen.gguf` → 1 worker
- Support multiple `--llm` flags

### 3. Worker Pool Structure

Current:
```go
type Options struct {
  LLMModel   string  // "qwen.gguf"
  LLMWorkers int     // 2
}
```

New:
```go
type LLMConfig struct {
  Model  string  // "qwen.gguf"
  Workers int    // 2
}

type Options struct {
  LLMConfigs []LLMConfig  // [{"qwen.gguf", 2}, {"llama.gguf", 3}]
}
```

## Implementation Steps

1. **Modify CLI parsing** (cmd/meridian-server/main.go)
   - Add `--llm model:count` flag (repeatable)
   - Parse each into LLMConfig
   - Support both old flags (convert to new format) for backwards compatibility

2. **Update Options struct** (internal/balancer/supervisor.go)
   - Add `LLMConfigs []LLMConfig`
   - Keep LLMModel/LLMWorkers for backwards compat (convert to LLMConfigs)

3. **Update pool initialization** (internal/balancer/pool.go)
   - Create multiple worker pools, one per model
   - Spawn workers for each model independently

4. **Update routing** (internal/balancer/handler.go)
   - Extract `model` from request
   - Route to correct pool
   - Failover within same pool only

5. **Update catalog API** (/v1/catalog)
   - Return all models with their worker counts

6. **Update status API** (/status)
   - Return workers grouped by model

## Benefits

✅ Single node serves multiple models
✅ Flexible worker counts per model
✅ Independent health FSM per worker
✅ Independent load balancing per model pool
✅ Fault tolerance within each pool
✅ Real-time status per model in UI
✅ Backwards compatible with old flags

