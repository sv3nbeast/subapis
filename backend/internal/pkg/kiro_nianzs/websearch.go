package kiro

import (
	"cmp"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
)

const minimalWebSearchDescription = "Search the web for information. Use this tool again when the previous search results are insufficient or need refinement."
const remoteWebSearchDescription = "WebSearch looks up information outside the model's training data. Supports multiple queries to gather comprehensive information."

var cachedWebSearchDescription atomic.Value // stores string

// webSearchOpaqueKey protects gateway-generated web-search passback fields.
// Anthropic treats encrypted_content and encrypted_index as opaque values that
// clients must replay verbatim. Kiro's MCP endpoint returns plaintext snippets,
// so the adapter seals them before exposing the Anthropic-compatible response.
// A process-local key is intentional: history replay after a restart still has
// the public title/URL and degrades without leaking the original snippet.
var webSearchOpaqueKey = newWebSearchOpaqueKey()

type MCPRequest struct {
	ID      string `json:"id"`
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type MCPResponse struct {
	Result *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	} `json:"result,omitempty"`
	Error *struct {
		Code    *int    `json:"code,omitempty"`
		Message *string `json:"message,omitempty"`
	} `json:"error,omitempty"`
}

type WebSearchResults struct {
	Results  []WebSearchResult `json:"results"`
	OpaqueID string            `json:"-"`
}

type WebSearchResult struct {
	Title                string  `json:"title"`
	URL                  string  `json:"url"`
	Snippet              *string `json:"snippet,omitempty"`
	PublishedDate        *int64  `json:"publishedDate,omitempty"`
	ID                   *string `json:"id,omitempty"`
	Domain               *string `json:"domain,omitempty"`
	MaxVerbatimWordLimit *int    `json:"maxVerbatimWordLimit,omitempty"`
	PublicDomain         *bool   `json:"publicDomain,omitempty"`
}

type SearchIndicator struct {
	ToolUseID string
	Query     string
	Results   *WebSearchResults
	ErrorCode string
}

type WebSearchToolConfig struct {
	MaxUses        int
	AllowedDomains []string
	BlockedDomains []string
}

const (
	WebSearchErrorInvalidToolInput = "invalid_tool_input"
	WebSearchErrorUnavailable      = "unavailable"
	WebSearchErrorMaxUsesExceeded  = "max_uses_exceeded"
	WebSearchErrorTooManyRequests  = "too_many_requests"
	WebSearchErrorQueryTooLong     = "query_too_long"
	WebSearchErrorRequestTooLarge  = "request_too_large"
)

func GetCachedWebSearchDescription() string {
	if v := cachedWebSearchDescription.Load(); v != nil {
		desc, _ := v.(string)
		return strings.TrimSpace(desc)
	}
	return ""
}

func SetCachedWebSearchDescription(desc string) {
	cachedWebSearchDescription.Store(strings.TrimSpace(desc))
}

func BuildMcpEndpoint(region string) string {
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}
	return fmt.Sprintf("https://q.%s.amazonaws.com/mcp", region)
}

func ParseSearchResults(resp *MCPResponse) *WebSearchResults {
	if resp == nil || resp.Result == nil || len(resp.Result.Content) == 0 {
		return nil
	}
	for _, item := range resp.Result.Content {
		if item.Type != "" && item.Type != "text" {
			continue
		}
		var results WebSearchResults
		if err := json.Unmarshal([]byte(item.Text), &results); err == nil {
			return &results
		}
	}
	return nil
}

func ExtractSearchQuery(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	arr := messages.Array()
	for i := len(arr) - 1; i >= 0; i-- {
		msg := arr[i]
		if msg.Get("role").String() != "user" {
			continue
		}
		text := extractSearchText(msg.Get("content"))
		const prefix = "Perform a web search for the query: "
		text = strings.TrimSpace(text)
		if len(text) >= len(prefix) && strings.EqualFold(text[:len(prefix)], prefix) {
			text = strings.TrimSpace(text[len(prefix):])
		}
		if text != "" {
			return text
		}
	}
	return ""
}

func ExtractWebSearchToolConfig(body []byte) WebSearchToolConfig {
	config := WebSearchToolConfig{MaxUses: 5}
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return config
	}
	for _, tool := range tools.Array() {
		if !isWebSearchToolName(tool.Get("name").String(), tool.Get("type").String()) {
			continue
		}
		if maxUses := int(tool.Get("max_uses").Int()); maxUses > 0 {
			config.MaxUses = maxUses
		}
		config.AllowedDomains = webSearchDomainList(tool.Get("allowed_domains"))
		config.BlockedDomains = webSearchDomainList(tool.Get("blocked_domains"))
		return config
	}
	return config
}

