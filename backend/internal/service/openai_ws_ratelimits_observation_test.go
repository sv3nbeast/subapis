package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseOpenAIWSCodexRateLimitsObservationNestedShape 覆盖 primary/secondary 嵌套对象形态
// ——与 x-codex-* 响应头最接近的一种，也是最可能的上游形状。
func TestParseOpenAIWSCodexRateLimitsObservationNestedShape(t *testing.T) {
	message := []byte(`{
		"type": "codex.rate_limits",
		"rate_limits": {
			"primary":   {"used_percent": 58.0, "window_minutes": 10080, "reset_after_seconds": 271733},
			"secondary": {"used_percent": 0.0,  "window_minutes": 0,     "reset_after_seconds": 0}
		}
	}`)

	obs := parseOpenAIWSCodexRateLimitsObservation(message)

	require.True(t, obs.HasAnyValue)
	require.NotNil(t, obs.PrimaryUsedPercent)
	require.InDelta(t, 58.0, *obs.PrimaryUsedPercent, 1e-9)
	require.NotNil(t, obs.PrimaryWindowMinutes)
	require.Equal(t, 10080, *obs.PrimaryWindowMinutes)
	require.NotNil(t, obs.PrimaryResetSeconds)
	require.Equal(t, 271733, *obs.PrimaryResetSeconds)

	// 0 是合法数值，不能被当成"缺失"。
	require.NotNil(t, obs.SecondaryUsedPercent)
	require.InDelta(t, 0.0, *obs.SecondaryUsedPercent, 1e-9)
	require.NotNil(t, obs.SecondaryWindowMinutes)
	require.Equal(t, 0, *obs.SecondaryWindowMinutes)

	require.Contains(t, obs.PayloadKeys, "primary")
	require.Contains(t, obs.PayloadKeys, "secondary")
	require.Contains(t, obs.RootKeys, "type")
}

// TestParseOpenAIWSCodexRateLimitsObservationFlatShape 覆盖扁平键与 5h/7d 别名形态：
// 载荷形状未经生产样本确认，两种命名都必须能解析。
func TestParseOpenAIWSCodexRateLimitsObservationFlatShape(t *testing.T) {
	message := []byte(`{
		"type": "codex.rate_limits",
		"rate_limits": {
			"7d_used_percent": 58,
			"7d_window_minutes": 10080,
			"5h_used_percent": 12.5,
			"5h_window_minutes": 300,
			"5h_reset_after_seconds": 900
		}
	}`)

	obs := parseOpenAIWSCodexRateLimitsObservation(message)

	require.True(t, obs.HasAnyValue)
	// 7d 是 primary 的别名。
	require.NotNil(t, obs.PrimaryUsedPercent)
	require.InDelta(t, 58.0, *obs.PrimaryUsedPercent, 1e-9)
	// 5h 是 secondary 的别名。
	require.NotNil(t, obs.SecondaryUsedPercent)
	require.InDelta(t, 12.5, *obs.SecondaryUsedPercent, 1e-9)
	require.NotNil(t, obs.SecondaryWindowMinutes)
	require.Equal(t, 300, *obs.SecondaryWindowMinutes)
	require.NotNil(t, obs.SecondaryResetSeconds)
	require.Equal(t, 900, *obs.SecondaryResetSeconds)
}

// TestParseOpenAIWSCodexRateLimitsObservationUnknownShapeKeepsKeys 固化"事件到达但形状未知"
// 这一情况：必须仍然返回键名，否则无法发现上游改了字段名。
func TestParseOpenAIWSCodexRateLimitsObservationUnknownShapeKeepsKeys(t *testing.T) {
	message := []byte(`{"type":"codex.rate_limits","rate_limits":{"something_new":{"foo":1}}}`)

	obs := parseOpenAIWSCodexRateLimitsObservation(message)

	require.False(t, obs.HasAnyValue, "未知形状不应伪造出数值")
	require.Contains(t, obs.PayloadKeys, "something_new")
	require.Contains(t, obs.RootKeys, "type")
	// 数值全缺失时日志仍要有可读输出，而不是空串。
	require.Equal(t, "-", openAIWSFormatOptionalFloat(obs.PrimaryUsedPercent))
	require.Equal(t, "-", openAIWSFormatOptionalInt(obs.SecondaryWindowMinutes))
	require.Equal(t, "something_new", joinOpenAIWSKeySample(obs.PayloadKeys))
}

// TestParseOpenAIWSCodexRateLimitsObservationRootShape 覆盖窗口字段直接挂在根对象上的形态。
func TestParseOpenAIWSCodexRateLimitsObservationRootShape(t *testing.T) {
	message := []byte(`{
		"type": "codex.rate_limits",
		"primary": {"used_percent": 58, "window_minutes": 10080}
	}`)

	obs := parseOpenAIWSCodexRateLimitsObservation(message)

	require.True(t, obs.HasAnyValue)
	require.NotNil(t, obs.PrimaryUsedPercent)
	require.InDelta(t, 58.0, *obs.PrimaryUsedPercent, 1e-9)
	require.Empty(t, obs.PayloadKeys, "根形态下没有独立的 rate_limits 子对象")
}

// TestParseOpenAIWSCodexRateLimitsObservationEmptyAndMalformed 固化空/畸形输入不 panic。
func TestParseOpenAIWSCodexRateLimitsObservationEmptyAndMalformed(t *testing.T) {
	for _, message := range [][]byte{
		nil,
		{},
		[]byte("not json"),
		[]byte(`{"type":"codex.rate_limits"}`),
		[]byte(`{"type":"codex.rate_limits","rate_limits":[]}`),
	} {
		obs := parseOpenAIWSCodexRateLimitsObservation(message)
		require.False(t, obs.HasAnyValue, "输入 %q 不应解析出数值", string(message))
	}
}

// TestParseOpenAIWSCodexRateLimitsObservationDoesNotLeakContent 固化"只记字段名与数值"：
// 观测结构里不允许出现提示内容或工具参数。
func TestParseOpenAIWSCodexRateLimitsObservationDoesNotLeakContent(t *testing.T) {
	message := []byte(`{
		"type": "codex.rate_limits",
		"prompt": "SECRET_PROMPT_TEXT",
		"tool_arguments": "SECRET_TOOL_ARGS",
		"rate_limits": {"primary": {"used_percent": 58}}
	}`)

	obs := parseOpenAIWSCodexRateLimitsObservation(message)
	encoded, err := json.Marshal(obs)
	require.NoError(t, err)

	// 键名会出现在 RootKeys 里（它们是协议字段名），但字段**值**绝不能出现。
	require.NotContains(t, string(encoded), "SECRET_PROMPT_TEXT")
	require.NotContains(t, string(encoded), "SECRET_TOOL_ARGS")
}

// TestOpenAIWSCodexRateLimitsEventTypeMatchesProductionSignature 固化生产观测到的事件名。
// 生产证据（2026-09-24）：read_fail first_event=codex.rate_limits，即该字符串必须与
// 上游实际下发的事件名逐字一致，否则解析器永远不会被触发。
func TestOpenAIWSCodexRateLimitsEventTypeMatchesProductionSignature(t *testing.T) {
	require.Equal(t, "codex.rate_limits", openAIWSCodexRateLimitsEventType)

	// 事件名经 parseOpenAIWSEventEnvelope 从 "type" 取出，需与常量一致。
	eventType, _, _ := parseOpenAIWSEventEnvelope([]byte(`{"type":"codex.rate_limits"}`))
	require.Equal(t, openAIWSCodexRateLimitsEventType, eventType)
}
