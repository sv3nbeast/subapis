-- Keep user_platform_quotas.platform CHECK aligned with service.AllowedQuotaPlatforms.
--
-- Historical sequence:
--   142 created the constraint with anthropic/openai/gemini/antigravity.
--   147 added kiro.
--   148 added droid.
--   157 added grok but accidentally replaced the constraint without kiro/droid.
--
-- Result in production: platform='kiro' usage/quota writes can fail with
-- user_platform_quotas_platform_check. Recreate the constraint as the full
-- current platform set. This is an additive change for existing rows and is
-- safe to re-run.
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'droid', 'grok'));
