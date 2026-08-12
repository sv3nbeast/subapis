package kiro

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGenerateSearchToolUseEventsStartsWithDirectServerTool(t *testing.T) {
	events := GenerateSearchToolUseEvents("golang concurrency", "srvtoolu_test", 3)
	require.Len(t, events, 3)
	require.Equal(t, "server_tool_use", gjson.Get(extractSSEDataForTest(t, events[0]), "content_block.type").String())
	require.Equal(t, "srvtoolu_test", gjson.Get(extractSSEDataForTest(t, events[0]), "content_block.id").String())
	require.Equal(t, int64(3), gjson.Get(extractSSEDataForTest(t, events[0]), "index").Int())
	require.Equal(t, "input_json_delta", gjson.Get(extractSSEDataForTest(t, events[1]), "delta.type").String())
	require.JSONEq(t, `{"query":"golang concurrency"}`, gjson.Get(extractSSEDataForTest(t, events[1]), "delta.partial_json").String())
	require.Equal(t, "content_block_stop", gjson.Get(extractSSEDataForTest(t, events[2]), "type").String())
}

func TestGenerateSearchIndicatorEvents_UsesInputJSONDelta(t *testing.T) {
	snippet := "result snippet"
	events := GenerateSearchIndicatorEvents("golang concurrency", "srvtoolu_test", &WebSearchResults{
		Results: []WebSearchResult{
			{Title: "Go", URL: "https://go.dev", Snippet: &snippet},
		},
	}, 0)

	require.Len(t, events, 5)
	require.Contains(t, string(events[0]), `"type":"server_tool_use"`)
	require.Contains(t, string(events[0]), `"input":{}`)
	require.Contains(t, string(events[1]), `"type":"input_json_delta"`)
	require.Contains(t, string(events[1]), `"{\"query\":\"golang concurrency\"}"`)
	require.Contains(t, string(events[3]), `"type":"web_search_tool_result"`)
	require.Contains(t, string(events[3]), `"tool_use_id":"srvtoolu_test"`)
	require.Equal(t, "direct", gjson.Get(extractSSEDataForTest(t, events[3]), "content_block.caller.type").String())
	opaqueContent := gjson.Get(extractSSEDataForTest(t, events[3]), "content_block.content.0.encrypted_content").String()
	require.NotEmpty(t, opaqueContent)
	require.NotEqual(t, "result snippet", opaqueContent)
	envelope, ok := openWebSearchOpaquePayload(opaqueContent)
	require.True(t, ok)
	require.Equal(t, "result snippet", envelope.Snippet)
}

func TestGenerateSearchIndicatorEvents_PairsSearchResultWithServerToolUse(t *testing.T) {
	events := GenerateSearchIndicatorEvents("current weather", "srvtoolu_pair", &WebSearchResults{}, 4)
	require.Len(t, events, 5)

	toolUse := gjson.Parse(extractSSEDataForTest(t, events[0]))
	toolResult := gjson.Parse(extractSSEDataForTest(t, events[3]))
	require.Equal(t, "server_tool_use", toolUse.Get("content_block.type").String())
	require.Equal(t, "web_search_tool_result", toolResult.Get("content_block.type").String())
	require.Equal(t, toolUse.Get("content_block.id").String(), toolResult.Get("content_block.tool_use_id").String())
	require.Equal(t, "direct", toolResult.Get("content_block.caller.type").String())
}

func TestGenerateSearchIndicatorEvents_UsesOfficialErrorContentShape(t *testing.T) {
	events := GenerateSearchIndicatorEventsWithError(
		"current weather", "srvtoolu_error", nil, WebSearchErrorTooManyRequests, 2,
	)
	require.Len(t, events, 5)
	toolResult := gjson.Parse(extractSSEDataForTest(t, events[3]))
	require.Equal(t, "web_search_tool_result", toolResult.Get("content_block.type").String())
	require.Equal(t, "srvtoolu_error", toolResult.Get("content_block.tool_use_id").String())
	require.Equal(t, "direct", toolResult.Get("content_block.caller.type").String())
	require.Equal(t, "web_search_tool_result_error", toolResult.Get("content_block.content.type").String())
	require.Equal(t, WebSearchErrorTooManyRequests, toolResult.Get("content_block.content.error_code").String())
}

func TestAnalyzeBufferedStream_ExtractsWebSearchToolUse(t *testing.T) {
	chunks := [][]byte{
		[]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"),
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"srvtoolu_next\",\"name\":\"web_search\",\"input\":{}}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"golang concurrency\\\"}\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"),
		[]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n"),
	}

	result := AnalyzeBufferedStream(chunks)
	require.True(t, result.HasWebSearchToolUse)
	require.Equal(t, "golang concurrency", result.WebSearchQuery)
	require.Equal(t, "srvtoolu_next", result.WebSearchToolUseID)
	require.Equal(t, 1, result.WebSearchToolUseIndex)
	require.Equal(t, "tool_use", result.StopReason)
}

