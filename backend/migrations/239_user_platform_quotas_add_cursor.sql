-- 允许为 cursor 平台设置 user × platform 配额。
-- 平台列表与 service.AllowedQuotaPlatforms 保持一致。
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'droid', 'grok',
                        'cursor', 'kimi', 'zhipu', 'deepseek'));
