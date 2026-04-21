#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

mkdir -p bin

if [[ ! -x "bin/antigravityworker" ]]; then
  echo "[local-antigravity] building standard worker..."
  make build-antigravityworker
fi

if [[ ! -x "bin/antigravityworker-boringcrypto" ]]; then
  echo "[local-antigravity] building boringcrypto worker..."
  if ! make build-antigravityworker-boringcrypto; then
    echo "[local-antigravity] boringcrypto build failed, will fall back to standard worker" >&2
  fi
fi

export ANTIGRAVITY_EXTERNAL_WORKER_BIN="$ROOT_DIR/bin/antigravityworker"
if [[ -x "$ROOT_DIR/bin/antigravityworker-boringcrypto" ]]; then
  export ANTIGRAVITY_EXTERNAL_WORKER_BIN_BORINGCRYPTO="$ROOT_DIR/bin/antigravityworker-boringcrypto"
  export ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO="${ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO:-true}"
fi

export SERVER_HOST="${SERVER_HOST:-127.0.0.1}"
export SERVER_PORT="${SERVER_PORT:-18731}"

echo "[local-antigravity] standard worker: $ANTIGRAVITY_EXTERNAL_WORKER_BIN"
if [[ -n "${ANTIGRAVITY_EXTERNAL_WORKER_BIN_BORINGCRYPTO:-}" ]]; then
  echo "[local-antigravity] boringcrypto worker: $ANTIGRAVITY_EXTERNAL_WORKER_BIN_BORINGCRYPTO"
fi
echo "[local-antigravity] prefer boringcrypto: ${ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO:-false}"
echo "[local-antigravity] local server: http://${SERVER_HOST}:${SERVER_PORT}"

exec go run -tags embed ./cmd/server "$@"
