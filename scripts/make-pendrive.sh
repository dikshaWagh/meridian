#!/usr/bin/env bash
# Copy a handoff bundle of Meridian to a folder (for example a USB stick mount point).
#
#   scripts/make-pendrive.sh /media/rudra/PENDRIVE/meridian
#
# The bundle has the source, the Linux server binary, the setup script, and the
# smallest model (0.49 GB). Larger models are downloaded by the teacher with
# `scripts/meridian-setup.sh models`, so the stick does not need 5 GB.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="${1:-}"
[ -n "$DEST" ] || { echo "usage: $0 <destination-folder>" >&2; exit 2; }
command -v rsync >/dev/null 2>&1 || { echo "error: rsync is required" >&2; exit 1; }

make -C "$ROOT" server >/dev/null

mkdir -p "$DEST/models"
rsync -a \
  --exclude 'node_modules/' \
  --exclude 'data/' \
  --exclude 'logs/' \
  --exclude 'models/' \
  --exclude 'bin/' \
  --exclude '*.jsonl' \
  --exclude 'stress-results*.json' \
  --exclude 'MULTI_MODEL_*.md' \
  "$ROOT/" "$DEST/"

mkdir -p "$DEST/bin"
cp "$ROOT/bin/meridian-server" "$DEST/bin/"
cp "$ROOT/models/qwen2.5-0.5b-instruct-q4_k_m.gguf" "$DEST/models/"

echo "bundle written to $DEST"
du -sh "$DEST"
