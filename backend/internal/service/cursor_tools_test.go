package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

func TestCursorToolsFromChat(t *testing.T) {
	strict := true
	tools := []apicompat.ChatTool{
		{
			Type: "function",
			Function: &apicompat.ChatFunction{
				Name:        "Read",
				Description: "Read a file",
				Parameters:  json.RawMessage(`{"type":"object"}`),
				Strict:      &strict,
			},
		},
		// Server-side tools are the upstream's own capability; forwarding them as
		// MCP definitions would advertise a tool Cursor cannot actually run.
		{Type: "web_search"},
		// A function entry with no definition carries nothing to forward.
		{Type: "function"},
	}
	functions := []apicompat.ChatFunction{{
		Name:        "LegacyFn",
		Description: "old shape",
		Parameters:  json.RawMessage(`{"type":"object"}`),
	}}

	got := cursorToolsFromChat(tools, functions)
	if len(got) != 2 {
		t.Fatalf("got %d tools, want 2 (function + legacy function)", len(got))
	}
	if got[0].Name != "Read" || got[0].Description != "Read a file" || got[0].Schema != `{"type":"object"}` {
		t.Errorf("tool[0] = %+v, want the Read definition carried through", got[0])
	}
	if got[1].Name != "LegacyFn" {
		t.Errorf("tool[1] = %+v, want the legacy function", got[1])
	}
}

