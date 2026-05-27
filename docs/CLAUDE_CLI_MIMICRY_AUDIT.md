# Claude CLI mimicry audit

This workflow compares official Claude Code CLI traffic with the request
sub2api forwards to Anthropic. It is intended for protocol consistency
debugging: headers, beta tokens, `metadata.user_id`, session headers, system
shape, tools, cache-control placement, auxiliary endpoint inventory, and timing.

Raw captures must stay local under `/tmp/sub2api-claude-cli-capture-*` and must
not be committed. Reports and samples should redact `Authorization`, cookies,
tokens, and account identifiers unless the operator explicitly keeps them local.

## Official CLI Capture

Use a process-scoped proxy so the rest of the machine, including Codex and
production traffic, does not depend on the capture proxy.

```bash
CAPTURE_DIR="/tmp/sub2api-claude-cli-capture-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$CAPTURE_DIR"
PORT="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"

mitmdump \
  --listen-host 127.0.0.1 \
  --listen-port "$PORT" \
  --mode upstream:http://127.0.0.1:7890 \
  -w "$CAPTURE_DIR/flows.mitm"
```

If the local network can reach Anthropic directly, omit `--mode upstream:...`.
When the host normally uses Clash/Proxifier, chaining mitmproxy to the local
proxy keeps capture process-scoped without changing the system proxy.

Before the formal run, confirm official auth while custom API/base-url
environment is cleared:

```bash
env -u ANTHROPIC_BASE_URL \
  -u ANTHROPIC_AUTH_TOKEN \
  -u ANTHROPIC_API_KEY \
  -u CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC \
  claude auth status
```

If user-level `~/.claude/settings.json` contains `ANTHROPIC_BASE_URL`, run the
official capture with project/local settings only:

```bash
env -u ANTHROPIC_BASE_URL \
  -u ANTHROPIC_AUTH_TOKEN \
  -u ANTHROPIC_API_KEY \
  -u CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC \
  HTTPS_PROXY="http://127.0.0.1:$PORT" \
  HTTP_PROXY="http://127.0.0.1:$PORT" \
  NODE_EXTRA_CA_CERTS="$HOME/.mitmproxy/mitmproxy-ca-cert.pem" \
  claude --setting-sources project,local -p "reply with one short sentence"
```

During a five-minute window, run:

- one normal streaming `claude -p` request
- one default request with tools enabled
- one `--continue` or resume request

Then export a structured summary with a local mitm addon or equivalent. The
committed audit script can consume that summary:

```bash
python3 scripts/audit_claude_cli_mimicry.py \
  --flow-summary "$CAPTURE_DIR/flows.summary.json"
```

When the summary includes `body_summary.cache_controls` or
`body_summary.cache_control_ttls` plus response `usage`/`usage_summary`, the
script also checks TTL and billing consistency. A request that sends `ttl=1h`
must not be recorded only as `ephemeral_5m_input_tokens`.

## Latest Local Capture Result

The local five-minute official capture on 2026-05-27 used Claude Code
`2.1.111`. Auth status was `firstParty/oauth_token`.

Observed official Anthropic endpoints:

- `GET /v1/mcp_servers?limit=1000`
- `GET /api/claude_cli/bootstrap`
- `GET /api/claude_code_penguin_mode`
- `GET /api/claude_code_grove`
- `GET /api/oauth/account/settings`
- `GET /mcp-registry/v0/servers?...`
- `POST /v1/messages?beta=true`

No separate high-frequency `recordCodeAssistMetrics`,
`recordTrajectoryAnalytics`, or `listExperiments` polling was observed in this
window.

Core `/v1/messages` fingerprint:

- `User-Agent`: `claude-cli/2.1.111 (external, sdk-cli)`
- `x-stainless-package-version`: `0.81.0`
- `x-stainless-os`: `MacOS`
- `x-stainless-arch`: `arm64`
- `x-stainless-runtime`: `node`
- `x-stainless-runtime-version`: `v24.3.0`
- `x-stainless-timeout`: `600`
- `x-app`: `cli`
- `anthropic-dangerous-direct-browser-access`: `true`
- `accept`: `application/json`
- `accept-encoding`: `gzip, deflate, br, zstd`
- `X-Claude-Code-Session-Id` matches `metadata.user_id.session_id`
- billing system block uses `cc_entrypoint=sdk-cli`

Main Sonnet beta set:

```text
claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,advanced-tool-use-2025-11-20,effort-2025-11-24
```

Haiku title beta set:

```text
oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,structured-outputs-2025-12-15
```

## Compare sub2api Upstream

Start sub2api with gateway body debug enabled in a local test environment:

```bash
SUB2API_DEBUG_GATEWAY_BODY=/tmp/sub2api-gateway-debug.log ./sub2api
```

Send the equivalent request through sub2api. List snapshots:

```bash
python3 scripts/audit_claude_cli_mimicry.py \
  --list-snapshots /tmp/sub2api-gateway-debug.log
```

Compare official capture sample and sub2api upstream snapshot:

```bash
python3 scripts/audit_claude_cli_mimicry.py \
  --real /tmp/real-claude-request.json \
  --sub2api /tmp/sub2api-gateway-debug.log \
  --sub2api-tag UPSTREAM_FORWARD \
  --strict
```

Use `--json` for automation. Use `--show-values` only on local trusted files.
Auth headers are redacted by default.

## Auxiliary Endpoint Policy

sub2api supports a Claude Code auxiliary compatibility layer:

```yaml
gateway:
  claude_code_aux_compat:
    mode: record # off | record | forward
```

Default `record` returns local success-compatible JSON for the auxiliary
endpoints captured above and logs compact redacted metadata. `forward` is
reserved and currently behaves record-compatible; production must not forward
telemetry or experiment traffic unless explicitly implemented and enabled.

For non-Claude-CLI clients routed through Anthropic OAuth/SetupToken mimicry,
sub2api may also send a throttled upstream companion set:

- `GET /api/claude_cli/bootstrap`
- `GET /api/claude_code_penguin_mode`
- `GET /api/claude_code_grove`
- `GET /api/oauth/account/settings`
- `GET /v1/mcp_servers?limit=1000`
- `GET /mcp-registry/v0/servers?...`

The audit script reports companion coverage from `--flow-summary`. Missing
companion endpoints are medium severity because they affect behavioral parity,
but they should remain throttled and fail-open so user requests are not blocked
by auxiliary traffic.

## Notes

- Different captures can legitimately use different session IDs. The audit only
  treats session data as high risk when sub2api's own
  `X-Claude-Code-Session-Id` does not match its own `metadata.user_id`.
- Header order findings are low severity because Go debug snapshots may be
  sorted for readability. Confirm with a true wire capture before changing
  transport behavior.
- Run this audit after Claude CLI upgrades, mimicry changes, or Anthropic OAuth
  compatibility regressions.
