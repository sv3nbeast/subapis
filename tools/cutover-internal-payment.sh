#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/cutover-internal-payment.sh --host HOST [--deploy-dir DIR]

Description:
  Upload and run the remote payment cutover helper that:
  - backs up sub2api / sub2apipay databases
  - migrates provider instances, subscription plans, and payment settings
  - imports legacy orders and audit logs into built-in payment
  - preserves all legacy sub2apipay data

Notes:
  - This script does NOT deploy application code.
  - Run it only after the sub2api built-in payment tables/routes are available.
EOF
}

REMOTE_HOST="${REMOTE_HOST:-}"
REMOTE_DEPLOY_DIR="${REMOTE_DEPLOY_DIR:-/root/sub2api-deploy}"

while (($# > 0)); do
  case "$1" in
    --host)
      REMOTE_HOST="$2"
      shift 2
      ;;
    --deploy-dir)
      REMOTE_DEPLOY_DIR="$2"
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

if [[ -z "${REMOTE_HOST}" ]]; then
  echo "REMOTE_HOST is required" >&2
  usage >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REMOTE_PATH="/tmp/remote-cutover-internal-payment-${RANDOM}.sh"

scp "${SCRIPT_DIR}/remote-cutover-internal-payment.sh" "${REMOTE_HOST}:${REMOTE_PATH}"
ssh "${REMOTE_HOST}" "chmod +x '${REMOTE_PATH}' && DEPLOY_DIR='${REMOTE_DEPLOY_DIR}' bash '${REMOTE_PATH}' && rm -f '${REMOTE_PATH}'"
