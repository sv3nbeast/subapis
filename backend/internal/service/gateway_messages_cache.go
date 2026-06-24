package service

import (
	"bytes"
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// bodyHasAnyCacheControl 判断请求体中是否已经存在任何 cache_control 块。
//
// 用于 Claude Desktop 3P / Agent SDK 桥接客户端的"按需补断点"判定：
// 这类 UA 上报为 claude-cli/* 但 SDK 主代理回合会自己打断点、子代理回合
// 完全不打。本函数让网关只在确认"客户端自己没标"时才补断点，
// 避免破坏主代理已有的缓存前缀。
//
// 复用 collectCacheControlPaths 同款扫描，保证与 enforceCacheControlLimit
// 的可见范围（system / messages.content / tools）一致。
//
// 入参为空或不含 "cache_control" 字面量时直接快速 false，避免无谓 gjson 扫描。
func bodyHasAnyCacheControl(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	if !bytes.Contains(body, []byte(`"cache_control"`)) {
		return false
	}
	_, messagePaths, toolPaths, systemPaths := collectCacheControlPaths(body)
	return len(messagePaths) > 0 || len(toolPaths) > 0 || len(systemPaths) > 0
}

// stripMessageCacheControl 移除 $.messages[*].content[*].cache_control。
// 与 Parrot _strip_message_cache_control 语义一致。
//
// 旧策略为什么整体清空：客户端（特别是 Claude Code）经常把 cache_control 打在
// "当前最后一条 user message" 上；下一轮对话 messages 追加后，原本的最后一条
// 变成中间某条，cache_control 还挂着就导致"前缀签名变化"，破坏缓存命中。
// 统一由代理重新打断点（addMessageCacheBreakpoints）才能在多轮间稳定。
func stripMessageCacheControl(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	msgIdx := -1
	messages.ForEach(func(_, msg gjson.Result) bool {
		msgIdx++
		content := msg.Get("content")
		if !content.IsArray() {
			return true
		}
		blockIdx := -1
		content.ForEach(func(_, block gjson.Result) bool {
			blockIdx++
			if !block.Get("cache_control").Exists() {
				return true
			}
			path := fmt.Sprintf("messages.%d.content.%d.cache_control", msgIdx, blockIdx)
			if next, err := sjson.DeleteBytes(body, path); err == nil {
				body = next
			}
			return true
		})
		return true
	})
	return body
}

// addMessageCacheBreakpoints 在 messages 上注入两个稳定的 cache 断点：
//  1. 最后一条 message
//  2. 当 messages 数量 ≥ 4 时，倒数第二个 role=user 的 message
//
// 与 Parrot add_cache_breakpoints 一致。两个断点 + system prompt block 的断点
// + tools[-1] 的断点共同构成最多 4 个断点（Anthropic 上限）。
//
// cache_control ttl 策略：
//   - 若目标 block 已有 cache_control.ttl → 不覆盖
//   - 否则写入 {"type":"ephemeral","ttl": claude.DefaultCacheControlTTL}
//
// 调用前应先 stripMessageCacheControl 以保证幂等和稳定。
func addMessageCacheBreakpoints(body []byte) []byte {
	return addMessageCacheBreakpointsWithTTL(body, claude.DefaultCacheControlTTL)
}

func addMessageCacheBreakpointsWithTTL(body []byte, ttl string) []byte {
	if ttl == "" {
		ttl = claude.DefaultCacheControlTTL
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return body
	}

	body = injectCacheControlOnLastContentBlockWithTTL(body, len(arr)-1, &arr[len(arr)-1], ttl)

	if len(arr) >= 4 {
		userCount := 0
		for i := len(arr) - 1; i >= 0; i-- {
			if arr[i].Get("role").String() != "user" {
				continue
			}
			userCount++
			if userCount == 2 {
				body = injectCacheControlOnLastContentBlockWithTTL(body, i, &arr[i], ttl)
				break
			}
		}
	}

	return body
}

// addBridgeMessageCacheBreakpointsWithTTL 是 Claude Desktop 3P / Agent SDK 桥接
// 客户端专用的 messages 断点策略：**只打一个靠前的稳定锚点**，不打"最后一条
// message"那个会随对话漂移的增量断点。
//
// 为什么和 addMessageCacheBreakpointsWithTTL 不同：抓包证实这类客户端自身只在
// "最后一条 message"打一个断点，且该断点在「最后一条」「倒数第二条」之间逐轮
// 跳动（distFromEnd 在 0/1 间变化）。由于没有靠前的稳定锚点，末尾断点一漂移、
// 本次声明的缓存写入边界就与上次对不上，cache_read 直接跌回 system-only、整段
// 重建。网关 strip 掉客户端那个漂移断点后，由本函数重打一个固定落在"倒数第二个
// role=user message"的锚点：即使锚点 index 随对话增长而后移，其之前的 messages
// 前缀逐字节不变，按 Anthropic 最长公共前缀语义即可稳定命中锚点边界，不再跌回
// system。
//
// 断点预算：bridge 路径下 system 已带 2 个断点（客户端，本函数不碰）+ 本函数 1
// 个 messages 锚点 + tools[-1] 1 个 = 4，恰好不超 maxCacheControlBlocks，
// 不会触发 enforceCacheControlLimit 把最靠前的锚点裁掉。
//
// messages < 4 时退化为打最后一条（短对话谈不上漂移），与 addMessageCacheBreakpoints
// 的退化行为一致。
//
// 调用前应先 stripMessageCacheControl 清掉客户端的漂移断点，保证幂等与稳定。
func addBridgeMessageCacheBreakpointsWithTTL(body []byte, ttl string) []byte {
	if ttl == "" {
		ttl = claude.DefaultCacheControlTTL
	}
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return body
	}
	arr := messages.Array()
	if len(arr) == 0 {
		return body
	}

	if len(arr) < 4 {
		return injectCacheControlOnLastContentBlockWithTTL(body, len(arr)-1, &arr[len(arr)-1], ttl)
	}

	userCount := 0
	for i := len(arr) - 1; i >= 0; i-- {
		if arr[i].Get("role").String() != "user" {
			continue
		}
		userCount++
		if userCount == 2 {
			return injectCacheControlOnLastContentBlockWithTTL(body, i, &arr[i], ttl)
		}
	}

	// 兜底：messages ≥ 4 但找不到两个 user（极少见），退化为最后一条。
	return injectCacheControlOnLastContentBlockWithTTL(body, len(arr)-1, &arr[len(arr)-1], ttl)
}

