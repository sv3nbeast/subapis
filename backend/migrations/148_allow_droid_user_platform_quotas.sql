-- Allow Droid to participate in per-user platform quota settings.
-- The admin UI submits all quota platforms, so the database constraint must
-- stay aligned with service.AllowedQuotaPlatforms.
ALTER TABLE user_platform_quotas
  DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
  ADD CONSTRAINT user_platform_quotas_platform_check
  CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'droid'));
