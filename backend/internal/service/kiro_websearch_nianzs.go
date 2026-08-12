// Source-faithful, namespaced integration of kiro_websearch.go from
// github.com/nianzs/sub2api at d483aefe7c2d1da5139c6f5b011eb6843b1e7dbb.
// Only package identifiers and the kiro package import are rewritten so the
// legacy engine remains available for an immediate rollback.

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
)

const nianzsKiroMaxWebSearchIterations = 5

var (
	nianzsErrKiroWebSearchFallback = errors.New("kiro web search fallback")
	nianzsKiroWebSearchDescCache   sync.Map
)

type nianzsKiroWebSearchExecution struct {
	ResponseBody []byte
	Usage        ClaudeUsage
	RequestID    string
}

type nianzsKiroWebSearchHTTPError struct {
	Response *http.Response
}

type nianzsKiroWebSearchMCPError struct {
	StatusCode int
	Code       int
	Message    string
}

type nianzsKiroStreamChunkCollector struct {
	chunks [][]byte
}

func (e *nianzsKiroWebSearchHTTPError) Error() string {
	if e == nil || e.Response == nil {
		return "kiro web search http error"
	}
	return fmt.Sprintf("kiro web search http error: %d", e.Response.StatusCode)
}

func (e *nianzsKiroWebSearchMCPError) Error() string {
	if e == nil {
		return "kiro web search MCP error"
	}
	return fmt.Sprintf("kiro web search MCP error: status=%d code=%d message=%s", e.StatusCode, e.Code, strings.TrimSpace(e.Message))
}

func nianzsKiroWebSearchErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var mcpErr *nianzsKiroWebSearchMCPError
	if errors.As(err, &mcpErr) {
		switch mcpErr.StatusCode {
		case http.StatusTooManyRequests:
			return nianzskiro.WebSearchErrorTooManyRequests
		case http.StatusRequestEntityTooLarge:
			return nianzskiro.WebSearchErrorRequestTooLarge
		}
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "max_uses"), strings.Contains(message, "max uses"):
		return nianzskiro.WebSearchErrorMaxUsesExceeded
	case strings.Contains(message, "query") && strings.Contains(message, "too long"):
		return nianzskiro.WebSearchErrorQueryTooLong
	case strings.Contains(message, "request") && strings.Contains(message, "too large"):
		return nianzskiro.WebSearchErrorRequestTooLarge
	case strings.Contains(message, "invalid"):
		return nianzskiro.WebSearchErrorInvalidToolInput
	default:
		return nianzskiro.WebSearchErrorUnavailable
	}
}

func (w *nianzsKiroStreamChunkCollector) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.chunks = append(w.chunks, append([]byte(nil), p...))
	}
	return len(p), nil
}

func nianzsBufferKiroAnthropicStream(ctx context.Context, body io.Reader, responseModel string, inputTokens int, requestCtx nianzskiro.KiroRequestContext) ([][]byte, *nianzskiro.StreamResult, error) {
	collector := &nianzsKiroStreamChunkCollector{}
	// The outer WebSearch stream owns message_start and the single protocol
	// ping. Preserve terminal response hints from the real upstream request,
	// but prevent a second ping from appearing after the server-tool blocks.
	requestCtx.EmitProtocolPing = false
	requestCtx.RequireTerminalEvent = true
	result, err := nianzskiro.StreamEventStreamAsAnthropicWithContext(ctx, body, collector, responseModel, inputTokens, requestCtx)
	if err != nil {
		return nil, nil, err
	}
	return collector.chunks, result, nil
}

