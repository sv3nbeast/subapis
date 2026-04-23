-- Remove the temporary /gemini-antigravity/v1 compatibility experiment.
--
-- This migration is intentionally separate from the already-applied 125-127
-- migrations so production checksum validation remains immutable.

UPDATE accounts
SET credentials = jsonb_set(
    credentials,
    '{model_mapping}',
    (credentials->'model_mapping') - 'claude-mythos-0417',
    true
)
WHERE platform = 'antigravity'
  AND deleted_at IS NULL
  AND credentials ? 'model_mapping'
  AND credentials->'model_mapping' ? 'claude-mythos-0417';

ALTER TABLE groups
DROP COLUMN IF EXISTS gemini_antigravity_compat_enabled;
