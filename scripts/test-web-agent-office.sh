#!/usr/bin/env bash
# Local synthetic integration only. Does not read any production configuration.
set -euo pipefail

: "${WEB_AGENT_TEST_DSN:?Set a loopback disposable PostgreSQL test DSN first}"
task_repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
task_image="sub2api-webagent-office:integration-test"
task_container=""
cleanup() {
  task_exit_code=$?
  if [[ -n "$task_container" ]]; then
    if [[ "$task_exit_code" -ne 0 ]]; then docker logs "$task_container" || true; fi
    docker stop "$task_container" >/dev/null || true
  fi
  return "$task_exit_code"
}
trap cleanup EXIT

docker build -q -t "$task_image" "$task_repo_root/runtimes/web-agent-office"
export WEB_AGENT_RENDERER_TOKEN
WEB_AGENT_RENDERER_TOKEN="$(openssl rand -hex 32)"
# Host-side Go tests need a loopback-only published port. Production uses the
# private application network instead, with no published renderer port.
task_container="$(docker run --rm -d --label sub2api.local-test=web-agent \
  --network bridge -p 127.0.0.1::8090 --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --memory 1g --cpus 1 --pids-limit 128 \
  --tmpfs /tmp:size=256m,mode=1777 \
  --tmpfs /home/renderer:size=16m,uid=10001,gid=10001,mode=700 \
  -e WEB_AGENT_RENDERER_TOKEN "$task_image")"
task_address="$(docker port "$task_container" 8090/tcp)"
[[ "$task_address" =~ ^127\.0\.0\.1:[0-9]+$ ]] || exit 1
export WEB_AGENT_OFFICE_ENDPOINT="http://$task_address"
go -C "$task_repo_root/backend" test -race -tags integration ./internal/repository \
  -run '^TestWebAgent|^TestWebChatExplicitIntent' -count=1
