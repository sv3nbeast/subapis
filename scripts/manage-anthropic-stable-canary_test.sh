#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAKE_BIN="${TMP_DIR}/bin"
DEPLOY_DIR="${TMP_DIR}/deploy"
ENV_FILE="${DEPLOY_DIR}/anthropic-stable-canary.env"
MOCK_LOG="${TMP_DIR}/docker.log"
mkdir -p "${FAKE_BIN}" "${DEPLOY_DIR}"
touch "${DEPLOY_DIR}/docker-compose.yml" "${DEPLOY_DIR}/docker-compose.override.yml"

cat > "${ENV_FILE}" <<'EOF'
GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=false
GATEWAY_ANTHROPIC_STABLE_CANARY_GROUP_ID=700
GATEWAY_ANTHROPIC_STABLE_CANARY_ACCOUNT_ID=701
GATEWAY_ANTHROPIC_STABLE_CANARY_OWNER_USER_ID=0
GATEWAY_ANTHROPIC_STABLE_CANARY_API_KEY_ID=0
GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_USERS=true
GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_API_KEY_IDS=702
GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_GENERATION=1
GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_HMAC_KEY=0123456789abcdef0123456789abcdef
GATEWAY_ANTHROPIC_STABLE_CANARY_MAX_BODY_BYTES=67108864
ANTHROPIC_STABLE_CANARY_DEVICE_ID=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
ANTHROPIC_STABLE_CANARY_PROFILE=claude_cli_2_1_222_v1
EOF
chmod 600 "${ENV_FILE}"

cat > "${FAKE_BIN}/flock" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "${FAKE_BIN}/flock"

cat > "${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

printf '%s\n' "$*" >> "${MOCK_LOG}"

if [[ "${1:-}" == "compose" ]]; then
  if [[ "${*}" == *"--anthropic-stable-canary-action"* ]]; then
    printf '{"enrolled_before":true,"validated":true}\n'
    exit 0
  fi
  if [[ "${*: -3}" == "ps -q sub2api" ]]; then
    printf 'container-id\n'
  fi
  exit 0
fi

if [[ "${1:-}" == "inspect" && "${3:-}" == "--format" ]]; then
  format="${4:-}"
  if [[ "${format}" == *"Config.Env"* ]]; then
    cat "${MOCK_RUNTIME_ENV_FILE}"
    exit 0
  fi
  if [[ "${format}" == *"Config.Image"* ]]; then
    printf 'test-image\n'
    exit 0
  fi
fi

if [[ "${1:-}" == "inspect" && "${2:-}" == "--format" ]]; then
  printf 'healthy\n'
  exit 0
fi

if [[ "${1:-}" == "inspect" ]]; then
  exit 0
fi

if [[ "${1:-}" == "exec" ]]; then
  exit 0
fi

echo "unexpected docker invocation: $*" >&2
exit 1
EOF
chmod +x "${FAKE_BIN}/docker"

run_manager() {
  PATH="${FAKE_BIN}:${PATH}" \
    MOCK_LOG="${MOCK_LOG}" \
    MOCK_RUNTIME_ENV_FILE="${ENV_FILE}" \
    bash "${SCRIPT_DIR}/manage-anthropic-stable-canary.sh" \
      --env-file "${ENV_FILE}" --deploy-dir "${DEPLOY_DIR}" --service sub2api "$@"
}

run_manager_with_env_file() {
  local file="$1"
  shift
  PATH="${FAKE_BIN}:${PATH}" \
    MOCK_LOG="${MOCK_LOG}" \
    MOCK_RUNTIME_ENV_FILE="${ENV_FILE}" \
    bash "${SCRIPT_DIR}/manage-anthropic-stable-canary.sh" \
      --env-file "${file}" --deploy-dir "${DEPLOY_DIR}" --service sub2api "$@"
}

replace_env_line() {
  local file="$1" key="$2" value="$3"
  awk -v key="${key}" -v value="${value}" '
    index($0, key "=") == 1 { print key "=" value; next }
    { print }
  ' "${file}" > "${file}.tmp"
  mv "${file}.tmp" "${file}"
}

preflight_output="$(run_manager preflight)"
if [[ "${preflight_output}" == *"0123456789abcdef0123456789abcdef"* ]]; then
  echo "preflight leaked the session HMAC key" >&2
  exit 1
fi
grep -Fq -- '--env-from-file' "${MOCK_LOG}"

if run_manager start >/dev/null 2>&1; then
  echo "start without --execute was accepted" >&2
  exit 1
fi

run_manager start --execute >/dev/null
grep -Fxq 'GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=true' "${ENV_FILE}"
run_manager stop --execute >/dev/null
grep -Fxq 'GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=false' "${ENV_FILE}"

cp "${ENV_FILE}" "${TMP_DIR}/invalid.env"
printf 'UNKNOWN_KEY=1\n' >> "${TMP_DIR}/invalid.env"
chmod 600 "${TMP_DIR}/invalid.env"
if run_manager_with_env_file "${TMP_DIR}/invalid.env" preflight >/dev/null 2>&1; then
  echo "unknown canary env key was accepted" >&2
  exit 1
fi

cp "${ENV_FILE}" "${TMP_DIR}/duplicate-ids.env"
replace_env_line "${TMP_DIR}/duplicate-ids.env" GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_API_KEY_IDS 702,702
chmod 600 "${TMP_DIR}/duplicate-ids.env"
if run_manager_with_env_file "${TMP_DIR}/duplicate-ids.env" preflight >/dev/null 2>&1; then
  echo "duplicate shared API key IDs were accepted" >&2
  exit 1
fi

cp "${ENV_FILE}" "${TMP_DIR}/short-hmac.env"
replace_env_line "${TMP_DIR}/short-hmac.env" GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_HMAC_KEY short
chmod 600 "${TMP_DIR}/short-hmac.env"
if run_manager_with_env_file "${TMP_DIR}/short-hmac.env" preflight >/dev/null 2>&1; then
  echo "short session HMAC key was accepted" >&2
  exit 1
fi

cp "${ENV_FILE}" "${TMP_DIR}/unknown-profile.env"
replace_env_line "${TMP_DIR}/unknown-profile.env" ANTHROPIC_STABLE_CANARY_PROFILE unknown_profile
chmod 600 "${TMP_DIR}/unknown-profile.env"
if run_manager_with_env_file "${TMP_DIR}/unknown-profile.env" preflight >/dev/null 2>&1; then
  echo "unknown stable canary profile was accepted" >&2
  exit 1
fi

chmod 644 "${ENV_FILE}"
if run_manager preflight >/dev/null 2>&1; then
  echo "insecure canary env mode was accepted" >&2
  exit 1
fi

echo "manage-anthropic-stable-canary tests passed"
