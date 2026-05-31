-- Add separate 5m/1h cache creation prices for channels.
-- cache_write_price remains the legacy/default cache creation price.

ALTER TABLE channel_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_5m_price NUMERIC(20,12),
    ADD COLUMN IF NOT EXISTS cache_write_1h_price NUMERIC(20,12);

ALTER TABLE channel_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_5m_price NUMERIC(20,12),
    ADD COLUMN IF NOT EXISTS cache_write_1h_price NUMERIC(20,12);

ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_5m_price NUMERIC(20,12),
    ADD COLUMN IF NOT EXISTS cache_write_1h_price NUMERIC(20,12);

ALTER TABLE channel_account_stats_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_5m_price NUMERIC(20,12),
    ADD COLUMN IF NOT EXISTS cache_write_1h_price NUMERIC(20,12);

COMMENT ON COLUMN channel_model_pricing.cache_write_5m_price IS '5分钟缓存写入每 token 价格，NULL 表示使用 cache_write_price 或默认';
COMMENT ON COLUMN channel_model_pricing.cache_write_1h_price IS '1小时缓存写入每 token 价格，NULL 表示使用 cache_write_price 或默认';
COMMENT ON COLUMN channel_pricing_intervals.cache_write_5m_price IS 'token 模式：5分钟缓存写入价';
COMMENT ON COLUMN channel_pricing_intervals.cache_write_1h_price IS 'token 模式：1小时缓存写入价';
COMMENT ON COLUMN channel_account_stats_model_pricing.cache_write_5m_price IS '账号统计用：5分钟缓存写入每 token 价格';
COMMENT ON COLUMN channel_account_stats_model_pricing.cache_write_1h_price IS '账号统计用：1小时缓存写入每 token 价格';
COMMENT ON COLUMN channel_account_stats_pricing_intervals.cache_write_5m_price IS '账号统计用：5分钟缓存写入区间价';
COMMENT ON COLUMN channel_account_stats_pricing_intervals.cache_write_1h_price IS '账号统计用：1小时缓存写入区间价';