func webSearchDomainList(value gjson.Result) []string {
	if !value.IsArray() {
		return nil
	}
	seen := make(map[string]struct{})
	domains := make([]string, 0, len(value.Array()))
	for _, item := range value.Array() {
		domain := normalizeWebSearchDomain(item.String())
		if domain == "" {
			continue
		}
		if _, ok := seen[domain]; ok {
			continue
		}
		seen[domain] = struct{}{}
		domains = append(domains, domain)
	}
	return domains
}

func ApplyWebSearchDomainFilters(results *WebSearchResults, config WebSearchToolConfig) *WebSearchResults {
	if results == nil || (len(config.AllowedDomains) == 0 && len(config.BlockedDomains) == 0) {
		return results
	}
	filtered := &WebSearchResults{Results: make([]WebSearchResult, 0, len(results.Results))}
	for _, result := range results.Results {
		domain := webSearchResultDomain(result)
		if len(config.AllowedDomains) > 0 && !webSearchDomainMatchesAny(domain, config.AllowedDomains) {
			continue
		}
		if webSearchDomainMatchesAny(domain, config.BlockedDomains) {
			continue
		}
		filtered.Results = append(filtered.Results, result)
	}
	return filtered
}

func webSearchResultDomain(result WebSearchResult) string {
	if parsed, err := url.Parse(strings.TrimSpace(result.URL)); err == nil {
		if domain := normalizeWebSearchDomain(parsed.Hostname()); domain != "" {
			return domain
		}
	}
	if result.Domain != nil {
		return normalizeWebSearchDomain(*result.Domain)
	}
	return ""
}

func normalizeWebSearchDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "www.")
	domain = strings.Trim(domain, ".")
	if slash := strings.IndexByte(domain, '/'); slash >= 0 {
		domain = domain[:slash]
	}
	if colon := strings.LastIndexByte(domain, ':'); colon >= 0 {
		domain = domain[:colon]
	}
	return strings.Trim(domain, ".")
}

func webSearchDomainMatchesAny(domain string, candidates []string) bool {
	domain = normalizeWebSearchDomain(domain)
	for _, candidate := range candidates {
		candidate = normalizeWebSearchDomain(candidate)
		if domain == candidate || (domain != "" && candidate != "" && strings.HasSuffix(domain, "."+candidate)) {
			return true
		}
	}
	return false
}

func RemoveWebSearchTools(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, err
	}
	normalizeFinalWebSearchMessages(payload)
	tools, ok := payload["tools"].([]any)
	if !ok {
		return body, nil
	}
	filtered := make([]any, 0, len(tools))
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if ok && isWebSearchToolName(getInterfaceString(tool["name"]), getInterfaceString(tool["type"])) {
			continue
		}
		filtered = append(filtered, rawTool)
	}
	payload["tools"] = filtered
	updated, err := json.Marshal(payload)
	if err != nil {
		return body, err
	}
	return updated, nil
}

func normalizeFinalWebSearchMessages(payload map[string]any) {
	messages, ok := payload["messages"].([]any)
	if !ok {
		return
	}
	queries := make(map[string]string)
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		for index, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			switch getInterfaceString(block["type"]) {
			case "tool_use":
				if !isWebSearchToolName(getInterfaceString(block["name"]), "") {
					continue
				}
				toolUseID := getInterfaceString(block["id"])
				query := webSearchQueryFromInterface(block["input"])
				queries[toolUseID] = query
				content[index] = map[string]any{
					"type": "text",
					"text": formatFinalWebSearchRequestText(query),
				}
			case "tool_result":
				toolUseID := getInterfaceString(block["tool_use_id"])
				query, isWebSearchResult := queries[toolUseID]
				if !isWebSearchResult {
					continue
				}
				content[index] = map[string]any{
					"type": "text",
					"text": formatFinalWebSearchResultText(query, block["content"]),
				}
			}
		}
		message["content"] = content
	}
}

func webSearchQueryFromInterface(value any) string {
	if input, ok := value.(map[string]any); ok {
		return getInterfaceString(input["query"])
	}
	return ""
}

func formatFinalWebSearchRequestText(query string) string {
	if strings.TrimSpace(query) == "" {
		return "Web search requested."
	}
	return "Web search requested for: " + strings.TrimSpace(query)
}

func formatFinalWebSearchResultText(query string, content any) string {
	resultText := getInterfaceString(content)
	if resultText == "" {
		resultText = "No search results found."
	}
	if strings.TrimSpace(query) == "" {
		return "Web search results:\n" + resultText
	}
	return "Web search results for " + strings.TrimSpace(query) + ":\n" + resultText
}

func extractSearchText(content gjson.Result) string {
	if content.Type == gjson.String {
		return content.String()
	}
	if !content.IsArray() {
		return ""
	}
	for _, block := range content.Array() {
		if block.Get("type").String() == "text" {
			if text := strings.TrimSpace(block.Get("text").String()); text != "" {
				return text
			}
		}
	}
	return ""
}

