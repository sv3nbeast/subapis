# Claude CLI mimicry audit

This workflow compares official Claude Code CLI traffic with the request
sub2api forwards to Anthropic. It is intended for protocol consistency
debugging: headers, beta tokens, `metadata.user_id`, session headers, system
shape, tools, cache-control placement, auxiliary endpoint inventory, and timing.

Raw captures must stay local under `/tmp/sub2api-claude-cli-capture-*` and must
not be committed. Reports and samples should redact `Authorization`, cookies,
tokens, and account identifiers unless the operator explicitly keeps them local.
The committed audit script always redacts authentication and cookie headers,
including when `--show-values` is used.

## Claude Desktop Boundary

Claude Desktop is an Electron shell around `claude.ai` plus local Claude Code
and MCP helpers. Its main chat pane uses the web app surface and is not a
drop-in `/v1/messages` Claude Code CLI request. Do not treat a Desktop web
session as a source of reusable OAuth headers, cookies, or bearer tokens.

For sub2api compatibility work, use Claude Desktop only as a redacted
observation source for endpoint inventory and installed Claude Code helper
versions. The upstream request sub2api can safely reproduce is the Claude Code
OAuth `/v1/messages` protocol shape: Claude Code UA, stainless headers, beta
tokens, `metadata.user_id`, `X-Claude-Code-Session-Id`, and billing attribution
block. If a future investigation needs Desktop web traffic, capture only
redacted endpoint/shape summaries and keep raw flows local.

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
  -s scripts/mitm_claude_flow_summary.py \
  --set claude_flow_summary_out="$CAPTURE_DIR/flows.summary.json" \
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

If user-level `~/.claude/settings.json` contains `ANTHROPIC_BASE_URL`, it
overrides the helper even when shell env vars are cleared. Run the official
capture with project/local settings only:

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

`scripts/mitm_claude_flow_summary.py` records endpoint inventory, redacted
headers, body shape, cache-control locations, metadata/session shape, system
entry previews, and usage summaries. It does not write raw request bodies,
cookies, or bearer/API keys. Keep `flows.mitm` local for short-lived debugging
only; use `flows.summary.json` for repeatable comparisons. The addon records
request attempts before the response arrives so companion endpoints are still
counted when upstream closes the connection mid-flight.

When the summary includes `body_summary.cache_controls` or
`body_summary.cache_control_ttls` plus response `usage`/`usage_summary`, the
script also checks TTL and billing consistency. A request that sends `ttl=1h`
must not be recorded only as `ephemeral_5m_input_tokens`.

## Latest Local Capture Result

The latest local redacted capture on 2026-06-09 used the Claude Desktop cached
Claude Code helper at
`~/Library/Application Support/Claude/claude-code/2.1.165/`. Running that
helper with `--version` reported `2.1.165 (Claude Code)`.

Two distinct local behaviors were observed:

1. With default user settings, helper traffic was redirected to
   `https://api.subapis.com` because `~/.claude/settings.json` set
   `ANTHROPIC_BASE_URL`. This path is not suitable as the official Claude Code
   reference capture.
2. With `--setting-sources project,local` and shell overrides cleared, the
   helper hit official `api.anthropic.com` endpoints. The request shape matched
   official Claude Code 2.1.165, but all official OAuth requests returned
   `401 Invalid authentication credentials`.

Treat the captured request shape as valid fingerprint evidence for headers,
beta tokens, endpoint inventory, metadata/session shape, and billing block
construction. Do not treat it as proof that the local helper's official-host
OAuth path is currently healthy.

Observed official Anthropic endpoints:

- `GET /v1/mcp_servers?limit=1000`
- `GET /api/claude_cli/bootstrap`
- `GET /api/claude_code_penguin_mode`
- `GET /api/claude_code_grove`
- `GET /api/oauth/profile`
- `GET /v1/mcp_servers?limit=1000`
- `GET /mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth`
- `POST /v1/messages?beta=true`

No separate high-frequency `recordCodeAssistMetrics`,
`recordTrajectoryAnalytics`, or `listExperiments` polling was observed in this
window.

Core `/v1/messages` fingerprint:

- `User-Agent`: `claude-cli/2.1.165 (external, sdk-cli)`
- `x-stainless-package-version`: `0.94.0`
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
- billing block preview:
  `x-anthropic-billing-header: cc_version=2.1.165.e05; cc_entrypoint=sdk-cli; cch=300c5;`
- title probe model: `awsclaude4.5-haiku`
- title probe system text starts with:
  `Generate a concise, sentence-case title (3-7 words)...`

Main Sonnet beta set:

```text
claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,advanced-tool-use-2025-11-20,effort-2025-11-24,extended-cache-ttl-2025-04-11
```

Haiku title beta set:

```text
oauth-2025-04-20,interleaved-thinking-2025-05-14,context-management-2025-06-27,prompt-caching-scope-2026-01-05,mid-conversation-system-2026-04-07,effort-2025-11-24,structured-outputs-2025-12-15
```

The previous successful local baseline from 2026-05-27 used Claude Code
`2.1.111` with `x-stainless-package-version=0.81.0` and
`firstParty/oauth_token` auth. Keep it as historical evidence only; current
sub2api constants track the newer 2.1.165 request construction.

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
- `GET /api/oauth/profile`
- `GET /v1/mcp_servers?limit=1000`
- `GET /mcp-registry/v0/servers?version=latest&limit=100&visibility=commercial%2Cgsuite%2Centerprise%2Chealth`

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
