#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAKE_BIN="${TMP_DIR}/bin"
DEPLOY_DIR="${TMP_DIR}/deploy"
MOCK_ENV_FILE="${TMP_DIR}/container.env"
mkdir -p "${FAKE_BIN}" "${DEPLOY_DIR}"
touch "${DEPLOY_DIR}/docker-compose.yml"

cat > "${MOCK_ENV_FILE}" <<'EOF'
ANTIGRAVITY_USER_AGENT_VERSION=1.23.2
ANTIGRAVITY_EXTERNAL_WORKER_PREFER_BORINGCRYPTO=true
GATEWAY_OPENAI_KIRO_BRIDGE_ENABLED=true
GATEWAY_FIRST_SEMANTIC_TIMEOUT=50
KIRO_CODE_EXECUTION_SOCKET=/app/kiro-code-exec/worker.sock
GATEWAY_KIRO_RESILIENCE_MODE=enforce
GATEWAY_KIRO_RESILIENCE_GROUP_IDS=9,10,11,23,29,33
GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS=60
GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS=90
GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS=150
EOF

cat > "${FAKE_BIN}/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "$1" == "inspect" && "$2" == "sub2api" && "${3:-}" == "--format" ]]; then
  if [[ "${4:-}" == *"Config.Env"* ]]; then
    cat "${MOCK_ENV_FILE}"
  else
    printf 'test-image\n'
  fi
  exit 0
fi
if [[ "$1" == "inspect" && "$2" == "sub2api" ]]; then
  exit 0
fi
if [[ "$1" == "inspect" && "${2:-}" == "--format" ]]; then
  printf 'healthy\n'
  exit 0
fi
if [[ "$1" == "compose" ]]; then
  for arg in "$@"; do
    if [[ "${arg}" == "ps" && "${*: -1}" == "kiro-code-exec" ]]; then
      printf 'worker-container-id\n'
      exit 0
    fi
    if [[ "${arg}" == "ps" && "${*: -1}" == "sub2api" ]]; then
      printf 'container-id\n'
      exit 0
    fi
  done
  exit 0
fi
if [[ "$1" == "exec" && "$3" == "printenv" ]]; then
  value="$(sed -n "s/^${4}=//p" "${MOCK_ENV_FILE}" | head -n1)"
  if [[ -z "${value}" ]]; then
    exit 1
  fi
  printf '%s\n' "${value}"
  exit 0
fi
if [[ "$1" == "exec" ]]; then
  exit 0
fi

echo "unexpected docker invocation: $*" >&2
exit 1
EOF
chmod +x "${FAKE_BIN}/docker"

cat > "${FAKE_BIN}/ssh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" > "${SSH_ARGS_FILE}"
EOF
chmod +x "${FAKE_BIN}/ssh"

run_rebuild() {
  PATH="${FAKE_BIN}:${PATH}" \
    MOCK_ENV_FILE="${MOCK_ENV_FILE}" \
    IMAGE_TAG="test-timeout-preservation" \
    DEPLOY_DIR="${DEPLOY_DIR}" \
    SKIP_BUILD=1 \
    bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh"
}

run_rebuild

grep -Fq -- 'ARG TARGETARCH' "${REPO_ROOT}/deploy/Dockerfile"
grep -Fq -- 'GOARCH=${TARGETARCH} go build' "${REPO_ROOT}/deploy/Dockerfile"
grep -Fq -- 'required: false' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- 'kiro-code-exec:' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- 'network_mode: none' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- 'read_only: true' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- 'cap_drop:' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- 'cap_add:' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- '      - CHOWN' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- '      - SETGID' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- '      - SETUID' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- 'no-new-privileges:true' "${DEPLOY_DIR}/docker-compose.override.yml"
if grep -Fq -- 'user: "1000:1000"' "${DEPLOY_DIR}/docker-compose.override.yml"; then
  echo "Kiro code execution worker must allow the image entrypoint to initialize the socket volume" >&2
  exit 1
