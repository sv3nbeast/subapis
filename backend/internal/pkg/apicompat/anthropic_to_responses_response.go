package apicompat

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Non-streaming: AnthropicResponse → ResponsesResponse
// ---------------------------------------------------------------------------

// AnthropicToResponsesResponse converts an Anthropic Messages response into a
// Responses API response. This is the reverse of ResponsesToAnthropic and
// enables Anthropic upstream responses to be returned in OpenAI Responses format.
func AnthropicToResponsesResponse(resp *AnthropicResponse) *ResponsesResponse {
	return AnthropicToResponsesResponseWithOptions(resp, AnthropicEventToResponsesOptions{})
}

// AnthropicToResponsesResponseWithOptions applies the same opt-in bridge
// metadata as the streaming converter while preserving the default response
// conversion when options are empty.
func AnthropicToResponsesResponseWithOptions(resp *AnthropicResponse, options AnthropicEventToResponsesOptions) *ResponsesResponse {
	id := resp.ID
	if id == "" {
		id = generateResponsesID()
	}

	out := &ResponsesResponse{
		ID:     id,
		Object: "response",
		Model:  resp.Model,
	}

	var outputs []ResponsesOutput
	var msgParts []ResponsesContentPart

	for _, block := range resp.Content {
		switch block.Type {
		case "thinking":
			if block.Thinking != "" {
				outputs = append(outputs, ResponsesOutput{
					Type: "reasoning",
					ID:   generateItemID(),
					Summary: []ResponsesSummary{{
						Type: "summary_text",
						Text: block.Thinking,
					}},
				})
			}
		case "text":
			if block.Text != "" {
				msgParts = append(msgParts, ResponsesContentPart{
					Type: "output_text",
					Text: block.Text,
				})
			}
		case "tool_use":
			args := "{}"
			if len(block.Input) > 0 {
				args = string(block.Input)
			}
			if _, custom := options.CustomToolNames[block.Name]; custom {
				outputs = append(outputs, ResponsesOutput{
					Type:   "custom_tool_call",
					ID:     generateItemID(),
					CallID: toResponsesCallID(block.ID),
					Name:   block.Name,
					Input:  customToolInputFromArguments(args),
					Status: "completed",
				})
			} else {
				outputs = append(outputs, ResponsesOutput{
					Type:      "function_call",
					ID:        generateItemID(),
					CallID:    toResponsesCallID(block.ID),
					Name:      block.Name,
					Arguments: args,
					Status:    "completed",
				})
			}
		}
	}

	// Assemble message output item from text parts
	if len(msgParts) > 0 {
		outputs = append(outputs, ResponsesOutput{
			Type:    "message",
			ID:      generateItemID(),
			Role:    "assistant",
			Content: msgParts,
			Status:  "completed",
		})
	}

	if len(outputs) == 0 {
		outputs = append(outputs, ResponsesOutput{
			Type:    "message",
			ID:      generateItemID(),
			Role:    "assistant",
			Content: []ResponsesContentPart{{Type: "output_text", Text: ""}},
			Status:  "completed",
		})
	}
	out.Output = outputs

	// Map stop_reason → status
	out.Status = anthropicStopReasonToResponsesStatus(resp.StopReason, resp.Content)
	if out.Status == "incomplete" {
		out.IncompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	}

	// Usage
	// Anthropic's input_tokens excludes cache_read/cache_creation, while OpenAI
	// Responses' input_tokens is the total including cached tokens. Add them back
	// when converting so downstream consumers see OpenAI semantics.
	totalInputTokens := resp.Usage.InputTokens +
		resp.Usage.CacheReadInputTokens +
		resp.Usage.CacheCreationInputTokens
	out.Usage = &ResponsesUsage{
		InputTokens:  totalInputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		TotalTokens:  totalInputTokens + resp.Usage.OutputTokens,
	}
	if resp.Usage.CacheReadInputTokens > 0 {
		out.Usage.InputTokensDetails = &ResponsesInputTokensDetails{
			CachedTokens: resp.Usage.CacheReadInputTokens,
		}
	}

	return out
}

