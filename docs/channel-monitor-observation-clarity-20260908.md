# V1 monitor observation clarity

Scope: improve monitor evidence, persistence and recovery checks. No gateway,
account routing, model translation, request timeout, challenge threshold or
production configuration changes.

## Behavior

- Preserve all raw statuses and historical availability counts.
- Use one latest snapshot for the user-facing status, latency and check time.
- Return public-safe check time, configured interval/jitter, mode and protocol
  path; do not expose endpoint hosts, credentials, account IDs or raw errors.
- Show last-probe success, slow success and failure explicitly. Expire both green
  and failed observations after configured interval + maximum jitter + 120s
  scheduling/request/persistence grace; unknown is not healthy or unavailable.
- Page refresh remains read-only and does not invoke a paid probe.
- Following a failed scheduled probe, permit one extra recovery check after 60s
  only if earlier than the existing schedule. Preserve history for both checks.
  A persistent failure streak does not receive more expedited checks; an
  observed recovery resets this in-memory gate. Restart/edit recreates the gate.
  Short-interval probes are not delayed; slow success creates no extra traffic.
- A history write error is returned to the caller and does not update the
  monitor check time. Mark-checked errors are also returned instead of silently
  claiming that a refresh completed.

## Regression coverage

Backend tests cover mixed-snapshot prevention, protocol path metadata, DTO
privacy, recovery limits/reset, worker completion/in-flight release, and history
failure at both persistence helper and actual RunCheck entrypoint.
Frontend tests cover freshness boundaries, null/invalid timestamps, backward
compatibility, expired successes and errors, no-data/slow overall status,
automatic expiry without an API refresh, quota modes and shared card rendering.

This is not a fix for the previously observed upstream 403 or missing terminal
502 responses. Those remain real failures and remain visible. Historical
incidents are not claimed to have been caused by the potential snapshot race
or silent persistence errors; these are source-proven defects addressed here.

Deployment requires a separate production-release request.

Validation: full service/handler/admin package tests pass (final service rerun
175.021s); tagged unit/race monitor and runner tests pass. All 16 focused frontend
tests, typecheck, touched-component lint, production build and diff checks pass.
Build still reports existing chunk-size/mixed-import and outdated Browserslist
warnings. No dependency manifest/lock changes were made.