fi
grep -Fq -- 'KIRO_CODE_EXECUTION_SOCKET=/app/kiro-code-exec/worker.sock' "${DEPLOY_DIR}/docker-compose.override.yml"
grep -Fq -- 'kiro_code_exec_socket:/app/kiro-code-exec' "${DEPLOY_DIR}/docker-compose.override.yml"
if grep -Fq -- 'GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=' "${DEPLOY_DIR}/docker-compose.override.yml"; then
  echo "stable canary runtime settings leaked into the generated override" >&2
  exit 1
fi

STABLE_ENV_FILE="${TMP_DIR}/anthropic-stable-canary.env"
cat > "${STABLE_ENV_FILE}" <<'EOF'
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
chmod 600 "${STABLE_ENV_FILE}"
printf 'GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=false\n' >> "${MOCK_ENV_FILE}"
PATH="${FAKE_BIN}:${PATH}" \
  MOCK_ENV_FILE="${MOCK_ENV_FILE}" \
  IMAGE_TAG="test-stable-configured" \
  DEPLOY_DIR="${DEPLOY_DIR}" \
  ANTHROPIC_STABLE_CANARY_ENV_FILE="${STABLE_ENV_FILE}" \
  SKIP_BUILD=1 \
  bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh" >/dev/null
grep -Fq -- "path: ${STABLE_ENV_FILE}" "${DEPLOY_DIR}/docker-compose.override.yml"

replace_env_line() {
  local file="$1" key="$2" value="$3"
  awk -v key="${key}" -v value="${value}" '
    index($0, key "=") == 1 { print key "=" value; next }
    { print }
  ' "${file}" > "${file}.tmp"
  mv "${file}.tmp" "${file}"
}

cp "${STABLE_ENV_FILE}" "${TMP_DIR}/duplicate-stable-ids.env"
replace_env_line "${TMP_DIR}/duplicate-stable-ids.env" GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_API_KEY_IDS 702,702
chmod 600 "${TMP_DIR}/duplicate-stable-ids.env"
if PATH="${FAKE_BIN}:${PATH}" \
  MOCK_ENV_FILE="${MOCK_ENV_FILE}" \
  IMAGE_TAG="test-duplicate-stable-ids" \
  DEPLOY_DIR="${DEPLOY_DIR}" \
  ANTHROPIC_STABLE_CANARY_ENV_FILE="${TMP_DIR}/duplicate-stable-ids.env" \
  SKIP_BUILD=1 \
  bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh" >/dev/null 2>&1; then
  echo "duplicate stable canary API key IDs were accepted" >&2
  exit 1
fi

cp "${STABLE_ENV_FILE}" "${TMP_DIR}/short-stable-hmac.env"
replace_env_line "${TMP_DIR}/short-stable-hmac.env" GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_HMAC_KEY short
chmod 600 "${TMP_DIR}/short-stable-hmac.env"
if PATH="${FAKE_BIN}:${PATH}" \
  MOCK_ENV_FILE="${MOCK_ENV_FILE}" \
  IMAGE_TAG="test-short-stable-hmac" \
  DEPLOY_DIR="${DEPLOY_DIR}" \
  ANTHROPIC_STABLE_CANARY_ENV_FILE="${TMP_DIR}/short-stable-hmac.env" \
  SKIP_BUILD=1 \
  bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh" >/dev/null 2>&1; then
  echo "short stable canary HMAC was accepted" >&2
  exit 1
fi

