-- Image tasks reuse the artifact pipeline, so both kind whitelists must accept
-- them. Existing rows are unaffected: the new value only widens the constraint.
ALTER TABLE web_agent_tasks DROP CONSTRAINT IF EXISTS web_agent_tasks_kind_check;
ALTER TABLE web_agent_tasks ADD CONSTRAINT web_agent_tasks_kind_check
    CHECK (kind IN ('slides','spreadsheet','document','image'));

ALTER TABLE web_agent_artifacts DROP CONSTRAINT IF EXISTS web_agent_artifacts_kind_check;
ALTER TABLE web_agent_artifacts ADD CONSTRAINT web_agent_artifacts_kind_check
    CHECK (kind IN ('slides','document','spreadsheet','image'));

-- An image is its own preview, so it stores no second blob and leaves the key
-- empty. The plain UNIQUE constraint would then reject the second image, since
-- it compares empty strings as equal. Swap it for a partial unique index that
-- only covers real keys, keeping the column NOT NULL so Go still scans a string.
ALTER TABLE web_agent_artifacts DROP CONSTRAINT IF EXISTS web_agent_artifacts_preview_key_key;
CREATE UNIQUE INDEX IF NOT EXISTS web_agent_artifacts_preview_key_uniq
    ON web_agent_artifacts(preview_key) WHERE preview_key <> '';

ALTER TABLE web_agent_blob_stages DROP CONSTRAINT IF EXISTS web_agent_blob_stages_preview_key_key;
CREATE UNIQUE INDEX IF NOT EXISTS web_agent_blob_stages_preview_key_uniq
    ON web_agent_blob_stages(preview_key) WHERE preview_key <> '';

-- Office artifacts must still pair a file with a preview; only images may omit it.
ALTER TABLE web_agent_artifacts DROP CONSTRAINT IF EXISTS web_agent_artifacts_preview_required;
ALTER TABLE web_agent_artifacts ADD CONSTRAINT web_agent_artifacts_preview_required
    CHECK ((kind = 'image') = (preview_key = ''));
