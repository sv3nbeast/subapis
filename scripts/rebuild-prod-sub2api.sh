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
                                   Default: 1.23.2
  ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO
                                   Default: true
  VITE_UI_V2_ROLLOUT_MODE         UI rollout: off, preview, percentage, or full.
                                   Default: full
  VITE_UI_V2_ROLLOUT_PERCENT      Stable cohort percentage for percentage mode. Default: 0
  VITE_PUBLIC_UI_V2_ROLLOUT_MODE  Public UI rollout: off, preview, percentage, or full.
                                   Default: full
  VITE_PUBLIC_UI_V2_ROLLOUT_PERCENT
                                   Stable public UI cohort percentage. Default: 0
  GATEWAY_OPENAI_KIRO_BRIDGE_ENABLED
                                   true/false. When unset, preserve the current
                                   sub2api container value; default false.
  GATEWAY_KIRO_RESILIENCE_MODE     Optional off/observe/enforce override. When
                                   unset, preserve the running container value.
  GATEWAY_KIRO_RESILIENCE_GROUP_IDS
                                   Optional comma-separated rollout group IDs.
                                   When unset, preserve the running container value.
  GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS
                                   Optional positive integer. When unset, preserve
                                   the running container value.
  GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS
                                   Optional positive integer. When unset, preserve
                                   the running container value.
  GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS
                                   Optional positive integer. When unset, preserve
                                   the running container value.
  GATEWAY_FIRST_SEMANTIC_TIMEOUT   Generic Anthropic stream first-semantic timeout
                                   in seconds. Default: 50; 0 disables the guard.
  SUB2API_KIRO_EVENT_DIAGNOSTICS_USER_IDS
                                   Optional comma-separated user IDs for redacted Kiro event diagnostics.
                                   When unset, preserve the running container value.
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
ANTIGRAVITY_VERSION="${ANTIGRAVITY_USER_AGENT_VERSION:-1.23.2}"
PREFER_BORINGCRYPTO="${ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO:-true}"
UI_V2_ROLLOUT_MODE="${VITE_UI_V2_ROLLOUT_MODE:-full}"
UI_V2_ROLLOUT_PERCENT="${VITE_UI_V2_ROLLOUT_PERCENT:-0}"
PUBLIC_UI_V2_ROLLOUT_MODE="${VITE_PUBLIC_UI_V2_ROLLOUT_MODE:-full}"
PUBLIC_UI_V2_ROLLOUT_PERCENT="${VITE_PUBLIC_UI_V2_ROLLOUT_PERCENT:-0}"
OPENAI_KIRO_BRIDGE_ENABLED="${GATEWAY_OPENAI_KIRO_BRIDGE_ENABLED:-}"
KIRO_RESILIENCE_MODE="${GATEWAY_KIRO_RESILIENCE_MODE:-}"
KIRO_RESILIENCE_GROUP_IDS="${GATEWAY_KIRO_RESILIENCE_GROUP_IDS:-}"
KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS="${GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS:-}"
KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS="${GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS:-}"
KIRO_FAILOVER_BUDGET_SECONDS="${GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS:-}"
FIRST_SEMANTIC_TIMEOUT_SECONDS="${GATEWAY_FIRST_SEMANTIC_TIMEOUT:-50}"
KIRO_EVENT_DIAGNOSTICS_USER_IDS="${SUB2API_KIRO_EVENT_DIAGNOSTICS_USER_IDS:-}"

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

case "${UI_V2_ROLLOUT_MODE}" in
  off|preview|percentage|full) ;;
  *)
    echo "VITE_UI_V2_ROLLOUT_MODE must be one of: off, preview, percentage, full." >&2
    exit 1
    ;;
esac

if ! [[ "${UI_V2_ROLLOUT_PERCENT}" =~ ^[0-9]+$ ]] || (( UI_V2_ROLLOUT_PERCENT > 100 )); then
  echo "VITE_UI_V2_ROLLOUT_PERCENT must be an integer from 0 to 100." >&2
  exit 1
