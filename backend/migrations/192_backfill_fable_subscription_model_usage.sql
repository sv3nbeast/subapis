-- The initial per-model subscription quota release persisted the group rule but
-- omitted it from API-key auth snapshots. Requests were billed against the
-- subscription total without populating user_subscriptions.model_usage.
-- Rebuild the current Fable windows from durable usage logs before the fixed
-- gateway starts enforcing the configured ratio.
WITH policy_rollout AS (
    SELECT COALESCE(
        (
            SELECT applied_at
            FROM schema_migrations
            WHERE filename = '178_subscription_model_quota_ratios.sql'
        ),
        NOW()
    ) AS started_at
),
target_subscriptions AS (
    SELECT
        us.id,
        us.starts_at,
        us.daily_window_start,
        us.weekly_window_start,
        us.monthly_window_start,
        GREATEST(
            us.starts_at,
            COALESCE(us.quota_cycle_start_at, us.starts_at),
            policy_rollout.started_at
        ) AS accounting_start
    FROM user_subscriptions us
    JOIN groups g ON g.id = us.group_id
    CROSS JOIN policy_rollout
    WHERE us.deleted_at IS NULL
      AND us.status = 'active'
      AND us.expires_at > NOW()
      AND (us.quota_cycle_end_at IS NULL OR us.quota_cycle_end_at > NOW())
      AND g.deleted_at IS NULL
      AND g.status = 'active'
      AND g.subscription_type = 'subscription'
      AND g.model_quota_ratios ? 'claude-fable-5'
),
normalized_usage AS (
    SELECT
        ul.subscription_id,
        ul.actual_cost,
        ul.created_at,
        regexp_replace(
            regexp_replace(
                lower(trim(replace(COALESCE(NULLIF(ul.requested_model, ''), ul.model), '_', '-'))),
                '^.*(/publishers/(google|anthropic)/models/|/models/)',
                ''
            ),
            '^(publishers/(google|anthropic)/models/|models/|anthropic\.)',
            ''
        ) AS normalized_model
    FROM usage_logs ul
    JOIN target_subscriptions target ON target.id = ul.subscription_id
    WHERE ul.created_at >= target.accounting_start
),
fable_usage AS (
    SELECT
        target.id,
        COALESCE(SUM(usage.actual_cost) FILTER (
            WHERE target.daily_window_start IS NOT NULL
              AND target.daily_window_start > NOW() - INTERVAL '24 hours'
              AND usage.created_at >= GREATEST(target.accounting_start, target.daily_window_start)
        ), 0) AS daily_usage_usd,
        COALESCE(SUM(usage.actual_cost) FILTER (
            WHERE target.weekly_window_start IS NOT NULL
              AND target.weekly_window_start > NOW() - INTERVAL '7 days'
              AND usage.created_at >= GREATEST(target.accounting_start, target.weekly_window_start)
        ), 0) AS weekly_usage_usd,
        COALESCE(SUM(usage.actual_cost) FILTER (
            WHERE target.monthly_window_start IS NOT NULL
              AND target.monthly_window_start > NOW() - INTERVAL '30 days'
              AND usage.created_at >= GREATEST(target.accounting_start, target.monthly_window_start)
        ), 0) AS monthly_usage_usd
    FROM target_subscriptions target
    LEFT JOIN normalized_usage usage
        ON usage.subscription_id = target.id
       AND (
            usage.normalized_model = 'claude-fable-5'
            OR usage.normalized_model LIKE 'claude-fable-5-%'
            OR usage.normalized_model LIKE 'claude-fable-5[%'
            OR usage.normalized_model LIKE 'claude-fable-5:%'
       )
    GROUP BY
        target.id,
        target.accounting_start,
        target.daily_window_start,
        target.weekly_window_start,
        target.monthly_window_start
)
UPDATE user_subscriptions subscription
SET
    model_usage = jsonb_set(
        COALESCE(subscription.model_usage, '{}'::jsonb),
        ARRAY['claude-fable-5'],
        jsonb_build_object(
            'daily_usage_usd', GREATEST(
                COALESCE((subscription.model_usage->'claude-fable-5'->>'daily_usage_usd')::numeric, 0),
                usage.daily_usage_usd
            ),
            'weekly_usage_usd', GREATEST(
                COALESCE((subscription.model_usage->'claude-fable-5'->>'weekly_usage_usd')::numeric, 0),
                usage.weekly_usage_usd
            ),
            'monthly_usage_usd', GREATEST(
                COALESCE((subscription.model_usage->'claude-fable-5'->>'monthly_usage_usd')::numeric, 0),
                usage.monthly_usage_usd
            )
        ),
        true
    ),
    updated_at = NOW()
FROM fable_usage usage
WHERE subscription.id = usage.id
  AND (
      usage.daily_usage_usd > COALESCE(
          (subscription.model_usage->'claude-fable-5'->>'daily_usage_usd')::numeric,
          0
      )
      OR usage.weekly_usage_usd > COALESCE(
          (subscription.model_usage->'claude-fable-5'->>'weekly_usage_usd')::numeric,
          0
      )
      OR usage.monthly_usage_usd > COALESCE(
          (subscription.model_usage->'claude-fable-5'->>'monthly_usage_usd')::numeric,
          0
      )
  );
