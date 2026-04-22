#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/deploy-prod.sh [options]

Description:
  Sync the current local repository to the production source directory,
  then trigger a remote image rebuild and a zero-data-impact sub2api rollout.

Options:
  --host HOST                    SSH host alias. Required if REMOTE_HOST is unset
  --src-dir DIR                  Remote source directory. Default: /root/sub2api-src
  --deploy-dir DIR               Remote deploy directory. Default: /root/sub2api-deploy
  --tag TAG                      Docker image tag suffix. Default: prod-YYYYmmdd-HHMMSS-<gitsha>
  --image-repo NAME              Docker image repo name. Default: sub2api
  --antigravity-version VERSION  ANTIGRAVITY_USER_AGENT_VERSION to inject. Default: 1.22.2
  --skip-sync                    Skip rsync and only trigger remote rebuild/redeploy
  --no-delete                    Disable rsync --delete
  -n, --dry-run                  Show rsync changes only; skip remote rebuild
  -h, --help                     Show this help

Examples:
  scripts/deploy-prod.sh --host your-prod-host
  scripts/deploy-prod.sh --tag prod-manual-001
  scripts/deploy-prod.sh --host your-prod-host --skip-sync --tag prod-hotfix-001
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

REMOTE_HOST="${REMOTE_HOST:-}"
REMOTE_SRC_DIR="${REMOTE_SRC_DIR:-/root/sub2api-src}"
REMOTE_DEPLOY_DIR="${REMOTE_DEPLOY_DIR:-/root/sub2api-deploy}"
IMAGE_REPO="${IMAGE_REPO:-sub2api}"
ANTIGRAVITY_VERSION="${ANTIGRAVITY_USER_AGENT_VERSION:-1.22.2}"
IMAGE_TAG=""
SKIP_SYNC=0
DRY_RUN=0
RSYNC_DELETE=1

while (($# > 0)); do
  case "$1" in
    --host)
      REMOTE_HOST="$2"
      shift 2
      ;;
    --src-dir)
      REMOTE_SRC_DIR="$2"
      shift 2
      ;;
    --deploy-dir)
      REMOTE_DEPLOY_DIR="$2"
      shift 2
      ;;
    --tag)
      IMAGE_TAG="$2"
      shift 2
      ;;
    --image-repo)
      IMAGE_REPO="$2"
      shift 2
      ;;
    --antigravity-version)
      ANTIGRAVITY_VERSION="$2"
      shift 2
      ;;
    --skip-sync)
      SKIP_SYNC=1
      shift
      ;;
    --no-delete)
      RSYNC_DELETE=0
      shift
      ;;
    -n|--dry-run)
      DRY_RUN=1
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

if [[ -z "${REMOTE_HOST}" ]]; then
  echo "REMOTE_HOST is required. Use --host or set REMOTE_HOST." >&2
  usage >&2
  exit 1
fi

if [[ -z "${IMAGE_TAG}" ]]; then
  timestamp="$(date +%Y%m%d-%H%M%S)"
  git_sha="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo local)"
  IMAGE_TAG="prod-${timestamp}-${git_sha}"
fi

IMAGE_REF="${IMAGE_REPO}:${IMAGE_TAG}"

RSYNC_ARGS=(
  -az
  --exclude
  .git
  --exclude
  node_modules
  --exclude
  frontend/node_modules
  --exclude
  backend/internal/web/dist
  --exclude
  deploy/data
  --exclude
  deploy/postgres_data
  --exclude
  deploy/redis_data
  --exclude
  deploy/.env
  --exclude
  .DS_Store
)

if rsync --info=progress2 --version >/dev/null 2>&1; then
  RSYNC_ARGS+=(--info=progress2)
else
  RSYNC_ARGS+=(--progress)
fi

TAR_EXCLUDES=(
  --exclude=.git
  --exclude=node_modules
  --exclude=frontend/node_modules
  --exclude=backend/internal/web/dist
  --exclude=deploy/data
  --exclude=deploy/postgres_data
  --exclude=deploy/redis_data
  --exclude=deploy/.env
  --exclude=.DS_Store
)

if [[ "${RSYNC_DELETE}" -eq 1 ]]; then
  RSYNC_ARGS+=(--delete)
fi

if [[ "${DRY_RUN}" -eq 1 ]]; then
  RSYNC_ARGS+=(--dry-run)
fi

echo "Remote host:        ${REMOTE_HOST}"
echo "Remote source dir:  ${REMOTE_SRC_DIR}"
echo "Remote deploy dir:  ${REMOTE_DEPLOY_DIR}"
echo "Image ref:          ${IMAGE_REF}"
echo "Antigravity ver:    ${ANTIGRAVITY_VERSION}"

if [[ "${SKIP_SYNC}" -eq 0 ]]; then
  ssh "${REMOTE_HOST}" "mkdir -p '${REMOTE_SRC_DIR}'"

  if ssh "${REMOTE_HOST}" "command -v rsync >/dev/null 2>&1"; then
    require_cmd rsync
    rsync "${RSYNC_ARGS[@]}" "${REPO_ROOT}/" "${REMOTE_HOST}:${REMOTE_SRC_DIR}/"
  else
    if [[ "${DRY_RUN}" -eq 1 ]]; then
      echo
      echo "Remote host has no rsync; tar fallback does not support dry-run."
      echo "Dry run stopped before remote sync."
      exit 0
    fi

    echo "Remote rsync not found. Falling back to tar stream sync."
    tar -C "${REPO_ROOT}" "${TAR_EXCLUDES[@]}" -cf - . \
      | ssh "${REMOTE_HOST}" \
          "set -euo pipefail; mkdir -p '${REMOTE_SRC_DIR}'; find '${REMOTE_SRC_DIR}' -mindepth 1 -maxdepth 1 -exec rm -rf {} +; tar -xf - -C '${REMOTE_SRC_DIR}'"
  fi
fi

if [[ "${DRY_RUN}" -eq 1 ]]; then
  echo
  echo "Dry run complete. Remote rebuild was skipped."
  exit 0
fi

ssh "${REMOTE_HOST}" \
  env \
    IMAGE_REPO="${IMAGE_REPO}" \
    IMAGE_TAG="${IMAGE_TAG}" \
    DEPLOY_DIR="${REMOTE_DEPLOY_DIR}" \
    ANTIGRAVITY_USER_AGENT_VERSION="${ANTIGRAVITY_VERSION}" \
    bash "${REMOTE_SRC_DIR}/scripts/rebuild-prod-sub2api.sh"
