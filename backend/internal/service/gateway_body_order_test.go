package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type gatewayTTLSettingRepo struct {
	data map[string]string
}

func (r *gatewayTTLSettingRepo) Get(context.Context, string) (*Setting, error) {
	return nil, ErrSettingNotFound
}

func (r *gatewayTTLSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if r == nil {
		return "", ErrSettingNotFound
	}
	v, ok := r.data[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return v, nil
}

func (r *gatewayTTLSettingRepo) Set(_ context.Context, key, value string) error {
	if r == nil {
		return errors.New("setting repo is nil")
	}
	if r.data == nil {
		r.data = map[string]string{}
	}
	r.data[key] = value
	return nil
}

func (r *gatewayTTLSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string)
	if r == nil {
		return result, nil
	}
	for _, key := range keys {
		if v, ok := r.data[key]; ok {
			result[key] = v
		}
	}
	return result, nil
}

func (r *gatewayTTLSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	if r == nil {
		return errors.New("setting repo is nil")
	}
	if r.data == nil {
		r.data = map[string]string{}
	}
	for key, value := range settings {
		r.data[key] = value
	}
	return nil
}

func (r *gatewayTTLSettingRepo) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string)
	if r == nil {
		return result, nil
	}
	for key, value := range r.data {
		result[key] = value
	}
	return result, nil
}

func (r *gatewayTTLSettingRepo) Delete(_ context.Context, key string) error {
	if r != nil {
		delete(r.data, key)
	}
	return nil
}

func assertJSONTokenOrder(t *testing.T, body string, tokens ...string) {
	t.Helper()

	last := -1
	for _, token := range tokens {
		pos := strings.Index(body, token)
		require.NotEqualf(t, -1, pos, "missing token %s in body %s", token, body)
		require.Greaterf(t, pos, last, "token %s should appear after previous tokens in body %s", token, body)
		last = pos
	}
}

func topLevelJSONKeysInOrder(t *testing.T, body []byte) []string {
	t.Helper()

	dec := json.NewDecoder(strings.NewReader(string(body)))
	tok, err := dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), tok)

	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		require.NoError(t, err)
		key, ok := tok.(string)
		require.True(t, ok)
		keys = append(keys, key)

		var raw json.RawMessage
		require.NoError(t, dec.Decode(&raw))
	}

	tok, err = dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('}'), tok)
	_, err = dec.Token()
	require.ErrorIs(t, err, io.EOF)
	return keys
}

func TestReplaceModelInBody_PreservesTopLevelFieldOrder(t *testing.T) {
	svc := &GatewayService{}
	body := []byte(`{"alpha":1,"model":"claude-3-5-sonnet-latest","messages":[],"omega":2}`)

	result := svc.replaceModelInBody(body, "claude-3-5-sonnet-20241022")
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"model"`, `"messages"`, `"omega"`)
	require.Contains(t, resultStr, `"model":"claude-3-5-sonnet-20241022"`)
}

func TestNormalizeClaudeOAuthRequestBody_PreservesTopLevelFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"model":"claude-3-5-sonnet-latest","temperature":0.2,"system":"You are OpenCode, the best coding agent on the planet.","messages":[],"tool_choice":{"type":"auto"},"omega":2}`)

	result, modelID := normalizeClaudeOAuthRequestBody(body, "claude-3-5-sonnet-latest", claudeOAuthNormalizeOptions{
		injectMetadata: true,
		metadataUserID: "user-1",
	})
	resultStr := string(result)

	require.Equal(t, claude.NormalizeModelID("claude-3-5-sonnet-latest"), modelID)
	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"model"`, `"temperature"`, `"system"`, `"messages"`, `"omega"`, `"tools"`, `"metadata"`, `"max_tokens"`)
	require.Contains(t, resultStr, `"temperature":0.2`)
	require.NotContains(t, resultStr, `"tool_choice"`)
	require.Contains(t, resultStr, `"system":"`+claudeCodeSystemPrompt+`"`)
	require.Contains(t, resultStr, `"tools":[]`)
	require.Contains(t, resultStr, `"metadata":{"user_id":"user-1"}`)
	require.Contains(t, resultStr, `"max_tokens":64000`)
}

func TestNormalizeClaudeOAuthRequestBody_CapturedOpusDefaults(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","messages":[],"system":[],"tools":[],"metadata":{"user_id":"u"},"thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"max"},"stream":true}`)

	result, _ := normalizeClaudeOAuthRequestBody(body, "claude-opus-4-7", claudeOAuthNormalizeOptions{})

	require.Equal(t, int64(64000), gjson.GetBytes(result, "max_tokens").Int())
	require.False(t, gjson.GetBytes(result, "temperature").Exists())
	require.True(t, gjson.GetBytes(result, "context_management").Exists())
}

