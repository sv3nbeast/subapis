package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const allUserPlatformQuotasCheck = "CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'kiro', 'droid', 'grok', 'kimi', 'zhipu', 'deepseek'))"

func requireAllUserPlatformQuotaPlatforms(t *testing.T, filename string) {
	t.Helper()

	content, err := FS.ReadFile(filename)
	require.NoError(t, err)

	sql := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, sql, "DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check")
	require.Contains(t, sql, allUserPlatformQuotasCheck)
}

// TestUserPlatformQuotasCNProvidersMigration 校验 224 号迁移把 kimi/zhipu/deepseek
// 加入 user_platform_quotas.platform 的 CHECK 约束，同时保留本地既有 kiro/droid。
// 约束未完整放宽时，注册预填充 10 平台默认配额会整条 INSERT 中止 → 新用户零配额行
// （缺失配额行 = 无限额），管理端设置国产平台配额直接 500。
func TestUserPlatformQuotasCNProvidersMigration(t *testing.T) {
	requireAllUserPlatformQuotaPlatforms(t, "224_user_platform_quotas_add_cn_providers.sql")
}

// 已经成功记录旧 224 的环境不会重新运行被修正的文件，因此 234 必须再次收敛
// 完整平台集合，保证后续 kiro/droid 写入不会被旧 224 生成的窄约束拒绝。
func TestUserPlatformQuotasAllPlatformsRepairMigration(t *testing.T) {
	requireAllUserPlatformQuotaPlatforms(t, "234_user_platform_quotas_all_platforms.sql")
}
