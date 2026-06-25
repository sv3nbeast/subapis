package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildDynamicToolMap_BelowThreshold(t *testing.T) {
	// Parrot 行为：tools 数量 ≤ 5 时不做动态映射。
	names := []string{"bash", "edit", "read", "write", "search"}
	require.Nil(t, buildDynamicToolMap(names))
}

func TestBuildDynamicToolMap_AboveThresholdIsStable(t *testing.T) {
	// Parrot 不变量：同一组 tool_names 在同进程内映射稳定（保证 cache 命中）。
	names := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	a := buildDynamicToolMap(names)
	b := buildDynamicToolMap(names)
	require.NotNil(t, a)
	require.Equal(t, a, b, "same input tool_names must yield identical mapping")
	require.Len(t, a, 6)
	for _, name := range names {
		require.Contains(t, a, name)
		require.NotEqual(t, name, a[name])
	}
}

func TestSanitizeToolName_StaticPrefix(t *testing.T) {
	require.Equal(t, "cc_sess_list", sanitizeToolName("sessions_list", nil))
	require.Equal(t, "cc_ses_get", sanitizeToolName("session_get", nil))
	require.Equal(t, "Bash", sanitizeToolName("bash", nil))
}

func TestSanitizeToolName_DynamicTakesPrecedence(t *testing.T) {
	dyn := map[string]string{"sessions_list": "analyze_ses00"}
	got := sanitizeToolName("sessions_list", dyn)
	require.Equal(t, "analyze_ses00", got, "dynamic mapping wins over static prefix")
}

func TestRestoreToolNamesInBytes_LongestFirst(t *testing.T) {
	// 当假名 "abc_12" 是另一个更长假名的子串（真实场景极少但算法必须防御）时，
	// 长的必须先替换。本测试用显式构造的映射来验证排序不变量。
	rw := &ToolNameRewrite{
		Forward: map[string]string{"foo": "abc_12", "bar": "abc_12_ext"},
		Reverse: map[string]string{"abc_12": "foo", "abc_12_ext": "bar"},
	}
	// 手工构造 ReverseOrdered：长的在前
	rw.ReverseOrdered = [][2]string{
		{"abc_12_ext", "bar"},
		{"abc_12", "foo"},
	}
	data := []byte(`{"tool":"abc_12_ext","other":"abc_12"}`)
	restored := string(restoreToolNamesInBytes(data, rw))
	require.Equal(t, `{"tool":"bar","other":"foo"}`, restored)
}

func TestRestoreToolNamesInBytes_StaticPrefixRollback(t *testing.T) {
	data := []byte(`{"name":"sessions_list","id":"cc_ses_xyz"}`)
	got := string(restoreToolNamesInBytes(data, nil))
	require.Equal(t, `{"name":"sessions_list","id":"session_xyz"}`, got)
}

func TestApplyToolNameRewriteToBody_RenamesToolsAndToolChoice(t *testing.T) {
	body := []byte(`{"tools":[{"name":"sessions_list","input_schema":{}},{"name":"session_get","input_schema":{}},{"name":"web_search","type":"web_search_20250305"}],"tool_choice":{"type":"tool","name":"sessions_list"}}`)
	rw := buildToolNameRewriteFromBody(body)
	require.NotNil(t, rw)
	require.Contains(t, rw.Forward, "sessions_list")
	require.Contains(t, rw.Forward, "session_get")
	// web_search 是 server tool，不参与工具名改写
	require.NotContains(t, rw.Forward, "web_search")

	out := applyToolNameRewriteToBody(body, rw)

	// tools[0].name 和 tools[1].name 被改写，tools[2].name 保持不变
	require.Equal(t, "cc_sess_list", gjson.GetBytes(out, "tools.0.name").String())
	require.Equal(t, "cc_ses_get", gjson.GetBytes(out, "tools.1.name").String())
	require.Equal(t, "web_search", gjson.GetBytes(out, "tools.2.name").String())

	// tool_choice.name 被同步改写
	require.Equal(t, "cc_sess_list", gjson.GetBytes(out, "tool_choice.name").String())
	require.Equal(t, "tool", gjson.GetBytes(out, "tool_choice.type").String())
}