// anthropicStopReasonToResponsesStatus maps Anthropic stop_reason to Responses status.
func anthropicStopReasonToResponsesStatus(stopReason string, blocks []AnthropicContentBlock) string {
	switch stopReason {
	case "max_tokens":
		return "incomplete"
	case "end_turn", "tool_use", "stop_sequence":
		return "completed"
	default:
		return "completed"
	}
}

// ---------------------------------------------------------------------------
// Streaming: AnthropicStreamEvent → []ResponsesStreamEvent (stateful converter)
// ---------------------------------------------------------------------------

// AnthropicEventToResponsesState tracks state for converting a sequence of
// Anthropic SSE events into Responses SSE events.
type AnthropicEventToResponsesState struct {
	ResponseID     string
	Model          string
	Created        int64
	SequenceNumber int

	// CreatedSent tracks whether response.created has been emitted.
	CreatedSent bool
	// CompletedSent tracks whether the terminal event has been emitted.
	CompletedSent bool

	// Current output tracking. OutputIndex is the index assigned to the open
	// item and advances only after that item has been fully closed.
	OutputIndex     int
	CurrentItemID   string
	CurrentItemType string // "message" | "function_call" | "reasoning"

	// Per-item content accumulated for the terminal item and response.completed.
	ContentIndex        int
	TextPartOpen        bool
	SummaryPartOpen     bool
	CurrentText         strings.Builder
	CurrentReasoning    strings.Builder
	CurrentArguments    strings.Builder
	CurrentMessageParts []ResponsesContentPart
	Output              []ResponsesOutput
	StopReason          string

	// For function_call: track per-output info
	CurrentCallID   string
	CurrentName     string
	CurrentToolKind string

	// Kiro GPT Responses compatibility is opt-in. It must not change the
	// Anthropic /v1/messages path or the default Anthropic->Responses adapter.
	CoalesceInterleavedText bool
	VisibleTextSeen         bool
	SuppressReasoningBlock  bool
	CustomToolNames         map[string]struct{}

	// Usage from message_start / message_delta. InputTokens here follows
	// Anthropic semantics (excludes cached tokens); they are added back when
	// emitting the OpenAI Responses usage.
	InputTokens              int
	OutputTokens             int
	CacheReadInputTokens     int
	CacheCreationInputTokens int
}

// NewAnthropicEventToResponsesState returns an initialised stream state.
func NewAnthropicEventToResponsesState() *AnthropicEventToResponsesState {
	return NewAnthropicEventToResponsesStateWithOptions(AnthropicEventToResponsesOptions{})
}

// AnthropicEventToResponsesOptions contains compatibility switches used by
// protocol bridges. The zero value preserves the existing event lifecycle.
type AnthropicEventToResponsesOptions struct {
	CoalesceInterleavedText bool
	CustomToolNames         map[string]struct{}
}

// NewAnthropicEventToResponsesStateWithOptions builds an event conversion
// state with bridge-specific behavior. Maps are copied so a request cannot
// mutate another request's tool metadata.
func NewAnthropicEventToResponsesStateWithOptions(opts AnthropicEventToResponsesOptions) *AnthropicEventToResponsesState {
	customTools := make(map[string]struct{}, len(opts.CustomToolNames))
	for name := range opts.CustomToolNames {
		customTools[name] = struct{}{}
	}
	return &AnthropicEventToResponsesState{
		ResponseID:              generateResponsesID(),
		Created:                 time.Now().Unix(),
		CoalesceInterleavedText: opts.CoalesceInterleavedText,
		CustomToolNames:         customTools,
	}
}

// AnthropicEventToResponsesEvents converts a single Anthropic SSE event into
// zero or more Responses SSE events, updating state as it goes.
func AnthropicEventToResponsesEvents(
	evt *AnthropicStreamEvent,
	state *AnthropicEventToResponsesState,
) []ResponsesStreamEvent {
	switch evt.Type {
	case "message_start":
		return anthToResHandleMessageStart(evt, state)
	case "content_block_start":
		return anthToResHandleContentBlockStart(evt, state)
	case "content_block_delta":
		return anthToResHandleContentBlockDelta(evt, state)
	case "content_block_stop":
		return anthToResHandleContentBlockStop(evt, state)
	case "message_delta":
		return anthToResHandleMessageDelta(evt, state)
	case "message_stop":
		return anthToResHandleMessageStop(state)
	default:
		return nil
	}
}

