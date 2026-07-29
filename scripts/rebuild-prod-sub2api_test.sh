#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

FAKE_BIN="${TMP_DIR}/bin"
DEPLOY_DIR="${TMP_DIR}/deploy"
MOCK_ENV_FILE="${TMP_DIR}/container.env"
mkdir -p "${FAKE_BIN}" "${DEPLOY_DIR}"
touch "${DEPLOY_DIR}/docker-compose.yml"

cat > "${MOCK_ENV_FILE}" <<'EOF'
GATEWAY_OPENAI_KIRO_BRIDGE_ENABLED=true
GATEWAY_FIRST_SEMANTIC_TIMEOUT=50
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
  cat "${MOCK_ENV_FILE}"
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
    if [[ "${arg}" == "ps" && "${*: -1}" == "sub2api" ]]; then
      printf 'container-id\n'
      exit 0
    fi
  done
  exit 0
fi
if [[ "$1" == "exec" && "$3" == "printenv" ]]; then
  sed -n "s/^${4}=//p" "${MOCK_ENV_FILE}"
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
    bash "${SCRIPT_DIR}/rebuild-prod-sub2api.sh" >/dev/null
}

run_rebuild

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