func TestNormalizeClaudeOAuthRequestBody_CapturedHaikuDefaults(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","messages":[],"system":[],"tools":[],"metadata":{"user_id":"u"},"output_config":{"format":{"type":"json_schema"}},"stream":true}`)

	result, _ := normalizeClaudeOAuthRequestBody(body, "claude-haiku-4-5", claudeOAuthNormalizeOptions{})

	require.Equal(t, int64(32000), gjson.GetBytes(result, "max_tokens").Int())
	require.Equal(t, float64(1), gjson.GetBytes(result, "temperature").Float())
}

func TestEnsureAnthropicThinkingForModelAlias(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-6","messages":[]}`)

	result := ensureAnthropicThinkingForModelAlias(body, "claude-opus-4-6-thinking")

	require.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
	require.Equal(t, int64(BudgetRectifyBudgetTokens), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	require.Equal(t, int64(BudgetRectifyMaxTokens), gjson.GetBytes(result, "max_tokens").Int())
}

func TestEnsureAnthropicThinkingForModelAlias_NormalizesAdaptiveThinking(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","max_tokens":80000,"thinking":{"type":"adaptive","budget_tokens":12345},"messages":[]}`)

	result := ensureAnthropicThinkingForModelAlias(body, "claude-opus-4.7-thinking")

	require.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
	require.Equal(t, int64(12345), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	require.Equal(t, int64(80000), gjson.GetBytes(result, "max_tokens").Int())
}

func TestSanitizeAnthropicUpstreamRequestBody_DropsTopLevelSpeedOnly(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","speed":"fast","max_tokens":64000,"messages":[{"role":"user","content":[{"type":"text","text":"keep speed mention"}]}],"metadata":{"speed":"nested"}}`)

	result := sanitizeAnthropicUpstreamRequestBody(body)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"model"`, `"max_tokens"`, `"messages"`, `"metadata"`)
	require.False(t, gjson.GetBytes(result, "speed").Exists())
	require.Equal(t, "keep speed mention", gjson.GetBytes(result, "messages.0.content.0.text").String())
	require.Equal(t, "nested", gjson.GetBytes(result, "metadata.speed").String())
}

func TestMigrateAnthropicInlineSystemMessages_MovesSystemMessagesToTopLevel(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-8","system":[{"type":"text","text":"base","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"hello"},{"role":"system","content":[{"type":"text","text":"mid","cache_control":{"type":"ephemeral"}},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"x"}}]},{"role":"assistant","content":"ok"},{"role":"system","content":"tail"}],"metadata":{"user_id":"u"}}`)

	result, changed := migrateAnthropicInlineSystemMessages(body)

	require.True(t, changed)
	require.Equal(t, "base", gjson.GetBytes(result, "system.0.text").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(result, "system.0.cache_control.type").String())
	require.Equal(t, "mid", gjson.GetBytes(result, "system.1.text").String())
	require.Equal(t, "ephemeral", gjson.GetBytes(result, "system.1.cache_control.type").String())
	require.Equal(t, "tail", gjson.GetBytes(result, "system.2.text").String())
	require.Len(t, gjson.GetBytes(result, "messages").Array(), 2)
	require.Equal(t, "user", gjson.GetBytes(result, "messages.0.role").String())
	require.Equal(t, "assistant", gjson.GetBytes(result, "messages.1.role").String())
	require.Equal(t, "u", gjson.GetBytes(result, "metadata.user_id").String())
}

func TestMigrateAnthropicInlineSystemMessages_NoOpWithoutSystemMessages(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hello"}]}`)

	result, changed := migrateAnthropicInlineSystemMessages(body)

	require.False(t, changed)
	require.Equal(t, string(body), string(result))
}

func TestSanitizeAnthropicCountTokensRequestBody_DropsGenerationFields(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","temperature":0.2,"top_p":0.9,"top_k":40,"stream":true,"max_tokens":64000,"stop_sequences":["x"],"service_tier":"auto","metadata":{"user_id":"u"},"thinking":{"type":"enabled","budget_tokens":32000},"context_management":{"edits":[]},"messages":[{"role":"user","content":[{"type":"text","text":"keep temperature mention"}]}],"tools":[{"name":"t","input_schema":{"type":"object"}}],"speed":"fast"}`)

	result := sanitizeAnthropicCountTokensRequestBody(body)

	for _, field := range []string{"temperature", "top_p", "top_k", "stream", "max_tokens", "stop_sequences", "service_tier", "metadata", "context_management", "speed"} {
		require.False(t, gjson.GetBytes(result, field).Exists(), "%s should be removed for count_tokens", field)
	}
	require.Equal(t, "claude-opus-4-7", gjson.GetBytes(result, "model").String())
	require.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
	require.Equal(t, "keep temperature mention", gjson.GetBytes(result, "messages.0.content.0.text").String())
	require.Equal(t, "t", gjson.GetBytes(result, "tools.0.name").String())
}

func TestApplyAnthropicThinkingAliasToRequest_NormalizesAdaptiveThinking(t *testing.T) {
	req := &apicompat.AnthropicRequest{
		Model:     "claude-opus-4-6",
		MaxTokens: 1024,
		Thinking:  &apicompat.AnthropicThinking{Type: "adaptive"},
	}

	applyAnthropicThinkingAliasToRequest(req, "claude-opus-4-6-thinking")

	require.Equal(t, "enabled", req.Thinking.Type)
	require.Equal(t, BudgetRectifyBudgetTokens, req.Thinking.BudgetTokens)
	require.Equal(t, BudgetRectifyMaxTokens, req.MaxTokens)
}

func TestApplyAnthropicThinkingAliasToRequest_Opus48(t *testing.T) {
	req := &apicompat.AnthropicRequest{
		Model:     "claude-opus-4-8",
		MaxTokens: 1024,
	}

	applyAnthropicThinkingAliasToRequest(req, "claude-opus-4.8-thinking")

	require.NotNil(t, req.Thinking)
	require.Equal(t, "enabled", req.Thinking.Type)
	require.Equal(t, BudgetRectifyBudgetTokens, req.Thinking.BudgetTokens)
	require.Equal(t, BudgetRectifyMaxTokens, req.MaxTokens)
}

func TestInjectClaudeCodePrompt_PreservesFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"system":[{"id":"block-1","type":"text","text":"Custom"}],"messages":[],"omega":2}`)

	result := injectClaudeCodePrompt(body, []any{
		map[string]any{"id": "block-1", "type": "text", "text": "Custom"},
	})
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"system"`, `"messages"`, `"omega"`)
	require.Contains(t, resultStr, `{"id":"block-1","type":"text","text":"`+claudeCodeSystemPrompt+`\n\nCustom"}`)
}

