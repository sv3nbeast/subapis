#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/check-prod-antigravity-boringcrypto.sh [options]

Description:
  Verify whether a production sub2api deployment actually carries and prefers
  the Antigravity boringcrypto worker.

Options:
  --host HOST         SSH host alias. Default: sub2api-prod
  --container NAME    Docker container name. Default: sub2api
  --deploy-dir DIR    Remote deploy directory. Default: /root/sub2api-deploy
  -h, --help          Show this help

Exit codes:
  0  boringcrypto worker is present in the running container
  1  check failed or boringcrypto worker is missing
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

HOST="sub2api-prod"
CONTAINER="sub2api"
DEPLOY_DIR="/root/sub2api-deploy"

while (($# > 0)); do
  case "$1" in
    --host)
      HOST="$2"
      shift 2
      ;;
    --container)
      CONTAINER="$2"
      shift 2
      ;;
    --deploy-dir)
      DEPLOY_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

require_cmd ssh

read_remote_file() {
  local path="$1"
  ssh "$HOST" "test -f '$path' && cat '$path' || true"
}

echo "Host:       $HOST"
echo "Container:  $CONTAINER"
echo "Deploy dir: $DEPLOY_DIR"

container_image="$(ssh "$HOST" "docker inspect --format '{{.Config.Image}}' '$CONTAINER' 2>/dev/null" || true)"
if [[ -z "$container_image" ]]; then
  echo "Container not found: $CONTAINER" >&2
  exit 1
fi

echo "Image:      $container_image"

override_file="$DEPLOY_DIR/docker-compose.override.yml"
override_contents="$(read_remote_file "$override_file")"
if [[ -n "$override_contents" ]]; then
  echo
  echo "Compose override:"
  echo "$override_contents"
fi

container_env="$(ssh "$HOST" "docker exec '$CONTAINER' sh -lc 'env | grep ANTIGRAVITY | sort' 2>/dev/null" || true)"
echo
echo "Container env:"
if [[ -n "$container_env" ]]; then
  echo "$container_env"
else
  echo "(no ANTIGRAVITY_* env vars)"
fi

worker_listing="$(ssh "$HOST" "docker exec '$CONTAINER' sh -lc 'ls -l /app/antigravityworker* 2>/dev/null'")"
echo
echo "Worker files:"
echo "$worker_listing"

has_boring="false"
if grep -q '/app/antigravityworker-boringcrypto' <<<"$worker_listing"; then
  has_boring="true"
fi

prefer_boring="true"
if grep -Eq '^ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO=(0|false|no)$' <<<"$container_env"; then
  prefer_boring="false"
fi

echo
echo "Summary:"
echo "  image carries boringcrypto worker: $has_boring"
echo "  runtime prefers boringcrypto:      $prefer_boring"

if [[ "$has_boring" != "true" ]]; then
  echo
  echo "Result: NOT OK - running image does not contain /app/antigravityworker-boringcrypto" >&2
  exit 1
fi

if [[ "$prefer_boring" != "true" ]]; then
  echo
  echo "Result: NOT OK - runtime explicitly disables boringcrypto preference" >&2
  exit 1
fi

echo
echo "Result: OK - boringcrypto worker is present and not disabled."
