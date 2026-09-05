# GPT-6 Astra support — 2026-09-05

Implementation base: `9d9508779`, isolated branch `codex/add-gpt-6-astra-20260905`.
No production settings, database rows, or deployment were changed. The only
upstream writes were bounded short generation probes; probe tools were never executed.

## Verified contract

Official sources retrieved 2026-09-05:

- [Model card](https://developers.openai.com/api/docs/models/gpt-6-astra): `gpt-6-astra`;
  total context 1,050,000, maximum input 922,000, maximum output 128,000;
  text/image input, text output, structured output and function tools.
- [Release](https://openai.com/index/gpt-6-astra/): released 2026-09-03, staged rollout.
- [Migration guide](https://developers.openai.com/api/docs/guides/latest-model): efforts
  low/medium/high/xhigh/max; no none/minimal. Function tools require Responses,
  even when the client uses Chat Completions. Sampling/logprob parameters are unsupported.
  Native Responses configuration-update items and async tool fields remain untouched.
- [Pricing](https://developers.openai.com/api/docs/pricing): USD per million tokens:
  input 10, output 50, cache read 1, cache write 12.5. Above 272,000 input tokens,
  all input/cache prices double and output is multiplied by 1.5. Fast is 2x;
  Flex/Batch are 0.5x. No separate 5-minute/1-hour cache-write rates are advertised
  for this model; do not invent Anthropic TTL prices.

Only the official base ID is exposed in discovery. Local effort aliases are
`gpt-6-astra-{low,medium,high,xhigh,max}` and normalize to the base on the wire.
No dated, bare `gpt-6`, pro, ultra, Bedrock, or Azure aliases were invented.
Existing defaults and older models remain unchanged.

## Live capability evidence

Provider: OpenAI ChatGPT OAuth; account 2613; proxy 60;
endpoint `https://chatgpt.com/backend-api/codex/responses`.

| Probe | HTTP | Completion | Input/output tokens | Duration |
| --- | --- | --- | --- | --- |
| low short text | 200 | one response.completed | 29 / 5 | 2.652 s |
| medium function call | 200 | one response.completed | 76 / 19 | 2.836 s |
| max short text | 200 | one response.completed | 29 / 5 | 2.425 s |
| high function call | 200 | function_call item + one response.completed | 76 / 19 | 3.603 s |
| xhigh short text | 200 | message item + one response.completed | 29 / 5 | 2.150 s |

High tool response ID: `resp_0fc01abb4615434f016a9b8c1e92f087d1a1c804dbdc22f244`;
tool name `probe_echo`. Xhigh response ID:
`resp_060ff6980d65bfe4016a9b8c22860c87d1b233d9796761cba6`.
Terminal envelopes omitted output items, which were present in output_item.done;
the three protocol adapters' tests explicitly cover that observed shape.
These tiny probes establish schema acceptance, not a quality or throughput benchmark.
TTFT was not separately instrumented in the standalone live probe.

Important account-type difference: adding `prompt_cache_options` to ChatGPT OAuth
produced HTTP 400 with `Unsupported parameter: prompt_cache_options`.
The same probes without it succeeded. OAuth strips these unsupported cache options;
public API-key request normalization preserves native options and converts the legacy
retention field to a supported 30m option. Production currently has no active OpenAI
API-key account, so that path has local tests, not a live capability claim.

Kiro account 2612 / proxy 59 / AmazonQ generateAssistantResponse returned HTTP 400
`INVALID_MODEL_ID` for Astra (0.548 s). The identical probe shape with gpt-5.6-sol
returned 200 with assistant/contextUsage/metering frames (1.499 s).
Therefore Kiro model lists/mappings and Kiro-only OpenAI groups are not expanded.

## Persisted configuration and pricing

Migration `235_add_gpt6_astra_support.sql` is forward-only and idempotent.
It extends direct OAuth accounts already mapping Sol to itself, excluding deleted
rows, credential shadows, wildcard mappings and explicit Astra overrides.
Group lists preserve their enabled flag, order and deduplication, and require a
direct OpenAI OAuth account membership. Kiro-only groups are excluded.

Each eligible billing channel gets one enabled dedicated Astra row containing all
six billable aliases, with independent standard and >272K intervals. Dedicated
custom rows retain their prices, mode and intervals. Mixed/duplicate rows lose only
Astra aliases; empty rows remain stored but disabled.

Account-statistics pricing is handled separately: only scopes already explicitly
containing Astra are normalized; Sol cost rows are not expanded or copied from
customer billing. Read-only production audit found no OpenAI account-statistics
price rows. Both interval tables were inspected. Astra's 5m/1h breakdown remains
unset on new rows; existing custom interval values remain untouched.

## Surface audit disposition

The bundled model-surface audit found 114 Sol-reference files and zero Astra files
at baseline. Applicable production surfaces were discovery, alias/mapping helpers,
Codex catalog effort/vision/context metadata, HTTP/WS request compatibility,
pricing lookup/fallback/catalog, persisted allowlists, channel rules, admin response
serialization, model selectors and generated client configurations.

Kiro translator/runtime/model-reference hits are intentionally unchanged after the
negative live capability result. Existing test fixtures that merely use Sol as an
example are retained as regression controls, not mechanically renamed. Historic
SQL migrations, sample deployment defaults, unrelated usage/UI fixtures and
default client model choices are unchanged. Generic routing, count-token estimation,
response parsing and usage/cache parsing are reused; no timers, retry policies,
scheduling, streaming lifecycle, or billing-write paths were changed.

## Validation and release gate

- Astra model, billing, HTTP request builders and all three protocol forwarders
  (streaming/non-streaming) have focused tests. The terminal-output-empty fixture
  preserves text/tool items and asserts one client terminal.
- PostgreSQL 18 container migration test passes, including mixed-row splitting,
  duplicate disabling, wildcard/custom preservation, Kiro exclusion, independent
  account-statistics rules and both custom interval tables, and double-run equality.
- Admin channel-response serializer test asserts the dedicated row increases the
  rule count and contains all aliases, enabled state and cache-write rate.
- New frontend model-list and actual generated OpenCode-config tests pass.
- Typecheck and production frontend build pass (existing large-chunk warnings).
- Frontend full suite: 54 failing tests and one failed suite across 10 files are
  identical to clean base 9d9508779; no new failure names. Base 1879 tests pass,
  candidate 1881 pass. Existing missing saveAsMock, GroupSelectorStub/translate and
  stale Fable/platform/payment expectations were not repaired in this model patch.
- A valid 600KB native Astra request remains byte-identical: benchmark ~1.29ms,
  40 bytes / 3 allocations on Apple M4 Pro. No response buffering or added I/O.

- Final `go test ./... -count=1` and `go build ./...` both pass. Focused gateway
  review tests cover Astra and existing terminal/EOF, client-cancellation,
  prompt-prefix/cache-key and first-write regressions; both service/admin packages pass.
- Additional `-tags=unit` run is blocked by identical baseline compile errors
  (duplicate accountBelongsToGroup and stale account-statistics/settings test APIs).
  The normal full suite and all newly added tests compile and pass without that tag.
- `$sub2api-gateway-regression-review`: PASS for the task-owned diff from 9d9508779.
  Stream completion: PASS (three forwarders, empty terminal output and existing
  incomplete/cancel controls). Cache hits: PASS (native options/account-specific
  rejection handling, stable key and replay preservation). Creation deduplication:
  PASS (no state/accounting changes; normalization is idempotent). Latency: PASS
  (no response buffering/network/locks; unchanged 600KB native request ~1.29ms).
  Residual limits: public API-key/Azure/Bedrock paths have no live credential proof;
  Kiro is explicitly unsupported. Async/steering fields are preserved on native
  Responses, but full advanced multi-agent/steering workflows were not exercised.

Production release and post-release settled-usage reconciliation remain a separate,
explicitly authorized step; this document does not claim deployment.