fi

case "${PUBLIC_UI_V2_ROLLOUT_MODE}" in
  off|preview|percentage|full) ;;
  *)
    echo "VITE_PUBLIC_UI_V2_ROLLOUT_MODE must be one of: off, preview, percentage, full." >&2
    exit 1
    ;;
esac

if ! [[ "${PUBLIC_UI_V2_ROLLOUT_PERCENT}" =~ ^[0-9]+$ ]] || (( PUBLIC_UI_V2_ROLLOUT_PERCENT > 100 )); then
  echo "VITE_PUBLIC_UI_V2_ROLLOUT_PERCENT must be an integer from 0 to 100." >&2
  exit 1
fi

if [[ -z "${OPENAI_KIRO_BRIDGE_ENABLED}" ]] && docker inspect "${SERVICE_NAME}" >/dev/null 2>&1; then
  OPENAI_KIRO_BRIDGE_ENABLED="$(
    docker inspect "${SERVICE_NAME}" --format '{{range .Config.Env}}{{println .}}{{end}}' \
      | sed -n 's/^GATEWAY_OPENAI_KIRO_BRIDGE_ENABLED=//p' \
      | head -n 1
  )"
fi
OPENAI_KIRO_BRIDGE_ENABLED="${OPENAI_KIRO_BRIDGE_ENABLED:-false}"
if [[ "${OPENAI_KIRO_BRIDGE_ENABLED}" != "true" && "${OPENAI_KIRO_BRIDGE_ENABLED}" != "false" ]]; then
  echo "GATEWAY_OPENAI_KIRO_BRIDGE_ENABLED must be true or false" >&2
  exit 1
fi

if docker inspect "${SERVICE_NAME}" >/dev/null 2>&1; then
  if [[ -z "${KIRO_RESILIENCE_MODE}" ]]; then
    KIRO_RESILIENCE_MODE="$(
      docker inspect "${SERVICE_NAME}" --format '{{range .Config.Env}}{{println .}}{{end}}' \
        | sed -n 's/^GATEWAY_KIRO_RESILIENCE_MODE=//p' \
        | head -n 1
    )"
  fi
  if [[ -z "${KIRO_RESILIENCE_GROUP_IDS}" ]]; then
    KIRO_RESILIENCE_GROUP_IDS="$(
      docker inspect "${SERVICE_NAME}" --format '{{range .Config.Env}}{{println .}}{{end}}' \
        | sed -n 's/^GATEWAY_KIRO_RESILIENCE_GROUP_IDS=//p' \
        | head -n 1
    )"
  fi
  if [[ -z "${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS}" ]]; then
    KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS="$(
      docker inspect "${SERVICE_NAME}" --format '{{range .Config.Env}}{{println .}}{{end}}' \
        | sed -n 's/^GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS=//p' \
        | head -n 1
    )"
  fi
  if [[ -z "${KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS}" ]]; then
    KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS="$(
      docker inspect "${SERVICE_NAME}" --format '{{range .Config.Env}}{{println .}}{{end}}' \
        | sed -n 's/^GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS=//p' \
        | head -n 1
    )"
  fi
  if [[ -z "${KIRO_FAILOVER_BUDGET_SECONDS}" ]]; then
    KIRO_FAILOVER_BUDGET_SECONDS="$(
      docker inspect "${SERVICE_NAME}" --format '{{range .Config.Env}}{{println .}}{{end}}' \
        | sed -n 's/^GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS=//p' \
        | head -n 1
    )"
  fi
  if [[ -z "${KIRO_EVENT_DIAGNOSTICS_USER_IDS}" ]]; then
    KIRO_EVENT_DIAGNOSTICS_USER_IDS="$(
      docker inspect "${SERVICE_NAME}" --format '{{range .Config.Env}}{{println .}}{{end}}' \
        | sed -n 's/^SUB2API_KIRO_EVENT_DIAGNOSTICS_USER_IDS=//p' \
        | head -n 1
    )"
  fi