// FinalizeAnthropicResponsesStream emits synthetic termination events if the
// stream ended without a proper message_stop.
func FinalizeAnthropicResponsesStream(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if !state.CreatedSent || state.CompletedSent {
		return nil
	}

	var events []ResponsesStreamEvent

	// Close any open item
	events = append(events, closeCurrentResponsesItem(state)...)

	// Emit response.completed
	events = append(events, makeResponsesCompletedEvent(state, "completed", nil))
	state.CompletedSent = true
	return events
}

// ResponsesEventToSSE formats a ResponsesStreamEvent as an SSE data line.
func ResponsesEventToSSE(evt ResponsesStreamEvent) (string, error) {
	data, err := json.Marshal(evt)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", evt.Type, data), nil
}

// --- internal handlers ---

func anthToResHandleMessageStart(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if evt.Message != nil {
		if state.Model == "" {
			state.Model = evt.Message.Model
		}
		if evt.Message.Usage.InputTokens > 0 {
			state.InputTokens = evt.Message.Usage.InputTokens
		}
		if evt.Message.Usage.CacheReadInputTokens > 0 {
			state.CacheReadInputTokens = evt.Message.Usage.CacheReadInputTokens
		}
		if evt.Message.Usage.CacheCreationInputTokens > 0 {
			state.CacheCreationInputTokens = evt.Message.Usage.CacheCreationInputTokens
		}
	}

	if state.CreatedSent {
		return nil
	}
	state.CreatedSent = true

	// Emit response.created
	return []ResponsesStreamEvent{makeResponsesCreatedEvent(state)}
}

func anthToResHandleContentBlockStart(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if evt.ContentBlock == nil {
		return nil
	}

	var events []ResponsesStreamEvent

	switch evt.ContentBlock.Type {
	case "thinking":
		// Kiro GPT can emit text -> reasoning -> text. Codex Desktop treats the
		// second text item as a separate assistant message and may render only
		// the first one. Once visible text has started, suppress only that late
		// reasoning block and keep the message item open for subsequent text.
		// This option is enabled exclusively by the Kiro Responses bridge.
		if state.CoalesceInterleavedText && state.CurrentItemType == "message" && state.VisibleTextSeen {
			events = append(events, closeCurrentResponsesTextPart(state)...)
			state.SuppressReasoningBlock = true
			return events
		}
		events = append(events, closeCurrentResponsesItem(state)...)
		state.CurrentItemID = generateItemID()
		state.CurrentItemType = "reasoning"
		state.CurrentReasoning.Reset()
		state.SummaryPartOpen = true

		events = append(events, makeResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
			OutputIndex: state.OutputIndex,
			Item: &ResponsesOutput{
				Type:   "reasoning",
				ID:     state.CurrentItemID,
				Status: "in_progress",
			},
		}))
		events = append(events, makeResponsesEvent(state, "response.reasoning_summary_part.added", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			SummaryIndex: 0,
			ItemID:       state.CurrentItemID,
			Part:         &ResponsesContentPart{Type: "summary_text"},
		}))

	case "text":
		// A non-message item must be closed before a text item starts. Consecutive
		// Anthropic text blocks remain content parts of the same message item.
		if state.CurrentItemType != "message" {
			events = append(events, closeCurrentResponsesItem(state)...)
			state.CurrentItemID = generateItemID()
			state.CurrentItemType = "message"
			state.VisibleTextSeen = false
			state.ContentIndex = 0
			state.CurrentMessageParts = nil

			events = append(events, makeResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
				OutputIndex: state.OutputIndex,
				Item: &ResponsesOutput{
					Type:   "message",
					ID:     state.CurrentItemID,
					Role:   "assistant",
					Status: "in_progress",
				},
			}))
		} else {
			events = append(events, closeCurrentResponsesTextPart(state)...)
		}
		state.CurrentText.Reset()
		state.TextPartOpen = true
		events = append(events, makeResponsesEvent(state, "response.content_part.added", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			ContentIndex: state.ContentIndex,
			ItemID:       state.CurrentItemID,
			Part:         &ResponsesContentPart{Type: "output_text"},
		}))

	case "tool_use":
		// Close previous item if any
		events = append(events, closeCurrentResponsesItem(state)...)

		state.CurrentItemID = generateItemID()
		state.CurrentItemType = "function_call"
		state.CurrentCallID = toResponsesCallID(evt.ContentBlock.ID)
		state.CurrentName = evt.ContentBlock.Name
		state.CurrentToolKind = "function"
		if _, ok := state.CustomToolNames[state.CurrentName]; ok {
			state.CurrentToolKind = "custom"
		}
		state.CurrentArguments.Reset()
		initialArguments := strings.TrimSpace(string(evt.ContentBlock.Input))
		if initialArguments != "" && initialArguments != "null" && initialArguments != "{}" {
			_, _ = state.CurrentArguments.WriteString(initialArguments)
		}

		events = append(events, makeResponsesEvent(state, "response.output_item.added", &ResponsesStreamEvent{
			OutputIndex: state.OutputIndex,
			Item: &ResponsesOutput{
				Type:   responsesToolOutputType(state.CurrentToolKind),
				ID:     state.CurrentItemID,
				CallID: state.CurrentCallID,
				Name:   state.CurrentName,
				Status: "in_progress",
			},
		}))
	}

	return events
}

