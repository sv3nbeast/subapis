# Anthropic OAuth Stable Canary D1

## Purpose

D1 is a single-owner, single-account online canary for comparing Sub2API with
the known-good local `claude-gateway` request path. It is deliberately isolated
from normal Anthropic scheduling, OAuth mimicry, companion requests, account
failover, and background token refresh.

The feature is disabled by default. Deploying the code does not enroll or route
any existing account. Enrollment and runtime activation are two separate
operator actions.

D1 is evidence collection, not a general Claude compatibility mode and not a
guarantee against an upstream account restriction. Draw conclusions only after
a controlled like-for-like observation window.

## Reference Wire Contract

The message request mirrors the local `claude-gateway` proxy path:

- target is exactly `https://api.anthropic.com/v1/messages`;
- the inbound JSON body is sent byte-for-byte, with field order unchanged;
- outbound headers are limited to `Content-Type`, OAuth `Authorization`,
  `anthropic-version`, and the captured `anthropic-beta` value plus
  `oauth-2025-04-20`;
- the inbound API key, bearer token, cookies, forwarding headers, `x-app`,
  session header, and Stainless headers are not forwarded;
- no explicit upstream `User-Agent` is set, so Go uses its normal transport
  identity, matching the reference gateway rather than impersonating Node;
- no system prompt, metadata, model, tools, thinking, cache control, CCH,
  billing block, TTL, or tool name is inserted, deleted, or rewritten;
- the direct Go transport is used without account proxy, TLS impersonation,
  redirect following, generic retry, or application failover;
- one account-scoped HTTP client is reused so normal connection pooling remains
  stable across requests.

The refresh request mirrors the same reference implementation:

- target is exactly `https://platform.claude.com/v1/oauth/token`;
- the JSON payload contains `client_id`, `grant_type=refresh_token`, and the
  stored refresh token in the deterministic `encoding/json` order;
- headers are only JSON content type and `anthropic-beta: oauth-2025-04-20`;
- refresh is reactive only after the first message attempt returns 401;
- at most one refresh and one replay are allowed for a physical client request.

A second 401 or an ambiguous/rejected refresh durably pauses the account. A
transient refresh failure returns an error without changing credential state.
No other upstream status is retried by D1.

## Accepted Client Cohort

D1 accepts only the reviewed Claude Code 2.1.222 installation family:

- `claude-cli/2.1.222 (external, cli)`;
- `claude-cli/2.1.222 (external, sdk-cli)`;
- only the finite beta-header variants captured for those two forms.

Every request must also satisfy all of these conditions:

- `POST /v1/messages?beta=true` with JSON content and no compression;
- `x-app: cli` and `anthropic-version: 2023-06-01`;
- `stream=true` and a positive `max_tokens` field;
- strict valid JSON with no duplicate object keys;
- a lowercase canonical session UUID, identical in
  `X-Claude-Code-Session-Id` and `metadata.user_id.session_id`;
- an empty `metadata.user_id.account_uuid`;
- the exact 64-character lowercase device ID captured from the enrolled local
  installation;
- no Claude Code CCH billing marker in the system block.

Unknown Claude versions, unknown beta combinations, non-streaming messages,
compressed bodies, CCH requests, and `/v1/messages/count_tokens` fail locally
before any account credential is loaded or any upstream request is sent.
`count_tokens` currently returns 404 for this group.

Channel model restrictions remain active. A disallowed model is rejected
locally. A channel or account model mapping is not applied: enrollment or
request execution fails closed if a mapping would change the raw model value.

## Account Isolation Requirements

Use a new test account and a dedicated group and API key. Do not reuse an
existing shared production account for the first cohort.

Before enrollment, all of the following must be true:

- account platform is Anthropic, type is OAuth, status is active, concurrency
  is 1, and the account is schedulable;
- access token has the `sk-ant-oat` OAuth shape and a refresh token exists;
- account has no proxy, proxy fallback, parent account, custom base URL, TLS
  fingerprint, session masking, cache TTL override, generic OAuth passthrough,
  model mapping, compact mapping, or header override;
- the account belongs to exactly one active Anthropic group;
- the group is exclusive, Claude-Code-only, OAuth-only, contains exactly one
  account, and has no fallback or model routing;
- the group has exactly one active API key, owned by the configured active
  user; on first enrollment that key must never have been used;
- the account contains no partial D1 lifecycle marker.

Enrollment records the fixed device/profile, captures the previous schedulable
state, and makes the account unschedulable. Repository guards then prevent
generic account, group, API-key, credential-refresh, and background mutation
paths from changing its identity. Runtime traffic loads the account directly
and never enters the normal shared scheduler.

## Configuration

Set the identifiers while keeping the runtime switch off:

