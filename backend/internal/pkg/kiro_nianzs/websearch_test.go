package kiro

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestReplaceWebSearchToolDescriptionUsesTypeFallback(t *testing.T) {
	body := []byte(`{
		"tools":[{"type":"web_search_20250305","description":"old"}],
		"messages":[{"role":"user","content":"golang"}]
	}`)

	updated, err := ReplaceWebSearchToolDescription(body)
	require.NoError(t, err)
	require.Equal(t, "web_search", gjson.GetBytes(updated, "tools.0.name").String())
	require.Equal(t, minimalWebSearchDescription, gjson.GetBytes(updated, "tools.0.description").String())
	require.Equal(t, "string", gjson.GetBytes(updated, "tools.0.input_schema.properties.query.type").String())
	require.Equal(t, "The search query to execute", gjson.GetBytes(updated, "tools.0.input_schema.properties.query.description").String())
	require.Equal(t, "query", gjson.GetBytes(updated, "tools.0.input_schema.required.0").String())
	require.True(t, gjson.GetBytes(updated, "tools.0.input_schema.additionalProperties").Bool() == false)
}

func TestExtractSearchQueryAcceptsClaudeCodePrefixCaseInsensitively(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"perform a web search for the query: latest Go release"}]}`)
	require.Equal(t, "latest Go release", ExtractSearchQuery(body))
}

func TestExtractSearchQueryFallsBackToUserText(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":"latest Go release"}]}`)
	require.Equal(t, "latest Go release", ExtractSearchQuery(body))
}

func TestExtractWebSearchToolConfigAndFilterDomains(t *testing.T) {
	body := []byte(`{"tools":[{"type":"web_search_20250305","max_uses":2,"allowed_domains":["go.dev","example.com"],"blocked_domains":["blog.example.com"]}]}`)
	config := ExtractWebSearchToolConfig(body)
	require.Equal(t, 2, config.MaxUses)
	require.Equal(t, []string{"go.dev", "example.com"}, config.AllowedDomains)
	require.Equal(t, []string{"blog.example.com"}, config.BlockedDomains)

	filtered := ApplyWebSearchDomainFilters(&WebSearchResults{Results: []WebSearchResult{
		{Title: "Go", URL: "https://pkg.go.dev/net/http"},
		{Title: "Allowed", URL: "https://docs.example.com/page"},
		{Title: "Blocked", URL: "https://blog.example.com/post"},
		{Title: "Outside", URL: "https://outside.test"},
	}}, config)
	require.Len(t, filtered.Results, 2)
	require.Equal(t, "Go", filtered.Results[0].Title)
	require.Equal(t, "Allowed", filtered.Results[1].Title)
}

func TestRemoveWebSearchToolsKeepsOtherToolsAndNormalizesSearchHistory(t *testing.T) {
	body := []byte(`{
		"tools":[{"type":"web_search_20250305","name":"web_search"},{"name":"lookup","input_schema":{"type":"object"}}],
		"messages":[
			{"role":"user","content":"search Go"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tool_search","name":"web_search","input":{"query":"Go docs"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_search","content":"Found https://go.dev"}]}
		]
	}`)
	updated, err := RemoveWebSearchTools(body)
	require.NoError(t, err)
	require.Equal(t, int64(1), gjson.GetBytes(updated, "tools.#").Int())
	require.Equal(t, "lookup", gjson.GetBytes(updated, "tools.0.name").String())
	require.Equal(t, "text", gjson.GetBytes(updated, "messages.1.content.0.type").String())
	require.Contains(t, gjson.GetBytes(updated, "messages.1.content.0.text").String(), "Go docs")
	require.Equal(t, "text", gjson.GetBytes(updated, "messages.2.content.0.type").String())
	require.Contains(t, gjson.GetBytes(updated, "messages.2.content.0.text").String(), "https://go.dev")
	require.NotContains(t, string(updated), `"type":"tool_use"`)
	require.NotContains(t, string(updated), `"type":"tool_result"`)
}

func TestInjectToolResultsClaudeAppendsMessages(t *testing.T) {
	body := []byte(`{
		"messages":[{"role":"user","content":"what is golang"}]
	}`)
	results := &WebSearchResults{
		Results: []WebSearchResult{
			{Title: "Go", URL: "https://go.dev"},
		},
	}

	updated, err := InjectToolResultsClaude(body, "srvtoolu_test", "golang", results)
	require.NoError(t, err)
	require.Equal(t, "assistant", gjson.GetBytes(updated, "messages.1.role").String())
	require.Equal(t, "tool_use", gjson.GetBytes(updated, "messages.1.content.0.type").String())
	require.Equal(t, "srvtoolu_test", gjson.GetBytes(updated, "messages.1.content.0.id").String())
	require.Equal(t, "user", gjson.GetBytes(updated, "messages.2.role").String())
	require.Equal(t, "tool_result", gjson.GetBytes(updated, "messages.2.content.0.type").String())
	require.Contains(t, gjson.GetBytes(updated, "messages.2.content.0.content").String(), "https://go.dev")
	require.Contains(t, gjson.GetBytes(updated, "messages.2.content.0.content").String(), `"title": "Go"`)
	require.Contains(t, gjson.GetBytes(updated, "messages.2.content.1.text").String(), "<search_guidance>")
}