func anthToResHandleContentBlockDelta(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if evt.Delta == nil {
		return nil
	}

	switch evt.Delta.Type {
	case "text_delta":
		if evt.Delta.Text == "" {
			return nil
		}
		_, _ = state.CurrentText.WriteString(evt.Delta.Text)
		state.VisibleTextSeen = true
		return []ResponsesStreamEvent{makeResponsesEvent(state, "response.output_text.delta", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			ContentIndex: state.ContentIndex,
			Delta:        evt.Delta.Text,
			ItemID:       state.CurrentItemID,
		})}

	case "thinking_delta":
		if state.SuppressReasoningBlock {
			return nil
		}
		if evt.Delta.Thinking == "" {
			return nil
		}
		_, _ = state.CurrentReasoning.WriteString(evt.Delta.Thinking)
		return []ResponsesStreamEvent{makeResponsesEvent(state, "response.reasoning_summary_text.delta", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			SummaryIndex: 0,
			Delta:        evt.Delta.Thinking,
			ItemID:       state.CurrentItemID,
		})}

	case "input_json_delta":
		if evt.Delta.PartialJSON == "" {
			return nil
		}
		_, _ = state.CurrentArguments.WriteString(evt.Delta.PartialJSON)
		if state.CurrentToolKind == "custom" {
			// Kiro transports tools as JSON objects. A Responses custom tool is
			// freeform, so buffer until the object is complete and unwrap its
			// input field in closeCurrentResponsesItem.
			return nil
		}
		return []ResponsesStreamEvent{makeResponsesEvent(state, "response.function_call_arguments.delta", &ResponsesStreamEvent{
			OutputIndex: state.OutputIndex,
			Delta:       evt.Delta.PartialJSON,
			ItemID:      state.CurrentItemID,
			CallID:      state.CurrentCallID,
			Name:        state.CurrentName,
		})}

	case "signature_delta":
		// Anthropic signature deltas have no Responses equivalent; skip
		return nil
	}

	return nil
}

func anthToResHandleContentBlockStop(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if state.SuppressReasoningBlock {
		state.SuppressReasoningBlock = false
		return nil
	}
	switch state.CurrentItemType {
	case "reasoning", "function_call":
		return closeCurrentResponsesItem(state)

	case "message":
		return closeCurrentResponsesTextPart(state)
	}

	return nil
}

func anthToResHandleMessageDelta(evt *AnthropicStreamEvent, state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	// Update usage
	if evt.Usage != nil {
		state.OutputTokens = evt.Usage.OutputTokens
		if evt.Usage.InputTokens > 0 {
			state.InputTokens = evt.Usage.InputTokens
		}
		if evt.Usage.CacheReadInputTokens > 0 {
			state.CacheReadInputTokens = evt.Usage.CacheReadInputTokens
		}
		if evt.Usage.CacheCreationInputTokens > 0 {
			state.CacheCreationInputTokens = evt.Usage.CacheCreationInputTokens
		}
	}
	if evt.Delta != nil && evt.Delta.StopReason != "" {
		state.StopReason = evt.Delta.StopReason
	}

	return nil
}

