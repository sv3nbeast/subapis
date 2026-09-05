-- User requested the single official model name, gpt-6-astra. Remove only the
-- five built-in effort aliases introduced by migration 235. Reasoning remains
-- a separate parameter. Keep price IDs, custom amounts, tiers and history intact.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

DO $astra$
DECLARE
    price_table TEXT;
    retired TEXT[] := ARRAY[
        'gpt-6-astra-low', 'gpt-6-astra-medium', 'gpt-6-astra-high',
        'gpt-6-astra-xhigh', 'gpt-6-astra-max'
    ];
BEGIN
    -- These are independent pricing surfaces; do not copy prices between them.
    FOREACH price_table IN ARRAY ARRAY[
        'channel_model_pricing', 'channel_account_stats_model_pricing'
    ] LOOP
        EXECUTE format($sql$
            WITH cleaned AS (
                SELECT p.id, COALESCE((
                    SELECT jsonb_agg(m.model ORDER BY m.position)
                    FROM jsonb_array_elements(p.models) WITH ORDINALITY AS m(model, position)
                    WHERE COALESCE(m.model #>> '{}', '') <> ALL($1)
                ), '[]'::jsonb) AS models
                FROM %I p
                WHERE p.platform = 'openai'
                  AND jsonb_typeof(p.models) = 'array'
                  AND p.models ?| $1
            )
            UPDATE %I p
            SET models = c.models,
                enabled = p.enabled AND jsonb_array_length(c.models) > 0,
                updated_at = NOW()
            FROM cleaned c
            WHERE p.id = c.id
        $sql$, price_table, price_table) USING retired;
    END LOOP;
END $astra$;
