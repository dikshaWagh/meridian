#!/usr/bin/env bash
# Meridian setup helper for a new machine.
#
#   scripts/meridian-setup.sh check    what is installed and what is missing
#   scripts/meridian-setup.sh models   download missing model files (needs internet)
#   scripts/meridian-setup.sh update   pull the latest code and rebuild (needs git and Go)
#   scripts/meridian-setup.sh run      start the server with every model present
#   scripts/meridian-setup.sh all      check, models, update, run
#
# Overrides (environment variables):
#   MODELS_DIR  model folder             (default: <repo>/models)
#   LLAMA_BIN   llama-server binary      (default: llama-server on PATH, else /usr/local/lib/ollama/llama-server)
#   INSTANCES   instances per model      (default: 1)
#   PORT        gateway port             (default: 8080)
#   THREADS     threads per worker       (default: CPU cores / total workers)
#   EXTRA_FLAGS extra serve flags, e.g. "--ngl 99 --gpu-workers 1" for an NVIDIA GPU

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODELS_DIR="${MODELS_DIR:-$ROOT/models}"
BIN="$ROOT/bin/meridian-server"
LLAMA_BIN="${LLAMA_BIN:-$(command -v llama-server 2>/dev/null || echo /usr/local/lib/ollama/llama-server)}"
PORT="${PORT:-8080}"
INSTANCES="${INSTANCES:-1}"
REPO_URL="https://github.com/dikshaWagh/meridian"

# file name | download URL | approximate size in GB
MODELS=(
  "qwen2.5-0.5b-instruct-q4_k_m.gguf|https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/qwen2.5-0.5b-instruct-q4_k_m.gguf|0.49"
  "qwen2.5-3b-instruct-q4_k_m.gguf|https://huggingface.co/Qwen/Qwen2.5-3B-Instruct-GGUF/resolve/main/qwen2.5-3b-instruct-q4_k_m.gguf|2.10"
  "llama-3.2-3b-instruct-q4_k_m.gguf|https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF/resolve/main/Llama-3.2-3B-Instruct-Q4_K_M.gguf|2.02"
)

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

cmd_check() {
  say "Meridian check ($ROOT)"
  if [ -x "$BIN" ]; then say "  ok       server binary: bin/meridian-server"; else say "  missing  server binary (run: update)"; fi
  if [ -x "$LLAMA_BIN" ]; then say "  ok       llama-server: $LLAMA_BIN"; else say "  missing  llama-server at $LLAMA_BIN (set LLAMA_BIN)"; fi
  if command -v go >/dev/null 2>&1; then say "  ok       Go installed (update can rebuild)"; else say "  note     Go not installed (bundled binary only)"; fi
  for entry in "${MODELS[@]}"; do
    IFS='|' read -r name _ size <<< "$entry"
    if [ -s "$MODELS_DIR/$name" ]; then say "  ok       model $name"; else say "  missing  model $name (~${size} GB)"; fi
  done
  awk '/MemAvailable/ { printf "  info     RAM available: %.1f GB\n", $2/1024/1024 }' /proc/meminfo
  if command -v nvidia-smi >/dev/null 2>&1; then
    say "  info     GPU: $(nvidia-smi --query-gpu=name,memory.total --format=csv,noheader | head -1)"
  else
    say "  info     no NVIDIA GPU detected (CPU mode)"
  fi
}

cmd_models() {
  command -v curl >/dev/null 2>&1 || die "curl is required to download models"
  mkdir -p "$MODELS_DIR"
  for entry in "${MODELS[@]}"; do
    IFS='|' read -r name url size <<< "$entry"
    dest="$MODELS_DIR/$name"
    if [ -s "$dest" ]; then say "skip: $name already present"; continue; fi
    say "downloading $name (~${size} GB)"
    # -C - resumes an interrupted download; the .part file is renamed only when complete.
    if curl -fL -C - -o "$dest.part" "$url"; then
      mv "$dest.part" "$dest"
    else
      die "download failed for $name; run models again to resume"
    fi
  done
}

cmd_update() {
  if [ -d "$ROOT/.git" ]; then
    if git -C "$ROOT" pull --ff-only; then
      say "code is up to date"
    else
      say "could not update (offline, or local changes); keeping current code"
    fi
  else
    say "not a git checkout; to get the latest code, download it from $REPO_URL"
  fi
  if command -v go >/dev/null 2>&1; then
    if (cd "$ROOT" && make server); then
      say "rebuilt server from source"
    else
      say "build failed; keeping the bundled binary"
    fi
  else
    say "Go not installed: using the bundled binary"
  fi
}

cmd_run() {
  [ -x "$BIN" ] || die "no server binary at $BIN (run: update)"
  [ -x "$LLAMA_BIN" ] || die "llama-server not found at $LLAMA_BIN (set LLAMA_BIN)"

  local args=() count=0 entry name
  for entry in "${MODELS[@]}"; do
    IFS='|' read -r name _ _ <<< "$entry"
    if [ -s "$MODELS_DIR/$name" ]; then
      args+=(--llm "$name:$INSTANCES")
      count=$((count + 1))
    fi
  done
  [ "$count" -gt 0 ] || die "no model files in $MODELS_DIR (run: models)"

  local total=$((count * INSTANCES))
  local threads="${THREADS:-$(( $(nproc) / total ))}"
  [ "$threads" -ge 1 ] || threads=1

  local extra=()
  if [ -n "${EXTRA_FLAGS:-}" ]; then read -r -a extra <<< "$EXTRA_FLAGS"; fi

  say "starting $total worker(s) for $count model(s) on port $PORT"
  say "open http://$(hostname -I 2>/dev/null | awk '{print $1}'):$PORT/ from another device on the same network"
  exec "$BIN" serve "${args[@]}" \
    --models-dir "$MODELS_DIR" --llama-bin "$LLAMA_BIN" \
    --threads "$threads" --ctx 4096 --routing-policy token-aware \
    --host 0.0.0.0 --port "$PORT" --ram-headroom-mb 1024 \
    ${extra[@]+"${extra[@]}"}
}

case "${1:-}" in
  check)  cmd_check ;;
  models) cmd_models ;;
  update) cmd_update ;;
  run)    cmd_run ;;
  all)    cmd_check; cmd_models; cmd_update; cmd_run ;;
  *)
    sed -n '2,15p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
esac
