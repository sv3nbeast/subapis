-- 收敛 user_platform_quotas.platform CHECK 为本地 + 官方平台全集。
--
-- 背景：官方 237/238（minimax/opencode_go）与本地 239（cursor）按文件名排序交错执行：
--   全新库：234 → 237 → 238 → 239，239 重建的约束不含 minimax/opencode_go；
--   生产库：239 已记账，随后才执行 237/238（已改为超集，见各文件注释）。
-- 两种路径都需要最后再收敛一次。DROP IF EXISTS + 重建，幂等；新约束是所有前序约束的超集，
-- 存量行瞬时校验通过。平台列表与 service.AllowedQuotaPlatforms 保持一致。
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'droid', 'grok',
                        'cursor', 'kimi', 'zhipu', 'deepseek', 'minimax', 'opencode_go'));
