package service

import (
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

func normalizeOpenAICompatAnthropicResponse(resp *apicompat.AnthropicResponse) (*apicompat.AnthropicResponse, bool) {
	if resp == nil {
		return resp, false
	}
	body, err := json.Marshal(resp)
	if err != nil {
		return resp, false
	}
	normalized, changed := normalizeAnthropicXMLInvokeResponseBody(body)
	if !changed {
		return resp, false
	}
	var out apicompat.AnthropicResponse
	if err := json.Unmarshal(normalized, &out); err != nil {
		return resp, false
	}
	return &out, true
}

type openAICompatAnthropicXMLInvokeBridge struct {
	normalizer *anthropicXMLInvokeStreamNormalizer
	sawToolUse bool
}

func newOpenAICompatAnthropicXMLInvokeBridge() *openAICompatAnthropicXMLInvokeBridge {
	return &openAICompatAnthropicXMLInvokeBridge{
		normalizer: newAnthropicXMLInvokeStreamNormalizer(),
	}
}

func (b *openAICompatAnthropicXMLInvokeBridge) normalizeEvents(events []apicompat.AnthropicStreamEvent) []apicompat.AnthropicStreamEvent {
	if len(events) == 0 || b == nil || b.normalizer == nil {
		return events
	}
	out := make([]apicompat.AnthropicStreamEvent, 0, len(events))
	for _, evt := range events {
		if evt.Type == "message_delta" || evt.Type == "message_stop" {
			out = append(out, b.flushPendingEvents()...)
		}

		eventMap, ok := openAICompatAnthropicStreamEventToMap(evt)
		if !ok {
			out = append(out, evt)
			continue
		}
		if generated, handled, changed := b.normalizer.handleEvent(eventMap); handled {
			normalizedEvents := openAICompatAnthropicStreamMapsToEvents(generated)
			b.observeEvents(normalizedEvents)
			out = append(out, normalizedEvents...)
			continue
		} else if changed {
			if normalized, ok := openAICompatAnthropicStreamMapToEvent(eventMap); ok {
				b.observeEvent(&normalized)
				out = append(out, normalized)
				continue
			}
		}
		b.patchMessageDeltaStopReason(&evt)
		b.observeEvent(&evt)
		out = append(out, evt)
	}
	return out
}

func (b *openAICompatAnthropicXMLInvokeBridge) flushPendingEvents() []apicompat.AnthropicStreamEvent {
	if b == nil || b.normalizer == nil {
		return nil
	}
	out := openAICompatAnthropicStreamMapsToEvents(b.normalizer.flushPendingEvents())
	b.observeEvents(out)
	return out
}

func (b *openAICompatAnthropicXMLInvokeBridge) observeEvents(events []apicompat.AnthropicStreamEvent) {
	for i := range events {
		b.observeEvent(&events[i])
	}
}

func (b *openAICompatAnthropicXMLInvokeBridge) observeEvent(evt *apicompat.AnthropicStreamEvent) {
	if b == nil || evt == nil {
		return
	}
	if evt.ContentBlock != nil && evt.ContentBlock.Type == "tool_use" {
		b.sawToolUse = true
	}
}

func (b *openAICompatAnthropicXMLInvokeBridge) patchMessageDeltaStopReason(evt *apicompat.AnthropicStreamEvent) {
	if b == nil || evt == nil || !b.sawToolUse || evt.Type != "message_delta" || evt.Delta == nil {
		return
	}
	if evt.Delta.StopReason == "end_turn" || evt.Delta.StopReason == "" {
		evt.Delta.StopReason = "tool_use"
	}
}

func openAICompatAnthropicStreamEventToMap(evt apicompat.AnthropicStreamEvent) (map[string]any, bool) {
	body, err := json.Marshal(evt)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, false
	}
	return out, true
}

func openAICompatAnthropicStreamMapToEvent(event map[string]any) (apicompat.AnthropicStreamEvent, bool) {
	body, err := json.Marshal(event)
	if err != nil {
		return apicompat.AnthropicStreamEvent{}, false
	}
	var out apicompat.AnthropicStreamEvent
	if err := json.Unmarshal(body, &out); err != nil {
		return apicompat.AnthropicStreamEvent{}, false
	}
	return out, true
}

func openAICompatAnthropicStreamMapsToEvents(events []map[string]any) []apicompat.AnthropicStreamEvent {
	out := make([]apicompat.AnthropicStreamEvent, 0, len(events))
	for _, event := range events {
		if evt, ok := openAICompatAnthropicStreamMapToEvent(event); ok {
			out = append(out, evt)
		}
	}
	return out
}
