# Kiro Cache Shadow Validation

The cache shadow is observational. It never changes `usage_logs`, account
selection, cache-emulation billing, group multipliers, or user balance.

## Data

- Request samples: Redis stream `kiro_cache_shadow_samples:v1`
- Aggregates: `kiro_cache_shadow_metrics:v1:<date>:group_<id>:<model>:<ua>:<context_bucket>`
- Retention: 7 days
- Stream cap: approximately 200,000 samples
- Prompt and response content are not stored

Each successful Anthropic OAuth/SetupToken request records:

- actual Anthropic Input, Cache Read, Cache Creation, 5m and 1h usage;
- current algorithm at ratio `0.9`;
- current algorithm at ratio `1.0`;
- protocol V2 using explicit breakpoints and a 20-block lookback;
- input-side cost at model list price;
- model, account, group, UA form, context bucket and request ID.

Set `SUB2API_KIRO_CACHE_SHADOW_ENABLED=false` to disable collection. The default
is enabled when the configured `GatewayCache` supports the shadow store.

## Report

Run directly on a host with `redis-cli`:

```bash
scripts/kiro-cache-shadow-report.sh
```

Run against the production Redis container:

```bash
REDIS_CLI_BIN=docker \
REDIS_CLI_ARGS='exec sub2api-redis redis-cli' \
scripts/kiro-cache-shadow-report.sh
```

Optional first argument filters metric keys:

```bash
scripts/kiro-cache-shadow-report.sh 'kiro_cache_shadow_metrics:v1:*:group_19:claude-opus-4-8:agent-sdk:180k_220k'
```

Do not evaluate a cohort until its warm request count is representative. After
a process restart, exclude the first cache TTL window.

## Acceptance

Protocol V2 can enter billing gray release only when all important model, UA and
context cohorts meet these thresholds for 24-48 hours:

- combined bucket absolute error below 5% of actual input context;
- aggregate bucket-share difference below 1 percentage point;
- input-side cost absolute error below 5%;
- cache-hit-rate difference below 3 percentage points;
- cold start, growing context, over-20-block growth and TTL expiry all pass.

Changing `kiro_cache_emulation_ratio` is not part of shadow validation. Token
classification and commercial discount must remain separate; discount continues
to use the group `rate_multiplier`.