func TestExtractWebSearchToolUseFromResponse(t *testing.T) {
	response := []byte(`{
		"content":[
			{"type":"text","text":"let me search"},
			{"type":"tool_use","id":"srvtoolu_next","name":"remote_web_search","input":{"query":"golang concurrency"}}
		]
	}`)

	toolUseID, query, ok := ExtractWebSearchToolUseFromResponse(response)
	require.True(t, ok)
	require.Equal(t, "srvtoolu_next", toolUseID)
	require.Equal(t, "golang concurrency", query)
}

func TestInjectSearchIndicatorsInResponse(t *testing.T) {
	response := []byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"kiro",
		"content":[{"type":"text","text":"final"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":1,"output_tokens":1}
	}`)

	snippet := "result snippet"
	updated, err := InjectSearchIndicatorsInResponse(response, []SearchIndicator{
		{
			ToolUseID: "srvtoolu_test",
			Query:     "golang",
			Results: &WebSearchResults{
				Results: []WebSearchResult{{Title: "Go", URL: "https://go.dev", Snippet: &snippet}},
			},
		},
	})
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(updated, &decoded))
	require.Equal(t, "text", gjson.GetBytes(updated, "content.0.type").String())
	require.Contains(t, gjson.GetBytes(updated, "content.0.text").String(), "golang")
	require.Equal(t, "server_tool_use", gjson.GetBytes(updated, "content.1.type").String())
	require.Equal(t, "srvtoolu_test", gjson.GetBytes(updated, "content.1.id").String())
	require.Equal(t, "web_search_tool_result", gjson.GetBytes(updated, "content.2.type").String())
	require.Equal(t, gjson.GetBytes(updated, "content.1.id").String(), gjson.GetBytes(updated, "content.2.tool_use_id").String())
	opaqueContent := gjson.GetBytes(updated, "content.2.content.0.encrypted_content").String()
	require.NotEmpty(t, opaqueContent)
	require.NotEqual(t, "result snippet", opaqueContent)
	envelope, ok := openWebSearchOpaquePayload(opaqueContent)
	require.True(t, ok)
	require.Equal(t, "content", envelope.Kind)
	require.Equal(t, "result snippet", envelope.Snippet)
	require.Equal(t, "null", gjson.GetBytes(updated, "content.2.content.0.page_age").Raw)
	require.False(t, gjson.GetBytes(updated, "content.2.content.0.page_content").Exists())
	require.Equal(t, "text", gjson.GetBytes(updated, "content.3.type").String())
	require.Equal(t, "web_search_result_location", gjson.GetBytes(updated, "content.3.citations.0.type").String())
	require.Equal(t, "https://go.dev", gjson.GetBytes(updated, "content.3.citations.0.url").String())
	require.Equal(t, "Go", gjson.GetBytes(updated, "content.3.citations.0.title").String())
	require.Equal(t, "result snippet", gjson.GetBytes(updated, "content.3.citations.0.cited_text").String())
	require.NotEmpty(t, gjson.GetBytes(updated, "content.3.citations.0.encrypted_index").String())
	require.Equal(t, int64(1), gjson.GetBytes(updated, "usage.server_tool_use.web_search_requests").Int())
}

func TestWebSearchOpaquePayloadIsEncryptedAndRandomized(t *testing.T) {
	snippet := "private result text"
	result := WebSearchResult{Title: "Example", URL: "https://example.com", Snippet: &snippet}
	first := sealWebSearchOpaquePayload("content", result)
	second := sealWebSearchOpaquePayload("content", result)

	require.NotEqual(t, first, second)
	require.NotContains(t, first, snippet)
	opened, ok := openWebSearchOpaquePayload(first)
	require.True(t, ok)
	require.Equal(t, "content", opened.Kind)
	require.Equal(t, result.Title, opened.Title)
	require.Equal(t, result.URL, opened.URL)
	require.Equal(t, snippet, opened.Snippet)
}

func TestBuildWebSearchResultBlockFormatsPublishedDate(t *testing.T) {
	published := int64(1710000000000)
	block := buildWebSearchResultBlock(WebSearchResult{
		Title: "Example", URL: "https://example.com", PublishedDate: &published,
	})
	require.Equal(t, "March 9, 2024", block["page_age"])
}

func TestBuildWebSearchCitationsPrefersURLsUsedInAnswerAndCapsQuotedText(t *testing.T) {
	longSnippet := strings.Repeat("界", 180)
	otherSnippet := "other"
	searches := []SearchIndicator{{Results: &WebSearchResults{Results: []WebSearchResult{
		{Title: "Other", URL: "https://other.example", Snippet: &otherSnippet},
		{Title: "Matched", URL: "https://matched.example", Snippet: &longSnippet},
	}}}}

	citations := buildWebSearchCitations(searches, "See https://matched.example for details")
	require.Len(t, citations, 1)
	require.Equal(t, "https://matched.example", citations[0]["url"])
	require.Len(t, []rune(citations[0]["cited_text"].(string)), 150)
	require.NotEmpty(t, citations[0]["encrypted_index"])
}

func TestNormalizeWebSearchHistoryForKiroRestoresSealedResultAndRemovesServerBlocks(t *testing.T) {
	snippet := "official documentation"
	result := WebSearchResult{Title: "Go", URL: "https://go.dev", Snippet: &snippet}
	serverID := "srvtoolu_01history"
	body, err := json.Marshal(map[string]any{
		"model": "claude-opus-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "search go"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "server_tool_use", "id": serverID, "name": "web_search", "input": map[string]any{"query": "go"}},
				map[string]any{"type": "web_search_tool_result", "tool_use_id": serverID, "content": []any{buildWebSearchResultBlock(result)}},
				map[string]any{"type": "text", "text": "Go is documented here.", "citations": buildWebSearchCitations([]SearchIndicator{{Results: &WebSearchResults{Results: []WebSearchResult{result}}}}, "")},
			}},
			map[string]any{"role": "user", "content": "continue"},
		},
		"tools": []any{map[string]any{"type": "web_search_20250305", "name": "web_search"}},
	})
	require.NoError(t, err)

	normalized, err := NormalizeWebSearchHistoryForKiro(body)
	require.NoError(t, err)
	require.NotContains(t, string(normalized), `"server_tool_use"`)
	require.NotContains(t, string(normalized), `"web_search_tool_result"`)
	require.NotContains(t, string(normalized), `"citations"`)
	require.Contains(t, string(normalized), "Go is documented here.")
	require.Contains(t, gjson.GetBytes(normalized, "messages.1.content.1.text").String(), "<web_search_history>")
	require.Contains(t, string(normalized), "official documentation")
	require.Contains(t, string(normalized), "https://go.dev")
}

func TestInjectSearchIndicatorsInResponse_DoesNotBillFailedSearch(t *testing.T) {
	response := []byte(`{"content":[],"usage":{"input_tokens":1,"output_tokens":1}}`)
	updated, err := InjectSearchIndicatorsInResponse(response, []SearchIndicator{
		{ToolUseID: "srvtoolu_failed", Query: "unavailable", Results: nil, ErrorCode: WebSearchErrorUnavailable},
	})
	require.NoError(t, err)
	require.Equal(t, "web_search_tool_result_error", gjson.GetBytes(updated, "content.2.content.type").String())
	require.Equal(t, WebSearchErrorUnavailable, gjson.GetBytes(updated, "content.2.content.error_code").String())
	require.False(t, gjson.GetBytes(updated, "usage.server_tool_use").Exists())
}

func TestNormalizeWebSearchToolResultErrorCodeRejectsProviderValues(t *testing.T) {
	require.Equal(t, WebSearchErrorQueryTooLong, normalizeWebSearchToolResultErrorCode(" QUERY_TOO_LONG "))
	require.Empty(t, normalizeWebSearchToolResultErrorCode("provider_timeout"))
}

func TestParseSearchResults_PreservesExtendedFields(t *testing.T) {
	resp := &MCPResponse{
		Result: &struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		}{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
				{
					Type: "text",
					Text: `{"results":[{"title":"Go","url":"https://go.dev","snippet":"snippet","publishedDate":1710000000,"id":"doc-1","domain":"go.dev","maxVerbatimWordLimit":25,"publicDomain":true}]}`,
				},
			},
		},
	}

	results := ParseSearchResults(resp)
	require.NotNil(t, results)
	require.Len(t, results.Results, 1)
	require.Equal(t, int64(1710000000), *results.Results[0].PublishedDate)
	require.Equal(t, "doc-1", *results.Results[0].ID)
	require.Equal(t, "go.dev", *results.Results[0].Domain)
	require.Equal(t, 25, *results.Results[0].MaxVerbatimWordLimit)
	require.True(t, *results.Results[0].PublicDomain)
}

func TestSearchGuidanceText_IsStructured(t *testing.T) {
	guidance := searchGuidanceText()
	require.Contains(t, guidance, "<search_guidance>")
	require.Contains(t, guidance, "Current date:")
	require.Contains(t, guidance, "Then you MUST use the web_search tool again with a refined query.")
	require.Contains(t, guidance, "Rephrasing in English for better coverage")
}