func TestApplyToolNameRewriteToBody_RenamesToolUseInMessages(t *testing.T) {
	// sessions_list 通过静态前缀规则改写为 cc_sess_list
	// web_search 是 server tool（type != ""），不参与工具名改写
	// messages 中的 tool_use.name 必须同步改写，才能和 tools[] 保持一致
	body := []byte(`{"tools":[{"name":"sessions_list","input_schema":{}},{"name":"web_search","type":"web_search_20250305"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]},{"role":"assistant","content":[{"type":"tool_use","id":"tu_01","name":"sessions_list","input":{}},{"type":"text","text":"thinking"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_01","content":"ok"}]}]}`)
	rw := buildToolNameRewriteFromBody(body)
	require.NotNil(t, rw)
	require.Equal(t, "cc_sess_list", rw.Forward["sessions_list"])

	out := applyToolNameRewriteToBody(body, rw)

	// tools[0].name 被改写
	require.Equal(t, "cc_sess_list", gjson.GetBytes(out, "tools.0.name").String())
	// tools[1].name 是 server tool，保持不变
	require.Equal(t, "web_search", gjson.GetBytes(out, "tools.1.name").String())
	// messages[1].content[0].name 是 tool_use，必须同步改写以匹配 tools[]
	require.Equal(t, "cc_sess_list", gjson.GetBytes(out, "messages.1.content.0.name").String())
	// messages[1].content[1] 是 text，保持不变
	require.Equal(t, "thinking", gjson.GetBytes(out, "messages.1.content.1.text").String())
	// messages[2].content[0] 是 tool_result，不包含 name 字段，保持不变
	require.Equal(t, "ok", gjson.GetBytes(out, "messages.2.content.0.content").String())
}

func TestApplyToolNameRewriteToBody_RenamesToolUseWithDynamicMapping(t *testing.T) {
	body := []byte(`{"tools":[{"name":"alpha_search","input_schema":{}},{"name":"beta_lookup","input_schema":{}},{"name":"gamma_fetch","input_schema":{}},{"name":"delta_update","input_schema":{}},{"name":"epsilon_parse","input_schema":{}},{"name":"zeta_render","input_schema":{}},{"name":"web_search","type":"web_search_20250305"}],"tool_choice":{"type":"tool","name":"gamma_fetch"},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tu_dyn","name":"gamma_fetch","input":{}},{"type":"tool_use","id":"tu_srv","name":"web_search","input":{}},{"type":"text","text":"done"}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu_dyn","content":"ok"}]}]}`)
	rw := buildToolNameRewriteFromBody(body)
	require.NotNil(t, rw)
	require.Len(t, rw.Forward, 6)

	fakeGamma := rw.Forward["gamma_fetch"]
	require.NotEmpty(t, fakeGamma)
	require.NotEqual(t, "gamma_fetch", fakeGamma)
	require.NotContains(t, rw.Forward, "web_search")

	out := applyToolNameRewriteToBody(body, rw)

	// 动态映射会改写 tools[]、tool_choice 和历史 tool_use 中的同一个工具名
	require.Equal(t, fakeGamma, gjson.GetBytes(out, "tools.2.name").String())
	require.Equal(t, fakeGamma, gjson.GetBytes(out, "tool_choice.name").String())
	require.Equal(t, fakeGamma, gjson.GetBytes(out, "messages.0.content.0.name").String())
	// server tool 不参与动态映射，历史 tool_use 中同名引用也保持不变
	require.Equal(t, "web_search", gjson.GetBytes(out, "tools.6.name").String())
	require.Equal(t, "web_search", gjson.GetBytes(out, "messages.0.content.1.name").String())
	// tool_result 依靠 tool_use_id 关联，不需要 name 字段
	require.Equal(t, "ok", gjson.GetBytes(out, "messages.1.content.0.content").String())
}