func TestEnforceCacheControlLimit_PreservesTopLevelFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"s2","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"m1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m2","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m3","cache_control":{"type":"ephemeral"}}]}],"omega":2}`)

	result := enforceCacheControlLimit(body)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"system"`, `"messages"`, `"omega"`)
	require.Equal(t, 4, strings.Count(resultStr, `"cache_control"`))
}

func TestEnforceCacheControlLimit_CountsToolsAndPreservesMessageAnchorsFirst(t *testing.T) {
	body := []byte(`{"alpha":1,"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"m1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m2","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m3","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral"}}],"omega":2}`)

	result := enforceCacheControlLimit(body)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"system"`, `"messages"`, `"tools"`, `"omega"`)
	require.Equal(t, 4, strings.Count(resultStr, `"cache_control"`))
	require.True(t, gjson.GetBytes(result, "system.0.cache_control").Exists())
	require.True(t, gjson.GetBytes(result, "messages.0.content.0.cache_control").Exists())
	require.True(t, gjson.GetBytes(result, "messages.0.content.1.cache_control").Exists())
	require.True(t, gjson.GetBytes(result, "messages.0.content.2.cache_control").Exists())
	require.False(t, gjson.GetBytes(result, "tools.0.cache_control").Exists())
}

// TestEnforceCacheControlLimit_PreservesBridgeMessageAnchors 验证超限裁剪时
// 保护 messages 首尾锚点(bridge 的 stable@首 + trailing@尾),只删中段。
func TestEnforceCacheControlLimit_PreservesBridgeMessageAnchors(t *testing.T) {
	// 5 个 messages 断点(分布在 5 条 message),无 system/tools 断点 → 超限 1。
	body := []byte(`{"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"m0","cache_control":{"type":"ephemeral"}}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"m1","cache_control":{"type":"ephemeral"}}]},` +
		`{"role":"user","content":[{"type":"text","text":"m2","cache_control":{"type":"ephemeral"}}]},` +
		`{"role":"assistant","content":[{"type":"text","text":"m3","cache_control":{"type":"ephemeral"}}]},` +
		`{"role":"user","content":[{"type":"text","text":"m4","cache_control":{"type":"ephemeral"}}]}` +
		`]}`)

	result := enforceCacheControlLimit(body)

	require.True(t, gjson.GetBytes(result, "messages.0.content.0.cache_control").Exists(), "首锚点存活")
	require.True(t, gjson.GetBytes(result, "messages.4.content.0.cache_control").Exists(), "尾锚点存活")
	require.Equal(t, 4, strings.Count(string(result), `"cache_control"`))
}

