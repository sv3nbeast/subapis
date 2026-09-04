-- Repair deployments that may already have recorded the original CN-provider
-- migration while its CHECK constraint omitted the existing kiro/droid values.
-- Keep this list aligned with service.AllowedQuotaPlatforms.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'droid', 'grok',
                        'kimi', 'zhipu', 'deepseek'));
