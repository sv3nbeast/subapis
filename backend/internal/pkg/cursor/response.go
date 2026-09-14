package cursor

import "io"

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
	Type  string // "text", "thinking", "turn_ended", "token_delta"
	Text  string
	Usage *TokenUsage
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
