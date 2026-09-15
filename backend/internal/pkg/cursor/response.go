package cursor

import (
	"encoding/json"
	"io"
	"math"
)

// TokenUsage is agent.v1.TurnEndedUpdate / TokenDeltaUpdate usage.
// Turn-ended counts are Anthropic-style: input is uncached; cache read/write
// are separate. Token deltas are incremental output tokens used as a fallback
// when turn_ended omits totals.
type TokenUsage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
}

// Empty reports whether every count is zero.
func (u TokenUsage) Empty() bool {
	return u.InputTokens == 0 &&
		u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 &&
		u.CacheWriteTokens == 0 &&
		u.ReasoningTokens == 0
}

// UsageAccumulator prefers turn_ended totals and falls back to summed token_delta.
type UsageAccumulator struct {
	usage         TokenUsage
	fromTurnEnded bool
}

// Observe records usage from a parsed stream event.
func (a *UsageAccumulator) Observe(ev StreamEvent) {
	if a == nil || ev.Usage == nil {
		return
	}
	switch ev.Type {
	case "turn_ended":
		if !ev.Usage.Empty() {
			a.usage = *ev.Usage
			a.fromTurnEnded = true
		}
	case "token_delta":
		if !a.fromTurnEnded {
			a.usage.OutputTokens += ev.Usage.OutputTokens
		}
	}
}

// Result returns the accumulated usage.
func (a *UsageAccumulator) Result() TokenUsage {
	if a == nil {
		return TokenUsage{}
	}
	return a.usage
}

// StreamEvent represents a parsed piece of a Cursor streaming response.
type StreamEvent struct {
	Type  string // "text", "thinking", "turn_ended", "token_delta", "tool_call"
	Text  string
	Usage *TokenUsage
	Tool  *ToolCall
}

// ToolCall is one tool invocation the agent asked for. Cursor streams it as a
// start event naming the tool, argument deltas, then an end event; Partial is
// false only on the terminating event.
type ToolCall struct {
	ID      string
	Name    string
	ArgsRaw string
	Partial bool
}

// ParseResponseFrame extracts StreamEvents from an agent.v1.AgentServerMessage
// Connect-RPC protobuf payload.
func ParseResponseFrame(data []byte) []StreamEvent {
	events, _ := parseAgentServerMessage(data)
	return events
}

func parseAgentServerMessage(data []byte) ([]StreamEvent, bool) {
	var events []StreamEvent
	handled := false
	pr := NewProtobufReader(data)
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			break
		}
		if f.WireType != WireBytes {
			continue
		}
		switch f.Num {
		case fieldAgentServerInteraction:
			evs, ok := parseInteractionUpdate(f.Data)
			if ok {
				handled = true
				events = append(events, evs...)
			}
		case fieldAgentServerKV:
			handled = true
		case fieldAgentServerExec:
			// exec 帧携带的是工具调用（实测 field 11 就是调用体），必须解析出来
			// 交给客户端；否则客户端只会看到一段文字，永远拿不到 tool_use。
			handled = true
			if GetNested(f.Data, fieldExecInvocation) != nil {
				if call := parseToolCall(f.Data, false); call != nil {
					events = append(events, StreamEvent{Type: "tool_call", Tool: call})
				}
			}
		}
	}
	return events, handled
}

