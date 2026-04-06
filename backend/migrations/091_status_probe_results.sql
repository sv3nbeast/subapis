-- 091_status_probe_results.sql
-- Service status probe results for uptime monitoring

CREATE TABLE IF NOT EXISTS status_probe_results (
    id         BIGSERIAL PRIMARY KEY,
    model      VARCHAR(128) NOT NULL,
    status     VARCHAR(16)  NOT NULL,
    latency_ms INTEGER      NOT NULL DEFAULT 0,
    error_message TEXT,
    created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_status_probe_results_model_created
    ON status_probe_results (model, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_status_probe_results_created
    ON status_probe_results (created_at);