func GenerateToolUseID() string {
	return "01" + randomBase62(22)
}

func ReplaceWebSearchToolDescription(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, err
	}
	rawTools, ok := payload["tools"].([]any)
	if !ok {
		return body, nil
	}

	replaced := make([]any, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			replaced = append(replaced, rawTool)
			continue
		}
		name := getInterfaceString(tool["name"])
		toolType := getInterfaceString(tool["type"])
		if !isWebSearchToolName(name, toolType) {
			replaced = append(replaced, rawTool)
			continue
		}
		replaced = append(replaced, map[string]any{
			"name":        "web_search",
			"description": minimalWebSearchDescription,
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query to execute",
					},
					"allowed_domains": map[string]any{
						"type":        "array",
						"description": "Only include search results from these domains",
						"items":       map[string]any{"type": "string"},
					},
					"blocked_domains": map[string]any{
						"type":        "array",
						"description": "Never include search results from these domains",
						"items":       map[string]any{"type": "string"},
					},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		})
	}

	payload["tools"] = replaced
	updated, err := json.Marshal(payload)
	if err != nil {
		return body, err
	}
	return updated, nil
}

// NormalizeWebSearchHistoryForKiro converts gateway-generated Anthropic server
// tool blocks from earlier assistant turns into ordinary text context before
// Kiro translation. This makes encrypted_content genuinely replayable: when
// the process key is still available the sealed snippet is restored, while a
// post-restart continuation still retains the public title and URL.
func NormalizeWebSearchHistoryForKiro(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, err
	}
	messages, ok := payload["messages"].([]any)
	if !ok {
		return body, nil
	}
	modified := false
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, ok := message["content"].([]any)
		if !ok {
			continue
		}
		normalized := make([]any, 0, len(content)+1)
		history := make([]WebSearchResult, 0)
		messageModified := false
		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				normalized = append(normalized, rawBlock)
				continue
			}
			switch getInterfaceString(block["type"]) {
			case "server_tool_use":
				if isWebSearchToolName(getInterfaceString(block["name"]), "") {
					modified = true
					messageModified = true
					continue
				}
			case "web_search_tool_result":
				modified = true
				messageModified = true
				history = append(history, webSearchResultsFromHistoryBlock(block)...)
				continue
			}
			normalized = append(normalized, rawBlock)
		}
		if messageModified {
			normalized = stripWebSearchCitationsFromTextBlocks(normalized)
		}
		if len(history) > 0 {
			normalized = append(normalized, map[string]any{
				"type": "text",
				"text": formatWebSearchHistoryText(history),
			})
		}
		if len(normalized) == 0 {
			normalized = append(normalized, map[string]any{
				"type": "text", "text": "(web search completed)",
			})
		}
		if messageModified {
			message["content"] = normalized
		}
	}
	if !modified {
		return body, nil
	}
	updated, err := json.Marshal(payload)
	if err != nil {
		return body, err
	}
	return updated, nil
}

func stripWebSearchCitationsFromTextBlocks(content []any) []any {
	for i, rawBlock := range content {
		block, ok := rawBlock.(map[string]any)
		if !ok || getInterfaceString(block["type"]) != "text" {
			continue
		}
		if _, hasCitations := block["citations"]; !hasCitations {
			continue
		}
		copyBlock := make(map[string]any, len(block)-1)
		for key, value := range block {
			if key != "citations" {
				copyBlock[key] = value
			}
		}
		content[i] = copyBlock
	}
	return content
}

func webSearchResultsFromHistoryBlock(block map[string]any) []WebSearchResult {
	items, ok := block["content"].([]any)
	if !ok {
		return nil
	}
	results := make([]WebSearchResult, 0, len(items))
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || getInterfaceString(item["type"]) != "web_search_result" {
			continue
		}
		result := WebSearchResult{
			Title: getInterfaceString(item["title"]),
			URL:   getInterfaceString(item["url"]),
		}
		if envelope, ok := openWebSearchOpaquePayload(getInterfaceString(item["encrypted_content"])); ok {
			if result.Title == "" {
				result.Title = envelope.Title
			}
			if result.URL == "" {
				result.URL = envelope.URL
			}
			if strings.TrimSpace(envelope.Snippet) != "" {
				snippet := strings.TrimSpace(envelope.Snippet)
				result.Snippet = &snippet
			}
		}
		if result.Title != "" || result.URL != "" || result.Snippet != nil {
			results = append(results, result)
		}
		if len(results) == 20 {
			break
		}
	}
	return results
}