func TestCursorToolsFromAnthropic(t *testing.T) {
	got := cursorToolsFromAnthropic([]apicompat.AnthropicTool{
		{Name: "Read", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object"}`)},
		// Anthropic server tools (web_search_20250305 and friends) are executed by
		// Anthropic; Cursor cannot stand in for them.
		{Type: "web_search_20250305", Name: "web_search"},
	})
	if len(got) != 1 {
		t.Fatalf("got %d tools, want only the client-side one", len(got))
	}
	if got[0].Name != "Read" || got[0].Schema != `{"type":"object"}` {
		t.Errorf("tool = %+v, want the Read definition", got[0])
	}
}

func TestRawJSONString(t *testing.T) {
	for raw, want := range map[string]string{
		`{"a":1}`: `{"a":1}`,
		`null`:    "",
		``:        "",
		`   `:     "",
	} {
		if got := rawJSONString(json.RawMessage(raw)); got != want {
			t.Errorf("rawJSONString(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestCursorToolAggregatorAssemblesFragments(t *testing.T) {
	agg := newCursorToolAggregator()

	// Cursor streams the name on start, then argument fragments, then a terminal
	// event. Only the assembled call is usable: a client cannot act on half a
	// JSON argument object.
	if got := agg.Observe(&cursor.ToolCall{ID: "call_1", Name: "Read", Partial: true}); got != nil {
		t.Fatalf("start event delivered a call early: %+v", got)
	}
	if got := agg.Observe(&cursor.ToolCall{ID: "call_1", ArgsRaw: `{"path":`, Partial: true}); got != nil {
		t.Fatalf("argument fragment delivered a call early: %+v", got)
	}
	if got := agg.Observe(&cursor.ToolCall{ID: "call_1", ArgsRaw: `"main.go"}`, Partial: true}); got != nil {
		t.Fatalf("second fragment delivered a call early: %+v", got)
	}

	done := agg.Observe(&cursor.ToolCall{ID: "call_1", Partial: false})
	if done == nil {
		t.Fatal("terminal event delivered nothing")
	}
	if done.ID != "call_1" || done.Type != "function" {
		t.Errorf("call = %+v, want id call_1 as a function call", done)
	}
	if done.Function.Name != "Read" {
		t.Errorf("name = %q, want %q — the name only arrives on start", done.Function.Name, "Read")
	}
	if done.Function.Arguments != `{"path":"main.go"}` {
		t.Errorf("arguments = %q, want the reassembled JSON", done.Function.Arguments)
	}
	if !agg.HasCalls() {
		t.Error("HasCalls = false after a delivered call; finish_reason would say stop")
	}
	// Already delivered: it must not be handed out twice.
	if pending := agg.Pending(); len(pending) != 0 {
		t.Errorf("Pending = %+v, want empty after delivery", pending)
	}
}

func TestCursorToolAggregatorDeliversUnterminatedCalls(t *testing.T) {
	// The upstream often stops sending tool events before turn_ended without a
	// terminal event. Dropping those leaves the client waiting on a tool call
	// that never arrives.
	agg := newCursorToolAggregator()
	agg.Observe(&cursor.ToolCall{ID: "call_1", Name: "Read", ArgsRaw: `{"path":"a"}`, Partial: true})

	pending := agg.Pending()
	if len(pending) != 1 {
		t.Fatalf("Pending = %+v, want the unterminated call", pending)
	}
	if pending[0].Function.Name != "Read" || pending[0].Function.Arguments != `{"path":"a"}` {
		t.Errorf("pending call = %+v, want the accumulated Read call", pending[0])
	}
	if again := agg.Pending(); len(again) != 0 {
		t.Errorf("Pending returned the same call twice: %+v", again)
	}
}

func TestCursorToolAggregatorHandlesMultipleAndEdgeCases(t *testing.T) {
	agg := newCursorToolAggregator()
	agg.Observe(&cursor.ToolCall{ID: "call_1", Name: "Read", ArgsRaw: `{"a":1}`, Partial: true})
	agg.Observe(&cursor.ToolCall{ID: "call_2", Name: "Write", ArgsRaw: `{"b":2}`, Partial: true})

	first := agg.Observe(&cursor.ToolCall{ID: "call_1", Partial: false})
	second := agg.Observe(&cursor.ToolCall{ID: "call_2", Partial: false})
	if first == nil || second == nil {
		t.Fatalf("parallel calls not both delivered: %+v / %+v", first, second)
	}
	// Chat Completions identifies parallel calls by index, so they must differ.
	if first.Index == nil || second.Index == nil || *first.Index == *second.Index {
		t.Errorf("indexes = %v / %v, want distinct values", first.Index, second.Index)
	}

	// A call with no arguments still needs valid JSON: clients parse it.
	noArgs := newCursorToolAggregator()
	noArgs.Observe(&cursor.ToolCall{ID: "c", Name: "Now", Partial: true})
	if done := noArgs.Observe(&cursor.ToolCall{ID: "c", Partial: false}); done == nil || done.Function.Arguments != "{}" {
		t.Errorf("argument-free call = %+v, want arguments {}", done)
	}

	// A terminal event without an id belongs to the open call; inventing a new
	// empty call instead would emit a nameless tool the client cannot run.
	noID := newCursorToolAggregator()
	noID.Observe(&cursor.ToolCall{ID: "c", Name: "Now", Partial: true})
	if done := noID.Observe(&cursor.ToolCall{Partial: false}); done == nil || done.ID != "c" {
		t.Errorf("id-less terminal event = %+v, want it attributed to call c", done)
	}

	// Nothing to attribute an id-less event to.
	empty := newCursorToolAggregator()
	if got := empty.Observe(&cursor.ToolCall{Partial: false}); got != nil {
		t.Errorf("id-less event on an empty aggregator = %+v, want nil", got)
	}
	if got := empty.Observe(nil); got != nil {
		t.Errorf("nil event = %+v, want nil", got)
	}
	if empty.HasCalls() {
		t.Error("HasCalls = true without any call")
	}
}

func TestCursorInputTokensFallbackCoversToolTurns(t *testing.T) {
	// A tool turn ends as soon as the call is complete, so turn_ended never
	// arrives and the upstream reports no input tokens. Recording 0 would give a
	// real, billable turn away for free.
	run := cursorRunRequest{
		Messages: []cursor.ChatMessage{{Role: "user", Content: "What is the weather in Paris?"}},
		Tools:    []cursor.Tool{{Name: "get_weather", Description: "Get weather", Schema: `{"type":"object"}`}},
	}
	if got := cursorInputTokensFallback(run); got <= 0 {
		t.Fatalf("fallback = %d, want a positive estimate", got)
	}
	// The tool definitions travel upstream too, so they have to be counted.
	withoutTools := cursorRunRequest{Messages: run.Messages}
	if cursorInputTokensFallback(run) <= cursorInputTokensFallback(withoutTools) {
		t.Error("tool definitions are not counted; a tool-heavy request would be under-billed")
	}
}

func TestCursorForwardResultFallsBackOnMissingInputTokens(t *testing.T) {
	run := cursorRunRequest{
		RequestModel: "claude-sonnet-5",
		StartTime:    time.Now(),
		Messages:     []cursor.ChatMessage{{Role: "user", Content: "hello there"}},
	}
	// Upstream reported output but no input: the tool-turn shape.
	result := cursorForwardResult(run, "claude-sonnet-5", nil, cursor.TokenUsage{OutputTokens: 12})
	if result.Usage.InputTokens <= 0 {
		t.Errorf("InputTokens = %d, want the estimate to cover the missing count", result.Usage.InputTokens)
	}
	// A reported count must never be replaced by an estimate.
	result = cursorForwardResult(run, "claude-sonnet-5", nil, cursor.TokenUsage{InputTokens: 999, OutputTokens: 12})
	if result.Usage.InputTokens != 999 {
		t.Errorf("InputTokens = %d, want the upstream's own 999", result.Usage.InputTokens)
	}
}
