package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const kiroMaxWebSearchIterations = 5

var (
	errKiroWebSearchFallback = errors.New("kiro web search fallback")
	kiroWebSearchDescCache   sync.Map
)

type kiroWebSearchExecution struct {
	ResponseBody []byte
	Usage        ClaudeUsage
	RequestID    string
}

type kiroWebSearchHTTPError struct {
	Response *http.Response
}

type kiroStreamChunkCollector struct {
	chunks [][]byte
}

func (e *kiroWebSearchHTTPError) Error() string {
	if e == nil || e.Response == nil {
		return "kiro web search http error"
	}
	return fmt.Sprintf("kiro web search http error: %d", e.Response.StatusCode)
}

func (w *kiroStreamChunkCollector) Write(p []byte) (int, error) {
	if len(p) > 0 {
		w.chunks = append(w.chunks, append([]byte(nil), p...))
	}
	return len(p), nil
}

func bufferKiroAnthropicStream(ctx context.Context, body io.Reader, mappedModel string, inputTokens int, requestCtx kiropkg.KiroRequestContext) ([][]byte, *kiropkg.StreamResult, error) {
	collector := &kiroStreamChunkCollector{}
	result, err := kiropkg.StreamEventStreamAsAnthropicWithContext(ctx, body, collector, mappedModel, inputTokens, requestCtx)
	if err != nil {
		return nil, nil, err
	}
	return collector.chunks, result, nil
}