func formatWebSearchHistoryText(results []WebSearchResult) string {
	var builder strings.Builder
	_, _ = builder.WriteString("<web_search_history>\n")
	for i, result := range results {
		_, _ = fmt.Fprintf(&builder, "%d. %s\nURL: %s\n", i+1, result.Title, result.URL)
		if result.Snippet != nil && strings.TrimSpace(*result.Snippet) != "" {
			_, _ = fmt.Fprintf(&builder, "Snippet: %s\n", truncateWebSearchCitationText(strings.TrimSpace(*result.Snippet), 500))
		}
	}
	_, _ = builder.WriteString("</web_search_history>")
	return builder.String()
}

func InjectToolResultsClaude(claudePayload []byte, toolUseID, query string, results *WebSearchResults) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(claudePayload, &payload); err != nil {
		return claudePayload, fmt.Errorf("parse claude payload: %w", err)
	}

	rawMessages, ok := payload["messages"].([]any)
	if !ok {
		return claudePayload, fmt.Errorf("claude payload missing messages array")
	}

	assistantMsg := map[string]any{
		"role": "assistant",
		"content": []any{
			map[string]any{
				"type":  "tool_use",
				"id":    toolUseID,
				"name":  "web_search",
				"input": map[string]any{"query": query},
			},
		},
	}

	userContent := []any{
		map[string]any{
			"type":        "tool_result",
			"tool_use_id": toolUseID,
			"content":     formatToolResultText(results),
		},
	}
	if guidance := searchGuidanceText(); guidance != "" {
		userContent = append(userContent, map[string]any{
			"type": "text",
			"text": guidance,
		})
	}
	userMsg := map[string]any{
		"role":    "user",
		"content": userContent,
	}

	rawMessages = append(rawMessages, assistantMsg, userMsg)
	payload["messages"] = rawMessages
	updated, err := json.Marshal(payload)
	if err != nil {
		return claudePayload, fmt.Errorf("marshal updated payload: %w", err)
	}
	return updated, nil
}

func InjectSearchIndicatorsInResponse(responsePayload []byte, searches []SearchIndicator) ([]byte, error) {
	if len(searches) == 0 {
		return responsePayload, nil
	}

	var response map[string]any
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		return responsePayload, err
	}
	content, _ := response["content"].([]any)
	updated := make([]any, 0, len(searches)*2+len(content))
	for _, search := range searches {
		updated = append(updated, map[string]any{
			"type":  "server_tool_use",
			"id":    search.ToolUseID,
			"name":  "web_search",
			"input": map[string]any{"query": search.Query},
		})
		updated = append(updated, map[string]any{
			"type":        "web_search_tool_result",
			"tool_use_id": search.ToolUseID,
			"caller":      map[string]any{"type": "direct"},
			"content":     buildWebSearchToolResultContent(search.Results, search.ErrorCode),
		})
	}
	updated = append(updated, content...)
	updated = segmentWebSearchCitationsInContent(updated, searches)
	response["content"] = updated
	if successfulSearches := successfulSearchCount(searches); successfulSearches > 0 {
		usage, ok := response["usage"].(map[string]any)
		if !ok {
			usage = map[string]any{}
			response["usage"] = usage
		}
		usage["server_tool_use"] = map[string]any{"web_search_requests": successfulSearches}
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		return responsePayload, err
	}
	return encoded, nil
}

func successfulSearchCount(searches []SearchIndicator) int {
	count := 0
	for _, search := range searches {
		if search.Results != nil {
			count++
		}
	}
	return count
}

func buildSearchResultContent(results *WebSearchResults) []map[string]any {
	content := make([]map[string]any, 0)
	if results == nil {
		return content
	}
	opaqueID := ensureWebSearchOpaqueID(results)
	for _, result := range results.Results {
		content = append(content, buildWebSearchResultBlockWithOpaqueID(result, opaqueID))
	}
	return content
}

func buildWebSearchToolResultContent(results *WebSearchResults, errorCode string) any {
	if normalized := normalizeWebSearchToolResultErrorCode(errorCode); normalized != "" {
		return map[string]any{
			"type":       "web_search_tool_result_error",
			"error_code": normalized,
		}
	}
	return buildSearchResultContent(results)
}

func normalizeWebSearchToolResultErrorCode(errorCode string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(errorCode)); normalized {
	case WebSearchErrorInvalidToolInput,
		WebSearchErrorUnavailable,
		WebSearchErrorMaxUsesExceeded,
		WebSearchErrorTooManyRequests,
		WebSearchErrorQueryTooLong,
		WebSearchErrorRequestTooLarge:
		return normalized
	default:
		return ""
	}
}

func buildWebSearchResultBlock(result WebSearchResult) map[string]any {
	return buildWebSearchResultBlockWithOpaqueID(result, newWebSearchOpaqueID())
}

