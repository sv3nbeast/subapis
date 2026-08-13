#!/usr/bin/env bash

set -euo pipefail

# Host-side operator guard for the opt-in Anthropic stable canary. The
# application owns enrollment semantics; this script validates the isolated
# env file, invokes the existing lifecycle CLI, and restarts one container.

usage() {
  cat <<'EOF'
Usage:
  scripts/manage-anthropic-stable-canary.sh <action> [options]

Actions:
  preflight  Validate isolation without changing data.
  enroll     Validate/enroll the account (dry-run unless --execute).
  start      Enable runtime traffic after lifecycle validation.
  stop       Disable runtime traffic but retain enrollment.
  retire     Disable runtime traffic and remove enrollment.
  status     Show redacted runtime and lifecycle state.
  report     Show redacted usage/error/TTFT metrics.

Options:
  --env-file FILE       Root-only env file. Default:
                        /root/sub2api-deploy/anthropic-stable-canary.env
  --deploy-dir DIR      Compose directory. Default: /root/sub2api-deploy
  --service NAME        Compose service. Default: sub2api
  --postgres NAME       PostgreSQL container. Default: sub2api-postgres
  --hours N             Report window. Default: 24
  --execute             Commit a lifecycle/runtime change.
  -h, --help            Show this help.

The env file must be mode 600 and contain only reviewed canary settings. Do
not place OAuth credentials in it; credentials remain in the accounts table.
EOF
}

die() {
  echo "stable canary: $*" >&2
  exit 1
}

ACTION=""
CANARY_ENV_FILE="${ANTHROPIC_STABLE_CANARY_ENV_FILE:-/root/sub2api-deploy/anthropic-stable-canary.env}"
DEPLOY_DIR="${DEPLOY_DIR:-/root/sub2api-deploy}"
SERVICE_NAME="${SERVICE_NAME:-sub2api}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-sub2api-postgres}"
REPORT_HOURS="${REPORT_HOURS:-24}"
EXECUTE=0