func writeSSEChunks(w io.Writer, chunks [][]byte) error {
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

func writeAnthropicMessageStart(w io.Writer, msgID, model string, inputTokens int, cacheUsage *kiroCacheEmulationUsage) error {
	if strings.TrimSpace(msgID) == "" {
		msgID = "msg_" + kiropkg.GenerateToolUseID()
	}
	if strings.TrimSpace(model) == "" {
		model = "kiro"
	}
	usage := map[string]any{
		"input_tokens":  inputTokens,
		"output_tokens": 0,
	}
	if cacheUsage != nil {
		usage["input_tokens"] = cacheUsage.InputTokens
		usage["cache_creation_input_tokens"] = cacheUsage.CacheCreationInputTokens
		usage["cache_read_input_tokens"] = cacheUsage.CacheReadInputTokens
	}
	payload, err := json.Marshal(map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
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

func (s *GatewayService) streamKiroWebSearchAsAnthropic(
	ctx context.Context, account *Account, parsed *ParsedRequest, group *Group, anthropicBody []byte, mappedModel, requestModel, token string, headers http.Header, w io.Writer, completion *kiroStreamCompletion,
) error {
	query := kiropkg.ExtractSearchQuery(anthropicBody)
	if strings.TrimSpace(query) == "" {
		return errKiroWebSearchFallback
	}

	currentBody, err := kiropkg.ReplaceWebSearchToolDescription(anthropicBody)
	if err != nil {
		currentBody = anthropicBody
	}
	currentToolUseID := "srvtoolu_" + kiropkg.GenerateToolUseID()
	nextContentBlockIndex := 0
	messageStartSent := false
	var cacheUsage *kiroCacheEmulationUsage
	groupID := parsedGroupID(parsed)
	resilienceEnforced := s.kiroResilienceEnforced(groupID)

	for iteration := 0; iteration < kiroMaxWebSearchIterations; iteration++ {
		if prefetchErr := s.prefetchKiroWebSearchDescription(ctx, account, token, groupID); prefetchErr != nil && resilienceEnforced {
			return prefetchErr
		}

		results, nextToken, mcpErr := s.callKiroWebSearchMCP(ctx, account, token, query, groupID)
		if strings.TrimSpace(nextToken) != "" {
			token = nextToken
		}
		if mcpErr != nil {
			if resilienceEnforced {
				return mcpErr
			}
			results = nil
		}

		currentBody, err = kiropkg.InjectToolResultsClaude(currentBody, currentToolUseID, query, results)
		if err != nil {
			return errKiroWebSearchFallback
		}

		resp, requestCtx, err := s.executeKiroUpstreamWithParsed(ctx, account, parsed, currentBody, mappedModel, requestModel, token, headers)
		if err != nil {
			return err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return &kiroWebSearchHTTPError{Response: resp}
		}
		semanticInputTokens := estimateKiroInputTokens(currentBody)
		effectiveInputTokens := kiroInputTokenBudgetForBody(&requestCtx, currentBody, semanticInputTokens)
		if !messageStartSent {
			cacheUsage = s.buildKiroCacheEmulationUsageForRequest(ctx, account, group, anthropicBody, mappedModel, effectiveInputTokens)
			if err := writeAnthropicMessageStart(w, "", mappedModel, effectiveInputTokens, nil); err != nil {
				_ = resp.Body.Close()
				return err
			}
			messageStartSent = true
		}
		if err := writeSSEChunks(w, kiropkg.GenerateSearchIndicatorEvents(query, currentToolUseID, results, nextContentBlockIndex)); err != nil {
			_ = resp.Body.Close()
			return err
		}
		nextContentBlockIndex += 2
		requestCtx.CacheEmulationUsage = cacheUsage.toKiroUsage()
		requestCtx.FinalizeCacheEmulationUsage = s.kiroCacheEmulationTerminalFinalizer(ctx, cacheUsage)
		if resilienceEnforced {
			requestCtx.RequireTerminalEvent = true
		}

		chunks, _, streamErr := func() ([][]byte, *kiropkg.StreamResult, error) {
			defer func() { _ = resp.Body.Close() }()
			return bufferKiroAnthropicStream(ctx, resp.Body, mappedModel, effectiveInputTokens, requestCtx)
		}()
		if streamErr != nil {
			return streamErr
		}

		analysis := kiropkg.AnalyzeBufferedStream(chunks)
		if analysis.HasWebSearchToolUse && strings.TrimSpace(analysis.WebSearchQuery) != "" && iteration+1 < kiroMaxWebSearchIterations {
			filtered := kiropkg.FilterChunksForClient(chunks, analysis.WebSearchToolUseIndex, nextContentBlockIndex)
			if err := writeSSEChunks(w, filtered); err != nil {
				return err
			}
			if maxIndex := kiropkg.MaxContentBlockIndex(filtered); maxIndex >= nextContentBlockIndex {
				nextContentBlockIndex = maxIndex + 1
			}
			query = analysis.WebSearchQuery
			if strings.TrimSpace(analysis.WebSearchToolUseID) == "" {
				currentToolUseID = "srvtoolu_" + kiropkg.GenerateToolUseID()
			} else {
				currentToolUseID = analysis.WebSearchToolUseID
			}
			continue
		}

		for _, chunk := range chunks {
			adjusted, shouldForward := kiropkg.AdjustSSEChunk(chunk, nextContentBlockIndex)
			if !shouldForward {
				continue
			}
			if _, err := w.Write(adjusted); err != nil {
				return err
			}
		}
		if completion != nil {
			completion.markTerminal(cacheUsage)
		} else {
			s.commitKiroCacheEmulationUsage(ctx, cacheUsage)
		}
		return nil
	}

	return fmt.Errorf("kiro web search exceeded max iterations")
}

func (s *GatewayService) openKiroWebSearchStreamResponse(
	requestCtx context.Context,
	streamCtx context.Context,
	releaseStreamCtx context.CancelFunc,
	stopPreSemanticTimer func(),
	accountAttemptStarted time.Time,
	account *Account,
	parsed *ParsedRequest,
	group *Group,
	anthropicBody []byte,
	mappedModel string,
	requestModel string,
	token string,
	headers http.Header,
	inputTokens int,
) (*http.Response, int, error) {
	groupID := parsedGroupID(parsed)
	completion := &kiroStreamCompletion{service: s, account: account}
	rawReader, rawWriter := io.Pipe()
	clientReader, clientWriter := io.Pipe()
	ready := make(chan error, 1)
	gateDone := startKiroFirstSemanticGate(streamCtx, rawReader, clientWriter, s.kiroPreSemanticBufferBytes(groupID), s.kiroSemanticGateMaxLineSize(), ready)

	requestID := kiropkg.NewClaudeRequestID()
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		streamErr := s.streamKiroWebSearchAsAnthropic(streamCtx, account, parsed, group, anthropicBody, mappedModel, requestModel, token, headers, rawWriter, completion)
		if streamErr != nil {
			_ = rawWriter.CloseWithError(streamErr)
			return
		}
		_ = rawWriter.Close()
	}()
	upstreamDone := joinKiroGatedStreamCleanup(producerDone, gateDone, releaseStreamCtx)

	var gateErr error
	select {
	case gateErr = <-ready:
	case <-streamCtx.Done():
		gateErr = context.Cause(streamCtx)
	}
	if gateErr == nil {
		stopPreSemanticTimer()
		accountRound, _ := AccountSwitchCountFromContext(requestCtx)
		responseHeaders := make(http.Header)
		responseHeaders.Set("Content-Type", "text/event-stream")
		responseHeaders.Set("x-request-id", requestID)
		responseHeaders.Set("request-id", requestID)
		logger.L().Info("kiro web search first semantic output ready",
			zap.Int64("account_id", accountID),
			zap.String("request_id", requestID),
			zap.Int64("group_id", derefGroupID(groupID)),
			zap.Int("account_round", accountRound+1),
			zap.Int64("first_semantic_ms", time.Since(accountAttemptStarted).Milliseconds()),
			zap.Int64("remaining_budget_ms", kiroResilienceBudgetRemaining(requestCtx).Milliseconds()),
		)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     responseHeaders,
			Body: &kiroTrackedStreamBody{
				ReadCloser: clientReader,
				cancel:     releaseStreamCtx,
				done:       upstreamDone,
				completion: completion,
			},
		}, inputTokens, nil
	}

	releaseStreamCtx()
	_ = rawReader.CloseWithError(gateErr)
	_ = clientReader.CloseWithError(gateErr)
	cleanupStarted := time.Now()
	cleaned := waitKiroUpstreamCleanup(upstreamDone, s.kiroCleanupGrace(groupID))
	if !cleaned {
		logger.L().Error("kiro web search upstream cleanup exceeded grace",
			zap.String("request_id", requestID),
			zap.Int64("account_id", accountID),
			zap.Int64("cleanup_ms", time.Since(cleanupStarted).Milliseconds()),
		)
	}
	if requestCtx.Err() != nil {
		if !cleaned {
			return nil, inputTokens, &kiroUpstreamCleanupPendingError{cause: context.Cause(requestCtx), done: upstreamDone}
		}
		return nil, inputTokens, context.Cause(requestCtx)
	}

	failoverErr := s.kiroStreamErrorToFailover(requestCtx, account, gateErr)
	if failoverErr == nil {
		body, _ := json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]string{"type": "upstream_error", "message": "Kiro web search stream did not produce output"},
		})
		failoverErr = &UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: body, Cause: gateErr}
	}
	failoverErr.RetryableOnSameAccount = false
	if failoverErr.KiroRateLimited {
		failoverErr.FailureKind = UpstreamFailureRateLimited
		s.ensureKiro429Cooldown(requestCtx, account, groupID, failoverErr)
	} else if errors.Is(gateErr, errKiroFirstSemanticTimeout) || errors.Is(gateErr, errKiroFailoverBudgetExceeded) {
		failoverErr.StatusCode = http.StatusServiceUnavailable
		failoverErr.FailureKind = UpstreamFailureFirstSemanticTimeout
		if errors.Is(gateErr, errKiroFailoverBudgetExceeded) {
			kiroResilienceMetrics.failoverBudgetTimeout.Add(1)
		} else {
			kiroResilienceMetrics.firstSemanticTimeout.Add(1)
		}
		failoverErr.RetryAfter = s.markKiroAccountUnresponsive(requestCtx, account, groupID, failoverErr.FailureKind)
	} else if failoverErr.FailureKind == "" {
		failoverErr.FailureKind = UpstreamFailureIncompleteStream
	}
	if failoverErr.FailureKind == UpstreamFailureIncompleteStream {
		failoverErr.StatusCode = http.StatusServiceUnavailable
		if failoverErr.RetryAfter <= 0 {
			failoverErr.RetryAfter = defaultKiroTransportRetryAfter
		}
	}
	if !cleaned {
		failoverErr.UpstreamDone = upstreamDone
	}
	return nil, inputTokens, failoverErr
}