fi

if [[ -n "${KIRO_RESILIENCE_MODE}" ]]; then
  KIRO_RESILIENCE_MODE="$(printf '%s' "${KIRO_RESILIENCE_MODE}" | tr '[:upper:]' '[:lower:]')"
  case "${KIRO_RESILIENCE_MODE}" in
    off|observe|enforce) ;;
    *)
      echo "GATEWAY_KIRO_RESILIENCE_MODE must be off, observe, or enforce" >&2
      exit 1
      ;;
  esac
fi

if [[ -n "${KIRO_RESILIENCE_GROUP_IDS}" ]] &&
  [[ ! "${KIRO_RESILIENCE_GROUP_IDS}" =~ ^[0-9]+(,[0-9]+)*$ ]]; then
  echo "GATEWAY_KIRO_RESILIENCE_GROUP_IDS must be comma-separated numeric IDs" >&2
  exit 1
fi

validate_optional_positive_integer() {
  local name="$1"
  local value="$2"
  if [[ -n "${value}" ]] && { [[ ! "${value}" =~ ^[0-9]+$ ]] || ((10#${value} <= 0)); }; then
    echo "${name} must be a positive integer" >&2
    exit 1
  fi
}

validate_optional_positive_integer "GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS" "${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS}"
validate_optional_positive_integer "GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS" "${KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS}"
validate_optional_positive_integer "GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS" "${KIRO_FAILOVER_BUDGET_SECONDS}"
if [[ ! "${FIRST_SEMANTIC_TIMEOUT_SECONDS}" =~ ^[0-9]+$ ]] || ((10#${FIRST_SEMANTIC_TIMEOUT_SECONDS} > 110)); then
  echo "GATEWAY_FIRST_SEMANTIC_TIMEOUT must be an integer from 0 to 110 seconds" >&2
  exit 1
fi

if [[ -n "${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS}" && -n "${KIRO_FAILOVER_BUDGET_SECONDS}" ]] &&
  ((10#${KIRO_FAILOVER_BUDGET_SECONDS} < 10#${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS})); then
  echo "GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS must be greater than or equal to GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS" >&2
  exit 1
fi

if [[ "${KIRO_RESILIENCE_MODE}" == "enforce" ]]; then
  if [[ -z "${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS}" ||
    -z "${KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS}" ||
    -z "${KIRO_FAILOVER_BUDGET_SECONDS}" ]]; then
    echo "Kiro enforce rollout requires explicit response-header, first-semantic, and failover-budget values; pass them once or preserve them from the running container" >&2
    exit 1
  fi
fi

KIRO_RESILIENCE_ENV=""
if [[ -n "${KIRO_RESILIENCE_MODE}" ]]; then
  KIRO_RESILIENCE_ENV+="      - GATEWAY_KIRO_RESILIENCE_MODE=${KIRO_RESILIENCE_MODE}"$'\n'
fi
if [[ -n "${KIRO_RESILIENCE_GROUP_IDS}" ]]; then
  KIRO_RESILIENCE_ENV+="      - GATEWAY_KIRO_RESILIENCE_GROUP_IDS=${KIRO_RESILIENCE_GROUP_IDS}"$'\n'
fi
if [[ -n "${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS}" ]]; then
  KIRO_RESILIENCE_ENV+="      - GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS=${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS}"$'\n'
fi
if [[ -n "${KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS}" ]]; then
  KIRO_RESILIENCE_ENV+="      - GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS=${KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS}"$'\n'
fi
if [[ -n "${KIRO_FAILOVER_BUDGET_SECONDS}" ]]; then
  KIRO_RESILIENCE_ENV+="      - GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS=${KIRO_FAILOVER_BUDGET_SECONDS}"$'\n'
fi
KIRO_EVENT_DIAGNOSTICS_ENV=""
if [[ -n "${KIRO_EVENT_DIAGNOSTICS_USER_IDS}" ]]; then
  KIRO_EVENT_DIAGNOSTICS_ENV="      - SUB2API_KIRO_EVENT_DIAGNOSTICS_USER_IDS=${KIRO_EVENT_DIAGNOSTICS_USER_IDS}"$'\n'
fi

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
  docker build \
    --build-arg "VITE_UI_V2_ROLLOUT_MODE=${UI_V2_ROLLOUT_MODE}" \
    --build-arg "VITE_UI_V2_ROLLOUT_PERCENT=${UI_V2_ROLLOUT_PERCENT}" \
    --build-arg "VITE_PUBLIC_UI_V2_ROLLOUT_MODE=${PUBLIC_UI_V2_ROLLOUT_MODE}" \
    --build-arg "VITE_PUBLIC_UI_V2_ROLLOUT_PERCENT=${PUBLIC_UI_V2_ROLLOUT_PERCENT}" \
    -t "${IMAGE_REF}" \
    "${REPO_ROOT}"
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
      - GATEWAY_OPENAI_KIRO_BRIDGE_ENABLED=${OPENAI_KIRO_BRIDGE_ENABLED}
      - GATEWAY_FIRST_SEMANTIC_TIMEOUT=${FIRST_SEMANTIC_TIMEOUT_SECONDS}
${KIRO_RESILIENCE_ENV%$'\n'}
${KIRO_EVENT_DIAGNOSTICS_ENV%$'\n'}
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
echo "--- OpenAI Kiro bridge env ---"
docker exec "${CONTAINER_ID}" printenv GATEWAY_OPENAI_KIRO_BRIDGE_ENABLED

assert_container_env() {
  local name="$1"
  local expected="$2"
  local actual
  if ! actual="$(docker exec "${CONTAINER_ID}" printenv "${name}")"; then
    echo "Required production environment variable is missing after rollout: ${name}" >&2
    exit 1
  fi
  if [[ "${actual}" != "${expected}" ]]; then
    echo "Production environment mismatch for ${name}: expected ${expected}, got ${actual}" >&2
    exit 1
  fi
  printf '%s=%s\n' "${name}" "${actual}"
}

echo "--- Generic first-semantic timeout env ---"
assert_container_env GATEWAY_FIRST_SEMANTIC_TIMEOUT "${FIRST_SEMANTIC_TIMEOUT_SECONDS}"

if [[ -n "${KIRO_RESILIENCE_MODE}" ]]; then
  echo "--- Kiro resilience env ---"
  assert_container_env GATEWAY_KIRO_RESILIENCE_MODE "${KIRO_RESILIENCE_MODE}"
  if [[ -n "${KIRO_RESILIENCE_GROUP_IDS}" ]]; then
    assert_container_env GATEWAY_KIRO_RESILIENCE_GROUP_IDS "${KIRO_RESILIENCE_GROUP_IDS}"
  else
    echo "all groups"
  fi
fi
if [[ -n "${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS}" ]]; then
  assert_container_env GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS "${KIRO_RESPONSE_HEADER_TIMEOUT_SECONDS}"
fi
if [[ -n "${KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS}" ]]; then
  assert_container_env GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS "${KIRO_FIRST_SEMANTIC_TIMEOUT_SECONDS}"
fi
if [[ -n "${KIRO_FAILOVER_BUDGET_SECONDS}" ]]; then
  assert_container_env GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS "${KIRO_FAILOVER_BUDGET_SECONDS}"
fi
echo "--- antigravity worker files ---"
docker exec "${CONTAINER_ID}" sh -lc 'ls -l /app/antigravityworker*'
if ! docker exec "${CONTAINER_ID}" test -x /app/antigravityworker-boringcrypto; then
  echo "boringcrypto worker missing in running container" >&2
  exit 1
fi
echo "--- container health endpoint ---"
docker exec "${CONTAINER_ID}" wget -q -T 5 -S -O /dev/null http://localhost:8080/health
