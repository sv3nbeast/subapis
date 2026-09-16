-- An image artifact is its own preview, so it reserves no preview blob and
-- carries preview_bytes = 0. Migration 240 relaxed preview_key for that shape
-- but left preview_bytes > 0 in place, which rejected every generated image at
-- publish time after the model had already produced (and billed) it.
-- Office artifacts keep the original guarantee: a rendered PDF must be present.
ALTER TABLE web_agent_artifacts DROP CONSTRAINT IF EXISTS web_agent_artifacts_preview_bytes_check;
ALTER TABLE web_agent_artifacts ADD CONSTRAINT web_agent_artifacts_preview_bytes_check
    CHECK (
        CASE WHEN kind = 'image'
            THEN preview_bytes = 0
            ELSE preview_bytes > 0 AND preview_bytes <= 33554432
        END
    );
