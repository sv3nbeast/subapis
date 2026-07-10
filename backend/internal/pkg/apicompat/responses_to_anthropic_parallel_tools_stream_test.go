package apicompat

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ptrInt is a small helper for building tool_call chunks.
func ptrInt(v int) *int { return &v }

// ptrStr is a small helper for optional delta content/finish_reason fields.
func ptrStr(v string) *string { return &v }

// assertAnthropicBlockLifecycle enforces the Anthropic streaming invariant that
// every content_block_delta / content_block_stop targets a block that a prior
// content_block_start opened, and that no block is opened twice or closed twice.
// A violation is exactly what the Anthropic client surfaces as
// "API Error: Content block not found".
func assertAnthropicBlockLifecycle(t *testing.T, events []AnthropicStreamEvent) {
	t.Helper()
	open := map[int]bool{}   // index → currently open
	seen := map[int]bool{}   // index → was ever started
	for i, ev := range events {
		switch ev.Type {
		case "content_block_start":
			require.NotNilf(t, ev.Index, "event %d: content_block_start without index", i)
			idx := *ev.Index
			require.Falsef(t, open[idx], "event %d: content_block_start for already-open block %d", i, idx)
			require.Falsef(t, seen[idx], "event %d: block %d started twice", i, idx)
			open[idx] = true
			seen[idx] = true
		case "content_block_delta":
			require.NotNilf(t, ev.Index, "event %d: content_block_delta without index", i)
			idx := *ev.Index
			require.Truef(t, open[idx], "event %d: content_block_delta for block %d that is not open (orphan → 'Content block not found')", i, idx)
		case "content_block_stop":
			require.NotNilf(t, ev.Index, "event %d: content_block_stop without index", i)
			idx := *ev.Index
			require.Truef(t, open[idx], "event %d: content_block_stop for block %d that is not open", i, idx)
			open[idx] = false
		}
	}
	for idx, stillOpen := range open {
		require.Falsef(t, stillOpen, "block %d was never closed", idx)
	}
}

// runBridgeToAnthropic drives the exact production pipeline a grok group uses for
// a Claude-format client: Chat Completions chunk → Responses events
// (ChatCompletionsChunkToResponsesEvents) → Anthropic events
// (ResponsesEventToAnthropicEvents), then the finalize passes of both stages.
func runBridgeToAnthropic(chunks []*ChatCompletionsChunk) []AnthropicStreamEvent {
	ccState := NewChatCompletionsToResponsesStreamState("grok-4")
	anthState := NewResponsesEventToAnthropicState()
	var out []AnthropicStreamEvent

	emit := func(rEvents []ResponsesStreamEvent) {
		for i := range rEvents {
			out = append(out, ResponsesEventToAnthropicEvents(&rEvents[i], anthState)...)
		}
	}

	for _, chunk := range chunks {
		emit(ChatCompletionsChunkToResponsesEvents(chunk, ccState))
	}
	emit(FinalizeChatCompletionsResponsesStream(ccState))
	out = append(out, FinalizeResponsesAnthropicStream(anthState)...)
	return out
}

// TestBridge_ParallelToolCalls_NoOrphanBlock reproduces the production bug:
// grok returns two parallel tool_calls, the Chat→Responses bridge defers both
// tools' func_args.done to finalize, and the Responses→Anthropic converter used
// to emit an input_json_delta against a stale block index — the orphan the
// Claude client rejected with "Content block not found" while the turn still
// completed. The fix makes the terminal handlers output-index-aware and
// idempotent; this test asserts the block lifecycle stays balanced.
func TestBridge_ParallelToolCalls_NoOrphanBlock(t *testing.T) {
	chunks := []*ChatCompletionsChunk{
		// tool0 announced + first args
		{ID: "chatcmpl_par", Model: "grok-4", Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ToolCalls: []ChatToolCall{{
				Index: ptrInt(0), ID: "call_0", Type: "function",
				Function: ChatFunctionCall{Name: "Bash", Arguments: `{"command":`},
			}}},
		}}},
		// tool0 args continue
		{Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ToolCalls: []ChatToolCall{{
				Index: ptrInt(0), Function: ChatFunctionCall{Arguments: `"ls"}`},
			}}},
		}}},
		// tool1 announced + args (opens a new block; tool0's block closes here)
		{Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ToolCalls: []ChatToolCall{{
				Index: ptrInt(1), ID: "call_1", Type: "function",
				Function: ChatFunctionCall{Name: "Grep", Arguments: `{"pattern":"foo"}`},
			}}},
		}}},
		// final chunk with finish_reason
		{Choices: []ChatChunkChoice{{
			Delta:        ChatDelta{},
			FinishReason: ptrStr("tool_calls"),
		}}, Usage: &ChatUsage{PromptTokens: 20, CompletionTokens: 10}},
	}

	events := runBridgeToAnthropic(chunks)
	assertAnthropicBlockLifecycle(t, events)

	// Both tool_use blocks must be present and balanced.
	var toolStarts int
	for _, ev := range events {
		if ev.Type == "content_block_start" && ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			toolStarts++
		}
	}
	assert.Equal(t, 2, toolStarts, "both parallel tool_use blocks should be started")

	// Stream must terminate as a tool_use turn.
	last := events[len(events)-1]
	assert.Equal(t, "message_stop", last.Type)
}

