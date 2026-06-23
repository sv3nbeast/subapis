#!/usr/bin/env bash

set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/verify-prod-proxy-dns.sh --host HOST [options]

Description:
  Read-only production evidence collection for account-pool proxy/DNS handling.
  The script does not install packages, change containers, restart services, or
  write production files. It reports what can be proven from current logs and
  host capabilities.

Options:
  --host HOST              SSH host alias. Required if REMOTE_HOST is unset.
  --container NAME         Container name. Default: sub2api.
  --since DURATION         docker logs --since value. Default: 2h.
  --target-regex REGEX     DNS target regex for optional tcpdump guidance.
                           Default: api\.anthropic\.com|claude\.ai|api\.openai\.com
  -h, --help               Show this help.

Examples:
  scripts/verify-prod-proxy-dns.sh --host sub2api-prod
  scripts/verify-prod-proxy-dns.sh --host sub2api-prod --since 24h
EOF
}

REMOTE_HOST="${REMOTE_HOST:-}"
CONTAINER="${CONTAINER:-sub2api}"
SINCE="${SINCE:-2h}"
TARGET_REGEX="${TARGET_REGEX:-api\\.anthropic\\.com|claude\\.ai|api\\.openai\\.com}"

while (($# > 0)); do
  case "$1" in
    --host)
      REMOTE_HOST="$2"
      shift 2
      ;;
    --container)
      CONTAINER="$2"
      shift 2
      ;;
    --since)
      SINCE="$2"
      shift 2
      ;;
    --target-regex)
      TARGET_REGEX="$2"
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
  echo "REMOTE_HOST is required. Use --host or set REMOTE_HOST." >&2
  usage >&2
  exit 1
fi

remote_cmd=$(
  printf 'CONTAINER=%q SINCE=%q TARGET_REGEX=%q bash -s' \
    "${CONTAINER}" \
    "${SINCE}" \
    "${TARGET_REGEX}"
)

ssh "${REMOTE_HOST}" "${remote_cmd}" <<'REMOTE'
set -euo pipefail

echo "== container =="
docker inspect "${CONTAINER}" --format 'image={{.Config.Image}} image_id={{.Image}} health={{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}} started={{.State.StartedAt}}'

echo
echo "== compose =="
if [[ -d /root/sub2api-deploy ]]; then
  (cd /root/sub2api-deploy && docker compose ps)
else
  echo "/root/sub2api-deploy missing"
fi

echo
echo "== host dns tools =="
for c in tcpdump tshark ngrep conntrack strace nsenter ss lsof journalctl docker grep awk sed timeout ip; do
  if command -v "${c}" >/dev/null 2>&1; then
    echo "${c}=ok"
  else
    echo "${c}=missing"
  fi
done

echo
echo "== host resolv.conf =="
sed -n '1,80p' /etc/resolv.conf || true

echo
echo "== container resolv.conf =="
docker exec "${CONTAINER}" sh -lc 'sed -n "1,80p" /etc/resolv.conf; echo; ip route 2>/dev/null || true'

echo
echo "== recent proxy evidence logs =="
docker logs --since "${SINCE}" "${CONTAINER}" 2>&1 \
  | grep -E 'proxy_enabled|proxy_protocol|ProxyEnabled|proxy_host|proxy_port|tls_fingerprint' \
  | tail -n 120 || true

echo
echo "== recent target dns strings in app logs =="
docker logs --since "${SINCE}" "${CONTAINER}" 2>&1 \
  | grep -Ei "${TARGET_REGEX}" \
  | tail -n 80 || true

echo
echo "== evidence grade =="
if command -v tcpdump >/dev/null 2>&1; then
  cat <<EOF
strong_possible: tcpdump is available. During a controlled account-pool request,
run a separate capture for udp/tcp port 53 and confirm no target-domain DNS
queries leave the host/container bridge. This script does not start captures.
EOF
else
  cat <<EOF
strong_missing: packet capture is unavailable without installing/adding tools.
medium_expected_after_deploy: app logs should show proxy_enabled=true and
proxy_protocol=socks5h for proxied account requests, and should no longer log
proxy_host/proxy_port. Use proxy-provider DNS logs for remote-DNS confirmation
when host packet capture is unavailable.
EOF
fi
REMOTE