func TestApplyToolNameRewriteToBody_CanonicalizesOAuthToolNames(t *testing.T) {
	body := []byte(`{"tools":[{"name":"bash","input_schema":{}},{"name":"glob","input_schema":{}},{"name":"Bash","input_schema":{}}],"tool_choice":{"type":"tool","name":"glob"},"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"tu_1","name":"bash","input":{}},{"type":"tool_reference","tool_name":"glob"},{"type":"tool_result","tool_use_id":"tu_1","content":[{"type":"tool_reference","tool_name":"bash"}]}]}]}`)
	rw := buildToolNameRewriteFromBody(body)
	require.NotNil(t, rw)
	require.Equal(t, "Bash", rw.Forward["bash"])
	require.Equal(t, "Glob", rw.Forward["glob"])
	require.NotContains(t, rw.Forward, "Bash")
	require.Equal(t, "bash", rw.ReverseFields["Bash"])
	require.Equal(t, "glob", rw.ReverseFields["Glob"])
	require.Empty(t, rw.ReverseOrdered, "canonical tool names should be restored only in JSON name fields")

	out := applyToolNameRewriteToBody(body, rw)

	require.Equal(t, "Bash", gjson.GetBytes(out, "tools.0.name").String())
	require.Equal(t, "Glob", gjson.GetBytes(out, "tools.1.name").String())
	require.Equal(t, "Bash", gjson.GetBytes(out, "tools.2.name").String())
	require.Equal(t, "Glob", gjson.GetBytes(out, "tool_choice.name").String())
	require.Equal(t, "Bash", gjson.GetBytes(out, "messages.0.content.0.name").String())
	require.Equal(t, "Glob", gjson.GetBytes(out, "messages.0.content.1.tool_name").String())
	require.Equal(t, "Bash", gjson.GetBytes(out, "messages.0.content.2.content.0.tool_name").String())
}

func TestRestoreToolNamesInBytes_CanonicalFieldsOnly(t *testing.T) {
	rw := &ToolNameRewrite{
		Forward:       map[string]string{"bash": "Bash", "glob": "Glob"},
		Reverse:       map[string]string{"Bash": "bash", "Glob": "glob"},
		ReverseFields: map[string]string{"Bash": "bash", "Glob": "glob"},
	}

	data := []byte(`{"content":[{"type":"tool_use","name":"Bash"},{"type":"tool_reference","tool_name":"Glob"},{"type":"text","text":"Bash and Glob are mentioned in prose"}]}`)
	restored := restoreToolNamesInBytes(data, rw)

	require.Equal(t, "bash", gjson.GetBytes(restored, "content.0.name").String())
	require.Equal(t, "glob", gjson.GetBytes(restored, "content.1.tool_name").String())
	require.Equal(t, "Bash and Glob are mentioned in prose", gjson.GetBytes(restored, "content.2.text").String())
}

func TestApplyToolsLastCacheBreakpoint_InjectsDefault(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{}},{"name":"b","input_schema":{}}]}`)
	out := applyToolsLastCacheBreakpoint(body)
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "tools.1.cache_control.type").String())
	require.Equal(t, "5m", gjson.GetBytes(out, "tools.1.cache_control.ttl").String())
	// First tool untouched
	require.False(t, gjson.GetBytes(out, "tools.0.cache_control").Exists())
}

func TestApplyToolsLastCacheBreakpoint_PassesThroughClientTTL(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"1h"}}]}`)
	out := applyToolsLastCacheBreakpoint(body)
	// User-provided ttl must be preserved.
	require.Equal(t, "1h", gjson.GetBytes(out, "tools.0.cache_control.ttl").String())
}

func TestApplyToolsLastCacheBreakpointWithTTL_UsesRequestedTTLWithoutOverridingExplicitTTL(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{}},{"name":"b","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"5m"}}]}`)
	out := applyToolsLastCacheBreakpointWithTTL(body, cacheTTLTarget1h)
	require.Equal(t, "5m", gjson.GetBytes(out, "tools.1.cache_control.ttl").String())

	body = []byte(`{"tools":[{"name":"a","input_schema":{}},{"name":"b","input_schema":{}}]}`)
	out = applyToolsLastCacheBreakpointWithTTL(body, cacheTTLTarget1h)
	require.Equal(t, "1h", gjson.GetBytes(out, "tools.1.cache_control.ttl").String())
}

func TestStripMessageCacheControl(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
	out := stripMessageCacheControl(body)
	require.False(t, gjson.GetBytes(out, "messages.0.content.0.cache_control").Exists())
}

func TestAddMessageCacheBreakpoints_LastMessageOnly(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	out := addMessageCacheBreakpoints(body)
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "5m", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
}