while (($# > 0)); do
  case "$1" in
    preflight|enroll|start|stop|retire|status|report)
      [[ -z "${ACTION}" ]] || die "only one action may be supplied"
      ACTION="$1"
      shift
      ;;
    --env-file)
      (($# >= 2)) || die "--env-file requires a value"
      CANARY_ENV_FILE="$2"
      shift 2
      ;;
    --deploy-dir)
      (($# >= 2)) || die "--deploy-dir requires a value"
      DEPLOY_DIR="$2"
      shift 2
      ;;
    --service)
      (($# >= 2)) || die "--service requires a value"
      SERVICE_NAME="$2"
      shift 2
      ;;
    --postgres)
      (($# >= 2)) || die "--postgres requires a value"
      POSTGRES_CONTAINER="$2"
      shift 2
      ;;
    --hours)
      (($# >= 2)) || die "--hours requires a value"
      REPORT_HOURS="$2"
      shift 2
      ;;
    --execute)
      EXECUTE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
done

[[ -n "${ACTION}" ]] || { usage >&2; exit 1; }
for command in docker awk stat mktemp flock; do
  command -v "${command}" >/dev/null 2>&1 || die "missing required command: ${command}"
done

COMPOSE_MAIN="${DEPLOY_DIR}/docker-compose.yml"
OVERRIDE_FILE="${DEPLOY_DIR}/docker-compose.override.yml"
[[ -f "${COMPOSE_MAIN}" ]] || die "compose file not found: ${COMPOSE_MAIN}"
[[ -f "${OVERRIDE_FILE}" ]] || die "compose override not found: ${OVERRIDE_FILE}"
[[ -f "${CANARY_ENV_FILE}" && ! -L "${CANARY_ENV_FILE}" ]] || die "canary env file must be a regular file"

file_mode() {
  if stat -c '%a' "$1" >/dev/null 2>&1; then
    stat -c '%a' "$1"
  else
    stat -f '%Lp' "$1"
  fi
}

[[ "$(file_mode "${CANARY_ENV_FILE}")" == "600" ]] || die "canary env file must have mode 600"

LOCK_FILE="${CANARY_ENV_FILE}.lock"
umask 077
exec 9>"${LOCK_FILE}"
flock -n 9 || die "another stable canary operation is already running"

# Accept simple KEY=value lines only. This prevents shell expansion and
# duplicate keys from silently changing the runtime policy.
awk '
  /^[[:space:]]*$/ || /^[[:space:]]*#/ { next }
  !/^[A-Za-z_][A-Za-z0-9_]*=[^[:cntrl:]]*$/ { bad = 1; next }
  { key = $0; sub(/=.*/, "", key); count[key]++ }
  END {
    for (key in count) {
      if (count[key] != 1) bad = 1
      if (key != "GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_GROUP_ID" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_ACCOUNT_ID" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_OWNER_USER_ID" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_API_KEY_ID" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_USERS" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_API_KEY_IDS" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_GENERATION" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_HMAC_KEY" &&
          key != "GATEWAY_ANTHROPIC_STABLE_CANARY_MAX_BODY_BYTES" &&
          key != "ANTHROPIC_STABLE_CANARY_DEVICE_ID" &&
          key != "ANTHROPIC_STABLE_CANARY_PROFILE") bad = 1
    }
    exit bad
  }
' "${CANARY_ENV_FILE}" || die "canary env file contains invalid or duplicate entries"

env_value() {
  local wanted="$1"
  awk -v wanted="${wanted}" '
    /^[[:space:]]*$/ || /^[[:space:]]*#/ { next }
    { line = $0; sub(/^[[:space:]]+/, "", line); prefix = wanted "="
      if (index(line, prefix) == 1) { print substr(line, length(prefix) + 1); found = 1; exit } }
    END { if (!found) exit 1 }
  ' "${CANARY_ENV_FILE}"
}

optional_env_value() {
  env_value "$1" 2>/dev/null || true
}

positive_int() {
  [[ "$1" =~ ^[1-9][0-9]*$ ]]
}

validate_shared_api_key_ids() {
  local raw="$1"
  local -a ids
  local id seen=,
  [[ "${raw}" =~ ^[1-9][0-9]*(,[1-9][0-9]*)*$ ]] || return 1
  IFS=',' read -r -a ids <<< "${raw}"
  ((${#ids[@]} >= 1 && ${#ids[@]} <= 32)) || return 1
  for id in "${ids[@]}"; do
    case "${seen}" in *",${id},"*) return 1 ;; esac
    seen="${seen}${id},"
  done
}

validate_hmac_key() {
  # The application accepts any stable secret; the env-file parser already
  # rejects control characters and duplicate keys.
  [[ ${#1} -ge 32 ]]
}

validate_max_body_bytes() {
  [[ "$1" =~ ^[1-9][0-9]*$ ]] && ((10#$1 <= 67108864))
}

known_profile() {
  case "$1" in
    claude_cli_2_1_222_v1|claude_sdk_cli_2_1_222_v1|claude_cli_custom_base_v1) return 0 ;;
    *) return 1 ;;
  esac
}

CANARY_ENABLED="$(env_value GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED)" || die "missing stable canary enabled setting"
case "${CANARY_ENABLED}" in true|false) ;; *) die "stable canary enabled must be true or false" ;; esac
GROUP_ID="$(env_value GATEWAY_ANTHROPIC_STABLE_CANARY_GROUP_ID)" || die "missing stable canary group id"
ACCOUNT_ID="$(env_value GATEWAY_ANTHROPIC_STABLE_CANARY_ACCOUNT_ID)" || die "missing stable canary account id"
positive_int "${GROUP_ID}" || die "group id must be positive"
positive_int "${ACCOUNT_ID}" || die "account id must be positive"

SHARED_USERS="$(optional_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_USERS)"
SHARED_USERS="${SHARED_USERS:-false}"
case "${SHARED_USERS}" in true|false) ;; *) die "shared_users must be true or false" ;; esac
OWNER_USER_ID="$(optional_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_OWNER_USER_ID)"
API_KEY_ID="$(optional_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_API_KEY_ID)"
SESSION_GENERATION="$(optional_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_GENERATION)"
SHARED_API_KEY_IDS="$(optional_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_API_KEY_IDS)"
SESSION_HMAC_KEY="$(optional_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_HMAC_KEY)"
DEVICE_ID="$(optional_env_value ANTHROPIC_STABLE_CANARY_DEVICE_ID)"
PROFILE="$(optional_env_value ANTHROPIC_STABLE_CANARY_PROFILE)"

if [[ "${ACTION}" != "retire" ]]; then
  if [[ "${SHARED_USERS}" == "true" ]]; then
    [[ -z "${OWNER_USER_ID}" || "${OWNER_USER_ID}" == "0" ]] || die "owner_user_id must be zero in shared mode"
    [[ -z "${API_KEY_ID}" || "${API_KEY_ID}" == "0" ]] || die "api_key_id must be zero in shared mode"
    validate_shared_api_key_ids "${SHARED_API_KEY_IDS}" || die "shared API key allow-list must contain 1..32 unique positive IDs"
    positive_int "${SESSION_GENERATION}" || die "session generation must be positive"
    validate_hmac_key "${SESSION_HMAC_KEY}" || die "session HMAC key must contain at least 32 characters"
  else
    positive_int "${OWNER_USER_ID}" || die "owner_user_id must be positive in owner mode"
    positive_int "${API_KEY_ID}" || die "api_key_id must be positive in owner mode"
  fi

  [[ "${DEVICE_ID}" =~ ^[0-9a-f]{64}$ ]] || die "device id must be reviewed lowercase 64-hex"
  known_profile "${PROFILE}" || die "stable canary profile is not a reviewed capture"
fi
MAX_BODY_BYTES="$(optional_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_MAX_BODY_BYTES)"
MAX_BODY_BYTES="${MAX_BODY_BYTES:-67108864}"
if [[ "${ACTION}" != "retire" ]]; then
  validate_max_body_bytes "${MAX_BODY_BYTES}" || die "max body bytes must be between 1 and 67108864"
  if [[ -n "${SESSION_GENERATION}" ]]; then
    positive_int "${SESSION_GENERATION}" || die "session generation must be positive when configured"
  fi
fi
[[ "${REPORT_HOURS}" =~ ^[1-9][0-9]*$ ]] && ((REPORT_HOURS <= 720)) || die "hours must be 1..720"

run_compose() {
  docker compose -f "${COMPOSE_MAIN}" -f "${OVERRIDE_FILE}" "$@"
}

run_lifecycle() {
  local lifecycle_action="$1"
  local execute_flag="${2:-}"
  local -a args=(
    --anthropic-stable-canary-action "${lifecycle_action}"
    --anthropic-stable-canary-profile "${PROFILE}"
  )
  [[ "${execute_flag}" == "execute" ]] && args+=(--anthropic-stable-canary-execute)
  run_compose run --rm --no-deps -T \
    --env-from-file "${CANARY_ENV_FILE}" \
    --entrypoint /app/sub2api "${SERVICE_NAME}" "${args[@]}"
}

container_env_value() {
  local key="$1"
  docker inspect "${SERVICE_NAME}" --format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null \
    | awk -v wanted="${key}" 'index($0, wanted "=") == 1 { print substr($0, length(wanted) + 2); exit }'
}

wait_healthy() {
  local container_id="$1"
  local deadline=$((SECONDS + 180))
  while true; do
    local state
    state="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${container_id}")"
    echo "health=${state}"
    case "${state}" in
      healthy) return 0 ;;
      unhealthy|exited|dead) return 1 ;;
    esac
    ((SECONDS < deadline)) || return 1
    sleep 2
  done
}

compose_up() {
  run_compose config >/dev/null
  run_compose up -d --no-deps "${SERVICE_NAME}" >/dev/null
  local container_id
  container_id="$(run_compose ps -q "${SERVICE_NAME}")"
  if [[ -z "${container_id}" ]]; then
    echo "cannot resolve service container" >&2
    return 1
  fi
  if ! wait_healthy "${container_id}"; then
    echo "service did not become healthy" >&2
    return 1
  fi
  if [[ "$(container_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED)" != "${CANARY_ENABLED}" ]]; then
    echo "container canary state does not match env file" >&2
    return 1
  fi
}

set_enabled() {
  local value="$1"
  local tmp
  tmp="$(mktemp "${CANARY_ENV_FILE}.tmp.XXXXXX")"
  chmod 600 "${tmp}"
  if ! awk -v wanted='GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED' -v replacement="${value}" '
    /^[[:space:]]*$/ || /^[[:space:]]*#/ { print; next }
    { line = $0; sub(/^[[:space:]]+/, "", line)
      if (index(line, wanted "=") == 1) { print wanted "=" replacement; replaced++ }
      else print }
    END { if (replaced != 1) exit 1 }
  ' "${CANARY_ENV_FILE}" > "${tmp}"; then
    rm -f "${tmp}"
    die "could not update stable canary enabled setting"
  fi
  mv -f "${tmp}" "${CANARY_ENV_FILE}"
  chmod 600 "${CANARY_ENV_FILE}"
  CANARY_ENABLED="${value}"
}

require_execute() {
  ((EXECUTE == 1)) || die "${ACTION} is validation-only; add --execute"
}

transition_runtime() {
  local previous="$1"
  local desired="$2"
  set_enabled "${desired}"
  if compose_up; then
    return 0
  fi
  echo "runtime transition to ${desired} failed; attempting rollback to ${previous}" >&2
  set_enabled "${previous}" || true
  compose_up || true
  return 1
}

case "${ACTION}" in
  preflight)
    echo "stable canary preflight: group=${GROUP_ID} account=${ACCOUNT_ID} shared_users=${SHARED_USERS}"
    run_lifecycle inspect
    if [[ "${CANARY_ENABLED}" == "false" ]]; then
      run_lifecycle enable
    fi
    ;;
  enroll)
    [[ "${CANARY_ENABLED}" == "false" ]] || die "runtime must be disabled before enrollment"
    echo "stable canary enrollment: group=${GROUP_ID} account=${ACCOUNT_ID} shared_users=${SHARED_USERS}"
    run_lifecycle inspect
    if ((EXECUTE == 1)); then run_lifecycle enable execute; else run_lifecycle enable; fi
    ;;
  start)
    require_execute
    [[ "${CANARY_ENABLED}" == "false" ]] || die "runtime is already enabled"
    lifecycle_state="$(run_lifecycle inspect)" || die "lifecycle inspection failed; runtime remains disabled"
    printf '%s\n' "${lifecycle_state}"
    [[ "${lifecycle_state}" == *'"enrolled_before":true'* ]] || die "stable canary account is not enrolled; run enroll --execute first"
    if ! run_lifecycle enable || ! transition_runtime false true; then
      die "runtime start failed; returned to disabled state"
    fi
    echo "stable canary runtime started (values redacted)"
    ;;
  stop)
    require_execute
    if [[ "${CANARY_ENABLED}" == "true" ]]; then
      transition_runtime true false || die "runtime stop failed; previous runtime state was restored when possible"
    fi
    echo "stable canary runtime stopped; enrollment retained"
    ;;
  retire)
    require_execute
    if [[ "${CANARY_ENABLED}" == "true" ]]; then
      transition_runtime true false || die "runtime stop failed; enrollment was not retired"
    fi
    run_lifecycle disable execute
    echo "stable canary enrollment retired"
    ;;
  status)
    echo "stable canary status: group=${GROUP_ID} account=${ACCOUNT_ID} shared_users=${SHARED_USERS} configured_enabled=${CANARY_ENABLED}"
    container_image="$(docker inspect "${SERVICE_NAME}" --format '{{.Config.Image}}' 2>/dev/null || true)"
    echo "container_image=${container_image:-unknown}"
    echo "container_enabled=$(container_env_value GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED)"
    run_lifecycle inspect || true
    ;;
  report)
    generation_sql="${SESSION_GENERATION:-0}"
    echo "stable canary report: group=${GROUP_ID} account=${ACCOUNT_ID} window_hours=${REPORT_HOURS}"
    docker exec "${POSTGRES_CONTAINER}" psql -U sub2api -d sub2api -v ON_ERROR_STOP=1 -P pager=off -F '|' -At <<SQL