func (s *GatewayService) executeKiroWebSearch(ctx context.Context, account *Account, group *Group, anthropicBody []byte, mappedModel, requestModel, token string, headers http.Header, onFirstSemantic func()) (*kiroWebSearchExecution, error) {
	query := kiropkg.ExtractSearchQuery(anthropicBody)
	if strings.TrimSpace(query) == "" {
		return nil, errKiroWebSearchFallback
	}

	currentBody, err := kiropkg.ReplaceWebSearchToolDescription(anthropicBody)
	if err != nil {
		currentBody = anthropicBody
	}

	inputTokens := estimateKiroInputTokens(anthropicBody)
	currentToolUseID := "srvtoolu_" + kiropkg.GenerateToolUseID()
	searches := make([]kiropkg.SearchIndicator, 0, 2)
	requestID := ""
	var cacheUsage *kiroCacheEmulationUsage
	cacheUsageResolved := false
	effectiveInputTokens := inputTokens

	for iteration := 0; iteration < kiroMaxWebSearchIterations; iteration++ {
		_ = s.prefetchKiroWebSearchDescription(ctx, account, token, nil)

		results, nextToken, mcpErr := s.callKiroWebSearchMCP(ctx, account, token, query, nil)
		if strings.TrimSpace(nextToken) != "" {
			token = nextToken
		}
		if mcpErr != nil {
			results = nil
		}
		searches = append(searches, kiropkg.SearchIndicator{
			ToolUseID: currentToolUseID,
			Query:     query,
			Results:   results,
		})

		currentBody, err = kiropkg.InjectToolResultsClaude(currentBody, currentToolUseID, query, results)
		if err != nil {
			return nil, errKiroWebSearchFallback
		}

		resp, requestCtx, err := s.executeKiroUpstream(ctx, account, currentBody, mappedModel, requestModel, token, headers)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &kiroWebSearchHTTPError{Response: resp}
		}
		semanticInputTokens := estimateKiroInputTokens(currentBody)
		effectiveInputTokens = kiroInputTokenBudgetForBody(&requestCtx, currentBody, semanticInputTokens)

		parseResult, parseErr := func() (*kiropkg.ParseResult, error) {
			defer func() { _ = resp.Body.Close() }()
			if !cacheUsageResolved {
				cacheUsage = s.buildKiroCacheEmulationUsageForRequest(ctx, account, group, anthropicBody, mappedModel, effectiveInputTokens)
				cacheUsageResolved = true
			}
			requestCtx.CacheEmulationUsage = cacheUsage.toKiroUsage()
			requestCtx.FinalizeCacheEmulationUsage = s.kiroCacheEmulationTerminalFinalizer(ctx, cacheUsage)
			requestCtx.OnFirstSemantic = onFirstSemantic
			return kiropkg.ParseNonStreamingEventStreamWithContext(resp.Body, mappedModel, requestCtx)
		}()
		if parseErr != nil {
			return nil, parseErr
		}
		if requestID == "" {
			requestID = buildKiroRequestID(resp)
		}

		nextToolUseID, nextQuery, hasNext := kiropkg.ExtractWebSearchToolUseFromResponse(parseResult.ResponseBody)
		if !hasNext || strings.TrimSpace(nextQuery) == "" || iteration+1 >= kiroMaxWebSearchIterations {
			finalBody, injectErr := kiropkg.InjectSearchIndicatorsInResponse(parseResult.ResponseBody, searches)
			if injectErr == nil {
				parseResult.ResponseBody = finalBody
			}
			s.commitKiroCacheEmulationUsage(ctx, cacheUsage)
			return &kiroWebSearchExecution{
				ResponseBody: parseResult.ResponseBody,
				Usage:        kiroUsageToClaude(parseResult.Usage, effectiveInputTokens),
				RequestID:    requestID,
			}, nil
		}

		query = nextQuery
		if strings.TrimSpace(nextToolUseID) == "" {
			nextToolUseID = "srvtoolu_" + kiropkg.GenerateToolUseID()
		}
		currentToolUseID = nextToolUseID
	}

	return nil, fmt.Errorf("kiro web search exceeded max iterations")
}