func TestAddMessageCacheBreakpointsWithTTL_UsesRequestedTTL(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
	out := addMessageCacheBreakpointsWithTTL(body, cacheTTLTarget1h)
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
}

func TestAddMessageCacheBreakpoints_SecondToLastUserTurn(t *testing.T) {
	// Parrot 不变量：messages ≥ 4 时才打第二个断点，且位置是"倒数第二个 user turn"。
	body := []byte(`{"messages":[
        {"role":"user","content":[{"type":"text","text":"q1"}]},
        {"role":"assistant","content":[{"type":"text","text":"a1"}]},
        {"role":"user","content":[{"type":"text","text":"q2"}]},
        {"role":"assistant","content":[{"type":"text","text":"a2"}]}
    ]}`)
	out := addMessageCacheBreakpoints(body)
	// 最后一条 assistant 被打断点
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.3.content.0.cache_control.type").String())
	// 倒数第二个 user turn = index 0（唯一另一个 user）
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
	// 其他不打断点
	require.False(t, gjson.GetBytes(out, "messages.1.content.0.cache_control").Exists())
	require.False(t, gjson.GetBytes(out, "messages.2.content.0.cache_control").Exists())
}

func TestAddMessageCacheBreakpoints_StringContentPromoted(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	out := addMessageCacheBreakpoints(body)
	// content 升级成数组
	require.True(t, gjson.GetBytes(out, "messages.0.content").IsArray())
	require.Equal(t, "text", gjson.GetBytes(out, "messages.0.content.0.type").String())
	require.Equal(t, "hi", gjson.GetBytes(out, "messages.0.content.0.text").String())
	require.Equal(t, "5m", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
}

func TestRewriteMessageCacheControlIfEnabled_DefaultKeepsClientAnchors(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
		{"role":"assistant","content":[{"type":"text","text":"ok"}]},
		{"role":"user","content":[{"type":"text","text":"latest","cache_control":{"type":"ephemeral","ttl":"5m"}}]}
	]}`)

	out := (&GatewayService{}).rewriteMessageCacheControlIfEnabled(context.Background(), body)

	require.JSONEq(t, string(body), string(out))
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "5m", gjson.GetBytes(out, "messages.2.content.0.cache_control.ttl").String())
}

func TestRewriteMessageCacheControlIfEnabled_OptInPreservesLegacyRewrite(t *testing.T) {
	body := []byte(`{"messages":[
		{"role":"user","content":[{"type":"text","text":"stable","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
		{"role":"assistant","content":[{"type":"text","text":"ok"}]},
		{"role":"user","content":[{"type":"text","text":"latest","cache_control":{"type":"ephemeral","ttl":"1h"}}]},
		{"role":"assistant","content":[{"type":"text","text":"done"}]}
	]}`)
	repo := &gatewayTTLSettingRepo{data: map[string]string{
		SettingKeyRewriteMessageCacheControl: "true",
	}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	svc := &GatewayService{settingService: NewSettingService(repo, &config.Config{})}

	out := svc.rewriteMessageCacheControlIfEnabled(context.Background(), body)

	require.Equal(t, "5m", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(out, "messages.2.content.0.cache_control").Exists())
	require.Equal(t, "5m", gjson.GetBytes(out, "messages.3.content.0.cache_control.ttl").String())
}

func TestBodyHasAnyCacheControl(t *testing.T) {
	t.Run("empty body", func(t *testing.T) {
		require.False(t, bodyHasAnyCacheControl(nil))
		require.False(t, bodyHasAnyCacheControl([]byte("")))
	})
	t.Run("no cache_control anywhere", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"tools":[{"name":"t1"}]}`)
		require.False(t, bodyHasAnyCacheControl(body))
	})
	t.Run("message cache_control present", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
		require.True(t, bodyHasAnyCacheControl(body))
	})
	t.Run("tool cache_control present", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"t","cache_control":{"type":"ephemeral"}}]}`)
		require.True(t, bodyHasAnyCacheControl(body))
	})
	t.Run("system cache_control present", func(t *testing.T) {
		body := []byte(`{"system":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}`)
		require.True(t, bodyHasAnyCacheControl(body))
	})
}

func TestShouldInjectBreakpointsForBridge(t *testing.T) {
	svc := &GatewayService{}
	oauthAcc := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	apiKeyAcc := &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}
	bridgeUA := "claude-cli/2.1.170 (external, claude-desktop-3p, agent-sdk/0.3.170)"
	plainCLIUA := "claude-cli/2.1.22 (external, cli)"
	bodyNoCache := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	bodyHasCache := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)

	t.Run("nil account → false", func(t *testing.T) {
		ctx := SetClaudeCodeUserAgent(context.Background(), bridgeUA)
		require.False(t, svc.shouldInjectBreakpointsForBridge(ctx, nil, bodyNoCache))
	})
	t.Run("non OAuth account → false", func(t *testing.T) {
		ctx := SetClaudeCodeUserAgent(context.Background(), bridgeUA)
		require.False(t, svc.shouldInjectBreakpointsForBridge(ctx, apiKeyAcc, bodyNoCache))
	})
	t.Run("plain CLI UA (no agent-sdk / desktop-3p) → false", func(t *testing.T) {
		ctx := SetClaudeCodeUserAgent(context.Background(), plainCLIUA)
		require.False(t, svc.shouldInjectBreakpointsForBridge(ctx, oauthAcc, bodyNoCache))
	})
	t.Run("bridge UA + body already has cache_control → true", func(t *testing.T) {
		// 放宽后：客户端自带 cache_control 反而是漂移源，网关也要接管规整。
		ctx := SetClaudeCodeUserAgent(context.Background(), bridgeUA)
		require.True(t, svc.shouldInjectBreakpointsForBridge(ctx, oauthAcc, bodyHasCache))
	})
	t.Run("bridge UA + no cache_control → true", func(t *testing.T) {
		ctx := SetClaudeCodeUserAgent(context.Background(), bridgeUA)
		require.True(t, svc.shouldInjectBreakpointsForBridge(ctx, oauthAcc, bodyNoCache))
	})
}

func TestInjectBridgeCacheBreakpoints_MessagesYesToolsNo(t *testing.T) {
	svc := &GatewayService{}
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}],"tools":[{"name":"bash","input_schema":{}}]}`)
	out := svc.injectBridgeCacheBreakpoints(nil, body)

	// 单条 message 退化为只打末尾(messages.0),1h 断点
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "messages.0.content.0.cache_control.type").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	// tools[-1] **不再**打断点(预算全给 messages,tools 由 system 累积前缀覆盖)
	require.False(t, gjson.GetBytes(out, "tools.0.cache_control").Exists())
	// system 字段未被修改（本来就没有）
	require.False(t, gjson.GetBytes(out, "system").Exists())
}

