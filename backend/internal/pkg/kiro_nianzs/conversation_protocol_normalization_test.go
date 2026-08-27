package kiro

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildKiroPayloadRepairsInterruptedToolProtocolAcrossModes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		model   string
		stream  bool
		options KiroPayloadOptions
	}{
		{name: "q claude stream", model: "claude-opus-5", stream: true},
		{name: "q claude nonstream", model: "claude-opus-5", stream: false},
		{name: "q gpt stream", model: "gpt-5.6-sol", stream: true},
		{name: "krs claude stream", model: "claude-opus-5", stream: true, options: KiroPayloadOptions{FlattenCompletedToolHistory: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{
				"model":%q,
				"stream":%t,
				"messages":[
					{"role":"user","content":"start"},
					{"role":"assistant","content":[
						{"type":"thinking","thinking":"private reasoning","signature":"opaque-signature-must-not-cross"},
						{"type":"text","text":"I will inspect and write."},
						{"type":"tool_use","id":"toolu_ok","name":"Read","input":{"path":"/tmp/a"}},
						{"type":"tool_use","id":"toolu_missing","name":"Write","input":{"secret":"missing-input-must-not-cross"}}
					]},
					{"role":"user","content":[
						{"type":"tool_result","tool_use_id":"toolu_ok","content":"read ok"},
						{"type":"text","text":"continue safely"}
					]}
				],
				"tools":[
					{"name":"Read","input_schema":{"type":"object"}},
					{"name":"Write","input_schema":{"type":"object"}}
				]
			}`, tc.model, tc.stream))
			options := tc.options
			options.Origin = "AI_EDITOR"
			result, err := BuildKiroPayloadWithOptions(body, tc.model, "", nil, options)
			require.NoError(t, err)
			assertKiroPayloadProtocolValid(t, result.Payload)

			payloadText := string(result.Payload)
			require.Contains(t, payloadText, "Previous tool call did not return a result")
			require.Contains(t, payloadText, "Write")
			require.NotContains(t, payloadText, "missing-input-must-not-cross", "unpaired tool input must not be replayed as prompt data")
			require.NotContains(t, payloadText, "opaque-signature-must-not-cross", "foreign opaque signatures must not cross provider boundaries")

			structuredUses := collectKiroPayloadToolUseIDs(result.Payload)
			require.Contains(t, structuredUses, "toolu_ok")
			require.NotContains(t, structuredUses, "toolu_missing")
			structuredResults := collectKiroPayloadToolResultIDs(result.Payload)
			require.Contains(t, structuredResults, "toolu_ok")
			require.NotContains(t, structuredResults, "toolu_missing")
		})
	}
}

func TestBuildKiroPayloadPreservesUnlinkedToolResultAsText(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5",
		"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"toolu_ghost","content":"important historical output"},
			{"type":"text","text":"continue"}
		]}]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	assertKiroPayloadProtocolValid(t, result.Payload)
	require.Empty(t, collectKiroPayloadToolResultIDs(result.Payload))
	current := gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.content").String()
	require.Contains(t, current, "Unlinked tool output from an interrupted conversation")
	require.Contains(t, current, "important historical output")
}

func TestBuildKiroPayloadDoesNotPairToolResultAcrossInterveningTurn(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5",
		"messages":[
			{"role":"user","content":"start"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_stale","name":"Bash","input":{"command":"touch /tmp/x"}}]},
			{"role":"user","content":"the client resumed without a result"},
			{"role":"assistant","content":"acknowledged"},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_stale","content":"late output"},{"type":"text","text":"continue"}]}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	assertKiroPayloadProtocolValid(t, result.Payload)
	require.Empty(t, collectKiroPayloadToolUseIDs(result.Payload))
	require.Empty(t, collectKiroPayloadToolResultIDs(result.Payload))
	require.Contains(t, string(result.Payload), "Previous tool call did not return a result")
	require.Contains(t, string(result.Payload), "late output")
}

func TestBuildKiroPayloadPreservesValidParallelToolCycle(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5",
		"messages":[
			{"role":"user","content":"inspect both"},
			{"role":"assistant","content":[
				{"type":"text","text":"checking"},
				{"type":"tool_use","id":"toolu_a","name":"Read","input":{"path":"a"}},
				{"type":"tool_use","id":"toolu_b","name":"Read","input":{"path":"b"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_a","content":"A"},
				{"type":"tool_result","tool_use_id":"toolu_b","content":"B"}
			]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		]
	}`)

	result, err := BuildKiroPayloadWithContext(body, "claude-opus-5", "", "AI_EDITOR", nil)
	require.NoError(t, err)
	assertKiroPayloadProtocolValid(t, result.Payload)
	require.Equal(t, []string{"toolu_a", "toolu_b"}, collectKiroPayloadToolUseIDs(result.Payload))
	require.Equal(t, []string{"toolu_a", "toolu_b"}, collectKiroPayloadToolResultIDs(result.Payload))
	require.NotContains(t, string(result.Payload), "Previous tool call did not return a result")
}

func TestBuildKiroPayloadKRSFlatteningKeepsAlternatingRoles(t *testing.T) {
	body := []byte(`{
		"model":"claude-opus-5",
		"messages":[
			{"role":"user","content":"start"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_old","name":"Read","input":{"path":"a"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_old","content":"A"}]},
			{"role":"assistant","content":"done"},
			{"role":"user","content":"continue"}
		]
	}`)

	result, err := BuildKiroPayloadWithOptions(body, "claude-opus-5", "", nil, KiroPayloadOptions{
		Origin:                      "AI_EDITOR",
		FlattenCompletedToolHistory: true,
	})
	require.NoError(t, err)
	assertKiroPayloadProtocolValid(t, result.Payload)
	require.Empty(t, collectKiroPayloadToolUseIDs(result.Payload))
	require.Empty(t, collectKiroPayloadToolResultIDs(result.Payload))
	require.Contains(t, string(result.Payload), "Tool results:")
}

func assertKiroPayloadProtocolValid(t *testing.T, payload []byte) {
	t.Helper()
	history := gjson.GetBytes(payload, "conversationState.history").Array()
	sequence := append(append([]gjson.Result(nil), history...), gjson.GetBytes(payload, "conversationState.currentMessage"))
	lastRole := ""
	seenToolUseIDs := make(map[string]struct{})
	for i, message := range sequence {
		role := ""
		assistant := message.Get("assistantResponseMessage")
		user := message.Get("userInputMessage")
		if assistant.Exists() {
			role = "assistant"
			require.NotEmpty(t, assistant.Get("content").String(), "assistant history must never be empty")
		} else if user.Exists() {
			role = "user"
		} else {
			t.Fatalf("message %d has no Kiro role: %s", i, message.Raw)
		}
		if lastRole != "" {
			require.NotEqual(t, lastRole, role, "adjacent Kiro history roles at index %d", i)
		}
		lastRole = role

		uses := assistant.Get("toolUses").Array()
		results := user.Get("userInputMessageContext.toolResults").Array()
		if len(uses) > 0 {
			require.Less(t, i+1, len(sequence), "structured tool_use requires a following user turn")
			nextResults := sequence[i+1].Get("userInputMessage.userInputMessageContext.toolResults").Array()
			require.Len(t, nextResults, len(uses), "parallel tool turns must have an exact adjacent result set")
			nextIDs := make(map[string]struct{}, len(nextResults))
			for _, result := range nextResults {
				nextIDs[result.Get("toolUseId").String()] = struct{}{}
			}
			for _, use := range uses {
				id := use.Get("toolUseId").String()
				require.NotEmpty(t, id)
				require.NotEmpty(t, use.Get("name").String())
				_, duplicate := seenToolUseIDs[id]
				require.False(t, duplicate, "tool_use IDs must be unique")
				seenToolUseIDs[id] = struct{}{}
				require.Contains(t, nextIDs, id)
			}
		}
		if len(results) > 0 {
			require.Greater(t, i, 0, "structured tool_result requires a preceding assistant turn")
			prevUses := sequence[i-1].Get("assistantResponseMessage.toolUses").Array()
			require.Len(t, prevUses, len(results), "tool_result set must exactly match the preceding assistant turn")
		}
	}
	require.Equal(t, "user", lastRole, "currentMessage must be the final user turn")
}

func collectKiroPayloadToolUseIDs(payload []byte) []string {
	var ids []string
	for _, message := range gjson.GetBytes(payload, "conversationState.history").Array() {
		for _, use := range message.Get("assistantResponseMessage.toolUses").Array() {
			ids = append(ids, use.Get("toolUseId").String())
		}
	}
	return ids
}

func collectKiroPayloadToolResultIDs(payload []byte) []string {
	var ids []string
	for _, message := range gjson.GetBytes(payload, "conversationState.history").Array() {
		for _, result := range message.Get("userInputMessage.userInputMessageContext.toolResults").Array() {
			ids = append(ids, result.Get("toolUseId").String())
		}
	}
	for _, result := range gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults").Array() {
		ids = append(ids, result.Get("toolUseId").String())
	}
	return ids
}
