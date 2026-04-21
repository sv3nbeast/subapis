#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

REMOTE_HOST="sub2api-prod"
SERVICE_NAME="sub2api"
COMPOSE_DIR="/root/sub2api-deploy"
RELEASE_DIR_GLOB="/root/sub2api-src-release-*"
TARGET_REF="HEAD"
PRINT_ONLY="false"

usage() {
  cat <<'EOF'
Usage:
  deploy/check_prod_descendant.sh [options]

Options:
  --target-ref <git-ref>       Local git ref to validate. Default: HEAD
  --remote-host <ssh-host>     SSH host alias. Default: sub2api-prod
  --service <container-name>   Running service/container name. Default: sub2api
  --compose-dir <path>         Remote compose directory. Default: /root/sub2api-deploy
  --release-dir-glob <glob>    Remote release dir glob fallback. Default: /root/sub2api-src-release-*
  --print-only                 Print resolved commits only; do not enforce
  -h, --help                   Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --target-ref)
      TARGET_REF="${2:?missing value for --target-ref}"
      shift 2
      ;;
    --remote-host)
      REMOTE_HOST="${2:?missing value for --remote-host}"
      shift 2
      ;;
    --service)
      SERVICE_NAME="${2:?missing value for --service}"
      shift 2
      ;;
    --compose-dir)
      COMPOSE_DIR="${2:?missing value for --compose-dir}"
      shift 2
      ;;
    --release-dir-glob)
      RELEASE_DIR_GLOB="${2:?missing value for --release-dir-glob}"
      shift 2
      ;;
    --print-only)
      PRINT_ONLY="true"
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

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

require_command git
require_command ssh

target_commit="$(git -C "${REPO_ROOT}" rev-parse --verify "${TARGET_REF}^{commit}")"
target_short="$(git -C "${REPO_ROOT}" rev-parse --short=8 "${target_commit}")"

remote_image="$(
  ssh "${REMOTE_HOST}" \
    "docker inspect ${SERVICE_NAME} --format '{{.Config.Image}}' 2>/dev/null || true"
)"

extract_commit_from_string() {
  local input="$1"
  if [[ "${input}" =~ -([0-9a-f]{7,40})$ ]]; then
    printf '%s\n' "${BASH_REMATCH[1]}"
    return 0
  fi
  return 1
}

prod_ref=""
if [[ -n "${remote_image}" ]]; then
  prod_ref="$(extract_commit_from_string "${remote_image}" || true)"
fi

if [[ -z "${prod_ref}" ]]; then
  prod_ref="$(
    ssh "${REMOTE_HOST}" \
      "ls -1dt ${RELEASE_DIR_GLOB} 2>/dev/null | head -n1 | sed -E 's#^.*/[^-]+-([0-9a-f]{7,40})\$#\\1#'"
  )"
fi

if [[ -z "${prod_ref}" ]]; then
  prod_ref="$(
    ssh "${REMOTE_HOST}" \
      "grep -E '^[[:space:]]*image:[[:space:]]+sub2api:prod-' '${COMPOSE_DIR}/docker-compose.override.yml' 2>/dev/null | head -n1 | sed -E 's#.*-([0-9a-f]{7,40})\$#\\1#'"
  )"
fi

if [[ -z "${prod_ref}" ]]; then
  echo "Unable to determine current production commit from ${REMOTE_HOST}" >&2
  exit 1
fi

if ! git -C "${REPO_ROOT}" rev-parse --verify "${prod_ref}^{commit}" >/dev/null 2>&1; then
  echo "Production commit ${prod_ref} is not available locally. Fetching remotes..." >&2
  git -C "${REPO_ROOT}" fetch --all --prune >/dev/null
fi

prod_commit="$(git -C "${REPO_ROOT}" rev-parse --verify "${prod_ref}^{commit}")"
prod_short="$(git -C "${REPO_ROOT}" rev-parse --short=8 "${prod_commit}")"

echo "production_image=${remote_image:-unknown}"
echo "production_commit=${prod_commit}"
echo "target_commit=${target_commit}"

if [[ "${PRINT_ONLY}" == "true" ]]; then
  exit 0
fi

if git -C "${REPO_ROOT}" merge-base --is-ancestor "${prod_commit}" "${target_commit}"; then
  echo "OK: ${target_short} descends from current production ${prod_short}"
  exit 0
fi

cat >&2 <<EOF
Refusing deployment:
  target ${target_short} is NOT a descendant of current production ${prod_short}

This usually means you are about to deploy from the wrong baseline and may
silently drop features already in production.

Recommended fix:
  git checkout -b codex/prod-hotfix ${prod_short}
  # cherry-pick or merge your intended changes onto that branch

If you intentionally need a non-descendant deployment, stop using this guard and
document the rollback explicitly before proceeding.
EOF
exit 1