func nianzsWriteSSEChunks(w io.Writer, chunks [][]byte) error {
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		if _, err := w.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

func nianzsWriteAnthropicMessageStart(w io.Writer, msgID, model string, inputTokens int, cacheUsage *nianzsKiroCacheEmulationUsage) error {
	if strings.TrimSpace(msgID) == "" {
		msgID = "msg_" + nianzskiro.GenerateToolUseID()
	}
	if strings.TrimSpace(model) == "" {
		model = "kiro"
	}
	usage := map[string]any{
		"input_tokens":                inputTokens,
		"output_tokens":               0,
		"cache_creation_input_tokens": 0,
		"cache_read_input_tokens":     0,
		"cache_creation": map[string]any{
			"ephemeral_5m_input_tokens": 0,
			"ephemeral_1h_input_tokens": 0,
		},
		"service_tier":  "standard",
		"inference_geo": "not_available",
	}
	if cacheUsage != nil {
		usage["input_tokens"] = cacheUsage.InputTokens
		usage["cache_creation_input_tokens"] = cacheUsage.CacheCreationInputTokens
		usage["cache_read_input_tokens"] = cacheUsage.CacheReadInputTokens
		usage["cache_creation"] = map[string]any{
			"ephemeral_5m_input_tokens": cacheUsage.CacheCreation5mInputTokens,
			"ephemeral_1h_input_tokens": cacheUsage.CacheCreation1hInputTokens,
		}
	}
	payload, err := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_details":  nil,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         usage,
		},
	})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "event: message_start\ndata: "+string(payload)+"\n\n")
	return err
}

func nianzsWriteAnthropicPing(w io.Writer) error {
	_, err := io.WriteString(w, "event: ping\ndata: {\"type\":\"ping\"}\n\n")
	return err
}

func (s *GatewayService) streamKiroWebSearchAsAnthropicNianzs(
	ctx context.Context, account *Account, anthropicBody []byte, mappedModel, requestModel, token string, inputTokens int, headers http.Header, w io.Writer, plan *nianzsKiroCacheEmulationPlan,
) error {
	query := nianzskiro.ExtractSearchQuery(anthropicBody)
	if strings.TrimSpace(query) == "" {
		return nianzsErrKiroWebSearchFallback
	}

	currentBody, err := nianzskiro.NormalizeWebSearchHistoryForKiro(anthropicBody)
	if err != nil {
		currentBody = anthropicBody
	}
	currentBody, err = nianzskiro.ReplaceWebSearchToolDescription(currentBody)
	if err != nil {
		currentBody = anthropicBody
	}
	currentToolUseID := "srvtoolu_" + nianzskiro.GenerateToolUseID()
	searchConfig := nianzskiro.ExtractWebSearchToolConfig(anthropicBody)
	maxIterations := min(searchConfig.MaxUses, nianzsKiroMaxWebSearchIterations)
	nextContentBlockIndex := 0
	webSearchRequests := 0
	searches := make([]nianzskiro.SearchIndicator, 0, 2)
	emitProtocolPing := anthropicBetaTokensContains(getHeaderRaw(headers, "Anthropic-Beta"), "claude-code-20250219")

	if err := nianzsWriteAnthropicMessageStart(w, "", requestModel, inputTokens, plan.result()); err != nil {
		return err
	}

	for iteration := 0; iteration < maxIterations; iteration++ {
		// Match Claude's direct server-tool stream: expose the server_tool_use
		// immediately, then perform the MCP network request, and emit its paired
		// result. This preserves both protocol order and first-visible latency.
		for chunkIndex, chunk := range nianzskiro.GenerateSearchToolUseEvents(query, currentToolUseID, nextContentBlockIndex) {
			if _, err := w.Write(chunk); err != nil {
				return err
			}
			if iteration == 0 && chunkIndex == 0 && emitProtocolPing {
				if err := nianzsWriteAnthropicPing(w); err != nil {
					return err
				}
			}
		}
		nextContentBlockIndex++
		s.prefetchKiroWebSearchDescriptionNianzs(ctx, account, token)

		results, nextToken, mcpErr := s.callKiroWebSearchMCPNianzs(ctx, account, token, query)
		resultErrorCode := nianzsKiroWebSearchErrorCode(mcpErr)
		if strings.TrimSpace(nextToken) != "" {
			token = nextToken
		}
		if mcpErr != nil {
			results = nil
		} else if results != nil {
			results = nianzskiro.ApplyWebSearchDomainFilters(results, searchConfig)
			webSearchRequests++
		}
		searches = append(searches, nianzskiro.SearchIndicator{
			ToolUseID: currentToolUseID,
			Query:     query,
			Results:   results,
			ErrorCode: resultErrorCode,
		})

		if err := nianzsWriteSSEChunks(w, nianzskiro.GenerateSearchToolResultEvents(currentToolUseID, results, resultErrorCode, nextContentBlockIndex)); err != nil {
			return err
		}
		nextContentBlockIndex++

		currentBody, err = nianzskiro.InjectToolResultsClaude(currentBody, currentToolUseID, query, results)
		if err != nil {
			return nianzsErrKiroWebSearchFallback
		}
		if iteration+1 >= maxIterations {
			if withoutSearch, removeErr := nianzskiro.RemoveWebSearchTools(currentBody); removeErr == nil {
				currentBody = withoutSearch
			}
		}

		resp, requestCtx, err := s.executeKiroUpstreamNianzs(ctx, account, currentBody, mappedModel, requestModel, token, headers)
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &nianzsKiroWebSearchHTTPError{Response: resp}
		}
		chunks, _, streamErr := func() ([][]byte, *nianzskiro.StreamResult, error) {
			defer func() { _ = resp.Body.Close() }()
			return nianzsBufferKiroAnthropicStream(ctx, resp.Body, requestModel, inputTokens, requestCtx)
		}()
		if streamErr != nil {
			return streamErr
		}
		if iteration == 0 {
			// A 2xx header is not proof that a Kiro Event Stream completed. Commit
			// only after the first turn reached explicit completion evidence.
			plan.commit()
		}

		analysis := nianzskiro.AnalyzeBufferedStream(chunks)
		if analysis.HasWebSearchToolUse && strings.TrimSpace(analysis.WebSearchQuery) != "" && iteration+1 < maxIterations {
			filtered := nianzskiro.FilterChunksForClient(chunks, analysis.WebSearchToolUseIndex, nextContentBlockIndex)
			if err := nianzsWriteSSEChunks(w, filtered); err != nil {
				return err
			}
			if maxIndex := nianzskiro.MaxContentBlockIndex(filtered); maxIndex >= nextContentBlockIndex {
				nextContentBlockIndex = maxIndex + 1
			}
			query = analysis.WebSearchQuery
			// The upstream custom-tool ID often carries toolu_bdrk_. A server
			// tool exposed through Anthropic must use a fresh srvtoolu_ ID.
			currentToolUseID = "srvtoolu_" + nianzskiro.GenerateToolUseID()
			continue
		}

		for _, chunk := range nianzskiro.FinalizeWebSearchSSEChunks(chunks, nextContentBlockIndex, webSearchRequests, searches) {
			if _, err := w.Write(chunk); err != nil {
				return err
			}
		}
		return nil
	}

	return fmt.Errorf("kiro web search exceeded max iterations")
}

