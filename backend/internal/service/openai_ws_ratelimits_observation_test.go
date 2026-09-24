package service

import (
	"encoding/json"
	"strconv"
	"strings"
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

// productionRateLimitsShape 复刻 2026-09-24 生产首批样本（账号 2678）已确认的结构：
// 根 7 个键，rate_limits 下 4 个键（allowed/limit_reached/primary/secondary）。
// 叶子字段名与值为构造数据；真实叶子字段名正是本次扩展要观测的对象。
const productionRateLimitsShape = `{
	"type": "codex.rate_limits",
	"plan_type": "pro",
	"rate_limits": {
		"allowed": false,
		"limit_reached": true,
		"primary":   {"used_percent": 91, "window_minutes": 10080, "reset_after_seconds": 269097},
		"secondary": null
	},
	"code_review_rate_limits": {"allowed": true, "limit_reached": false},
	"additional_rate_limits": [],
	"credits": {"has_credits": false, "unlimited": false, "balance": "0"},
	"promo": {"message": "PROMO_TEXT_SHOULD_NOT_LEAK"}
}`

// TestCodexRateLimitsObservationAdmissionFields 固化准入字段的解析：
// allowed / limit_reached 是上游对本请求能否被服务的显式决定。
func TestCodexRateLimitsObservationAdmissionFields(t *testing.T) {
	obs := parseOpenAIWSCodexRateLimitsObservation([]byte(productionRateLimitsShape))

	require.NotNil(t, obs.Allowed)
	require.False(t, *obs.Allowed)
	require.NotNil(t, obs.LimitReached)
	require.True(t, *obs.LimitReached)
	require.Equal(t, "false", openAIWSFormatOptionalBool(obs.Allowed))
	require.Equal(t, "true", openAIWSFormatOptionalBool(obs.LimitReached))

	// 准入字段只认 rate_limits 下的值，不能被 code_review_rate_limits 的同名键覆盖。
	require.True(t, *obs.LimitReached, "code_review_rate_limits.limit_reached=false 不得覆盖主窗口判定")
}

// TestCodexRateLimitsObservationSecondaryNullIsPresentButEmpty 固化生产样本里
// secondary 解析出 "-" 的成因区分：键存在但为 null，与键缺失是两回事。
func TestCodexRateLimitsObservationSecondaryNullIsPresentButEmpty(t *testing.T) {
	obs := parseOpenAIWSCodexRateLimitsObservation([]byte(productionRateLimitsShape))

	require.True(t, obs.PrimaryPresent)
	require.True(t, obs.SecondaryPresent, "secondary=null 必须被识别为存在")
	require.Nil(t, obs.SecondaryUsedPercent, "null 不得被解析成 0")

	missing := parseOpenAIWSCodexRateLimitsObservation([]byte(`{"type":"codex.rate_limits","rate_limits":{"primary":{"used_percent":1}}}`))
	require.False(t, missing.SecondaryPresent, "键缺失时必须报告不存在")
}

// TestCodexRateLimitsObservationKeyPathsRevealStructureWithoutContent 固化路径观测：
// 结构完整可见，但任意字符串值只记长度。
func TestCodexRateLimitsObservationKeyPathsRevealStructureWithoutContent(t *testing.T) {
	obs := parseOpenAIWSCodexRateLimitsObservation([]byte(productionRateLimitsShape))
	joined := joinOpenAIWSKeySample(obs.KeyPaths)

	for _, want := range []string{
		"type=codex.rate_limits",
		"plan_type=pro",
		"rate_limits.allowed=false",
		"rate_limits.limit_reached=true",
		"rate_limits.primary.used_percent=91",
		"rate_limits.primary.window_minutes=10080",
		"rate_limits.secondary=null",
		"code_review_rate_limits.limit_reached=false",
		"additional_rate_limits=[0]",
		"credits.has_credits=false",
		// balance 不在白名单：即使是 "0" 也只记长度。
		"credits.balance=<string:1>",
		"promo.message=<string:26>",
	} {
		require.Contains(t, joined, want)
	}
	require.NotContains(t, joined, "PROMO_TEXT_SHOULD_NOT_LEAK", "非白名单字符串值不得落盘")
}

// TestCodexRateLimitsObservationKeyPathsAreBounded 固化深度与条数上限，
// 防止畸形载荷撑爆日志。
func TestCodexRateLimitsObservationKeyPathsAreBounded(t *testing.T) {
	// 深度：超过上限的对象以 {} 收尾，不再展开。
	deep := `{"a":{"b":{"c":{"d":{"e":{"f":1}}}}}}`
	obs := parseOpenAIWSCodexRateLimitsObservation([]byte(deep))
	require.Equal(t, []string{"a.b.c.d={}"}, obs.KeyPaths)

	// 条数：宽对象被截断到上限。
	var wide strings.Builder
	wide.WriteString(`{`)
	for i := 0; i < 200; i++ {
		if i > 0 {
			wide.WriteString(",")
		}
		wide.WriteString(`"k` + strconv.Itoa(i) + `":` + strconv.Itoa(i))
	}
	wide.WriteString(`}`)
	obs = parseOpenAIWSCodexRateLimitsObservation([]byte(wide.String()))
	require.Len(t, obs.KeyPaths, openAIWSRateLimitsPathSampleLimit)

	// 白名单键的明文值也有长度上限。
	long := `{"plan_type":"` + strings.Repeat("x", 500) + `"}`
	obs = parseOpenAIWSCodexRateLimitsObservation([]byte(long))
	require.Len(t, obs.KeyPaths, 1)
	require.LessOrEqual(t, len(obs.KeyPaths[0]), len("plan_type=")+openAIWSRateLimitsPathValueMaxRunes+len("..."))
}

// TestCodexRateLimitsObservationBoolOnlyAcceptsJSONBool 固化准入字段只接受 JSON 布尔。
func TestCodexRateLimitsObservationBoolOnlyAcceptsJSONBool(t *testing.T) {
	for _, raw := range []string{`"true"`, `1`, `null`, `{}`} {
		obs := parseOpenAIWSCodexRateLimitsObservation([]byte(`{"rate_limits":{"limit_reached":` + raw + `}}`))
		require.Nil(t, obs.LimitReached, "limit_reached=%s 不得被读成布尔", raw)
	}
}