func parseInteractionUpdate(data []byte) ([]StreamEvent, bool) {
	var events []StreamEvent
	handled := false
	pr := NewProtobufReader(data)
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			break
		}
		switch f.Num {
		case fieldInteractionTextDelta:
			handled = true
			if f.WireType == WireBytes {
				notice := false
				inner := NewProtobufReader(f.Data)
				var text string
				for {
					sf, serr := inner.Next()
					if sf == nil || serr != nil {
						break
					}
					switch sf.Num {
					case fieldTextDeltaText:
						text = string(sf.Data)
					case fieldTextDeltaServerNotice:
						notice = sf.Varint != 0
					}
				}
				if text != "" && !notice {
					events = append(events, StreamEvent{Type: "text", Text: text})
				}
			}
		case fieldInteractionThinkingDelta:
			handled = true
			if f.WireType == WireBytes {
				text := GetString(f.Data, fieldThinkingDeltaText)
				if text != "" {
					events = append(events, StreamEvent{Type: "thinking", Text: text})
				}
			}
		case fieldInteractionTokenDelta:
			handled = true
			if f.WireType == WireBytes {
				if ev := parseTokenDelta(f.Data); ev != nil {
					events = append(events, *ev)
				}
			}
		case fieldInteractionToolStart:
			handled = true
			if f.WireType == WireBytes {
				if call := parseToolCall(f.Data, true); call != nil {
					events = append(events, StreamEvent{Type: "tool_call", Tool: call})
				}
			}
		case fieldInteractionToolDelta, fieldInteractionToolDelta2:
			handled = true
			if f.WireType == WireBytes {
				if call := parseToolCall(f.Data, true); call != nil {
					events = append(events, StreamEvent{Type: "tool_call", Tool: call})
				}
			}
		case fieldInteractionToolEnd:
			handled = true
			if f.WireType == WireBytes {
				if call := parseToolCall(f.Data, false); call != nil {
					events = append(events, StreamEvent{Type: "tool_call", Tool: call})
				}
			}
		case fieldInteractionHeartbeat:
			handled = true
		case fieldInteractionTurnEnded:
			handled = true
			events = append(events, parseTurnEnded(f.Data))
		default:
			if f.WireType == WireBytes && f.Num >= 2 && f.Num <= 24 {
				handled = true
			}
		}
	}
	return events, handled
}

func parseTurnEnded(data []byte) StreamEvent {
	ev := StreamEvent{Type: "turn_ended"}
	if len(data) == 0 {
		return ev
	}
	u := parseTokenUsage(data)
	if !u.Empty() {
		ev.Usage = &u
	}
	return ev
}

func parseTokenDelta(data []byte) *StreamEvent {
	tokens := int(getVarint(data, fieldTokenDeltaTokens))
	if tokens <= 0 {
		return nil
	}
	return &StreamEvent{
		Type:  "token_delta",
		Usage: &TokenUsage{OutputTokens: tokens},
	}
}

func parseTokenUsage(data []byte) TokenUsage {
	var u TokenUsage
	pr := NewProtobufReader(data)
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			break
		}
		if f.WireType != WireVarint {
			continue
		}
		n := int(f.Varint)
		switch f.Num {
		case fieldTurnEndedInputTokens:
			u.InputTokens = n
		case fieldTurnEndedOutputTokens:
			u.OutputTokens = n
		case fieldTurnEndedCacheReadTokens:
			u.CacheReadTokens = n
		case fieldTurnEndedCacheWriteTokens:
			u.CacheWriteTokens = n
		case fieldTurnEndedReasoningTokens:
			u.ReasoningTokens = n
		}
	}
	return u
}

func getVarint(data []byte, fieldNum uint32) uint64 {
	pr := NewProtobufReader(data)
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			return 0
		}
		if f.Num == fieldNum && f.WireType == WireVarint {
			return f.Varint
		}
	}
}

// ConsumeAssistantStream reads Connect-RPC AgentService/Run frames until the
// turn ends. emit is invoked for text and thinking deltas; usage is accumulated
// from turn_ended (preferred) or token_delta fallbacks.
// ConsumeAssistantStream 消费一次上游流。
//
// 收到终止的工具调用后立即返回，不等 turn_ended：Cursor 的 Agent 协议是有状态
// 的，它发完工具调用会挂着等工具结果；而 Anthropic / OpenAI 的工具协议是一轮
// 一往返，客户端要等本次响应结束才会去执行工具。两边一起等就是死锁——实测每次
// 工具调用都卡到 180 秒超时。工具结果由客户端在下一次请求里带回来。
func ConsumeAssistantStream(body io.Reader, emit func(StreamEvent) error) (TokenUsage, string) {
	var acc UsageAccumulator
	var connectErr string
	for {
		frame, err := DecodeFrame(body)
		if err != nil {
			return acc.Result(), connectErr
		}
		if msg := ConnectErrorJSON(frame); msg != "" {
			connectErr = msg
			break
		}
		events := ParseResponseFrame(frame.Payload)
		for _, ev := range events {
			acc.Observe(ev)
			switch ev.Type {
			case "text", "thinking":
				if emit != nil {
					if err := emit(ev); err != nil {
						return acc.Result(), connectErr
					}
				}
			case "tool_call":
				if emit != nil {
					if err := emit(ev); err != nil {
						return acc.Result(), connectErr
					}
				}
				// 参数已完整：继续读只会等一个永远不来的工具结果。
				if ev.Tool != nil && !ev.Tool.Partial && ev.Tool.Name != "" {
					return acc.Result(), connectErr
				}
			case "turn_ended":
				return acc.Result(), connectErr
			}
		}
		if frame.Flags&FrameFlagEndStream != 0 {
			break
		}
	}
	return acc.Result(), connectErr
}