SELECT 'account_state', status, schedulable::text
FROM accounts WHERE id=${ACCOUNT_ID} AND deleted_at IS NULL;
SELECT 'usage', COUNT(*)::text, COUNT(first_token_ms)::text,
       COALESCE(ROUND(AVG(first_token_ms)::numeric, 2)::text, ''),
       COALESCE(ROUND((percentile_cont(0.50) WITHIN GROUP (ORDER BY first_token_ms))::numeric, 2)::text, ''),
       COALESCE(ROUND((percentile_cont(0.90) WITHIN GROUP (ORDER BY first_token_ms))::numeric, 2)::text, ''),
       COALESCE(ROUND(AVG(duration_ms)::numeric, 2)::text, ''),
       COUNT(DISTINCT user_id)::text, COUNT(DISTINCT api_key_id)::text
FROM usage_logs
WHERE account_id=${ACCOUNT_ID} AND group_id=${GROUP_ID}
  AND created_at >= NOW() - INTERVAL '${REPORT_HOURS} hours';
SELECT 'errors', COUNT(*)::text,
       COALESCE(string_agg(status_code::text, ',' ORDER BY status_code), ''),
       COALESCE(string_agg(error_type, ',' ORDER BY error_type), '')
FROM ops_error_logs
WHERE account_id=${ACCOUNT_ID} AND group_id=${GROUP_ID}
  AND created_at >= NOW() - INTERVAL '${REPORT_HOURS} hours';
SELECT 'sessions', COUNT(*)::text FROM anthropic_stable_canary_sessions
WHERE group_id=${GROUP_ID} AND account_id=${ACCOUNT_ID} AND generation=${generation_sql};
SQL
    ;;
esac