func buildWebSearchResultBlockWithOpaqueID(result WebSearchResult, opaqueID string) map[string]any {
	return map[string]any{
		"type":              "web_search_result",
		"title":             result.Title,
		"url":               result.URL,
		"encrypted_content": sealWebSearchOpaquePayload("content", result, opaqueID),
		"page_age":          webSearchPageAge(result.PublishedDate),
	}
}

func webSearchPageAge(publishedDate *int64) any {
	if publishedDate == nil || *publishedDate <= 0 {
		return nil
	}
	seconds := *publishedDate
	// Some MCP implementations return Unix milliseconds.
	if seconds > 100000000000 {
		seconds /= 1000
	}
	return time.Unix(seconds, 0).UTC().Format("January 2, 2006")
}

func newWebSearchOpaqueKey() [32]byte {
	var key [32]byte
	if _, err := io.ReadFull(rand.Reader, key[:]); err == nil {
		return key
	}
	return sha256.Sum256([]byte(fmt.Sprintf("sub2api-web-search-%d", time.Now().UnixNano())))
}

type webSearchOpaqueEnvelope struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

func ensureWebSearchOpaqueID(results *WebSearchResults) string {
	if results == nil {
		return newWebSearchOpaqueID()
	}
	if opaqueID := strings.TrimSpace(results.OpaqueID); opaqueID != "" {
		return opaqueID
	}
	results.OpaqueID = newWebSearchOpaqueID()
	return results.OpaqueID
}

func newWebSearchOpaqueID() string {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		digest := sha256.Sum256([]byte(fmt.Sprintf("sub2api-web-search-id-%d", time.Now().UnixNano())))
		copy(raw[:], digest[:16])
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func sealWebSearchOpaquePayload(kind string, result WebSearchResult, opaqueIDs ...string) string {
	snippet := ""
	if result.Snippet != nil {
		snippet = strings.TrimSpace(*result.Snippet)
	}
	plain, err := json.Marshal(webSearchOpaqueEnvelope{
		Kind: kind, Title: result.Title, URL: result.URL, Snippet: snippet,
	})
	if err != nil {
		return ""
	}
	opaqueID := ""
	if len(opaqueIDs) > 0 {
		opaqueID = strings.TrimSpace(opaqueIDs[0])
	}
	if opaqueID == "" {
		opaqueID = newWebSearchOpaqueID()
	}

	block, err := aes.NewCipher(webSearchOpaqueKey[:])
	if err != nil {
		return fallbackWebSearchOpaquePayload(kind, opaqueID, plain)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fallbackWebSearchOpaquePayload(kind, opaqueID, plain)
	}
	salt := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fallbackWebSearchOpaquePayload(kind, opaqueID, plain)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fallbackWebSearchOpaquePayload(kind, opaqueID, plain)
	}
	var sealed []byte
	if webSearchOpaqueKindCode(kind) == 4 {
		sealed = webSearchOpaqueIndexToken(opaqueID, plain)
	} else {
		sealed = gcm.Seal(nil, nonce, plain, []byte("sub2api:web-search:v1"))
	}
	authenticator := webSearchOpaqueAuthenticator(kind, opaqueID, salt, nonce, sealed)
	return encodeWebSearchOpaqueProto(kind, opaqueID, salt, nonce, authenticator, sealed)
}

func fallbackWebSearchOpaquePayload(kind, opaqueID string, plain []byte) string {
	digest := sha256.Sum256(plain)
	sealed := digest[:]
	if webSearchOpaqueKindCode(kind) == 4 {
		sealed = digest[:19]
	}
	authenticator := webSearchOpaqueAuthenticator(kind, opaqueID, digest[:12], digest[12:24], sealed)
	return encodeWebSearchOpaqueProto(kind, opaqueID, digest[:12], digest[12:24], authenticator, sealed)
}

func webSearchOpaqueIndexToken(opaqueID string, plain []byte) []byte {
	mac := hmac.New(sha256.New, webSearchOpaqueKey[:])
	_, _ = mac.Write([]byte("sub2api:web-search:index:" + opaqueID + ":"))
	_, _ = mac.Write(plain)
	return mac.Sum(nil)[:19]
}

func webSearchOpaqueAuthenticator(kind, opaqueID string, salt, nonce, sealed []byte) []byte {
	mac := hmac.New(sha512.New384, webSearchOpaqueKey[:])
	_, _ = mac.Write([]byte("sub2api:web-search:v2:" + kind + ":" + opaqueID + ":"))
	_, _ = mac.Write(salt)
	_, _ = mac.Write(nonce)
	_, _ = mac.Write(sealed)
	return mac.Sum(nil)
}

