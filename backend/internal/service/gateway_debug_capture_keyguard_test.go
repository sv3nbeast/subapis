package service_test

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
)

// TestDebugCaptureAPIKeyContextKeyMatchesMiddleware 锁定 service 侧硬编码的
// ginContextKeyAPIKey("api_key") 与 middleware 实际写入 gin.Context 的键一致。
// 二者一旦漂移，用户门控抓包会静默失效（永远取不到 user_id）。
// service 不能 import middleware（会成环），故这里在外部测试包做守护。
func TestDebugCaptureAPIKeyContextKeyMatchesMiddleware(t *testing.T) {
	const ginContextKeyAPIKey = "api_key" // 必须与 service.ginContextKeyAPIKey 保持一致
	if got := string(middleware.ContextKeyAPIKey); got != ginContextKeyAPIKey {
		t.Fatalf("middleware.ContextKeyAPIKey = %q, service hardcodes %q; 抓包用户门控会失效", got, ginContextKeyAPIKey)
	}
}
