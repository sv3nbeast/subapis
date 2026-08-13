-- Kiro-first / Claude-fallback routing for Anthropic subscription groups.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS kiro_anthropic_fallback_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS kiro_anthropic_fallback_first_semantic_timeout_seconds INTEGER NOT NULL DEFAULT 90,
    ADD COLUMN IF NOT EXISTS kiro_anthropic_fallback_max_anthropic_attempts INTEGER NOT NULL DEFAULT 2;

UPDATE groups
SET kiro_anthropic_fallback_first_semantic_timeout_seconds = LEAST(110, GREATEST(5, kiro_anthropic_fallback_first_semantic_timeout_seconds)),
    kiro_anthropic_fallback_max_anthropic_attempts = LEAST(3, GREATEST(1, kiro_anthropic_fallback_max_anthropic_attempts));

ALTER TABLE groups
    ADD CONSTRAINT groups_kiro_anthropic_fallback_timeout_check
        CHECK (kiro_anthropic_fallback_first_semantic_timeout_seconds BETWEEN 5 AND 110),
    ADD CONSTRAINT groups_kiro_anthropic_fallback_attempts_check
        CHECK (kiro_anthropic_fallback_max_anthropic_attempts BETWEEN 1 AND 3);

COMMENT ON COLUMN groups.kiro_anthropic_fallback_enabled IS 'Kiro-first Claude fallback for Anthropic subscription groups';
COMMENT ON COLUMN groups.kiro_anthropic_fallback_first_semantic_timeout_seconds IS 'Kiro first semantic output timeout before Claude fallback';
COMMENT ON COLUMN groups.kiro_anthropic_fallback_max_anthropic_attempts IS 'Maximum Claude accounts attempted during fallback';
