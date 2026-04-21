#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

MODE="normal"
OUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --boringcrypto)
      MODE="boringcrypto"
      shift
      ;;
    --output)
      OUT="${2:-}"
      shift 2
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

mkdir -p bin

if [[ -z "$OUT" ]]; then
  if [[ "$MODE" == "boringcrypto" ]]; then
    OUT="bin/antigravityworker-boringcrypto"
  else
    OUT="bin/antigravityworker"
  fi
fi

LDFLAGS=""

if [[ "$MODE" == "boringcrypto" ]]; then
  echo "building Antigravity external worker with boringcrypto -> $OUT"
  CGO_ENABLED=1 GOEXPERIMENT=boringcrypto go build -trimpath -ldflags="$LDFLAGS" -o "$OUT" ./cmd/antigravityworker
else
  echo "building Antigravity external worker -> $OUT"
  CGO_ENABLED=0 go build -trimpath -ldflags="$LDFLAGS" -o "$OUT" ./cmd/antigravityworker
fi

chmod +x "$OUT"
echo "done: $OUT"
