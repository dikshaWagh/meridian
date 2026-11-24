#!/usr/bin/env bash
# Install Meridian into your home directory on a machine where you have no sudo.
#
#   scripts/install-nosudo.sh check     what is present, what is missing
#   scripts/install-nosudo.sh install   download llama.cpp and set up the folder
#   scripts/install-nosudo.sh models    download the model file
#   scripts/install-nosudo.sh run       start the server
#   scripts/install-nosudo.sh all       install, models, run
#
# Nothing is written outside $MERIDIAN_HOME (default ~/meridian). No apt, no root.
#
# The Meridian binary is not downloadable on its own, so copy it here first:
#   scp bin/meridian-server-static user@host:~/meridian/bin/meridian-server
# or point BIN_SOURCE at a local copy:
#   BIN_SOURCE=/tmp/meridian-server scripts/install-nosudo.sh install
#
# Overrides (environment variables):
#   MERIDIAN_HOME   install folder           (default: ~/meridian)
#   LLAMA_VERSION   llama.cpp build tag      (default: b12359)
#   LLAMA_TARBALL   already-downloaded llama.cpp tar.gz (offline install)
#   MODEL_SOURCE    already-downloaded .gguf (offline install)
#   BIN_SOURCE      local meridian-server to copy in
#   MODELS_DIR      model folder             (default: <home>/models)
#   INSTANCES       workers for the model    (default: 1)
#   PORT            HTTP port                (default: 8080)
#   THREADS         threads per worker       (default: cores / instances)
#   CTX             context size             (default: 4096)
#   PARALLEL        slots per worker         (default: 1)
#   RAM_HEADROOM_MB RAM kept free            (default: 512)

set -euo pipefail

MERIDIAN_HOME="${MERIDIAN_HOME:-$HOME/meridian}"
MODELS_DIR="${MODELS_DIR:-$MERIDIAN_HOME/models}"
LLAMA_DIR="$MERIDIAN_HOME/llama"
BIN="$MERIDIAN_HOME/bin/meridian-server"
LLAMA_VERSION="${LLAMA_VERSION:-b12359}"
PORT="${PORT:-8080}"
INSTANCES="${INSTANCES:-1}"
CTX="${CTX:-4096}"
PARALLEL="${PARALLEL:-1}"
RAM_HEADROOM_MB="${RAM_HEADROOM_MB:-512}"
MODEL_FILE="qwen2.5-0.5b-instruct-q4_k_m.gguf"
MODEL_URL="https://huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF/resolve/main/$MODEL_FILE"
LLAMA_URL="https://github.com/ggml-org/llama.cpp/releases/download/$LLAMA_VERSION/llama-$LLAMA_VERSION-bin-ubuntu-x64.tar.gz"