// encodeWebSearchOpaqueProto mirrors the captured Anthropic opaque-envelope
// topology while keeping the payload locally authenticated and replayable:
// outer field 2 contains the envelope, and outer field 3 distinguishes result
// content (0) from a citation index (4). The inner header is shared by every
// opaque value produced for one search response.
func encodeWebSearchOpaqueProto(kind, opaqueID string, salt, nonce, authenticator, sealed []byte) string {
	header := appendWebSearchProtoVarint(nil, 1, 18)
	header = appendWebSearchProtoVarint(header, 3, 2)
	header = appendWebSearchProtoBytes(header, 4, []byte(opaqueID))

	inner := appendWebSearchProtoBytes(nil, 1, header)
	inner = appendWebSearchProtoBytes(inner, 2, salt)
	inner = appendWebSearchProtoBytes(inner, 3, nonce)
	inner = appendWebSearchProtoBytes(inner, 4, authenticator)
	inner = appendWebSearchProtoBytes(inner, 5, sealed)

	wire := appendWebSearchProtoBytes(nil, 2, inner)
	wire = appendWebSearchProtoVarint(wire, 3, webSearchOpaqueKindCode(kind))
	return base64.StdEncoding.EncodeToString(wire)
}

func webSearchOpaqueKindCode(kind string) uint64 {
	if strings.EqualFold(strings.TrimSpace(kind), "index") {
		return 4
	}
	return 0
}

func webSearchOpaqueKind(code uint64) string {
	if code == 4 {
		return "index"
	}
	return "content"
}