func TestInjectAnthropicCacheControlTTL1h_OnlyUpdatesExistingEphemeralCacheControl(t *testing.T) {
	body := []byte(`{"alpha":1,"cache_control":{"type":"ephemeral"},"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"plain"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}},{"type":"text","text":"non","cache_control":{"type":"persistent","ttl":"5m"}}]}],"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral"}}],"omega":2}`)

	result := injectAnthropicCacheControlTTL1h(body)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"cache_control"`, `"system"`, `"messages"`, `"tools"`, `"omega"`)
	require.Equal(t, "1h", gjson.GetBytes(result, "cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(result, "system.0.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(result, "system.1.cache_control").Exists())
	require.Equal(t, "1h", gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "5m", gjson.GetBytes(result, "messages.0.content.1.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(result, "tools.0.cache_control.ttl").String())
}

func TestDisableThinkingIfToolChoiceForced_RemovesThinkingControls(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-8","temperature":0,"tool_choice":{"type":"any"},"thinking":{"type":"adaptive","budget_tokens":32000},"output_config":{"effort":"max"},"messages":[]}`)

	result := disableThinkingIfToolChoiceForced(body)

	require.False(t, gjson.GetBytes(result, "thinking").Exists())
	require.False(t, gjson.GetBytes(result, "output_config").Exists())
	require.Equal(t, "any", gjson.GetBytes(result, "tool_choice.type").String())
	require.Equal(t, float64(0), gjson.GetBytes(result, "temperature").Float())
}

func TestDisableThinkingIfToolChoiceForced_KeepsAutoThinking(t *testing.T) {
	body := []byte(`{"tool_choice":{"type":"auto"},"thinking":{"type":"enabled","budget_tokens":32000},"output_config":{"effort":"max"},"messages":[]}`)

	result := disableThinkingIfToolChoiceForced(body)

	require.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
	require.Equal(t, "max", gjson.GetBytes(result, "output_config.effort").String())
}

func TestDisableThinkingIfToolChoiceForced_PreservesOtherOutputConfig(t *testing.T) {
	body := []byte(`{"tool_choice":{"type":"tool","name":"Bash"},"thinking":{"type":"enabled"},"output_config":{"effort":"max","trace":true},"messages":[]}`)

	result := disableThinkingIfToolChoiceForced(body)

	require.False(t, gjson.GetBytes(result, "thinking").Exists())
	require.False(t, gjson.GetBytes(result, "output_config.effort").Exists())
	require.True(t, gjson.GetBytes(result, "output_config.trace").Bool())
}

func TestNormalizeCacheControlTTLOrder_DowngradesLaterOneHourBlocks(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"1h"}}],"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)

	result := normalizeCacheControlTTLOrder(body)

	require.Equal(t, "1h", gjson.GetBytes(result, "tools.0.cache_control.ttl").String())
	require.True(t, gjson.GetBytes(result, "system.0.cache_control").Exists())
	require.False(t, gjson.GetBytes(result, "system.0.cache_control.ttl").Exists())
	require.False(t, gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").Exists())
}

