#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/rollback-prod-sub2api.sh

Environment variables:
  DEPLOY_DIR               Production deploy directory. Default: /root/sub2api-deploy
  SERVICE_NAME             Compose service name. Default: sub2api
  BACKUP_FILE              Explicit override backup path to restore
  LIST_ONLY                Set to 1 to list backups and exit
  HEALTH_TIMEOUT_SECONDS   Health wait timeout in seconds. Default: 180
EOF
}

DEPLOY_DIR="${DEPLOY_DIR:-/root/sub2api-deploy}"
SERVICE_NAME="${SERVICE_NAME:-sub2api}"
BACKUP_FILE="${BACKUP_FILE:-}"
LIST_ONLY="${LIST_ONLY:-0}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-180}"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

COMPOSE_MAIN="${DEPLOY_DIR}/docker-compose.yml"
OVERRIDE_FILE="${DEPLOY_DIR}/docker-compose.override.yml"

if [[ ! -f "${COMPOSE_MAIN}" ]]; then
  echo "Compose file not found: ${COMPOSE_MAIN}" >&2
  exit 1
fi

if [[ "${LIST_ONLY}" == "1" ]]; then
  ls -1t "${OVERRIDE_FILE}".bak-* 2>/dev/null || {
    echo "No rollback backups found for ${OVERRIDE_FILE}" >&2
    exit 1
  }
  exit 0
fi

if [[ -z "${BACKUP_FILE}" ]]; then
  BACKUP_FILE="$(ls -1t "${OVERRIDE_FILE}".bak-* 2>/dev/null | head -n 1 || true)"
fi

if [[ -z "${BACKUP_FILE}" || ! -f "${BACKUP_FILE}" ]]; then
  echo "Rollback backup file not found: ${BACKUP_FILE:-<empty>}" >&2
  exit 1
fi

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
CURRENT_BACKUP="${OVERRIDE_FILE}.rollback-from-${TIMESTAMP}"

if [[ -f "${OVERRIDE_FILE}" ]]; then
  cp "${OVERRIDE_FILE}" "${CURRENT_BACKUP}"
fi

cp "${BACKUP_FILE}" "${OVERRIDE_FILE}"

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

echo "--- restored backup ---"
echo "${BACKUP_FILE}"
echo "--- compose ps ---"
docker compose -f "${COMPOSE_MAIN}" -f "${OVERRIDE_FILE}" ps
echo "--- current override ---"
sed -n '1,80p' "${OVERRIDE_FILE}"