cp "${STABLE_ENV_FILE}" "${TMP_DIR}/unknown-stable-profile.env"
replace_env_line "${TMP_DIR}/unknown-stable-profile.env" ANTHROPIC_STABLE_CANARY_PROFILE unknown_profile
chmod 600 "${TMP_DIR}/unknown-stable-profile.env"
if PATH="${FAKE_BIN}:${PATH}" \
  MOCK_ENV_FILE="${MOCK_ENV_FILE}" \
  IMAGE_TAG="test-unknown-stable-profile" \
  DEPLOY_DIR="${DEPLOY_DIR}" \
  ANTHROPIC_STABLE_CANARY_ENV_FILE="${TMP_DIR}/unknown-stable-profile.env" \
  SKIP_BUILD=1 \
  bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh" >/dev/null 2>&1; then
  echo "unknown stable canary profile was accepted" >&2
  exit 1
fi

printf '\nUNKNOWN_KEY=1\n' >> "${STABLE_ENV_FILE}"
if PATH="${FAKE_BIN}:${PATH}" \
  MOCK_ENV_FILE="${MOCK_ENV_FILE}" \
  IMAGE_TAG="test-invalid-stable-config" \
  DEPLOY_DIR="${DEPLOY_DIR}" \
  ANTHROPIC_STABLE_CANARY_ENV_FILE="${STABLE_ENV_FILE}" \
  SKIP_BUILD=1 \
  bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh" >/dev/null 2>&1; then
  echo "unknown stable canary env key was accepted" >&2
  exit 1
fi

for expected in \
  'GATEWAY_FIRST_SEMANTIC_TIMEOUT=50' \
  'GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS=60' \
  'GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS=90' \
  'GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS=150'; do
  grep -Fq -- "- ${expected}" "${DEPLOY_DIR}/docker-compose.override.yml"
done

if PATH="${FAKE_BIN}:${PATH}" \
  MOCK_ENV_FILE="${MOCK_ENV_FILE}" \
  IMAGE_TAG="test-invalid-timeout" \
  DEPLOY_DIR="${DEPLOY_DIR}" \
  SKIP_BUILD=1 \
  GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS=60 \
  GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS=30 \
  bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh" >/dev/null 2>&1; then
  echo "invalid Kiro timeout relationship was accepted" >&2
  exit 1
fi

MISSING_TIMEOUT_ENV_FILE="${TMP_DIR}/container-missing-timeouts.env"
grep -v -E 'GATEWAY_KIRO_RESILIENCE_(RESPONSE_HEADER_TIMEOUT|FIRST_SEMANTIC_TIMEOUT|FAILOVER_BUDGET)_SECONDS=' \
  "${MOCK_ENV_FILE}" > "${MISSING_TIMEOUT_ENV_FILE}"
if PATH="${FAKE_BIN}:${PATH}" \
  MOCK_ENV_FILE="${MISSING_TIMEOUT_ENV_FILE}" \
  IMAGE_TAG="test-missing-timeouts" \
  DEPLOY_DIR="${DEPLOY_DIR}" \
  SKIP_BUILD=1 \
  bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh" >/dev/null 2>&1; then
  echo "Kiro enforce rollout accepted missing timeout policy" >&2
  exit 1
fi

SSH_ARGS_FILE="${TMP_DIR}/ssh.args" \
  PATH="${FAKE_BIN}:${PATH}" \
  bash "${SCRIPT_DIR}/deploy-prod.sh" \
    --host mock-prod \
    --skip-sync \
    --tag test-timeout-forwarding \
    --kiro-response-header-timeout-seconds 60 \
    --kiro-first-semantic-timeout-seconds 90 \
    --kiro-failover-budget-seconds 150 >/dev/null

for expected in \
  'GATEWAY_KIRO_RESILIENCE_RESPONSE_HEADER_TIMEOUT_SECONDS=60' \
  'GATEWAY_KIRO_RESILIENCE_FIRST_SEMANTIC_TIMEOUT_SECONDS=90' \
  'GATEWAY_KIRO_RESILIENCE_FAILOVER_BUDGET_SECONDS=150'; do
  grep -Fxq "${expected}" "${TMP_DIR}/ssh.args"
done

echo "rebuild-prod-sub2api timeout preservation tests passed"
