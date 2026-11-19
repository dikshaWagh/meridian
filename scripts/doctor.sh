#!/usr/bin/env bash
# Collect everything needed to explain a Meridian start failure.
# Usage: ./scripts/doctor.sh
set -uo pipefail

cd "$(dirname "$0")/.."
MODELS_DIR="${MODELS_DIR:-$HOME/models}"
MODEL="${MODEL:-qwen2.5-1.5b-instruct-q4_k_m.gguf}"
MODEL_PATH="$MODELS_DIR/$MODEL"

echo "== session =="
echo "pwd:        $PWD"
echo "user:       $(id)"
echo "HOME:       $HOME"
echo "selinux:    $(tr -d '\0' < /proc/self/attr/current 2>/dev/null || echo n/a)"
echo "enforcing:  $(getenforce 2>/dev/null || echo n/a)"
[ -f /.flatpak-info ] && echo "flatpak:    YES (sandboxed terminal - this breaks model access)"
[ -f /.dockerenv ] && echo "docker:     YES (container - check bind mounts)"
[ -n "${container:-}" ] && echo "container:  $container"

echo
echo "== repo =="
git log --oneline -1 2>&1
echo "dirty files: $(git status --porcelain 2>/dev/null | wc -l)"

echo
echo "== binary =="
ls -l bin/meridian-server 2>&1
sha256sum bin/meridian-server 2>&1
./bin/meridian-server version 2>&1 | sed 's/^/  /' || echo "  version subcommand missing -> STALE BINARY"

echo
echo "== model =="
ls -ld "$MODELS_DIR" 2>&1
ls -l "$MODEL_PATH" 2>&1
stat "$MODEL_PATH" 2>&1 | sed -n '1,7p'
echo -n "first bytes: "
head -c 4 "$MODEL_PATH" 2>/dev/null | od -An -c | tr -d '\n'; echo
if head -c 4 "$MODEL_PATH" 2>/dev/null | grep -q GGUF; then
	echo "GGUF header: OK"
else
	echo "GGUF header: BAD - file is truncated or not a GGUF"
fi

echo
echo "== llama-server =="
LLAMA_BIN="${LLAMA_BIN:-/usr/local/lib/ollama/llama-server}"
ls -l "$LLAMA_BIN" 2>&1
file "$LLAMA_BIN" 2>&1 | cut -c1-140

echo
echo "== ports 8080-8089 =="
ss -ltn 2>/dev/null | grep -E ':808[0-9]' || echo "all free"

echo
echo "Paste this whole output when reporting the problem."