// TestBridge_TextThenParallelTools_NoOrphanBlock covers the broader trigger:
// assistant text followed by parallel tool calls. The bridge replays the
// message's output_text.done / output_item.done and every tool's func_args.done
// at finalize, out of order relative to the currently-open block. The lifecycle
// must stay balanced with no orphan delta.
func TestBridge_TextThenParallelTools_NoOrphanBlock(t *testing.T) {
	chunks := []*ChatCompletionsChunk{
		{ID: "chatcmpl_txt", Model: "grok-4", Choices: []ChatChunkChoice{{
			Delta: ChatDelta{Content: ptrStr("Let me check two things.")},
		}}},
		{Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ToolCalls: []ChatToolCall{{
				Index: ptrInt(0), ID: "call_a", Type: "function",
				Function: ChatFunctionCall{Name: "Bash", Arguments: `{"command":"ls"}`},
			}}},
		}}},
		{Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ToolCalls: []ChatToolCall{{
				Index: ptrInt(1), ID: "call_b", Type: "function",
				Function: ChatFunctionCall{Name: "Grep", Arguments: `{"pattern":"x"}`},
			}}},
		}}},
		{Choices: []ChatChunkChoice{{
			Delta:        ChatDelta{},
			FinishReason: ptrStr("tool_calls"),
		}}, Usage: &ChatUsage{PromptTokens: 30, CompletionTokens: 12}},
	}

	events := runBridgeToAnthropic(chunks)
	assertAnthropicBlockLifecycle(t, events)

	var textStarts, toolStarts int
	for _, ev := range events {
		if ev.Type == "content_block_start" && ev.ContentBlock != nil {
			switch ev.ContentBlock.Type {
			case "text":
				textStarts++
			case "tool_use":
				toolStarts++
			}
		}
	}
	assert.Equal(t, 1, textStarts, "one text block")
	assert.Equal(t, 2, toolStarts, "two tool_use blocks")
}

// TestBridge_ReasoningThenParallelTools_NoOrphanBlock adds a leading reasoning
// block (grok emits reasoning_content), which becomes an Anthropic thinking
// block, ahead of parallel tools. Exercises the reasoning terminal path through
// the same finalize-ordering hazard.
func TestBridge_ReasoningThenParallelTools_NoOrphanBlock(t *testing.T) {
	chunks := []*ChatCompletionsChunk{
		{ID: "chatcmpl_rsn", Model: "grok-4", Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ReasoningContent: ptrStr("Thinking about it...")},
		}}},
		{Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ToolCalls: []ChatToolCall{{
				Index: ptrInt(0), ID: "call_a", Type: "function",
				Function: ChatFunctionCall{Name: "Bash", Arguments: `{"command":"ls"}`},
			}}},
		}}},
		{Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ToolCalls: []ChatToolCall{{
				Index: ptrInt(1), ID: "call_b", Type: "function",
				Function: ChatFunctionCall{Name: "Grep", Arguments: `{"pattern":"x"}`},
			}}},
		}}},
		{Choices: []ChatChunkChoice{{
			Delta:        ChatDelta{},
			FinishReason: ptrStr("tool_calls"),
		}}, Usage: &ChatUsage{PromptTokens: 15, CompletionTokens: 8}},
	}

	events := runBridgeToAnthropic(chunks)
	assertAnthropicBlockLifecycle(t, events)

	var thinkingStarts, toolStarts int
	for _, ev := range events {
		if ev.Type == "content_block_start" && ev.ContentBlock != nil {
			switch ev.ContentBlock.Type {
			case "thinking":
				thinkingStarts++
			case "tool_use":
				toolStarts++
			}
		}
	}
	assert.Equal(t, 1, thinkingStarts, "one thinking block")
	assert.Equal(t, 2, toolStarts, "two tool_use blocks")
}
