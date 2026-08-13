# Anthropic Stable Canary Shared D2

Status: implemented behind an independent default-off switch. This does not
authorize production traffic or replace the D1 observation gate.

The durable session tables are added by migration `223_anthropic_stable_canary_sessions.sql`.

## Runtime Contract

- One exact Anthropic OAuth account and one isolated Claude-Code-only group.
- Multiple explicitly allow-listed Sub2API API keys may share that account.
- The account remains concurrency one across processes for the full response
  stream and the one permitted reactive 401 refresh/replay.
- Each Claude Code `session_id` is durably bound to the first authenticated
  Sub2API user that presents it. A different user presenting the same UUID is
  rejected before Anthropic egress.
- The account-level fixed `device_id` is the only body change. Replacement is
  equal length; all other JSON bytes, field order, tools, thinking, cache data,
  metadata, and the user/session-specific UUID remain unchanged.
- The raw session UUID, request body, device identifier, OAuth credentials, and
  HMAC key are never stored in the binding tables or emitted by lifecycle output.
- There is no cross-account failover, companion traffic, generic retry, model
  mapping, proxy, or generic account refresh path.

## Configuration

Shared D2 is independent from D1. `enabled=false` remains the deployment
default. In shared mode the legacy single-owner IDs must be zero.

```text
GATEWAY_ANTHROPIC_STABLE_CANARY_ENABLED=false
GATEWAY_ANTHROPIC_STABLE_CANARY_GROUP_ID=<dedicated group id>
GATEWAY_ANTHROPIC_STABLE_CANARY_ACCOUNT_ID=<dedicated account id>
GATEWAY_ANTHROPIC_STABLE_CANARY_OWNER_USER_ID=0
GATEWAY_ANTHROPIC_STABLE_CANARY_API_KEY_ID=0
GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_USERS=true
GATEWAY_ANTHROPIC_STABLE_CANARY_SHARED_API_KEY_IDS=<dedicated key ids>
GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_GENERATION=1
GATEWAY_ANTHROPIC_STABLE_CANARY_SESSION_HMAC_KEY=<dedicated random secret>
```

The HMAC key must contain at least 32 high-entropy characters and remain stable
for the lifetime of the group. The database locks its one-way fingerprint and
the allow-list fingerprint on first use; silent key, account, or policy drift
within a generation fails closed. Do not reuse `JWT_SECRET` or an OAuth
credential.

`session_generation` is the maintenance boundary. Adding/removing keys,
changing the account, or rotating the HMAC key requires the runtime to be
disabled first and the generation to be incremented. Existing rows are retained
as tombstones and can never be silently reinterpreted by the new generation.
Never reuse an older generation number, even when restoring a previous account
or secret; treat generation numbers as append-only deployment epochs.

## Enrollment And Rollback

1. Create one new exclusive Anthropic group with ClaudeCodeOnly and OAuthOnly
   enabled, no fallback/model routing, one disposable OAuth account, and only
   the dedicated unused API keys.
2. Keep runtime `enabled=false` and run lifecycle `inspect`, then `enable` as a
   dry-run and finally with `--anthropic-stable-canary-execute`.
3. Start the runtime with the exact frozen configuration. Begin with one key and
   one user; add users only after the current runtime is disabled, requests have
   drained, and `session_generation` is incremented.
4. To stop traffic, set runtime `enabled=false` and restart. This does not erase
   session ownership.
5. The lifecycle `disable` action is an emergency exit and does not require the
   HMAC key or allow-list to remain available. It clears account reservation
   markers but intentionally retains opaque session tombstones.

Never enable D2 with an existing production-shared account or key. D1/D2/D3
observation remains sequential: 20 sessions / 2 hours, then 24 hours, then
48-72 hours for A/B/C shared traffic.