func (s *GatewayService) prefetchKiroWebSearchDescription(ctx context.Context, account *Account, token string, groupID *int64) error {
	endpoint := kiropkg.BuildMcpEndpoint(kiroAPIRegion(account))
	if cached, ok := kiroWebSearchDescCache.Load(endpoint); ok {
		if desc, ok := cached.(string); ok && strings.TrimSpace(desc) != "" {
			kiropkg.SetCachedWebSearchDescription(desc)
		}
		return nil
	}

	reqBody, _ := json.Marshal(kiropkg.MCPRequest{
		ID:      "tools_list",
		JSONRPC: "2.0",
		Method:  "tools/list",
	})
	resp, _, err := s.doKiroMCPJSONRequest(ctx, account, endpoint, reqBody, token, groupID)
	if err != nil || resp == nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var result kiropkg.MCPResponse
	if err := json.Unmarshal(body, &result); err != nil || result.Result == nil {
		return nil
	}
	for _, tool := range result.Result.Tools {
		if strings.EqualFold(tool.Name, "web_search") && strings.TrimSpace(tool.Description) != "" {
			kiroWebSearchDescCache.Store(endpoint, tool.Description)
			kiropkg.SetCachedWebSearchDescription(tool.Description)
			return nil
		}
	}
	return nil
}

