#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/rebuild-prod-sub2api.sh

Environment variables:
  IMAGE_REPO                       Docker image repo name. Default: sub2api
  IMAGE_TAG                        Docker image tag suffix. Required
  DEPLOY_DIR                       Production deploy directory. Default: /root/sub2api-deploy
  ANTIGRAVITY_USER_AGENT_VERSION   Optional env override written into compose override.
                                   Default: 1.22.2
  ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO
                                   Default: true
  SERVICE_NAME                     Compose service name. Default: sub2api
  HEALTH_TIMEOUT_SECONDS           Health wait timeout. Default: 180
  SKIP_BUILD                       Set to 1 to skip docker build and only switch image
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

IMAGE_REPO="${IMAGE_REPO:-sub2api}"
IMAGE_TAG="${IMAGE_TAG:-}"
DEPLOY_DIR="${DEPLOY_DIR:-/root/sub2api-deploy}"
SERVICE_NAME="${SERVICE_NAME:-sub2api}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-180}"
SKIP_BUILD="${SKIP_BUILD:-0}"
ANTIGRAVITY_VERSION="${ANTIGRAVITY_USER_AGENT_VERSION:-1.22.2}"
PREFER_BORINGCRYPTO="${ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO:-true}"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

if [[ -z "${IMAGE_TAG}" ]]; then
  echo "IMAGE_TAG is required." >&2
  usage >&2
  exit 1
fi

require_cmd docker
require_cmd sed

if [[ ! -d "${REPO_ROOT}" ]]; then
  echo "Repo root not found: ${REPO_ROOT}" >&2
  exit 1
fi

if [[ ! -f "${DEPLOY_DIR}/docker-compose.yml" ]]; then
  echo "Compose file not found: ${DEPLOY_DIR}/docker-compose.yml" >&2
  exit 1
fi

IMAGE_REF="${IMAGE_REPO}:${IMAGE_TAG}"
OVERRIDE_FILE="${DEPLOY_DIR}/docker-compose.override.yml"
COMPOSE_MAIN="${DEPLOY_DIR}/docker-compose.yml"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"

if [[ "${SKIP_BUILD}" != "1" ]]; then
  echo "Building image: ${IMAGE_REF}"
  docker build -t "${IMAGE_REF}" "${REPO_ROOT}"
fi

if [[ -f "${OVERRIDE_FILE}" ]]; then
  cp "${OVERRIDE_FILE}" "${OVERRIDE_FILE}.bak-${TIMESTAMP}"
fi

cat > "${OVERRIDE_FILE}" <<EOF
services:
  ${SERVICE_NAME}:
    image: ${IMAGE_REF}
    environment:
      - ANTIGRAVITY_USER_AGENT_VERSION=${ANTIGRAVITY_VERSION}
      - ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO=${PREFER_BORINGCRYPTO}
EOF

docker compose -f "${COMPOSE_MAIN}" -f "${OVERRIDE_FILE}" config >/dev/null
docker compose -f "${COMPOSE_MAIN}" -f "${OVERRIDE_FILE}" up -d --no-deps "${SERVICE_NAME}"

CONTAINER_ID="$(docker compose -f "${COMPOSE_MAIN}" -f "${OVERRIDE_FILE}" ps -q "${SERVICE_NAME}")"
if [[ -z "${CONTAINER_ID}" ]]; then
  echo "Failed to resolve container id for service: ${SERVICE_NAME}" >&2
  exit 1
fi

deadline=$((SECONDS + HEALTH_TIMEOUT_SECONDS))
while true; do
  health_status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${CONTAINER_ID}")"
  echo "health=${health_status}"

  if [[ "${health_status}" == "healthy" ]]; then
    break
  fi

  if [[ "${health_status}" == "unhealthy" || "${health_status}" == "exited" || "${health_status}" == "dead" ]]; then
    docker logs --tail 120 "${CONTAINER_ID}" >&2
    exit 1
  fi

  if (( SECONDS >= deadline )); then
    echo "Health check timed out after ${HEALTH_TIMEOUT_SECONDS}s" >&2
    docker logs --tail 120 "${CONTAINER_ID}" >&2
    exit 1
  fi

  sleep 2
done

echo "--- compose ps ---"
docker compose -f "${COMPOSE_MAIN}" -f "${OVERRIDE_FILE}" ps
echo "--- antigravity env ---"
docker exec "${CONTAINER_ID}" printenv ANTIGRAVITY_USER_AGENT_VERSION
docker exec "${CONTAINER_ID}" printenv ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO
echo "--- antigravity worker files ---"
docker exec "${CONTAINER_ID}" sh -lc 'ls -l /app/antigravityworker*'
if ! docker exec "${CONTAINER_ID}" test -x /app/antigravityworker-boringcrypto; then
  echo "boringcrypto worker missing in running container" >&2
  exit 1
fi
echo "--- container health endpoint ---"
docker exec "${CONTAINER_ID}" wget -q -T 5 -S -O /dev/null http://localhost:8080/health
