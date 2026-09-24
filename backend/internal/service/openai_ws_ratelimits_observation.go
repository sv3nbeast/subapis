package service

import (
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// codex.rate_limits 是 Codex WS 传输携带限流信息的**唯一**通道：HTTP 路径把同一份
// 数据放在 x-codex-* 响应头里（见 ParseCodexRateLimitHeaders），WS 路径则以独立事件下发。
// 生产实测（2026-09-24，账号 2676）该事件是 account 级限流拒绝的前导事件：
//
//	read_fail close_status=1000(StatusNormalClosure) events=2 token_events=0
//	  first_event=codex.rate_limits last_event=codex.response.metadata
//
// 即上游先发限流快照、再发 metadata，然后零 token 干净关闭。历史上网关只把事件名
// 用于计数，内容从不解析，导致这类拒绝被归类为可重试的瞬时读错误 read_event，
// 在同一账号上重试至预算耗尽并返回通用 502。
//
// 本文件只做**观测**：解析出限流窗口的数值并记录字段形态，不写入账号状态、
// 不影响调度或重试决策。事件载荷的确切形状尚未在生产留下样本（从未被解析过），
// 因此解析器对多种合理形状保持兼容，并记录实际观测到的键名以便后续收敛。
const (
	openAIWSCodexRateLimitsEventType = "codex.rate_limits"

	// openAIWSRateLimitsKeySampleLimit 限制记录的键名个数，避免畸形载荷撑爆日志。
	openAIWSRateLimitsKeySampleLimit = 24

	// 递归路径观测的边界。生产实测（2026-09-24，账号 2678）该事件根对象有 7 个子对象
	// （plan_type/rate_limits/code_review_rate_limits/additional_rate_limits/credits/promo），
	// 需要知道嵌套结构才能确定窗口与额度字段的真实位置，因此记录受限的键路径。
	// 深度与条数都必须有界，否则畸形载荷会用超长路径撑爆日志。
	openAIWSRateLimitsPathDepthLimit    = 4
	openAIWSRateLimitsPathSampleLimit   = 40
	openAIWSRateLimitsPathValueMaxRunes = 48
)

// openAIWSCodexRateLimitsObservation 是一次 codex.rate_limits 事件的结构化观测结果。
//
// 只承载数值、布尔与协议字段名，不承载提示内容、工具参数或任何用户数据。
type openAIWSCodexRateLimitsObservation struct {
	// RootKeys / PayloadKeys 记录实际出现的键名，用于确认上游真实载荷形态。
	// 键名是协议字段名而非用户内容，因此可以安全落盘。
	RootKeys    []string
	PayloadKeys []string

	// KeyPaths 是受限深度的 "a.b.c=值" 路径样本，用于确认嵌套结构。
	// 字符串值默认只记长度（<string:N>），仅少数协议字段记明文；见
	// openAIWSRateLimitsPlaintextValueKeys。
	KeyPaths []string

	// 准入判定。生产实测该事件含 allowed / limit_reached 两个键，
	// 它们是上游对"本请求能否被服务"的显式决定，比按百分比阈值推断可靠。
	Allowed      *bool
	LimitReached *bool

	// 窗口键的**存在性**。用于区分"键缺失"与"键存在但为 null"——
	// 生产样本中 secondary 解析不出数值，需确认属于哪种。
	PrimaryPresent   bool
	SecondaryPresent bool

	// 归一化后的窗口数值，找不到即为 nil。
	PrimaryUsedPercent   *float64
	PrimaryWindowMinutes *int
	PrimaryResetSeconds  *int

	SecondaryUsedPercent   *float64
	SecondaryWindowMinutes *int
	SecondaryResetSeconds  *int

	// PrimaryOverSecondaryPercent 是超限比例（x-codex-primary-over-secondary-limit-percent 的 WS 对应项）。
	PrimaryOverSecondaryPercent *float64

	// HasAnyValue 表示至少解析出一个数值。全空时仍会记录键名，便于发现"事件存在但字段名不同"。
	HasAnyValue bool
}

// parseOpenAIWSCodexRateLimitsObservation 从 codex.rate_limits 事件载荷解析限流快照。
//
// 形状兼容策略（载荷形态未经生产样本确认，故做防御性匹配）：
//   - 载荷可能在根上，也可能嵌在 "rate_limits" 子对象里；
//   - 窗口可能命名为 primary/secondary，也可能直接用 5h/7d；
//   - 数值字段可能是 {used_percent, window_minutes, reset_after_seconds} 的嵌套对象，
//     也可能是扁平的 <window>_used_percent 形式。
//
// 解析不到任何数值不算错误：函数仍返回键名观测结果，调用方据此记录"事件到达但形状未知"。
func parseOpenAIWSCodexRateLimitsObservation(message []byte) openAIWSCodexRateLimitsObservation {
	var obs openAIWSCodexRateLimitsObservation
	if len(message) == 0 {
		return obs
	}
	root := gjson.ParseBytes(message)
	if !root.Exists() {
		return obs
	}
	obs.RootKeys = sampleOpenAIWSJSONKeys(root, openAIWSRateLimitsKeySampleLimit)
	obs.KeyPaths = sampleOpenAIWSJSONKeyPaths(root)

	payload := root.Get("rate_limits")
	if !payload.Exists() || !payload.IsObject() {
		// 兼容根对象直接承载窗口字段的情况。
		payload = root
	} else {
		obs.PayloadKeys = sampleOpenAIWSJSONKeys(payload, openAIWSRateLimitsKeySampleLimit)
	}

	obs.Allowed = openAIWSOptionalBool(payload.Get("allowed"))
	obs.LimitReached = openAIWSOptionalBool(payload.Get("limit_reached"))
	obs.PrimaryPresent = payload.Get("primary").Exists() || payload.Get("7d").Exists()
	obs.SecondaryPresent = payload.Get("secondary").Exists() || payload.Get("5h").Exists()

	obs.PrimaryUsedPercent, obs.PrimaryWindowMinutes, obs.PrimaryResetSeconds =
		readOpenAIWSRateLimitWindow(payload, "primary", "7d")
	obs.SecondaryUsedPercent, obs.SecondaryWindowMinutes, obs.SecondaryResetSeconds =
		readOpenAIWSRateLimitWindow(payload, "secondary", "5h")

	obs.PrimaryOverSecondaryPercent = firstOpenAIWSNumber(payload,
		"primary_over_secondary_limit_percent",
		"primary_over_secondary_percent",
	)

	obs.HasAnyValue = obs.PrimaryUsedPercent != nil || obs.PrimaryWindowMinutes != nil ||
		obs.PrimaryResetSeconds != nil || obs.SecondaryUsedPercent != nil ||
		obs.SecondaryWindowMinutes != nil || obs.SecondaryResetSeconds != nil ||
		obs.PrimaryOverSecondaryPercent != nil
	return obs
}

// readOpenAIWSRateLimitWindow 读取一个限流窗口的 used_percent / window_minutes / reset 秒数。
// 先试 <name> 嵌套对象，再试 <alias> 嵌套对象，最后回退到扁平键。
func readOpenAIWSRateLimitWindow(payload gjson.Result, name, alias string) (*float64, *int, *int) {
	for _, key := range []string{name, alias} {
		nested := payload.Get(key)
		if !nested.Exists() || !nested.IsObject() {
			continue
		}
		used := firstOpenAIWSNumber(nested, "used_percent", "used_percentage", "utilization")
		window := firstOpenAIWSInt(nested, "window_minutes", "window_min", "window_length_minutes")
		reset := firstOpenAIWSInt(nested, "reset_after_seconds", "reset_in_seconds", "resets_in_seconds")
		if used != nil || window != nil || reset != nil {
			return used, window, reset
		}
	}
	used := firstOpenAIWSNumber(payload, name+"_used_percent", alias+"_used_percent")
	window := firstOpenAIWSInt(payload, name+"_window_minutes", alias+"_window_minutes")
	reset := firstOpenAIWSInt(payload, name+"_reset_after_seconds", alias+"_reset_after_seconds")
	return used, window, reset
}

// firstOpenAIWSNumber 返回第一个存在且为数值的键。
func firstOpenAIWSNumber(node gjson.Result, keys ...string) *float64 {
	for _, key := range keys {
		value := node.Get(key)
		if !value.Exists() || value.Type != gjson.Number {
			continue
		}
		number := value.Float()
		return &number
	}
	return nil
}

// firstOpenAIWSInt 返回第一个存在且为数值的键，取整。
func firstOpenAIWSInt(node gjson.Result, keys ...string) *int {
	number := firstOpenAIWSNumber(node, keys...)
	if number == nil {
		return nil
	}
	value := int(*number)
	return &value
}

// sampleOpenAIWSJSONKeys 取对象的顶层键名样本（截断到 limit）。
// 键名是协议字段名，不含用户内容，可安全用于日志。
func sampleOpenAIWSJSONKeys(node gjson.Result, limit int) []string {
	if !node.Exists() || !node.IsObject() || limit <= 0 {
		return nil
	}
	keys := make([]string, 0, 8)
	node.ForEach(func(key, _ gjson.Result) bool {
		name := strings.TrimSpace(key.String())
		if name == "" {
			return true
		}
		keys = append(keys, name)
		return len(keys) < limit
	})
	if len(keys) == 0 {
		return nil
	}
	return keys
}

// openAIWSRateLimitsPlaintextValueKeys 是允许在 KeyPaths 里记录明文值的叶子键。
// 它们是上游的枚举/协议取值（如 plan_type=pro、limit_reached=true），不会承载用户内容。
// 其余字符串叶子一律只记长度——即使在这个本应纯协议的事件里，也不假设上游不会放文本。
var openAIWSRateLimitsPlaintextValueKeys = map[string]struct{}{
	"type":          {},
	"plan_type":     {},
	"limit_reached": {},
	"allowed":       {},
	"unlimited":     {},
	"has_credits":   {},
	"window":        {},
	"limit_id":      {},
	"limit_name":    {},
}

// sampleOpenAIWSJSONKeyPaths 以受限深度与条数遍历对象，返回 "a.b=值" 形式的路径样本。
//
// 值的渲染规则：数值与布尔原样；null 记 null；对象记 {}（仅在达到深度上限时）；
// 数组记 [N]；字符串默认记 <string:N>，仅白名单键记截断后的明文。
// 这样既能看清结构，又不会把任意字符串内容写进日志。
func sampleOpenAIWSJSONKeyPaths(root gjson.Result) []string {
	if !root.Exists() || !root.IsObject() {
		return nil
	}
	paths := make([]string, 0, 16)
	var walk func(node gjson.Result, prefix string, depth int) bool
	walk = func(node gjson.Result, prefix string, depth int) bool {
		keepGoing := true
		node.ForEach(func(key, value gjson.Result) bool {
			if len(paths) >= openAIWSRateLimitsPathSampleLimit {
				keepGoing = false
				return false
			}
			name := strings.TrimSpace(key.String())
			if name == "" {
				return true
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if value.IsObject() && depth+1 < openAIWSRateLimitsPathDepthLimit {
				if !walk(value, path, depth+1) {
					keepGoing = false
					return false
				}
				return true
			}
			paths = append(paths, path+"="+renderOpenAIWSRateLimitsPathValue(name, value))
			return true
		})
		return keepGoing
	}
	walk(root, "", 0)
	if len(paths) == 0 {
		return nil
	}
	return paths
}

func renderOpenAIWSRateLimitsPathValue(key string, value gjson.Result) string {
	switch value.Type {
	case gjson.Null:
		return "null"
	case gjson.True:
		return "true"
	case gjson.False:
		return "false"
	case gjson.Number:
		return strconv.FormatFloat(value.Float(), 'f', -1, 64)
	case gjson.String:
		if _, ok := openAIWSRateLimitsPlaintextValueKeys[key]; ok {
			text := value.String()
			if runes := []rune(text); len(runes) > openAIWSRateLimitsPathValueMaxRunes {
				text = string(runes[:openAIWSRateLimitsPathValueMaxRunes]) + "..."
			}
			return text
		}
		return "<string:" + strconv.Itoa(len(value.String())) + ">"
	}
	if value.IsArray() {
		return "[" + strconv.Itoa(len(value.Array())) + "]"
	}
	if value.IsObject() {
		return "{}"
	}
	return "?"
}

// openAIWSOptionalBool 只接受 JSON 布尔；缺失、null 或其他类型都返回 nil，
// 避免把 "true" 字符串之类的意外形态误读成准入决定。
func openAIWSOptionalBool(value gjson.Result) *bool {
	switch value.Type {
	case gjson.True:
		v := true
		return &v
	case gjson.False:
		v := false
		return &v
	default:
		return nil
	}
}

func openAIWSFormatOptionalBool(value *bool) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatBool(*value)
}

// joinOpenAIWSKeySample 把键名样本拼成一行日志值。
func joinOpenAIWSKeySample(keys []string) string {
	if len(keys) == 0 {
		return "-"
	}
	return strings.Join(keys, ",")
}

// openAIWSFormatOptionalFloat / openAIWSFormatOptionalInt 把可能为 nil 的数值
// 渲染成日志友好的字符串，nil 记为 "-"。限流百分比与窗口分钟都是小数字，
// 用最短表示即可。
func openAIWSFormatOptionalFloat(value *float64) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}

