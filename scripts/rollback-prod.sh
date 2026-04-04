#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/rollback-prod.sh [options]

Description:
  Roll back the production sub2api service by restoring a previous
  docker-compose override backup on the target host and recreating only
  the sub2api container.

Options:
  --host HOST             SSH host alias. Default: sub2api-prod
  --deploy-dir DIR        Remote deploy directory. Default: /root/sub2api-deploy
  --service NAME          Compose service name. Default: sub2api
  --backup FILE           Explicit remote backup file to restore
  --health-timeout SEC    Health wait timeout in seconds. Default: 180
  --list                  List remote override backups and exit
  -h, --help              Show this help

Examples:
  scripts/rollback-prod.sh --list
  scripts/rollback-prod.sh
  scripts/rollback-prod.sh --backup /root/sub2api-deploy/docker-compose.override.yml.bak-20260404-215321
EOF
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REMOTE_SCRIPT="${SCRIPT_DIR}/rollback-prod-sub2api.sh"

REMOTE_HOST="${REMOTE_HOST:-sub2api-prod}"
REMOTE_DEPLOY_DIR="${REMOTE_DEPLOY_DIR:-/root/sub2api-deploy}"
SERVICE_NAME="${SERVICE_NAME:-sub2api}"
HEALTH_TIMEOUT_SECONDS="${HEALTH_TIMEOUT_SECONDS:-180}"
BACKUP_FILE=""
LIST_ONLY=0

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
    --service)
      SERVICE_NAME="$2"
      shift 2
      ;;
    --backup)
      BACKUP_FILE="$2"
      shift 2
      ;;
    --health-timeout)
      HEALTH_TIMEOUT_SECONDS="$2"
      shift 2
      ;;
    --list)
      LIST_ONLY=1
      shift
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

if [[ ! -f "${REMOTE_SCRIPT}" ]]; then
  echo "Remote helper script not found: ${REMOTE_SCRIPT}" >&2
  exit 1
fi

env_args=(
  "DEPLOY_DIR=${REMOTE_DEPLOY_DIR}"
  "SERVICE_NAME=${SERVICE_NAME}"
  "HEALTH_TIMEOUT_SECONDS=${HEALTH_TIMEOUT_SECONDS}"
)

if [[ -n "${BACKUP_FILE}" ]]; then
  env_args+=("BACKUP_FILE=${BACKUP_FILE}")
fi

if [[ "${LIST_ONLY}" -eq 1 ]]; then
  env_args+=("LIST_ONLY=1")
fi

ssh "${REMOTE_HOST}" env "${env_args[@]}" bash -s -- < "${REMOTE_SCRIPT}"