// rewriteMessageCacheControlIfEnabled 按系统设置决定是否执行旧版 messages 缓存断点改写。
func (s *GatewayService) rewriteMessageCacheControlIfEnabled(ctx context.Context, body []byte) []byte {
	return s.rewriteMessageCacheControlIfEnabledWithTTL(ctx, body, claude.DefaultCacheControlTTL)
}

func (s *GatewayService) rewriteMessageCacheControlIfEnabledWithTTL(ctx context.Context, body []byte, ttl string) []byte {
	if s == nil || !s.isRewriteMessageCacheControlEnabled(ctx) {
		return body
	}
	if ttl == "" {
		ttl = claude.DefaultCacheControlTTL
	}
	body = stripMessageCacheControl(body)
	return addMessageCacheBreakpointsWithTTL(body, ttl)
}

func (s *GatewayService) isRewriteMessageCacheControlEnabled(ctx context.Context) bool {
	if s == nil {
		return false
	}
	if s.settingService != nil {
		return s.settingService.IsRewriteMessageCacheControlEnabled(ctx)
	}
	return false
}

// injectCacheControlOnLastContentBlock 把 cache_control 断点打在 messages[idx]
// 的最后一个 content block 上。若 content 是 string，先升级成单块 text 数组
// （对齐 Parrot _inject_cache_on_msg 的行为）。
//
// msg 是调用方已持有的 gjson.Result 快照，用于省一次 GetBytes。
func injectCacheControlOnLastContentBlock(body []byte, idx int, msg *gjson.Result) []byte {
	return injectCacheControlOnLastContentBlockWithTTL(body, idx, msg, claude.DefaultCacheControlTTL)
}

func injectCacheControlOnLastContentBlockWithTTL(body []byte, idx int, msg *gjson.Result, ttl string) []byte {
	if ttl == "" {
		ttl = claude.DefaultCacheControlTTL
	}
	content := msg.Get("content")

	if content.Type == gjson.String {
		text := content.String()
		blockRaw := fmt.Sprintf(
			`[{"type":"text","text":%s,"cache_control":{"type":"ephemeral","ttl":%q}}]`,
			mustJSONString(text), ttl,
		)
		if next, err := sjson.SetRawBytes(body, fmt.Sprintf("messages.%d.content", idx), []byte(blockRaw)); err == nil {
			body = next
		}
		return body
	}

	if !content.IsArray() {
		return body
	}
	contentArr := content.Array()
	if len(contentArr) == 0 {
		return body
	}
	lastBlockIdx := len(contentArr) - 1
	lastBlock := contentArr[lastBlockIdx]

	if cc := lastBlock.Get("cache_control"); cc.Exists() && cc.Get("ttl").String() != "" {
		return body
	}

	pathPrefix := fmt.Sprintf("messages.%d.content.%d.cache_control", idx, lastBlockIdx)
	existingCC := lastBlock.Get("cache_control")
	if existingCC.Exists() {
		if next, err := sjson.SetBytes(body, pathPrefix+".ttl", ttl); err == nil {
			body = next
		}
		return body
	}
	raw := fmt.Sprintf(`{"type":"ephemeral","ttl":%q}`, ttl)
	if next, err := sjson.SetRawBytes(body, pathPrefix, []byte(raw)); err == nil {
		body = next
	}
	return body
}

// mustJSONString 把一个 Go string 序列化为合法 JSON string（含引号），
// 用于 sjson.SetRawBytes 场景下手工拼 JSON。
func mustJSONString(s string) string {
	return fmt.Sprintf("%q", s)
}