```text
GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=false
GATEWAY_ANTHROPIC_STABLE_CANARY_GROUP_ID=<dedicated-group-id>
GATEWAY_ANTHROPIC_STABLE_CANARY_ACCOUNT_ID=<dedicated-account-id>
GATEWAY_ANTHROPIC_STABLE_CANARY_OWNER_USER_ID=<owner-user-id>
GATEWAY_ANTHROPIC_STABLE_CANARY_API_KEY_ID=<unused-dedicated-key-id>
GATEWAY_ANTHROPIC_STABLE_CANARY_MAX_BODY_BYTES=67108864
```

The fixed device is supplied only to the lifecycle process:

```text
ANTHROPIC_STABLE_CANARY_DEVICE_ID=<64-lowercase-hex-device-id>
```

The lifecycle JSON output intentionally excludes device ID, account name,
tokens, API-key value, and request content.

## Deployment And Enable Sequence

1. Release the binary with `GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=false`.
2. Create and validate the isolated account, group, owner, and unused API key.
3. Inspect the current state:

   ```bash
   ./sub2api --anthropic-stable-canary-action=inspect
   ```

4. Run enrollment validation without writing:

   ```bash
   ANTHROPIC_STABLE_CANARY_DEVICE_ID="$DEVICE_ID" \
     ./sub2api --anthropic-stable-canary-action=enable \
       --anthropic-stable-canary-profile=claude_cli_2_1_222_v1
   ```

5. Execute the same validated enrollment:

   ```bash
   ANTHROPIC_STABLE_CANARY_DEVICE_ID="$DEVICE_ID" \
     ./sub2api --anthropic-stable-canary-action=enable \
       --anthropic-stable-canary-profile=claude_cli_2_1_222_v1 \
       --anthropic-stable-canary-execute
   ```

6. Inspect again and require `validated=true`, `executed=false`, and
   `enrolled_before=true`.
7. Set `GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=true` and restart the service.
8. Send traffic only with the configured owner, API key, client profile, and
   device. Any other owner or key is rejected before the body is read.

Never enable the runtime switch before enrollment. The lifecycle command also
refuses enrollment while the runtime switch is on.

## Emergency Stop And Retirement

Stop egress first:

1. Set `GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=false` and restart the service.
2. Run a dry retirement:

   ```bash
   ./sub2api --anthropic-stable-canary-action=disable
   ```

3. Review `restored_schedulable` and `requires_manual_recovery`.
4. Execute retirement:

   ```bash
   ./sub2api --anthropic-stable-canary-action=disable \
     --anthropic-stable-canary-execute
   ```

The disable operation removes all D1 marker fields even when the group or API
key has drifted. It restores normal scheduling only when every original
identity, ownership, group, API-key, runtime, and block check is still valid.
Otherwise the account remains unschedulable and requires manual recovery. A
blocked or upstream-rejected credential is never automatically returned to the
shared pool.

## Observation Contract

Start with one request at a time. Run at least 20 low-volume warm requests that
cover normal text, tool use, thinking, cache reuse, resumed conversation, and a
single intentional client cancellation. Then observe for at least 24 hours and
again at 72 hours before expanding the cohort.

Monitor:

- 2xx, 400, 401, 403, 429, and transport failures;
- durable block reason and refresh count;
- `anthropic_stable_canary_http_error` and
  `anthropic_stable_canary_stream_error` events;
- end-to-end semantic TTFT, response duration, and client disconnects;
- input, output, cache-read, and cache-creation tokens;
- truncated streams, duplicate/missing terminal events, and usage records
  skipped because no billable evidence was observed;
- absence of companion traffic and generic scheduler selections for the
  reserved account.

D1 TTFT begins when the Messages handler receives the request. It includes body
read, security audit, database validation, billing eligibility, user/API-key
queue time, account lease wait, upstream connection, and time to the first
semantic text/thinking/tool output. It does not include client-side DNS/TLS to
Sub2API, terminal rendering buffers, or time before the request reaches the
handler.

Streaming bytes are copied unchanged and parsed only in a side observer. A
downstream write failure is classified as a client disconnect, not an upstream
SLA failure. Failed or truncated requests are billed only when semantic output
or positive upstream usage provides evidence.

## Future Multi-User Expansion

D1 intentionally has one owner and one API key. Do not broaden its ID fields to
simulate shared scheduling.

A later multi-user coordinator may share one account credential and one stable
device identity while preserving independent request ownership:

```text
fixed account device
  user A / session A1
  user A / session A2
  user B / session B1
  user C / session C1
```

The users do not share conversation state. Every request keeps its own incoming
session UUID, body, API-key ownership, concurrency admission, and billing row.
The shared device identifies the account installation only. The later design
must add durable session ownership, credential-generation fencing, fair
admission, cross-process serialization, and versioned capture profiles without
weakening D1's fail-closed contract.