func (s *GatewayService) callKiroWebSearchMCP(ctx context.Context, account *Account, token, query string, groupID *int64) (*kiropkg.WebSearchResults, string, error) {
	reqBody, err := json.Marshal(buildKiroWebSearchMCPRequest(query))
	if err != nil {
		return nil, token, err
	}

	endpoint := kiropkg.BuildMcpEndpoint(kiroAPIRegion(account))
	resp, nextToken, err := s.doKiroMCPJSONRequest(ctx, account, endpoint, reqBody, token, groupID)
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
		return nil, nextToken, fmt.Errorf("kiro mcp status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed kiropkg.MCPResponse
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
		return nil, nextToken, fmt.Errorf("kiro mcp error %d: %s", code, msg)
	}

	return kiropkg.ParseSearchResults(&parsed), nextToken, nil
}

func buildKiroWebSearchMCPRequest(query string) kiropkg.MCPRequest {
	return kiropkg.MCPRequest{
		ID:      fmt.Sprintf("web_search_%s", kiropkg.GenerateToolUseID()),
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

func (s *GatewayService) doKiroMCPJSONRequest(ctx context.Context, account *Account, endpoint string, payload []byte, token string, groupID *int64) (*http.Response, string, error) {
	currentToken := token
	accountKey := buildKiroAccountKey(account)
	cooldownKey := buildKiroCooldownKey(account)
	proxyURL := kiroProxyURL(account)
	tlsProfile := s.tlsFPProfileService.ResolveTLSProfile(account)
	resilienceEnforced := s.kiroResilienceEnforced(groupID)
	maxAttempts := 3
	if resilienceEnforced {
		maxAttempts = 2
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := s.checkAndWaitKiroCooldownWithMode(ctx, cooldownKey, resilienceEnforced); err != nil {
			if failoverErr := asKiroCooldownFailoverError(err); failoverErr != nil {
				return nil, currentToken, failoverErr
			}
			return nil, currentToken, err
		}

		req, err := newKiroJSONRequest(ctx, endpoint, payload, currentToken, accountKey, buildKiroMachineID(account), "", account)
		if err != nil {
			return nil, currentToken, err
		}

		var wroteRequest atomic.Bool
		if resilienceEnforced {
			trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { wroteRequest.Store(true) }}
			req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
		}
		accountRound, _ := AccountSwitchCountFromContext(ctx)
		responseHeaderTimeout := s.kiroResponseHeaderTimeout(groupID)
		stopHeaderObservation := s.startKiroResponseHeaderObservation(ctx, groupID, account, endpoint, accountRound+1, attempt+1, responseHeaderTimeout)
		resp, responseHeaderElapsed, physicalDone, err := doKiroWithResponseHeaderTimeout(req, responseHeaderTimeout, func(timedReq *http.Request) (*http.Response, error) {
			return s.httpUpstream.DoWithTLS(timedReq, proxyURL, account.ID, account.Concurrency, tlsProfile)
		})
		stopHeaderObservation()
		logger.L().Debug("kiro mcp response header completed",
			zap.String("request_id", resolveUsageBillingRequestID(ctx, "")),
			zap.Int64("group_id", derefGroupID(groupID)),
			zap.Int64("account_id", account.ID),
			zap.String("endpoint", endpoint),
			zap.Int("account_round", accountRound+1),
			zap.Int("endpoint_attempt", attempt+1),
			zap.Int64("response_header_ms", responseHeaderElapsed.Milliseconds()),
			zap.Int64("remaining_budget_ms", kiroResilienceBudgetRemaining(ctx).Milliseconds()),
			zap.Error(err),
		)
		if err != nil {
			if failoverErr := s.kiroContextCauseFailover(ctx, account, groupID); failoverErr != nil {
				if physicalDone != nil && !waitKiroUpstreamCleanup(physicalDone, s.kiroCleanupGrace(groupID)) {
					failoverErr.UpstreamDone = physicalDone
				}
				return nil, currentToken, failoverErr
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if physicalDone != nil && !waitKiroUpstreamCleanup(physicalDone, s.kiroCleanupGrace(groupID)) {
					return nil, currentToken, &kiroUpstreamCleanupPendingError{cause: err, done: physicalDone}
				}
				return nil, currentToken, err
			}
			if !resilienceEnforced {
				return nil, currentToken, err
			}
			physicalCleaned := waitKiroUpstreamCleanup(physicalDone, s.kiroCleanupGrace(groupID))
			if attempt == 0 && !wroteRequest.Load() && physicalCleaned {
				continue
			}
			transportErr := &kiroEndpointTransportError{
				EndpointName: "KiroMCP",
				EndpointURL:  endpoint,
				NetworkType:  classifyKiroEndpointNetworkError(err, proxyURL != ""),
				Cause:        err,
				UpstreamDone: physicalDone,
			}
			failoverErr := newKiroEndpointTransportFailover(transportErr)
			failoverErr.StatusCode = http.StatusServiceUnavailable
			failoverErr.FailoverProhibited = wroteRequest.Load() || !physicalCleaned
			failoverErr.RetryAfter = defaultKiroTransportRetryAfter
			var headerTimeout *kiroResponseHeaderTimeoutError
			if errors.As(err, &headerTimeout) {
				kiroResilienceMetrics.responseHeaderTimeout.Add(1)
				failoverErr.RetryAfter = s.markKiroAccountUnresponsive(ctx, account, groupID, UpstreamFailureResponseHeaderTimeout)
			}
			if physicalCleaned {
				failoverErr.UpstreamDone = nil
			}
			return nil, currentToken, failoverErr
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			respBody, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr != nil {
				return nil, currentToken, readErr
			}
			if resp.StatusCode == http.StatusForbidden && isKiroSuspendedBody(respBody) {
				if _, err := s.markKiroSuspended(ctx, cooldownKey); err != nil {
					return nil, currentToken, err
				}
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			if resp.StatusCode == http.StatusForbidden && !isKiroTokenErrorBody(respBody) {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			if resilienceEnforced {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			if s.kiroTokenProvider == nil {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			refreshedToken, refreshErr := s.kiroTokenProvider.ForceRefreshAccessToken(ctx, account)
			if refreshErr != nil {
				resp.Body = io.NopCloser(strings.NewReader(string(respBody)))
				return resp, currentToken, nil
			}
			currentToken = refreshedToken
			accountKey = buildKiroAccountKey(account)
			cooldownKey = buildKiroCooldownKey(account)
			if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
				return nil, currentToken, sleepErr
			}
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			if resilienceEnforced {
				respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, currentToken, readErr
				}
				failoverErr := &UpstreamFailoverError{
					StatusCode:      http.StatusTooManyRequests,
					ResponseBody:    respBody,
					ResponseHeaders: resp.Header.Clone(),
					KiroRateLimited: true,
					FailureKind:     UpstreamFailureRateLimited,
				}
				failoverErr.RetryAfter = s.markKiroAccount429(ctx, account, groupID, resp.Header)
				failoverErr.KiroCooldownCommitted = true
				return nil, currentToken, failoverErr
			}
			return resp, currentToken, nil
		}
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= 500 {
			if resilienceEnforced {
				respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, currentToken, readErr
				}
				retryAfter := kiroRetryAfterDuration(resp.Header, time.Now())
				if retryAfter <= 0 {
					retryAfter = defaultKiroTransportRetryAfter
				}
				return nil, currentToken, &UpstreamFailoverError{
					StatusCode:      http.StatusServiceUnavailable,
					ResponseBody:    respBody,
					ResponseHeaders: resp.Header.Clone(),
					FailureKind:     UpstreamFailureTransportError,
					RetryAfter:      retryAfter,
				}
			}
			if attempt < maxAttempts-1 {
				_ = resp.Body.Close()
				if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
					return nil, currentToken, sleepErr
				}
				continue
			}
		}
		if !resilienceEnforced && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := s.markKiroSuccessPreservingCooldown(ctx, cooldownKey); err != nil {
				_ = resp.Body.Close()
				return nil, currentToken, err
			}
		}

		return resp, currentToken, nil
	}

	return nil, currentToken, fmt.Errorf("kiro mcp request retries exhausted")
}