// parseToolCall decodes a tool start/delta/end payload.
//
// 实测结构（composer-2.5，2026-09-15）：start 的调用体嵌在 2.15.1 下，包含
// 工具名、结构化参数与 callID；delta 只带一个内部序号，没有参数片段，所以
// 参数不需要跨事件累加——start 那一刻就是完整的。
func parseToolCall(data []byte, partial bool) *ToolCall {
	call := &ToolCall{Partial: partial}
	call.ID = GetString(data, fieldToolCallID)

	invocation := toolInvocation(data)
	if invocation == nil {
		if call.ID == "" {
			return nil
		}
		return call
	}
	if name := GetString(invocation, fieldInvocationName); name != "" {
		call.Name = name
	}
	if call.ID == "" {
		call.ID = GetString(invocation, fieldInvocationCallID)
	}
	call.ArgsRaw = encodeInvocationArgs(invocation)
	if call.ID == "" && call.Name == "" {
		return nil
	}
	return call
}

// toolInvocation 取出调用体。tool_start 把它包在 2.15.1 里；exec 消息直接放在
// field 11 上。
func toolInvocation(data []byte) []byte {
	if payload := GetNested(data, fieldToolCallPayload); payload != nil {
		if mcp := GetNested(payload, fieldToolCallMcpWrap); mcp != nil {
			if inv := GetNested(mcp, fieldToolInvocation); inv != nil {
				return inv
			}
		}
	}
	return GetNested(data, fieldExecInvocation)
}

// encodeInvocationArgs 把 Cursor 的结构化参数重建成 JSON。上游给的是重复的
// {key, value} 对而不是 JSON 文本，而客户端的 tool_use.input /
// tool_calls.arguments 要求 JSON，所以必须在这里转换。
func encodeInvocationArgs(invocation []byte) string {
	args := make(map[string]any)
	pr := NewProtobufReader(invocation)
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			break
		}
		if f.Num != fieldInvocationArgs || f.WireType != WireBytes {
			continue
		}
		key := GetString(f.Data, fieldInvocationArgKey)
		if key == "" {
			continue
		}
		args[key] = decodeArgValue(GetNested(f.Data, fieldInvocationArgVal))
	}
	if len(args) == 0 {
		return ""
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// decodeArgValue 解出一个参数值。Cursor 用带类型标签的 Value 消息承载，
// 字符串在 3、数字在 2、布尔在 4。取不到时退回空串，让客户端至少拿到键。
func decodeArgValue(value []byte) any {
	if len(value) == 0 {
		return ""
	}
	pr := NewProtobufReader(value)
	for {
		f, err := pr.Next()
		if f == nil || err != nil {
			return ""
		}
		switch {
		case f.Num == fieldArgValueString && f.WireType == WireBytes:
			return string(f.Data)
		case f.Num == fieldArgValueBool && f.WireType == WireVarint:
			return f.Varint != 0
		case f.Num == fieldArgValueNumber && f.WireType == WireFixed64:
			return math.Float64frombits(f.Varint)
		case f.Num == fieldArgValueNumber && f.WireType == WireVarint:
			return int64(f.Varint)
		}
	}
}