func TestInjectBridgeCacheBreakpoints_DoesNotTouchSystem(t *testing.T) {
	svc := &GatewayService{}
	// 客户端自带 system（含 cache_control）+ 一个会漂移的 messages 断点。
	// bridge 接管路径：system 一字不动（含其 cache_control），messages 断点被
	// strip 后由网关重打。
	body := []byte(`{"system":[{"type":"text","text":"You are helpful","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`)
	out := svc.injectBridgeCacheBreakpoints(nil, body)

	// system 完全保留（文本 + 原 cache_control 不变）
	require.Equal(t, "You are helpful", gjson.GetBytes(out, "system.0.text").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(out, "system.0.cache_control.type").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "system.0.cache_control.ttl").String())
	// messages 断点被网关重打为 1h（单条 message 退化为最后一条）
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
}

// bridgeAnchorIndexes 返回 body 里 messages 上所有 cache_control 锚点的 message index(升序)。
func bridgeAnchorIndexes(out []byte) []int {
	var idxs []int
	gjson.GetBytes(out, "messages").ForEach(func(i, msg gjson.Result) bool {
		has := false
		msg.Get("content").ForEach(func(_, block gjson.Result) bool {
			if block.Get("cache_control").Exists() {
				has = true
				return false
			}
			return true
		})
		if has {
			idxs = append(idxs, int(i.Int()))
		}
		return true
	})
	return idxs
}