func (s *GatewayService) executeKiroWebSearchNianzs(ctx context.Context, account *Account, group *Group, anthropicBody []byte, mappedModel, requestModel, token string, headers http.Header) (*nianzsKiroWebSearchExecution, error) {
	query := nianzskiro.ExtractSearchQuery(anthropicBody)
	if strings.TrimSpace(query) == "" {
		return nil, nianzsErrKiroWebSearchFallback
	}

	currentBody, err := nianzskiro.NormalizeWebSearchHistoryForKiro(anthropicBody)
	if err != nil {
		currentBody = anthropicBody
	}
	currentBody, err = nianzskiro.ReplaceWebSearchToolDescription(currentBody)
	if err != nil {
		currentBody = anthropicBody
	}

	inputTokens := nianzsEstimateKiroInputTokens(ctx, anthropicBody)
	currentToolUseID := "srvtoolu_" + nianzskiro.GenerateToolUseID()
	searchConfig := nianzskiro.ExtractWebSearchToolConfig(anthropicBody)
	maxIterations := min(searchConfig.MaxUses, nianzsKiroMaxWebSearchIterations)
	searches := make([]nianzskiro.SearchIndicator, 0, 2)
	requestID := ""
	plan := s.prepareKiroCacheEmulationUsageNianzs(ctx, account, group, anthropicBody, mappedModel, inputTokens)

	for iteration := 0; iteration < maxIterations; iteration++ {
		s.prefetchKiroWebSearchDescriptionNianzs(ctx, account, token)

		results, nextToken, mcpErr := s.callKiroWebSearchMCPNianzs(ctx, account, token, query)
		resultErrorCode := nianzsKiroWebSearchErrorCode(mcpErr)
		if strings.TrimSpace(nextToken) != "" {
			token = nextToken
		}
		if mcpErr != nil {
			results = nil
		} else if results != nil {
			results = nianzskiro.ApplyWebSearchDomainFilters(results, searchConfig)
		}
		searches = append(searches, nianzskiro.SearchIndicator{
			ToolUseID: currentToolUseID,
			Query:     query,
			Results:   results,
			ErrorCode: resultErrorCode,
		})

		currentBody, err = nianzskiro.InjectToolResultsClaude(currentBody, currentToolUseID, query, results)
		if err != nil {
			return nil, nianzsErrKiroWebSearchFallback
		}
		if iteration+1 >= maxIterations {
			if withoutSearch, removeErr := nianzskiro.RemoveWebSearchTools(currentBody); removeErr == nil {
				currentBody = withoutSearch
			}
		}

		resp, _, err := s.executeKiroUpstreamNianzs(ctx, account, currentBody, mappedModel, requestModel, token, headers)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &nianzsKiroWebSearchHTTPError{Response: resp}
		}

		parseResult, parseErr := func() (*nianzskiro.ParseResult, error) {
			defer func() { _ = resp.Body.Close() }()
			var cacheUsage *nianzsKiroCacheEmulationUsage
			if iteration == 0 {
				cacheUsage = plan.result()
			}
			return nianzskiro.ParseNonStreamingEventStreamWithContext(resp.Body, requestModel, nianzskiro.KiroRequestContext{
				CacheEmulationUsage:  cacheUsage.toKiroUsage(),
				EstimatedInputTokens: inputTokens,
				RequireTerminalEvent: true,
			})
		}()
		if parseErr != nil {
			return nil, parseErr
		}
		if iteration == 0 {
			plan.commit()
		}
		if requestID == "" {
			requestID = nianzsBuildKiroRequestID(resp)
		}

		_, nextQuery, hasNext := nianzskiro.ExtractWebSearchToolUseFromResponse(parseResult.ResponseBody)
		if !hasNext || strings.TrimSpace(nextQuery) == "" || iteration+1 >= maxIterations {
			finalBody, injectErr := nianzskiro.InjectSearchIndicatorsInResponse(parseResult.ResponseBody, searches)
			if injectErr == nil {
				parseResult.ResponseBody = finalBody
			}
			return &nianzsKiroWebSearchExecution{
				ResponseBody: parseResult.ResponseBody,
				Usage:        nianzsKiroUsageToClaude(parseResult.Usage, inputTokens),
				RequestID:    requestID,
			}, nil
		}

		query = nextQuery
		// Never expose a custom Kiro/Bedrock tool ID as an Anthropic server
		// tool ID. The synthetic request/result pair owns its own namespace.
		currentToolUseID = "srvtoolu_" + nianzskiro.GenerateToolUseID()
	}

	return nil, fmt.Errorf("kiro web search exceeded max iterations")
}

