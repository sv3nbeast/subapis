package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// debugGatewayBodyUserEnv 把 SUB2API_DEBUG_GATEWAY_BODY 抓包收敛到单个用户，
// 防止生产上落盘全站请求体。取值为目标用户 ID（int64）；为空/0 表示不按用户过滤。
const debugGatewayBodyUserEnv = "SUB2API_DEBUG_GATEWAY_USER_ID"

// ginContextKeyAPIKey 与 middleware.ContextKeyAPIKey("api_key") 保持一致。
// 这里硬编码字面量以避免 service → middleware 的反向 import 循环；
// 由 gateway_debug_capture_test.go 的守护测试锁定二者一致。
const ginContextKeyAPIKey = "api_key"

// parseDebugGatewayUserID 解析 SUB2API_DEBUG_GATEWAY_USER_ID。
// 非法/负值/空 → 0（表示不按用户过滤）。
func parseDebugGatewayUserID(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		return 0
	}
	return id
}

// ginUserIDForDebug 从 gin.Context 读取已鉴权用户 ID。
// ApiKeyAuth 中间件以 "api_key" 键存入 *service.APIKey（service 自有类型，无循环依赖）。
func (s *GatewayService) ginUserIDForDebug(c *gin.Context) int64 {
	if c == nil {
		return 0
	}
	v, ok := c.Get(ginContextKeyAPIKey)
	if !ok {
		return 0
	}
	apiKey, ok := v.(*APIKey)
	if !ok || apiKey == nil {
		return 0
	}
	return apiKey.UserID
}

// debugBodyCaptureEnabled 控制请求体快照（CLIENT_ORIGINAL / UPSTREAM_FORWARD）。
// 未设置用户目标时保持旧的「文件开启即全局落盘」行为；设置目标后仅落该用户。
func (s *GatewayService) debugBodyCaptureEnabled(c *gin.Context) bool {
	if s.debugGatewayBodyFile.Load() == nil {
		return false
	}
	target := s.debugGatewayBodyUserID.Load()
	if target == 0 {
		return true
	}
	return s.ginUserIDForDebug(c) == target
}

// debugCaptureEnabledForUser 控制全链路响应抓取（上游返回 + 返回客户端）。
// 仅在显式设置用户目标且命中时触发——这样单独打开 SUB2API_DEBUG_GATEWAY_BODY
// 永远不会把响应体/SSE 全站刷屏，响应抓取必须主动指定用户。
func (s *GatewayService) debugCaptureEnabledForUser(c *gin.Context) bool {
	if s.debugGatewayBodyFile.Load() == nil {
		return false
	}
	target := s.debugGatewayBodyUserID.Load()
	if target == 0 {
		return false
	}
	return s.ginUserIDForDebug(c) == target
}

// AllowSyncForDebugCapture 报告是否应为「抓包定因」临时放行同步(非流式)/v1/messages 请求。
// 仅当 SUB2API_DEBUG_GATEWAY_BODY 抓包已开启且请求用户命中 SUB2API_DEBUG_GATEWAY_USER_ID
// 时返回 true——让目标用户的非流式请求越过止血守卫、走到 Forward(强制 stream=true 聚合),
// 以便抓取上游真实请求体+响应,定位非流式被上游 429 的根因(格式 vs stream 标志)。
// 抓包一旦关闭(BODY 置空)本方法即返回 false,守卫全量恢复,不留后门。
func (s *GatewayService) AllowSyncForDebugCapture(c *gin.Context) bool {
	return s.debugCaptureEnabledForUser(c)
}

// debugLogClientSSELine 把一条下行（网关→客户端）SSE 块写入同一份调试日志，
// 与 debugLogUpstreamSSELine（上行）对称，便于离线 diff 上下游事件变换。
func (s *GatewayService) debugLogClientSSELine(requestID, raw string) {
	f := s.debugGatewayBodyFile.Load()
	if f == nil {
		return
	}
	raw = strings.TrimRight(raw, "\n")
	var buf strings.Builder
	if requestID != "" {
		fmt.Fprintf(&buf, "CLIENT_SSE_LINE rid=%s  %s\n", requestID, raw)
	} else {
		fmt.Fprintf(&buf, "CLIENT_SSE_LINE  %s\n", raw)
	}
	_, _ = f.WriteString(buf.String())
}