func TestFilterChunksForClient_ConsumesRefinementToolUseAndOffsetsNarrative(t *testing.T) {
	chunks := [][]byte{
		[]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"),
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Searching...\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"),
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"srvtoolu_next\",\"name\":\"web_search\",\"input\":{}}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"golang concurrency\\\"}\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"),
		[]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n"),
	}

	filtered := FilterChunksForClient(chunks, 1, 2)
	require.NotEmpty(t, filtered)
	joined := ""
	for _, chunk := range filtered {
		joined += string(chunk)
	}
	require.NotContains(t, joined, `"type":"message_start"`)
	require.NotContains(t, joined, `"type":"message_delta"`)
	require.NotContains(t, joined, `"name":"web_search"`)
	require.NotContains(t, joined, `"srvtoolu_next"`)
	require.Contains(t, joined, `"index":2`)
	require.Equal(t, 2, MaxContentBlockIndex(filtered))
}

func TestFilterChunksForClient_ClosesSuppressedRefinementIndexGap(t *testing.T) {
	chunks := [][]byte{
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_bdrk_private\",\"name\":\"remote_web_search\",\"input\":{}}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"golang concurrency\\\"}\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"),
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Refining the search.\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"),
	}

	filtered := FilterChunksForClient(chunks, 0, 4)
	require.NotEmpty(t, filtered)
	joined := ""
	for _, chunk := range filtered {
		joined += string(chunk)
	}
	require.NotContains(t, joined, "toolu_bdrk_private")
	require.Contains(t, joined, `"text":"Refining the search."`)
	require.Contains(t, joined, `"index":4`)
	require.NotContains(t, joined, `"index":5`)
	require.Equal(t, 4, MaxContentBlockIndex(filtered))
}

func TestAdjustSSEChunk_OffsetsIndicesAndDropsMessageStart(t *testing.T) {
	_, shouldForward := AdjustSSEChunk([]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"), 2)
	require.False(t, shouldForward)

	adjusted, shouldForward := AdjustSSEChunk([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"), 3)
	require.True(t, shouldForward)
	require.Contains(t, string(adjusted), `"index":3`)
}

func TestAdjustSSEChunk_PreservesFinalTerminalEvents(t *testing.T) {
	messageDelta := []byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}\n\n")
	adjusted, shouldForward := AdjustSSEChunk(messageDelta, 2)
	require.True(t, shouldForward)
	require.JSONEq(t,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":12,"output_tokens":3}}`,
		extractSSEDataForTest(t, adjusted),
	)

	messageStop := []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	adjusted, shouldForward = AdjustSSEChunk(messageStop, 2)
	require.True(t, shouldForward)
	require.JSONEq(t, `{"type":"message_stop"}`, extractSSEDataForTest(t, adjusted))
}

func TestAdjustSSEChunkWithWebSearchUsage_AddsRequestCountOnlyToFinalUsage(t *testing.T) {
	messageDelta := []byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}\n\n")
	adjusted, shouldForward := AdjustSSEChunkWithWebSearchUsage(messageDelta, 2, 2)
	require.True(t, shouldForward)
	require.Equal(t, int64(2), gjson.Get(extractSSEDataForTest(t, adjusted), "usage.server_tool_use.web_search_requests").Int())

	contentDelta := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n")
	adjusted, shouldForward = AdjustSSEChunkWithWebSearchUsage(contentDelta, 2, 2)
	require.True(t, shouldForward)
	require.False(t, gjson.Get(extractSSEDataForTest(t, adjusted), "usage.server_tool_use").Exists())
}

func TestFinalizeWebSearchSSEChunksAddsOfficialCitationDeltaBeforeTextStop(t *testing.T) {
	chunks := [][]byte{
		[]byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n"),
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"See https://go.dev\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"),
		[]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n"),
		[]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
	}
	snippet := "The Go programming language documentation."
	searches := []SearchIndicator{{Results: &WebSearchResults{Results: []WebSearchResult{
		{Title: "Go", URL: "https://go.dev", Snippet: &snippet},
	}}}}

	finalized := FinalizeWebSearchSSEChunks(chunks, 4, 1, searches)
	wire := ""
	for _, chunk := range finalized {
		wire += string(chunk)
	}
	require.NotContains(t, wire, `"type":"message_start"`)
	require.Contains(t, wire, `"index":4`)
	require.Contains(t, wire, `"type":"citations_delta"`)
	require.Contains(t, wire, `"citations":[]`)
	require.Contains(t, wire, `"type":"web_search_result_location"`)
	require.Contains(t, wire, `"url":"https://go.dev"`)
	require.Less(t, strings.Index(wire, `"type":"citations_delta"`), strings.Index(wire, `"type":"text_delta"`))
	require.Less(t, strings.Index(wire, `"type":"citations_delta"`), strings.Index(wire, `"type":"content_block_stop"`))
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
	require.Equal(t, int64(1), gjson.Get(extractSSEDataForTest(t, finalized[len(finalized)-2]), "usage.server_tool_use.web_search_requests").Int())
}

