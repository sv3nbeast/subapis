#!/usr/bin/env bash
set -euo pipefail

HOST="${SERVER_HOST:-127.0.0.1}"
PORT="${SERVER_PORT:-18731}"
BASE_URL="http://${HOST}:${PORT}"

echo "[smoke-antigravity] health check -> ${BASE_URL}/health"
curl -fsS "${BASE_URL}/health"
echo

echo "[smoke-antigravity] anthropic endpoint -> ${BASE_URL}/antigravity/v1/messages"
echo "[smoke-antigravity] gemini endpoint -> ${BASE_URL}/antigravity/v1beta/models"

API_KEY="${ANTIGRAVITY_LOCAL_API_KEY:-${API_KEY:-}}"

if [[ -z "${API_KEY}" ]]; then
  cat <<EOF

[smoke-antigravity] no API key provided.

Set one of:
  ANTRIGRAVITY_LOCAL_API_KEY
  API_KEY

Then re-run this script for a real Claude request, or use:

curl -sS "${BASE_URL}/antigravity/v1/messages" \\
  -H "content-type: application/json" \\
  -H "x-api-key: <YOUR_KEY>" \\
  -H "anthropic-version: 2023-06-01" \\
  -d '{
    "model":"claude-opus-4-6",
    "max_tokens":64,
    "messages":[{"role":"user","content":"hi"}]
  }'
EOF
  exit 0
fi

echo
echo "[smoke-antigravity] real Claude request -> ${BASE_URL}/antigravity/v1/messages"
curl -fsS "${BASE_URL}/antigravity/v1/messages" \
  -H "content-type: application/json" \
  -H "x-api-key: ${API_KEY}" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model":"claude-opus-4-6",
    "max_tokens":64,
    "messages":[{"role":"user","content":"hi"}]
  }'
echo
