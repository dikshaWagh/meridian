#!/usr/bin/env bash
# Start Meridian with the local qwen2.5 1.5B GGUF on this machine.
# Override any value from the environment, e.g. CTX=8192 ./scripts/serve-local.sh
set -euo pipefail

cd "$(dirname "$0")/.."

MODEL="${MODEL:-qwen2.5-1.5b-instruct-q4_k_m.gguf}"
MODELS_DIR="${MODELS_DIR:-$HOME/models}"
LLAMA_BIN="${LLAMA_BIN:-/usr/local/lib/ollama/llama-server}"
LLM_WORKERS="${LLM_WORKERS:-2}"
THREADS="${THREADS:-8}"
CTX="${CTX:-4096}"

model_path="$MODELS_DIR/$MODEL"
if [[ ! -r "$model_path" ]]; then
	echo "error: cannot read $model_path" >&2
	echo "       ls -l \"$MODELS_DIR\"" >&2
	exit 1
fi

if ! command -v "$LLAMA_BIN" >/dev/null 2>&1 && [[ ! -x "$LLAMA_BIN" ]]; then
	echo "error: llama-server not found or not executable: $LLAMA_BIN" >&2
	exit 1
fi

# The balancer does not reap workers it does not own; orphaned ones keep
# 8081+ busy and the next start then reports "no healthy worker".
if ss -ltn 2>/dev/null | grep -qE ':808[1-9]'; then
	echo "warning: ports 8081+ are in use; stop leftover workers first:" >&2
	echo "         pkill -f llama-server" >&2
fi

exec ./bin/meridian-server serve \
	--llm-model "$MODEL" \
	--llm-workers "$LLM_WORKERS" \
	--llama-bin "$LLAMA_BIN" \
	--models-dir "$MODELS_DIR" \
	--threads "$THREADS" \
	--ctx "$CTX"