// TestAddBridgeMessageCacheBreakpoints_StableAndTrailing 验证双断点布局:
// stable 落在第一个 user(index 0)、trailing 落在最后一条。
func TestAddBridgeMessageCacheBreakpoints_StableAndTrailing(t *testing.T) {
	body := []byte(`{"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"u1"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"a1"}]},` +
		`{"role":"user","content":[{"type":"text","text":"u2"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"a2"}]},` +
		`{"role":"user","content":[{"type":"text","text":"u3"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"a3"}]}` +
		`]}`)
	out := addBridgeMessageCacheBreakpointsWithTTL(body, "1h")
	idxs := bridgeAnchorIndexes(out)
	require.Equal(t, []int{0, 5}, idxs, "应恰好 2 个断点:stable@第一个user(0) + trailing@末尾(5)")
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.5.content.0.cache_control.ttl").String())
}

// TestInjectBridgeCacheBreakpoints_StableAnchorAbsolutelyStableAcrossTurns 验证修复
// 核心不变量:stable 锚点的**绝对 index** 跨连续多轮恒定(=第一个 user index),
// 且锚点之前前缀逐字节一致 —— 这是缓存跨轮稳定命中、不再暴跌回 system 的依据。
func TestInjectBridgeCacheBreakpoints_StableAnchorAbsolutelyStableAcrossTurns(t *testing.T) {
	svc := &GatewayService{}
	mk := func(n int) []byte {
		var sb strings.Builder
		sb.WriteString(`{"messages":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				sb.WriteString(",")
			}
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			sb.WriteString(`{"role":"` + role + `","content":[{"type":"text","text":"m` + string(rune('A'+i%26)) + `"}]}`)
		}
		sb.WriteString(`]}`)
		return []byte(sb.String())
	}
	// round1=6 条, round2=8 条, round3=10 条(逐轮末尾追加)
	out1 := svc.injectBridgeCacheBreakpoints(nil, mk(6))
	out2 := svc.injectBridgeCacheBreakpoints(nil, mk(8))
	out3 := svc.injectBridgeCacheBreakpoints(nil, mk(10))

	// stable 锚点(最靠前的那个)绝对 index 跨三轮恒定 = 0
	require.Equal(t, 0, bridgeAnchorIndexes(out1)[0])
	require.Equal(t, 0, bridgeAnchorIndexes(out2)[0])
	require.Equal(t, 0, bridgeAnchorIndexes(out3)[0])
	// 每轮都是 2 个断点(stable + trailing)
	require.Len(t, bridgeAnchorIndexes(out1), 2)
	require.Len(t, bridgeAnchorIndexes(out3), 2)
	// trailing 跟着末尾走(随对话增长)
	require.Equal(t, 5, bridgeAnchorIndexes(out1)[1])
	require.Equal(t, 9, bridgeAnchorIndexes(out3)[1])
}

// TestInjectBridgeCacheBreakpoints_BudgetWithinLimit 验证断点预算：bridge 接管 +
// enforceCacheControlLimit 后，system 的 2 个断点全部存活、messages 锚点存活、
// 总数 ≤ 4，不会因超限把最靠前的锚点裁掉。
// TestInjectBridgeCacheBreakpoints_TTLOrderSafeAfterNormalize 回归:client system
// 是 5m、网关给 messages 打 1h 时,经公共的 normalizeCacheControlTTLOrder 后,
// messages 的 1h 必须被降级,避免上游报 "1h must not come after 5m" 400。
func TestInjectBridgeCacheBreakpoints_TTLOrderSafeAfterNormalize(t *testing.T) {
	svc := &GatewayService{}
	// 客户端 system 带 5m(裸 ephemeral 或显式 5m 同理)+ 6 条 messages。
	body := []byte(`{` +
		`"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}],` +
		`"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"u1"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"a1"}]},` +
		`{"role":"user","content":[{"type":"text","text":"u2"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"a2"}]},` +
		`{"role":"user","content":[{"type":"text","text":"u3"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"a3"}]}` +
		`]}`)

	out := svc.injectBridgeCacheBreakpoints(nil, body)
	// inject 后 messages 应是 1h(stable@0 + trailing@5)
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(out, "messages.5.content.0.cache_control.ttl").String())

	// 经公共归一化后:system 5m 在前 → messages 的 1h 被降级(删 ttl),不再违规。
	out = normalizeCacheControlTTLOrder(out)
	require.Equal(t, "5m", gjson.GetBytes(out, "system.0.cache_control.ttl").String(), "system 5m 保留")
	require.False(t, gjson.GetBytes(out, "messages.0.content.0.cache_control.ttl").Exists(), "messages stable 1h 应被降级")
	require.False(t, gjson.GetBytes(out, "messages.5.content.0.cache_control.ttl").Exists(), "messages trailing 1h 应被降级")
	// 断点本身仍在(只是 ttl 降为默认 5m),数量不变
	require.True(t, gjson.GetBytes(out, "messages.0.content.0.cache_control").Exists())
	require.True(t, gjson.GetBytes(out, "messages.5.content.0.cache_control").Exists())
}