func openAIWSFormatOptionalInt(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}

// logOpenAIWSCodexRateLimitsObservation 记录一次 codex.rate_limits 事件的结构化观测。
//
// 只记录数值与协议字段名，绝不记录提示内容、工具参数或凭证。事件在 Debug 级别关闭时
// 也必须可见：这类事件正是限流型拒绝的前导信号，属于诊断刚需而非调试细节。
func logOpenAIWSCodexRateLimitsObservation(accountID int64, connID string, eventIndex int, message []byte) {
	obs := parseOpenAIWSCodexRateLimitsObservation(message)
	logOpenAIWSModeInfo(
		"codex_rate_limits account_id=%d conn_id=%s idx=%d has_value=%v "+
			"allowed=%s limit_reached=%s primary_present=%v secondary_present=%v "+
			"primary_used_percent=%s primary_window_minutes=%s primary_reset_after_seconds=%s "+
			"secondary_used_percent=%s secondary_window_minutes=%s secondary_reset_after_seconds=%s "+
			"primary_over_secondary_percent=%s root_keys=%s payload_keys=%s key_paths=%s",
		accountID,
		truncateOpenAIWSLogValue(connID, openAIWSIDValueMaxLen),
		eventIndex,
		obs.HasAnyValue,
		openAIWSFormatOptionalBool(obs.Allowed),
		openAIWSFormatOptionalBool(obs.LimitReached),
		obs.PrimaryPresent,
		obs.SecondaryPresent,
		openAIWSFormatOptionalFloat(obs.PrimaryUsedPercent),
		openAIWSFormatOptionalInt(obs.PrimaryWindowMinutes),
		openAIWSFormatOptionalInt(obs.PrimaryResetSeconds),
		openAIWSFormatOptionalFloat(obs.SecondaryUsedPercent),
		openAIWSFormatOptionalInt(obs.SecondaryWindowMinutes),
		openAIWSFormatOptionalInt(obs.SecondaryResetSeconds),
		openAIWSFormatOptionalFloat(obs.PrimaryOverSecondaryPercent),
		truncateOpenAIWSLogValue(joinOpenAIWSKeySample(obs.RootKeys), openAIWSLogValueMaxLen),
		truncateOpenAIWSLogValue(joinOpenAIWSKeySample(obs.PayloadKeys), openAIWSLogValueMaxLen),
		// 路径样本本身已按条数与值长度封顶，这里再给整行一个独立上限：
		// 通用的 160 字符上限会截掉大部分结构信息，失去这条日志的意义。
		truncateOpenAIWSLogValue(joinOpenAIWSKeySample(obs.KeyPaths), openAIWSRateLimitsKeyPathsLogMaxLen),
	)
}

// openAIWSRateLimitsKeyPathsLogMaxLen 是 key_paths 字段的整行上限。
// 40 条路径 × 典型 40 字节 ≈ 1.6KB，4KB 足够容纳完整结构且仍有硬上限。
const openAIWSRateLimitsKeyPathsLogMaxLen = 4096