func TestNormalizeCacheControlTTLOrder_SameSectionOrdering(t *testing.T) {
	body := []byte(`{"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral","ttl":"1h"}},{"type":"text","text":"s2","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"s3","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[]}`)

	result := normalizeCacheControlTTLOrder(body)

	require.Equal(t, "1h", gjson.GetBytes(result, "system.0.cache_control.ttl").String())
	require.Equal(t, "5m", gjson.GetBytes(result, "system.1.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(result, "system.2.cache_control.ttl").Exists())
}

func TestNormalizeCacheControlTTLOrder_PreservesBytesWhenNoChange(t *testing.T) {
	body := []byte(`{"tools":[{"name":"a","cache_control":{"type":"ephemeral","ttl":"1h"}}],"system":[{"type":"text","text":"<sys>&</sys>","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)

	result := normalizeCacheControlTTLOrder(body)

	require.Equal(t, body, result)
}

func TestNormalizeClaudeCodeMimicryUpstreamBody_CombinesProtocolFixes(t *testing.T) {
	body := []byte(`{"tools":[{"name":"Bash","input_schema":{},"cache_control":{"type":"ephemeral","ttl":"5m"}}],"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"1h"}}],"messages":[],"tool_choice":{"type":"tool","name":"Bash"},"thinking":{"type":"enabled","budget_tokens":32000},"output_config":{"effort":"max"}}`)

	result := normalizeClaudeCodeMimicryUpstreamBody(body)

	require.False(t, gjson.GetBytes(result, "thinking").Exists())
	require.False(t, gjson.GetBytes(result, "output_config").Exists())
	require.Equal(t, "5m", gjson.GetBytes(result, "tools.0.cache_control.ttl").String())
	// tools.0 是 5m,排在前;system.0 的 1h 在后 → 被降级(删 ttl)以满足单调不增。
	require.False(t, gjson.GetBytes(result, "system.0.cache_control.ttl").Exists())
}

func TestNormalizeClaudeCodeMimicryUpstreamBody_OrdersCapturedOpusKeys(t *testing.T) {
	body := []byte(`{"stream":true,"output_config":{"effort":"max"},"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"thinking":{"type":"adaptive","display":"summarized"},"max_tokens":64000,"metadata":{"user_id":"u"},"tools":[],"system":[],"messages":[],"model":"claude-opus-4-7","extra":1}`)

	result := normalizeClaudeCodeMimicryUpstreamBody(body)

	require.Equal(t, []string{
		"model",
		"messages",
		"system",
		"tools",
		"metadata",
		"max_tokens",
		"thinking",
		"context_management",
		"output_config",
		"stream",
		"extra",
	}, topLevelJSONKeysInOrder(t, result))
}

func TestNormalizeClaudeCodeMimicryUpstreamBody_OrdersCapturedHaikuKeys(t *testing.T) {
	body := []byte(`{"stream":true,"output_config":{"format":{"type":"json_schema"}},"temperature":1,"max_tokens":32000,"metadata":{"user_id":"u"},"tools":[],"system":[],"messages":[],"model":"claude-haiku-4-5","extra":1}`)

	result := normalizeClaudeCodeMimicryUpstreamBody(body)

	require.Equal(t, []string{
		"model",
		"messages",
		"system",
		"tools",
		"metadata",
		"max_tokens",
		"temperature",
		"output_config",
		"stream",
		"extra",
	}, topLevelJSONKeysInOrder(t, result))
}

func TestGatewayCacheTTLGlobalSetting_TargetResolution(t *testing.T) {
	repo := &gatewayTTLSettingRepo{data: map[string]string{
		SettingKeyEnableAnthropicCacheTTL1hInjection: "true",
	}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	svc := &GatewayService{
		settingService: NewSettingService(repo, &config.Config{}),
	}
	account := &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	target, ok := svc.resolveCacheTTLUsageOverrideTarget(context.Background(), account)
	require.False(t, ok)
	require.Empty(t, target)

	account.Extra = map[string]any{
		"cache_ttl_override_enabled": true,
		"cache_ttl_override_target":  "1h",
	}
	target, ok = svc.resolveCacheTTLUsageOverrideTarget(context.Background(), account)
	require.True(t, ok)
	require.Equal(t, cacheTTLTarget1h, target)
}

func TestGatewayCacheTTLGlobalSetting_RequestInjectionScope(t *testing.T) {
	repo := &gatewayTTLSettingRepo{data: map[string]string{
		SettingKeyEnableAnthropicCacheTTL1hInjection: "true",
	}}
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	svc := &GatewayService{
		settingService: NewSettingService(repo, &config.Config{}),
	}

	require.True(t, svc.shouldInjectAnthropicCacheTTL1h(context.Background(), &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}))
	require.True(t, svc.shouldInjectAnthropicCacheTTL1h(context.Background(), &Account{Platform: PlatformAnthropic, Type: AccountTypeSetupToken}))
	require.False(t, svc.shouldInjectAnthropicCacheTTL1h(context.Background(), &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey}))
	require.False(t, svc.shouldInjectAnthropicCacheTTL1h(context.Background(), &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))

	repo.data[SettingKeyEnableAnthropicCacheTTL1hInjection] = "false"
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{})
	require.False(t, svc.shouldInjectAnthropicCacheTTL1h(context.Background(), &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth}))
}
