-- Allow selected groups to accept synchronous /v1/messages requests again.
--
-- When enabled, the gateway still forces stream=true upstream and aggregates
-- the SSE response back into a non-stream JSON response for the client. The
-- flag is group-scoped so rollout can be limited to specific Anthropic-style
-- groups instead of restoring the old global behavior for everyone at once.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS allow_non_stream_messages BOOLEAN NOT NULL DEFAULT FALSE;