func anthToResHandleMessageStop(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if state.CompletedSent {
		return nil
	}

	var events []ResponsesStreamEvent

	// Close any open item
	events = append(events, closeCurrentResponsesItem(state)...)

	status := "completed"
	var incompleteDetails *ResponsesIncompleteDetails
	if state.StopReason == "max_tokens" {
		status = "incomplete"
		incompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
	}

	// Emit response.completed
	events = append(events, makeResponsesCompletedEvent(state, status, incompleteDetails))
	state.CompletedSent = true
	return events
}

// --- helper functions ---

func closeCurrentResponsesItem(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if state.CurrentItemType == "" {
		return nil
	}

	var events []ResponsesStreamEvent
	var item ResponsesOutput

	switch state.CurrentItemType {
	case "message":
		events = append(events, closeCurrentResponsesTextPart(state)...)
		item = ResponsesOutput{
			Type:    "message",
			ID:      state.CurrentItemID,
			Role:    "assistant",
			Content: append([]ResponsesContentPart(nil), state.CurrentMessageParts...),
			Status:  "completed",
		}
	case "reasoning":
		reasoning := state.CurrentReasoning.String()
		if state.SummaryPartOpen {
			events = append(events,
				makeResponsesEvent(state, "response.reasoning_summary_text.done", &ResponsesStreamEvent{
					OutputIndex:  state.OutputIndex,
					SummaryIndex: 0,
					ItemID:       state.CurrentItemID,
					Text:         reasoning,
				}),
				makeResponsesEvent(state, "response.reasoning_summary_part.done", &ResponsesStreamEvent{
					OutputIndex:  state.OutputIndex,
					SummaryIndex: 0,
					ItemID:       state.CurrentItemID,
					Part:         &ResponsesContentPart{Type: "summary_text", Text: reasoning},
				}),
			)
		}
		item = ResponsesOutput{
			Type:    "reasoning",
			ID:      state.CurrentItemID,
			Status:  "completed",
			Summary: []ResponsesSummary{{Type: "summary_text", Text: reasoning}},
		}
	case "function_call":
		arguments := strings.TrimSpace(state.CurrentArguments.String())
		if arguments == "" {
			arguments = "{}"
		}
		if state.CurrentToolKind == "custom" {
			input := customToolInputFromArguments(arguments)
			if input != "" {
				events = append(events, makeResponsesEvent(state, "response.custom_tool_call_input.delta", &ResponsesStreamEvent{
					OutputIndex: state.OutputIndex,
					ItemID:      state.CurrentItemID,
					CallID:      state.CurrentCallID,
					Name:        state.CurrentName,
					Delta:       input,
				}))
			}
			events = append(events, makeResponsesEvent(state, "response.custom_tool_call_input.done", &ResponsesStreamEvent{
				OutputIndex: state.OutputIndex,
				ItemID:      state.CurrentItemID,
				CallID:      state.CurrentCallID,
				Name:        state.CurrentName,
				Input:       input,
			}))
			item = ResponsesOutput{
				Type:   "custom_tool_call",
				ID:     state.CurrentItemID,
				CallID: state.CurrentCallID,
				Name:   state.CurrentName,
				Input:  input,
				Status: "completed",
			}
		} else {
			events = append(events, makeResponsesEvent(state, "response.function_call_arguments.done", &ResponsesStreamEvent{
				OutputIndex: state.OutputIndex,
				ItemID:      state.CurrentItemID,
				CallID:      state.CurrentCallID,
				Name:        state.CurrentName,
				Arguments:   arguments,
			}))
			item = ResponsesOutput{
				Type:      "function_call",
				ID:        state.CurrentItemID,
				CallID:    state.CurrentCallID,
				Name:      state.CurrentName,
				Arguments: arguments,
				Status:    "completed",
			}
		}
	}

	events = append(events, makeResponsesEvent(state, "response.output_item.done", &ResponsesStreamEvent{
		OutputIndex: state.OutputIndex,
		Item:        &item,
	}))
	state.Output = append(state.Output, item)
	state.CurrentItemType = ""
	state.CurrentItemID = ""
	state.CurrentCallID = ""
	state.CurrentName = ""
	state.CurrentToolKind = ""
	state.CurrentText.Reset()
	state.CurrentReasoning.Reset()
	state.CurrentArguments.Reset()
	state.CurrentMessageParts = nil
	state.TextPartOpen = false
	state.SummaryPartOpen = false
	state.VisibleTextSeen = false
	state.SuppressReasoningBlock = false
	state.OutputIndex++
	state.ContentIndex = 0
	return events
}