func (s *GatewayService) prefetchKiroWebSearchDescriptionNianzs(ctx context.Context, account *Account, token string) {
	endpoint := nianzskiro.BuildMcpEndpoint(nianzsKiroAPIRegion(account))
	if cached, ok := nianzsKiroWebSearchDescCache.Load(endpoint); ok {
		if desc, ok := cached.(string); ok && strings.TrimSpace(desc) != "" {
			nianzskiro.SetCachedWebSearchDescription(desc)
		}
		return
	}

	reqBody, _ := json.Marshal(nianzskiro.MCPRequest{
		ID:      "tools_list",
		JSONRPC: "2.0",
		Method:  "tools/list",
	})
	resp, _, err := s.doKiroMCPJSONRequestNianzs(ctx, account, endpoint, reqBody, token)
	if err != nil || resp == nil {
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	var result nianzskiro.MCPResponse
	if err := json.Unmarshal(body, &result); err != nil || result.Result == nil {
		return
	}
	for _, tool := range result.Result.Tools {
		if strings.EqualFold(tool.Name, "web_search") && strings.TrimSpace(tool.Description) != "" {
			nianzsKiroWebSearchDescCache.Store(endpoint, tool.Description)
			nianzskiro.SetCachedWebSearchDescription(tool.Description)
			return
		}
	}
}

func (s *GatewayService) callKiroWebSearchMCPNianzs(ctx context.Context, account *Account, token, query string) (*nianzskiro.WebSearchResults, string, error) {
	reqBody, err := json.Marshal(nianzsBuildKiroWebSearchMCPRequest(query))
	if err != nil {
		return nil, token, err
	}

	endpoint := nianzskiro.BuildMcpEndpoint(nianzsKiroAPIRegion(account))
	resp, nextToken, err := s.doKiroMCPJSONRequestNianzs(ctx, account, endpoint, reqBody, token)
	if err != nil {
		return nil, nextToken, err
	}
	if resp == nil {
		return nil, nextToken, fmt.Errorf("kiro web search returned nil response")
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nextToken, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nextToken, &nianzsKiroWebSearchMCPError{StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
	}

	var parsed nianzskiro.MCPResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nextToken, err
	}
	if parsed.Error != nil {
		msg := "unknown error"
		if parsed.Error.Message != nil && strings.TrimSpace(*parsed.Error.Message) != "" {
			msg = strings.TrimSpace(*parsed.Error.Message)
		}
		code := 0
		if parsed.Error.Code != nil {
			code = *parsed.Error.Code
		}
		return nil, nextToken, &nianzsKiroWebSearchMCPError{StatusCode: resp.StatusCode, Code: code, Message: msg}
	}

	return nianzskiro.ParseSearchResults(&parsed), nextToken, nil
}

func nianzsBuildKiroWebSearchMCPRequest(query string) nianzskiro.MCPRequest {
	return nianzskiro.MCPRequest{
		ID:      fmt.Sprintf("web_search_%s", nianzskiro.GenerateToolUseID()),
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params: map[string]any{
			"name": "web_search",
			"arguments": map[string]any{
				"query": query,
				"_meta": map[string]any{
					"_isValid":        true,
					"_activePath":     []string{"query"},
					"_completedPaths": [][]string{{"query"}},
				},
			},
		},
	}
}

func (s *GatewayService) doKiroMCPJSONRequestNianzs(ctx context.Context, account *Account, endpoint string, payload []byte, token string) (*http.Response, string, error) {
	currentToken := token
	accountKey := nianzsBuildKiroAccountKey(account)
	proxyURL := nianzsKiroProxyURL(account)
	tlsProfile := s.tlsFPProfileService.ResolveTLSProfile(account)

	for attempt := 0; attempt < 3; attempt++ {
		if err := s.checkKiroCooldownNianzs(ctx, accountKey); err != nil {
			if failoverErr := nianzsAsKiroCooldownFailoverError(err); failoverErr != nil {
				return nil, currentToken, failoverErr
			}
			return nil, currentToken, err
		}

		req, err := nianzsNewKiroJSONRequest(ctx, endpoint, payload, currentToken, accountKey, nianzsBuildKiroMachineID(account), "", account)
		if err != nil {
			return nil, currentToken, err
		}

		resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
		if err != nil {
			return nil, currentToken, err
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			respBody, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				return nil, currentToken, readErr
			}
			if resp.StatusCode == http.StatusForbidden && nianzsIsKiroSuspendedBody(respBody) {
				if _, err := s.markKiroSuspendedNianzs(ctx, accountKey); err != nil {
					return nil, currentToken, err
				}
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			if resp.StatusCode == http.StatusForbidden && !nianzsIsKiroTokenErrorBody(respBody) {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			if s.nianzsKiroTokenProvider == nil {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			refreshedToken, refreshErr := s.nianzsKiroTokenProvider.ForceRefreshAccessToken(ctx, account)
			if refreshErr != nil {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			currentToken = refreshedToken
			accountKey = nianzsBuildKiroAccountKey(account)
			if sleepErr := nianzsSleepKiroRetry(ctx, attempt); sleepErr != nil {
				return nil, currentToken, sleepErr
			}
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			if _, err := s.markKiro429Nianzs(ctx, account.ID, accountKey); err != nil {
				_ = resp.Body.Close()
				return nil, currentToken, err
			}
		}
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
			if attempt < 2 {
				_ = resp.Body.Close()
				if sleepErr := nianzsSleepKiroRetry(ctx, attempt); sleepErr != nil {
					return nil, currentToken, sleepErr
				}
				continue
			}
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := s.markKiroSuccessNianzs(ctx, account.ID, accountKey); err != nil {
				_ = resp.Body.Close()
				return nil, currentToken, err
			}
		}

		return resp, currentToken, nil
	}

	return nil, currentToken, fmt.Errorf("kiro mcp request retries exhausted")
}