func TestInjectBridgeCacheBreakpoints_BudgetWithinLimit(t *testing.T) {
	svc := &GatewayService{}
	// system 2 个断点（客户端自带）+ 长 messages（≥4）+ tools。
	body := []byte(`{` +
		`"system":[` +
		`{"type":"text","text":"s1","cache_control":{"type":"ephemeral","ttl":"1h"}},` +
		`{"type":"text","text":"s2","cache_control":{"type":"ephemeral","ttl":"1h"}}` +
		`],` +
		`"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"u1"}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"a1"}]},` +
		`{"role":"user","content":[{"type":"text","text":"u2","cache_control":{"type":"ephemeral","ttl":"5m"}}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"a2","cache_control":{"type":"ephemeral","ttl":"5m"}}]}` +
		`],` +
		`"tools":[{"name":"bash","input_schema":{}}]` +
		`}`)

	out := svc.injectBridgeCacheBreakpoints(nil, body)
	out = enforceCacheControlLimit(out)

	_, messagePaths, toolPaths, systemPaths := collectCacheControlPaths(out)
	total := len(messagePaths) + len(toolPaths) + len(systemPaths)
	require.Equal(t, 4, total, "预算分配 system2 + messages2 + tools0 = 4")
	require.Equal(t, 2, len(systemPaths), "system 两个断点必须全部存活")
	require.Equal(t, 2, len(messagePaths), "messages stable + trailing 两个锚点存活")
	require.Equal(t, 0, len(toolPaths), "tools 不打断点")
}

func TestBuildToolNameRewriteFromBody_ReverseOrderedByLengthDesc(t *testing.T) {
	// 超过阈值触发动态映射，验证 ReverseOrdered 按假名长度倒序排列
	body := []byte(`{"tools":[
        {"name":"t1","input_schema":{}},
        {"name":"t2","input_schema":{}},
        {"name":"t3","input_schema":{}},
        {"name":"t4","input_schema":{}},
        {"name":"t5","input_schema":{}},
        {"name":"t6","input_schema":{}}
    ]}`)
	rw := buildToolNameRewriteFromBody(body)
	require.NotNil(t, rw)
	require.NotEmpty(t, rw.ReverseOrdered)
	for i := 1; i < len(rw.ReverseOrdered); i++ {
		require.GreaterOrEqual(t, len(rw.ReverseOrdered[i-1][0]), len(rw.ReverseOrdered[i][0]),
			"ReverseOrdered must be sorted by fake-name length descending")
	}
}

func TestRestoreToolNamesInBytes_NoMapping_NoStaticMatch_IsNoop(t *testing.T) {
	data := []byte("plain text without any tool names")
	require.Equal(t, string(data), string(restoreToolNamesInBytes(data, nil)))
}

// Ensure the fake name format follows Parrot's "{prefix}{name[:3]}{i:02d}".
func TestBuildDynamicToolMap_FakeNameShape(t *testing.T) {
	names := []string{"alphabet", "bravo", "charlie", "delta", "echo", "foxtrot"}
	m := buildDynamicToolMap(names)
	require.NotNil(t, m)
	for _, name := range names {
		fake, ok := m[name]
		require.True(t, ok)
		// fake = prefix + head3 + "%02d"
		// ends with two decimal digits
		require.Regexp(t, `^[a-z]+_[a-z0-9]{1,3}\d{2}$`, fake)
		head := name
		if len(head) > 3 {
			head = head[:3]
		}
		require.True(t, strings.Contains(fake, head), "fake %q should contain head3 %q of %q", fake, head, name)
	}
}