func responsesToolOutputType(kind string) string {
	if kind == "custom" {
		return "custom_tool_call"
	}
	return "function_call"
}

func customToolInputFromArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" || arguments == "{}" {
		return ""
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &object); err == nil {
		for _, key := range []string{"input", "code", "text"} {
			raw, ok := object[key]
			if !ok {
				continue
			}
			var value string
			if err := json.Unmarshal(raw, &value); err == nil {
				return value
			}
		}
	}
	var value string
	if err := json.Unmarshal([]byte(arguments), &value); err == nil {
		return value
	}
	return arguments
}

func closeCurrentResponsesTextPart(state *AnthropicEventToResponsesState) []ResponsesStreamEvent {
	if state.CurrentItemType != "message" || !state.TextPartOpen {
		return nil
	}
	text := state.CurrentText.String()
	part := ResponsesContentPart{Type: "output_text", Text: text}
	events := []ResponsesStreamEvent{
		makeResponsesEvent(state, "response.output_text.done", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			ContentIndex: state.ContentIndex,
			ItemID:       state.CurrentItemID,
			Text:         text,
		}),
		makeResponsesEvent(state, "response.content_part.done", &ResponsesStreamEvent{
			OutputIndex:  state.OutputIndex,
			ContentIndex: state.ContentIndex,
			ItemID:       state.CurrentItemID,
			Part:         &part,
		}),
	}
	state.CurrentMessageParts = append(state.CurrentMessageParts, part)
	state.CurrentText.Reset()
	state.TextPartOpen = false
	state.ContentIndex++
	return events
}

func makeResponsesCreatedEvent(state *AnthropicEventToResponsesState) ResponsesStreamEvent {
	seq := state.SequenceNumber
	state.SequenceNumber++
	return ResponsesStreamEvent{
		Type:           "response.created",
		SequenceNumber: seq,
		Response: &ResponsesResponse{
			ID:     state.ResponseID,
			Object: "response",
			Model:  state.Model,
			Status: "in_progress",
			Output: []ResponsesOutput{},
		},
	}
}

func makeResponsesCompletedEvent(
	state *AnthropicEventToResponsesState,
	status string,
	incompleteDetails *ResponsesIncompleteDetails,
) ResponsesStreamEvent {
	seq := state.SequenceNumber
	state.SequenceNumber++

	// Anthropic's input_tokens excludes cache_read/cache_creation; add them
	// back to match OpenAI Responses semantics where input_tokens is the total.
	totalInputTokens := state.InputTokens + state.CacheReadInputTokens + state.CacheCreationInputTokens
	usage := &ResponsesUsage{
		InputTokens:  totalInputTokens,
		OutputTokens: state.OutputTokens,
		TotalTokens:  totalInputTokens + state.OutputTokens,
	}
	if state.CacheReadInputTokens > 0 {
		usage.InputTokensDetails = &ResponsesInputTokensDetails{
			CachedTokens: state.CacheReadInputTokens,
		}
	}

	return ResponsesStreamEvent{
		Type:           "response.completed",
		SequenceNumber: seq,
		Response: &ResponsesResponse{
			ID:                state.ResponseID,
			Object:            "response",
			Model:             state.Model,
			Status:            status,
			Output:            append([]ResponsesOutput(nil), state.Output...),
			Usage:             usage,
			IncompleteDetails: incompleteDetails,
		},
	}
}

func makeResponsesEvent(state *AnthropicEventToResponsesState, eventType string, template *ResponsesStreamEvent) ResponsesStreamEvent {
	seq := state.SequenceNumber
	state.SequenceNumber++

	evt := *template
	evt.Type = eventType
	evt.SequenceNumber = seq
	return evt
}

func generateResponsesID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "resp_" + hex.EncodeToString(b)
}

func generateItemID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "item_" + hex.EncodeToString(b)
}