func appendWebSearchProtoBytes(dst []byte, field uint64, value []byte) []byte {
	dst = appendWebSearchProtoUvarint(dst, field<<3|2)
	dst = appendWebSearchProtoUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendWebSearchProtoVarint(dst []byte, field, value uint64) []byte {
	dst = appendWebSearchProtoUvarint(dst, field<<3)
	return appendWebSearchProtoUvarint(dst, value)
}

func appendWebSearchProtoUvarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func openWebSearchOpaquePayload(token string) (webSearchOpaqueEnvelope, bool) {
	var envelope webSearchOpaqueEnvelope
	wire, err := decodeWebSearchOpaqueBase64(token)
	if err != nil || len(wire) < 2 {
		return envelope, false
	}
	// Retain the v1 reader for continuations generated by an older process.
	if wire[0] == 1 {
		return openLegacyWebSearchOpaquePayload(wire)
	}
	outerBytes, outerVarints, ok := parseWebSearchOpaqueProto(wire)
	inner := outerBytes[2]
	if !ok || len(inner) == 0 {
		return envelope, false
	}
	innerBytes, _, ok := parseWebSearchOpaqueProto(inner)
	if !ok || len(innerBytes[1]) == 0 || len(innerBytes[2]) != 12 || len(innerBytes[3]) != 12 || len(innerBytes[4]) != sha512.Size384 || len(innerBytes[5]) == 0 {
		return envelope, false
	}
	headerBytes, headerVarints, ok := parseWebSearchOpaqueProto(innerBytes[1])
	if !ok || headerVarints[1] != 18 || headerVarints[3] != 2 || len(headerBytes[4]) != 36 {
		return envelope, false
	}
	kind := webSearchOpaqueKind(outerVarints[3])
	opaqueID := string(headerBytes[4])
	if !hmac.Equal(innerBytes[4], webSearchOpaqueAuthenticator(kind, opaqueID, innerBytes[2], innerBytes[3], innerBytes[5])) {
		return envelope, false
	}
	// Citation indices are authenticated references, not replayable content.
	if kind == "index" {
		return envelope, false
	}
	block, err := aes.NewCipher(webSearchOpaqueKey[:])
	if err != nil {
		return envelope, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(innerBytes[3]) != gcm.NonceSize() {
		return envelope, false
	}
	plain, err := gcm.Open(nil, innerBytes[3], innerBytes[5], []byte("sub2api:web-search:v1"))
	if err != nil || json.Unmarshal(plain, &envelope) != nil {
		return webSearchOpaqueEnvelope{}, false
	}
	if envelope.Kind != kind {
		return webSearchOpaqueEnvelope{}, false
	}
	return envelope, true
}

func decodeWebSearchOpaqueBase64(token string) ([]byte, error) {
	value := strings.TrimSpace(token)
	if wire, err := base64.StdEncoding.DecodeString(value); err == nil {
		return wire, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func openLegacyWebSearchOpaquePayload(wire []byte) (webSearchOpaqueEnvelope, bool) {
	var envelope webSearchOpaqueEnvelope
	block, err := aes.NewCipher(webSearchOpaqueKey[:])
	if err != nil {
		return envelope, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(wire) <= 1+gcm.NonceSize() {
		return envelope, false
	}
	nonce := wire[1 : 1+gcm.NonceSize()]
	plain, err := gcm.Open(nil, nonce, wire[1+gcm.NonceSize():], []byte("sub2api:web-search:v1"))
	if err != nil || json.Unmarshal(plain, &envelope) != nil {
		return webSearchOpaqueEnvelope{}, false
	}
	return envelope, true
}

func parseWebSearchOpaqueProto(wire []byte) (map[uint64][]byte, map[uint64]uint64, bool) {
	bytesFields := make(map[uint64][]byte)
	varintFields := make(map[uint64]uint64)
	for offset := 0; offset < len(wire); {
		tag, next, ok := readWebSearchProtoUvarint(wire, offset)
		if !ok || tag>>3 == 0 {
			return nil, nil, false
		}
		offset = next
		field, wireType := tag>>3, tag&7
		switch wireType {
		case 0:
			value, next, ok := readWebSearchProtoUvarint(wire, offset)
			if !ok {
				return nil, nil, false
			}
			varintFields[field] = value
			offset = next
		case 2:
			size, next, ok := readWebSearchProtoUvarint(wire, offset)
			if !ok || size > uint64(len(wire)-next) {
				return nil, nil, false
			}
			end := next + int(size)
			bytesFields[field] = wire[next:end]
			offset = end
		default:
			return nil, nil, false
		}
	}
	return bytesFields, varintFields, true
}

func readWebSearchProtoUvarint(wire []byte, offset int) (uint64, int, bool) {
	var value uint64
	for shift := uint(0); offset < len(wire) && shift < 70; shift += 7 {
		current := wire[offset]
		offset++
		value |= uint64(current&0x7f) << shift
		if current < 0x80 {
			return value, offset, true
		}
	}
	return 0, offset, false
}

type webSearchCitedTextSegment struct {
	Text      string
	Citations []map[string]any
}

func segmentWebSearchCitationsInContent(content []any, searches []SearchIndicator) []any {
	if len(searches) == 0 {
		return content
	}
	for i := len(content) - 1; i >= 0; i-- {
		block, ok := content[i].(map[string]any)
		if !ok || getInterfaceString(block["type"]) != "text" {
			continue
		}
		text := getInterfaceString(block["text"])
		segments := buildWebSearchCitationSegments(searches, text)
		if len(segments) == 0 {
			return content
		}

		replacement := make([]any, 0, len(content)+len(segments)-1)
		replacement = append(replacement, content[:i]...)
		for _, segment := range segments {
			segmentedBlock := make(map[string]any, len(block)+1)
			for key, value := range block {
				segmentedBlock[key] = value
			}
			segmentedBlock["text"] = segment.Text
			delete(segmentedBlock, "citations")
			if len(segment.Citations) > 0 {
				segmentedBlock["citations"] = segment.Citations
			}
			replacement = append(replacement, segmentedBlock)
		}
		replacement = append(replacement, content[i+1:]...)
		return replacement
	}
	return content
}

// buildWebSearchCitationSegments associates each citation with the line of
// answer text that actually contains its result URL. Anthropic emits cited and
// uncited prose as separate text blocks; keeping that association prevents a
// citation from appearing to qualify the entire answer. Kiro commonly writes
// search summaries as Markdown bullets, making line boundaries the most stable
// semantic boundary available without rewriting model output.
func buildWebSearchCitationSegments(searches []SearchIndicator, responseText string) []webSearchCitedTextSegment {
	citations := buildWebSearchCitations(searches, responseText)
	if len(citations) == 0 || responseText == "" {
		return nil
	}

	type citedRange struct {
		start     int
		end       int
		citations []map[string]any
	}
	ranges := make([]citedRange, 0, len(citations))
	for _, citation := range citations {
		url := strings.TrimSpace(getInterfaceString(citation["url"]))
		position := strings.Index(responseText, url)
		if url == "" || position < 0 {
			continue
		}
		start := strings.LastIndex(responseText[:position], "\n") + 1
		end := len(responseText)
		if lineEnd := strings.Index(responseText[position+len(url):], "\n"); lineEnd >= 0 {
			end = position + len(url) + lineEnd + 1
		}
		ranges = append(ranges, citedRange{start: start, end: end, citations: []map[string]any{citation}})
	}

	// When Kiro paraphrases a result without including its URL, preserve the
	// existing fallback: cite the complete answer with the first result.
	if len(ranges) == 0 {
		return []webSearchCitedTextSegment{{Text: responseText, Citations: citations}}
	}

	slices.SortFunc(ranges, func(left, right citedRange) int {
		if left.start != right.start {
			return cmp.Compare(left.start, right.start)
		}
		return cmp.Compare(left.end, right.end)
	})
	merged := make([]citedRange, 0, len(ranges))
	for _, current := range ranges {
		if len(merged) == 0 || current.start >= merged[len(merged)-1].end {
			merged = append(merged, current)
			continue
		}
		last := &merged[len(merged)-1]
		if current.end > last.end {
			last.end = current.end
		}
		last.citations = append(last.citations, current.citations...)
	}

	segments := make([]webSearchCitedTextSegment, 0, len(merged)*2+1)
	cursor := 0
	for _, cited := range merged {
		if cited.start > cursor {
			segments = append(segments, webSearchCitedTextSegment{Text: responseText[cursor:cited.start]})
		}
		segments = append(segments, webSearchCitedTextSegment{
			Text:      responseText[cited.start:cited.end],
			Citations: cited.citations,
		})
		cursor = cited.end
	}
	if cursor < len(responseText) {
		segments = append(segments, webSearchCitedTextSegment{Text: responseText[cursor:]})
	}
	return segments
}

func buildWebSearchCitations(searches []SearchIndicator, responseText string) []map[string]any {
	type opaqueResult struct {
		result   WebSearchResult
		opaqueID string
	}
	all := make([]opaqueResult, 0)
	for _, search := range searches {
		if search.Results != nil {
			opaqueID := ensureWebSearchOpaqueID(search.Results)
			for _, result := range search.Results.Results {
				all = append(all, opaqueResult{result: result, opaqueID: opaqueID})
			}
		}
	}
	if len(all) == 0 {
		return nil
	}

	matched := make([]opaqueResult, 0, 4)
	seen := make(map[string]struct{})
	for _, opaque := range all {
		result := opaque.result
		url := strings.TrimSpace(result.URL)
		if url == "" || !strings.Contains(responseText, url) {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		matched = append(matched, opaque)
		if len(matched) == 4 {
			break
		}
	}
	// Anthropic web-search responses always carry citations. Kiro sometimes
	// summarizes a result without repeating its URL, so attach the first result
	// rather than returning a structurally citation-free response.
	if len(matched) == 0 {
		matched = append(matched, all[0])
	}

	citations := make([]map[string]any, 0, len(matched))
	for _, opaque := range matched {
		result := opaque.result
		citedText := strings.TrimSpace(result.Title)
		if result.Snippet != nil && strings.TrimSpace(*result.Snippet) != "" {
			citedText = truncateWebSearchCitationText(strings.TrimSpace(*result.Snippet), 150)
		}
		citations = append(citations, map[string]any{
			"type":            "web_search_result_location",
			"url":             result.URL,
			"title":           result.Title,
			"encrypted_index": sealWebSearchOpaquePayload("index", result, opaque.opaqueID),
			"cited_text":      citedText,
		})
	}
	return citations
}

func truncateWebSearchCitationText(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes])
}

func ExtractWebSearchToolUseFromResponse(responsePayload []byte) (toolUseID, query string, ok bool) {
	content := gjson.GetBytes(responsePayload, "content")
	if !content.IsArray() {
		return "", "", false
	}
	for _, block := range content.Array() {
		if block.Get("type").String() != "tool_use" {
			continue
		}
		name := block.Get("name").String()
		if !isWebSearchToolName(name, "") {
			continue
		}
		query = strings.TrimSpace(block.Get("input.query").String())
		if query == "" {
			continue
		}
		return block.Get("id").String(), query, true
	}
	return "", "", false
}

func isWebSearchToolName(name, toolType string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	toolType = strings.ToLower(strings.TrimSpace(toolType))
	if strings.HasPrefix(toolType, "web_search") || toolType == "google_search" {
		return true
	}
	switch name {
	case "web_search", "web_search_20250305", "google_search", "remote_web_search":
		return true
	default:
		return false
	}
}

func getInterfaceString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	default:
		return strings.TrimSpace(fmt.Sprint(val))
	}
}

func formatToolResultText(results *WebSearchResults) string {
	if results == nil || len(results.Results) == 0 {
		return "No search results found."
	}
	payload, err := json.MarshalIndent(results.Results, "", "  ")
	if err != nil {
		return "Found search results, but failed to format them."
	}
	return fmt.Sprintf("Found %d search result(s):\n\n%s", len(results.Results), string(payload))
}

func searchGuidanceText() string {
	now := time.Now()
	return fmt.Sprintf(`<search_guidance>
Current date: %s (%s)

IMPORTANT: Evaluate the search results above carefully. If the results are:
- Mostly spam, SEO junk, or unrelated websites
- Missing actual information about the query topic
- Outdated or not matching the requested time frame

Then you MUST use the web_search tool again with a refined query. Try:
- Rephrasing in English for better coverage
- Using more specific keywords
- Adding date context

Do NOT apologize for bad results without first attempting a re-search.
</search_guidance>`, now.Format("January 2, 2006"), now.Format("Monday"))
}
