# Native remote compaction liveness fix

Base: `0b2a61d92` (production at diagnosis). Branch:
`codex/fix-native-compaction-liveness-20260907`.
No production settings, credentials, accounts, pricing, or deployments changed.
No captured user requests were replayed. Tests use a synthetic in-memory WS upstream.

## Observed failure

Request `0ecd463e-b6d5-4cf7-9852-8a6392a3c50f`, client
`df21b0f6-9861-437b-bfe1-ed73a47ab29b`, 2026-09-07 15:28:57–15:29:56 +08:
user 565 / key 996 / group 5, OpenAI OAuth account 2582 / proxy 64,
gpt-6-astra, HTTP /responses with native remote-compaction v2.
Upstream WS delivered five events, including a final observed
response.output_item.added; all five were buffered and zero bytes reached the
HTTP client. Downstream canceled around 59.2 seconds. Native compaction was not
eligible for the legacy-only heartbeat helper.

The exact downstream 60-second timer remains unobserved; the owned API Nginx
read/send timeout is 1800 seconds. This patch repairs the proven gateway silence,
not an assumed model limit, account quota, or client-specific timeout setting.

## Repair

- Scope is native v2 streaming HTTP→WS forwarding, selected by the existing
  request marker. No model-name list, altered timeout defaults or extra retries.
- Forward upstream progress and compaction item events immediately with flush,
  even when no text tokens exist. Ordinary non-compaction buffering is unchanged.
- During idle reads, emit configured SSE heartbeat comments from the same request
  goroutine that writes data events. The read goroutine never touches Gin/writers.
- The reader is demand-driven, never reads past the terminal event, is canceled
  and joined before pool-lease release, and does not reset upstream read timeout
  on heartbeats. Healthy completed connections remain reusable.
- Keep transport progress distinct from text-token/TTFT and token accounting.
  Heartbeat bytes use the existing non-semantic byte accounting.
- Do not replay an ambiguously submitted or interrupted native compaction. Emit
  one protocol-correct failure with the existing response ID when connected;
  preserve actual provider error classification, including rate limits.
- Require a real encrypted compaction item before successful completion. If the
  provider puts the complete item only in added or terminal output, deliver its
  item-done event once. Partial/missing items are not converted into fake success.
- Preserve reported usage across finalized native failures and submit it once at
  the handler. Do not fabricate usage when none was reported.
- Client cancellation/write failure after an HTTP 200 commit remains a failed
  compact outcome. A dead downstream is not an upstream-account fault; legitimate
  terminal usage is retained when draining completes.
- The legacy compact marker/JSON-to-SSE bridge is deliberately not reused for
  native v2. Actual HTTP-SSE and legacy paths keep their existing behavior.

## Validation

The new real-entrypoint regression `TestNativeCompactionWSAllModelsAndStableCache/gpt-6-astra`
was run unchanged on base 0b2a61d92 and failed with "missing client chunk
response.created"; the same test passes with this patch. The original five-event
diagnostic remains available in the separate audit worktree.

Focused tests exercise the real `OpenAIGatewayService.Forward` → WS path:

- heartbeat before the first event and between subsequent progress/item events;
- progress/item delivery before terminal; one terminal; no read-ahead or reader leak;
- Astra, Sol and a synthetic future model; unchanged request/cached-token semantics;
- EOF before any event or after progress, malformed JSON, bare error, rate limit,
  missing/partial compaction, provider read timeout, client cancellation;
- ambiguous request submission (one upstream write, no hidden replay);
- terminal-only and added-only compaction item variants;
- provider failure/incomplete with retained usage; client write failure;
- ordinary non-compaction buffering/TTFT control and native compact outcome logs.

Focused service/handler tests and their race-detector runs pass (latest race run:
service 8.781s, handler 1.591s). Final `go test ./... -count=1` passes, including
the full service package (174.655s), handler and all other packages.
`go build ./...` and `git diff --check` pass. The unchanged baseline test fails
as expected; that failure is the red control, not a remaining candidate failure.

## Gateway regression review

Reviewed range: 0b2a61d92 plus task-owned changes. No attributable P0/P1 after
repairing terminal-ID, usage-preservation and committed-200 failure classification.
Stream integrity: PASS in local progress/terminal/error/cancel and race tests.
Cache continuity: PASS for untouched request identity and exact reported tokens.
Creation duplication: PASS for no hidden replay after submission, no emulated-cache
state changes, and reported-only usage. Latency: PASS for local first-write tests,
immediate native progress, no response buffering, and no new ordinary-request I/O.

Deployment and real post-release client verification remain pending explicit
release authorization. External fixed total deadlines and provider overload are
not claimed to be fixed by transport progress/heartbeat handling.