func TestFinalizeWebSearchSSEChunksSegmentsCitationsAndPreservesText(t *testing.T) {
	const answer = "Summary\n\n- First [source](https://one.example)\n\nBridge\n\n- Second [source](https://two.example)\n\nConclusion"
	chunks := [][]byte{
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"),
		marshalSSEEvent("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": answer},
		}),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"),
		[]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n"),
		[]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"),
	}
	firstSnippet := "first result"
	secondSnippet := "second result"
	searches := []SearchIndicator{{Results: &WebSearchResults{Results: []WebSearchResult{
		{Title: "One", URL: "https://one.example", Snippet: &firstSnippet},
		{Title: "Two", URL: "https://two.example", Snippet: &secondSnippet},
	}}}}

	finalized := FinalizeWebSearchSSEChunks(chunks, 2, 1, searches)
	starts := make(map[int]gjson.Result)
	var joined strings.Builder
	var citationURLs []string
	for _, chunk := range finalized {
		data := gjson.Parse(extractSSEDataForTest(t, chunk))
		switch data.Get("type").String() {
		case "content_block_start":
			starts[int(data.Get("index").Int())] = data
		case "content_block_delta":
			switch data.Get("delta.type").String() {
			case "text_delta":
				joined.WriteString(data.Get("delta.text").String())
			case "citations_delta":
				citationURLs = append(citationURLs, data.Get("delta.citation.url").String())
			}
		}
	}
	require.Equal(t, answer, joined.String())
	require.Equal(t, []string{"https://one.example", "https://two.example"}, citationURLs)
	require.Len(t, starts, 5)
	for index := 2; index <= 6; index++ {
		require.Contains(t, starts, index)
	}
	require.False(t, starts[2].Get("content_block.citations").Exists())
	require.True(t, starts[3].Get("content_block.citations").IsArray())
	require.False(t, starts[4].Get("content_block.citations").Exists())
	require.True(t, starts[5].Get("content_block.citations").IsArray())
	require.False(t, starts[6].Get("content_block.citations").Exists())
}

func TestAnalyzeBufferedStream_DoesNotMixAdjacentNativeSearchBlocks(t *testing.T) {
	chunks := [][]byte{
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_bdrk_private\",\"name\":\"remote_web_search\",\"input\":{}}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":3,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"first query\\\"}\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":3}\n\n"),
		[]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":4,\"content_block\":{\"type\":\"server_tool_use\",\"id\":\"srvtoolu_native\",\"name\":\"web_search\",\"input\":{}}}\n\n"),
		[]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":4,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"native query\\\"}\"}}\n\n"),
		[]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":4}\n\n"),
	}

	result := AnalyzeBufferedStream(chunks)
	require.True(t, result.HasWebSearchToolUse)
	require.Equal(t, 3, result.WebSearchToolUseIndex)
	require.Equal(t, "first query", result.WebSearchQuery)
}

func TestFilterChunksForClient_ReassemblesFragmentedSSE(t *testing.T) {
	wire := strings.Join([]string{
		"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Searching\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_bdrk_private\",\"name\":\"remote_web_search\",\"input\":{}}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"query\\\":\\\"golang\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n",
	}, "")
	fragments := make([][]byte, 0, len(wire))
	for i := range wire {
		fragments = append(fragments, []byte(wire[i:i+1]))
	}

	analysis := AnalyzeBufferedStream(fragments)
	require.True(t, analysis.HasWebSearchToolUse)
	require.Equal(t, 1, analysis.WebSearchToolUseIndex)
	require.Equal(t, "golang", analysis.WebSearchQuery)
	filtered := FilterChunksForClient(fragments, analysis.WebSearchToolUseIndex, 2)
	joined := ""
	for _, chunk := range filtered {
		joined += string(chunk)
	}
	require.NotContains(t, joined, "toolu_bdrk_private")
	require.NotContains(t, joined, `"type":"message_delta"`)
	require.Contains(t, joined, `"index":2`)
}

func extractSSEDataForTest(t *testing.T, chunk []byte) string {
	t.Helper()
	for _, line := range strings.Split(string(chunk), "\n") {
		if strings.HasPrefix(line, "data: ") {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("SSE chunk has no data line: %q", string(chunk))
	return ""
}