say() { printf '%s\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

fetch() { # url destination
  local url="$1" dest="$2"
  mkdir -p "$(dirname "$dest")"
  if have curl; then
    curl -fL --retry 3 -C - -o "$dest.part" "$url"
  elif have wget; then
    wget -c -O "$dest.part" "$url"
  else
    die "need curl or wget to download files (or copy them over and use LLAMA_TARBALL / MODEL_SOURCE / BIN_SOURCE)"
  fi
  mv "$dest.part" "$dest"
}

missing_libs() {
  have ldd || return 0
  ldd "$LLAMA_DIR/llama-server" 2>/dev/null | awk '/not found/ {print $1}' | sort -u
}

# Pull a Debian package apart into $LLAMA_DIR/lib without root.
extract_pkg() { # package name
  local pkg="$1" tmp
  have apt-get && have dpkg-deb || return 1
  tmp="$(mktemp -d)" || return 1
  if (cd "$tmp" && apt-get download "$pkg" >/dev/null 2>&1); then
    mkdir -p "$LLAMA_DIR/lib"
    dpkg-deb -x "$tmp"/*.deb "$LLAMA_DIR/lib"
    rm -rf "$tmp"
    return 0
  fi
  rm -rf "$tmp"
  return 1
}

fix_libs() {
  local lib pkg fixed=0
  local libs
  libs="$(missing_libs)"
  [ -n "$libs" ] || return 0
  say "llama-server is missing: $(echo $libs)"

  for lib in $libs; do
    case "$lib" in
      libgomp.so.1)      pkg=libgomp1 ;;
      libstdc++.so.6)    pkg=libstdc++6 ;;
      libgcc_s.so.1)     pkg=libgcc-s1 ;;
      libssl.so.3|libcrypto.so.3) pkg=libssl3t64 ;;
      libz.so.1)         pkg=zlib1g ;;
      *) continue ;;
    esac
    # Debian 13 renamed the OpenSSL package; try the older name too.
    if extract_pkg "$pkg" || { [ "$pkg" = libssl3t64 ] && extract_pkg libssl3; }; then
      fixed=$((fixed + 1))
    else
      say "  could not unpack $pkg automatically"
    fi
  done

  libs="$(missing_libs)"
  if [ -z "$libs" ]; then
    say "  runtime libraries unpacked into llama/lib"
  else
    say "  still missing: $(echo $libs)"
    say "  copy these .deb files from another machine and unpack them with:"
    say "    dpkg-deb -x <file>.deb $LLAMA_DIR/lib"
  fi
}

cmd_check() {
  say "Meridian check ($MERIDIAN_HOME)"
  if [ -x "$BIN" ]; then say "  ok       server binary"; else say "  missing  server binary ($BIN)"; fi
  if [ -x "$LLAMA_DIR/llama-server" ]; then
    say "  ok       llama-server ($LLAMA_VERSION)"
    local libs; libs="$(missing_libs)"
    if [ -n "$libs" ]; then say "  missing  libraries: $(echo $libs)"; else say "  ok       libraries"; fi
  else
    say "  missing  llama-server (run: install)"
  fi
  if [ -s "$MODELS_DIR/$MODEL_FILE" ]; then say "  ok       model $MODEL_FILE"; else say "  missing  model $MODEL_FILE (run: models)"; fi
  say "  info     cores: $(nproc 2>/dev/null || echo '?')"
  awk '/MemAvailable/ { printf "  info     RAM available: %.1f GB\n", $2/1024/1024 }' /proc/meminfo 2>/dev/null || true
  local disk
  if [ -d "$MERIDIAN_HOME" ]; then
    disk="$(df -h "$MERIDIAN_HOME" 2>/dev/null | awk 'NR==2 {print $4}')"
  else
    disk="$(df -h "$HOME" 2>/dev/null | awk 'NR==2 {print $4}')"
  fi
  say "  info     disk free: ${disk:-?}"
}

cmd_install() {
  mkdir -p "$MERIDIAN_HOME/bin" "$MODELS_DIR"

  if [ -n "${BIN_SOURCE:-}" ]; then
    [ -f "$BIN_SOURCE" ] || die "BIN_SOURCE does not exist: $BIN_SOURCE"
    cp "$BIN_SOURCE" "$BIN"
    chmod +x "$BIN"
    say "installed server binary from $BIN_SOURCE"
  fi

  if [ -x "$LLAMA_DIR/llama-server" ]; then
    say "llama.cpp already installed in $LLAMA_DIR"
  else
    if [ -n "${LLAMA_TARBALL:-}" ]; then
      [ -f "$LLAMA_TARBALL" ] || die "LLAMA_TARBALL does not exist: $LLAMA_TARBALL"
      cp "$LLAMA_TARBALL" "$MERIDIAN_HOME/llama.tar.gz"
      say "using llama.cpp tarball from $LLAMA_TARBALL"
    else
      say "downloading llama.cpp $LLAMA_VERSION (about 23 MB)"
      fetch "$LLAMA_URL" "$MERIDIAN_HOME/llama.tar.gz"
    fi
    mkdir -p "$MERIDIAN_HOME/tmp-llama"
    tar xzf "$MERIDIAN_HOME/llama.tar.gz" -C "$MERIDIAN_HOME/tmp-llama"
    [ -d "$MERIDIAN_HOME/tmp-llama/llama-$LLAMA_VERSION" ] \
      || die "tarball does not contain llama-$LLAMA_VERSION (set LLAMA_VERSION to match)"
    rm -f "$MERIDIAN_HOME/llama.tar.gz"
    rm -rf "$LLAMA_DIR"
    mv "$MERIDIAN_HOME/tmp-llama/llama-$LLAMA_VERSION" "$LLAMA_DIR"
    rmdir "$MERIDIAN_HOME/tmp-llama"
    chmod +x "$LLAMA_DIR"/llama-server "$LLAMA_DIR"/llama 2>/dev/null || true
    say "llama.cpp unpacked into $LLAMA_DIR"
  fi
  fix_libs

  write_launcher
  say ""
  say "next:"
  [ -x "$BIN" ] || say "  copy the server binary to $BIN (scp or BIN_SOURCE)"
  say "  scripts/install-nosudo.sh models"
  say "  scripts/install-nosudo.sh run"
}

cmd_models() {
  [ -d "$MODELS_DIR" ] || mkdir -p "$MODELS_DIR"
  if [ -s "$MODELS_DIR/$MODEL_FILE" ]; then
    say "model already present: $MODEL_FILE"
    return
  fi
  if [ -n "${MODEL_SOURCE:-}" ]; then
    [ -f "$MODEL_SOURCE" ] || die "MODEL_SOURCE does not exist: $MODEL_SOURCE"
    cp "$MODEL_SOURCE" "$MODELS_DIR/$MODEL_FILE"
    say "model copied from $MODEL_SOURCE"
    return
  fi
  say "downloading $MODEL_FILE (about 0.49 GB)"
  fetch "$MODEL_URL" "$MODELS_DIR/$MODEL_FILE"
  say "model saved to $MODELS_DIR/$MODEL_FILE"
}

write_launcher() {
  local cores threads
  cores="$(nproc 2>/dev/null || echo 2)"
  threads="${THREADS:-$((cores / INSTANCES))}"
  [ "$threads" -ge 1 ] || threads=1

  cat > "$MERIDIAN_HOME/run.sh" <<EOF
#!/usr/bin/env bash
set -euo pipefail
export LD_LIBRARY_PATH="$LLAMA_DIR/lib:\${LD_LIBRARY_PATH:-}"
exec "$BIN" serve \\
  --llm "$MODEL_FILE:$INSTANCES" \\
  --models-dir "$MODELS_DIR" \\
  --llama-bin "$LLAMA_DIR/llama-server" \\
  --threads "$threads" --parallel "$PARALLEL" --ctx "$CTX" \\
  --routing-policy token-aware \\
  --host 0.0.0.0 --port "$PORT" \\
  --ram-headroom-mb "$RAM_HEADROOM_MB" "\$@"
EOF
  chmod +x "$MERIDIAN_HOME/run.sh"
}

cmd_run() {
  [ -x "$BIN" ] || die "no server binary at $BIN (copy one, or set BIN_SOURCE and run install)"
  [ -x "$LLAMA_DIR/llama-server" ] || die "llama-server not installed (run: install)"
  [ -s "$MODELS_DIR/$MODEL_FILE" ] || die "model not downloaded (run: models)"
  write_launcher
  local ip
  ip="$(hostname -I 2>/dev/null | awk '{print $1}')" || true
  say "starting Meridian on port $PORT"
  say "open http://${ip:-localhost}:$PORT/ from another machine"
  exec "$MERIDIAN_HOME/run.sh"
}

case "${1:-}" in
  check)   cmd_check ;;
  install) cmd_install ;;
  models)  cmd_models ;;
  run)     cmd_run ;;
  all)     cmd_install; cmd_models; cmd_run ;;
  *)
    sed -n '2,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
    exit 2
    ;;
esac
