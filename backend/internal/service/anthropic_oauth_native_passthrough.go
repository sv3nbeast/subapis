package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	claudeAPINativeMessagesURL     = "https://api.anthropic.com/v1/messages"
	claudeAPINativeCountTokensURL  = "https://api.anthropic.com/v1/messages/count_tokens"
	anthropicOAuthPassthroughExtra = "anthropic_oauth_passthrough"
	anthropicOAuthIngressHeaderKey = "anthropic_oauth_native_ingress_headers"
)

// CaptureAnthropicOAuthNativeIngressHeaders snapshots the client headers before
// any account-attempt compatibility layer can mutate the shared Gin request.
// A later failover from mimic/Bedrock compatibility into native mode must still
// use the original Claude client identity.
func CaptureAnthropicOAuthNativeIngressHeaders(c *gin.Context) {
	if c == nil || c.Request == nil {
		return
	}
	snapshot := make(http.Header)
	for key, values := range c.Request.Header {
		if !isAnthropicNativeRequestHeaderAllowed(key) {
			continue
		}
		snapshot[key] = append([]string(nil), values...)
	}
	c.Set(anthropicOAuthIngressHeaderKey, snapshot)
}

func isAnthropicNativeRequestHeaderAllowed(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	return allowedHeaders[lower] ||
		lower == "x-request-id" || lower == "request-id" ||
		lower == "x-session-id" || lower == "session-id"
}

// shouldUseAnthropicOAuthNativePassthrough is intentionally narrow: only a
// configured Anthropic OAuth/SetupToken account and an identified Claude Code
// request may enter the byte-preserving path. Other clients retain the
// existing OAuth compatibility/mimicry behavior.
func (s *GatewayService) shouldUseAnthropicOAuthNativePassthrough(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest) bool {
	if account == nil || !account.IsAnthropicOAuthPassthroughEnabled() {
		return false
	}
	if IsClaudeCodeClient(ctx) {
		return true
	}
	if c == nil || parsed == nil {
		return false
	}
	return isClaudeCodeClient(c.GetHeader("User-Agent"), parsed.MetadataUserID)
}

// ShouldUseAnthropicOAuthNativePassthrough exposes the same narrow routing
// predicate to the handler.  The handler must skip compatibility mutations
// (for example Bedrock CC beta/body cleanup) before Forward gets a chance to
// select the native path; keeping this predicate shared prevents the two
// decisions from drifting.
func (s *GatewayService) ShouldUseAnthropicOAuthNativePassthrough(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest) bool {
	return s.shouldUseAnthropicOAuthNativePassthrough(ctx, c, account, parsed)
}

func markAnthropicOAuthNativePassthroughFallback(c *gin.Context, account *Account) {
	if c == nil {
		return
	}
	if account == nil || !account.IsAnthropicOAuthOrSetupToken() {
		// A failover can reuse the same Gin context for an API-key, Bedrock, or
		// another platform account. Clear the prior OAuth marker so audit data
		// cannot incorrectly attribute that attempt to native/mimic mode.
		c.Set("anthropic_passthrough_mode", "")
		c.Set("anthropic_passthrough_fallback", "")
		return
	}
	fallback := ""
	if account.IsAnthropicOAuthPassthroughEnabled() {
		fallback = "non_claude_code"
	}
	c.Set("anthropic_passthrough_mode", "mimic")
	c.Set("anthropic_passthrough_fallback", fallback)
	if fallback != "" {
		logger.LegacyPrintf("service.gateway", "[Anthropic Native Passthrough] account=%d name=%s anthropic_passthrough_mode=mimic anthropic_passthrough_fallback=%s",
			account.ID, account.Name, fallback)
		return
	}
	logger.LegacyPrintf("service.gateway", "[Anthropic Native Passthrough] account=%d name=%s anthropic_passthrough_mode=mimic",
		account.ID, account.Name)
}

func anthropicOAuthNativeBody(parsed *ParsedRequest) []byte {
	if parsed == nil {
		return nil
	}
	if len(parsed.OriginalBody) > 0 {
		return parsed.OriginalBody
	}
	if parsed.Body == nil {
		return nil
	}
	return parsed.Body.Bytes()
}

func (s *GatewayService) forwardAnthropicOAuthNativePassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	parsed *ParsedRequest,
	startTime time.Time,
) (*ForwardResult, error) {
	if account == nil || parsed == nil {
		return nil, errors.New("anthropic native passthrough: missing account or request")
	}

	body := anthropicOAuthNativeBody(parsed)
	requestModel := gjson.GetBytes(body, "model").String()
	if requestModel == "" {
		requestModel = parsed.Model
	}
	requestStream := parsed.Stream
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	if tokenType != "oauth" {
		return nil, fmt.Errorf("anthropic native passthrough requires oauth token, got: %s", tokenType)
	}

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	logger.LegacyPrintf("service.gateway", "[Anthropic Native Passthrough] account=%d name=%s model=%s stream=%v anthropic_passthrough_mode=native",
		account.ID, account.Name, requestModel, requestStream)
	if c != nil {
		c.Set("anthropic_passthrough_mode", "native")
		c.Set("anthropic_passthrough_fallback", "")
	}
	setOpsUpstreamRequestBody(c, body)

	var resp *http.Response
	retryStart := time.Now()
	for attempt := 1; attempt <= maxRetryAttempts; attempt++ {
		upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, requestStream)
		upstreamReq, err := s.buildAnthropicOAuthNativeRequest(upstreamCtx, c, account, body, token)
		releaseUpstreamCtx()
		if err != nil {
			return nil, err
		}

		// A native passthrough request never uses the account's synthetic TLS
		// profile. This keeps transport identity consistent with the original CLI.
		resp, err = s.httpUpstream.DoWithTLS(upstreamReq, proxyURL, account.ID, account.Concurrency, nil)
		if err != nil {
			if resp != nil && resp.Body != nil {
				_ = resp.Body.Close()
			}
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			if isRetryablePreResponseNetworkError(err) {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
					UpstreamStatusCode: http.StatusBadGateway, UpstreamURL: safeUpstreamURL(upstreamReq.URL.String()),
					Passthrough: true, Kind: "native_request_network_failover", Message: safeErr,
				})
				errorBody, _ := json.Marshal(map[string]any{
					"type": "error",
					"error": map[string]string{
						"type":    "upstream_disconnected",
						"message": "upstream request disconnected before response: " + sanitizeStreamError(err),
					},
				})
				return nil, &UpstreamFailoverError{
					StatusCode: http.StatusBadGateway, ResponseBody: errorBody,
					// Do not replay an OAuth request on the same account after a
					// transport error: net/http may have written the request upstream
					// before the connection failed, so a same-account retry can create a
					// duplicate generation/charge.  The handler may still apply its
					// normal cross-account failover policy.
					RetryableOnSameAccount: false, RequestedModel: requestModel, Cause: err,
				}
			}
			setOpsUpstreamError(c, 0, safeErr, "")
			if c != nil {
				c.JSON(http.StatusBadGateway, gin.H{
					"type":  "error",
					"error": gin.H{"type": "upstream_error", "message": "Upstream request failed"},
				})
			}
			return nil, fmt.Errorf("upstream request failed: %s", safeErr)
		}

		if resp.StatusCode >= 400 && resp.StatusCode != http.StatusBadRequest && s.shouldRetryUpstreamError(account, resp.StatusCode) && attempt < maxRetryAttempts {
			elapsed := time.Since(retryStart)
			if elapsed >= maxRetryElapsed {
				break
			}
			bodyBytes, _ := s.readUpstreamErrorBody(resp)
			_ = resp.Body.Close()
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform: account.Platform, AccountID: account.ID, AccountName: account.Name,
				UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"),
				Passthrough: true, Kind: "native_retry", Message: extractUpstreamErrorMessage(bodyBytes),
			})
			delay := retryBackoffDelay(attempt)
			if remaining := maxRetryElapsed - elapsed; delay > remaining {
				delay = remaining
			}
			if delay <= 0 {
				break
			}
			if err := sleepWithContext(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		break
	}

	if resp == nil || resp.Body == nil {
		return nil, errors.New("upstream request failed: empty response")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			respBody, _ := s.readUpstreamErrorBody(resp)
			softRateLimit := IsAnthropicSoftRateLimitResponse(account, resp.StatusCode, resp.Header, respBody)
			if !softRateLimit {
				s.handleFailoverSideEffects(ctx, resp, account, requestModel)
			}
			return nil, &UpstreamFailoverError{
				StatusCode: resp.StatusCode, ResponseBody: respBody, ResponseHeaders: resp.Header.Clone(),
				RetryableOnSameAccount: softRateLimit || (account.IsPoolMode() && isPoolModeRetryableStatus(resp.StatusCode)),
				AnthropicSoftRateLimit: softRateLimit,
				RequestedModel:         requestModel,
			}
		}
		return s.handleAnthropicOAuthNativeErrorResponse(ctx, resp, c, account, requestModel)
	}

	if parsed.OnUpstreamAccepted != nil {
		parsed.OnUpstreamAccepted()
	}

	var usage *ClaudeUsage
	var firstTokenMs *int
	clientDisconnect := false
	if requestStream {
		var streamErr error
		usage, firstTokenMs, clientDisconnect, streamErr = s.handleAnthropicOAuthNativeStreamingResponse(ctx, resp, c, account, startTime, requestModel)
		if streamErr != nil {
			streamResult := &streamingResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: clientDisconnect}
			if partial := partialStreamUsageResult(resp, streamResult, requestModel, requestModel, startTime, streamErr); partial != nil {
				return partial, streamErr
			}
			return nil, streamErr
		}
	} else {
		usage, err = s.handleAnthropicOAuthNativeNonStreamingResponse(ctx, resp, c, account)
	}
	if err != nil {
		return nil, err
	}
	if usage == nil {
		usage = &ClaudeUsage{}
	}

	return &ForwardResult{
		RequestID: resp.Header.Get("x-request-id"), Usage: *usage,
		Model: requestModel, UpstreamModel: requestModel, Stream: requestStream,
		Duration: time.Since(startTime), FirstTokenMs: firstTokenMs, ClientDisconnect: clientDisconnect,
	}, nil
}

// handleAnthropicOAuthNativeErrorResponse keeps non-failover upstream errors
// byte-for-byte visible to the native Claude client. Account state, ops
// telemetry, and response-commit bookkeeping still follow the gateway's
// existing lifecycle; only the compatibility error rewrite is skipped.
func (s *GatewayService) handleAnthropicOAuthNativeErrorResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestedModel string,
) (*ForwardResult, error) {
	scheduleOllamaCloudUsageActivity(s.deferredService, account)
	body, err := s.readUpstreamErrorBody(resp)
	if err != nil {
		return nil, err
	}
	upstreamMsg := strings.TrimSpace(sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(body)))
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Passthrough:        true,
		Kind:               "native_http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})
	if s.rateLimitService != nil {
		if shouldDisable := s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel); shouldDisable {
			return nil, &UpstreamFailoverError{
				StatusCode: resp.StatusCode, ResponseBody: body, ResponseHeaders: resp.Header.Clone(), RequestedModel: requestedModel,
			}
		}
	}
	MarkResponseCommitted(c)
	writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, body)
	if upstreamMsg == "" {
		return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}
	return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
}

func (s *GatewayService) buildAnthropicOAuthNativeRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, error) {
	targetURL := claudeAPINativeMessagesURL
	if account.IsCustomBaseURLEnabled() {
		baseURL := account.GetCustomBaseURL()
		if baseURL == "" {
			return nil, fmt.Errorf("custom_base_url is enabled but not configured for account %d", account.ID)
		}
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		targetURL = strings.TrimRight(validatedURL, "/") + "/v1/messages"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	copyAnthropicNativeRequestHeaders(c, req)

	for _, key := range []string{
		"authorization", "x-api-key", "x-goog-api-key", "cookie", "proxy-authorization",
		"forwarded", "via", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto",
	} {
		deleteHeaderAllForms(req.Header, key)
	}
	setHeaderRaw(req.Header, "authorization", "Bearer "+token)
	return req, nil
}

func (s *GatewayService) handleAnthropicOAuthNativeNonStreamingResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
) (*ClaudeUsage, error) {
	if s.rateLimitService != nil {
		s.rateLimitService.UpdateSessionWindow(ctx, account, resp.Header)
	}
	body, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, anthropicTooLargeError)
	if err != nil {
		return nil, err
	}
	usage := parseClaudeUsageFromResponseBody(body)
	writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, body)
	return usage, nil
}

func (s *GatewayService) handleAnthropicOAuthNativeStreamingResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	startTime time.Time,
	model string,
) (*ClaudeUsage, *int, bool, error) {
	if s.rateLimitService != nil {
		s.rateLimitService.UpdateSessionWindow(ctx, account, resp.Header)
	}
	writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "text/event-stream"
	}
	c.Header("Content-Type", contentType)
	if c.Writer.Header().Get("Cache-Control") == "" {
		c.Header("Cache-Control", "no-cache")
	}
	if c.Writer.Header().Get("Connection") == "" {
		c.Header("Connection", "keep-alive")
	}
	c.Header("X-Accel-Buffering", "no")
	if requestID := resp.Header.Get("x-request-id"); requestID != "" {
		c.Header("x-request-id", requestID)
	}
	flusher, _ := c.Writer.(http.Flusher)

	usage := &ClaudeUsage{}
	reader := bufio.NewReader(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	firstTokenMs := (*int)(nil)
	clientDisconnected := false
	sawTerminalEvent := false
	lastEventName := ""
	type nativeReadEvent struct {
		chunk []byte
		err   error
	}
	events := make(chan nativeReadEvent)
	done := make(chan struct{})
	go func() {
		defer close(events)
		line := make([]byte, 0, min(maxLineSize, 64*1024))
		send := func(event nativeReadEvent) bool {
			select {
			case events <- event:
				return true
			case <-done:
				return false
			}
		}
		for {
			fragment, readErr := reader.ReadSlice('\n')
			if len(fragment) > 0 {
				if len(line)+len(fragment) > maxLineSize {
					_ = send(nativeReadEvent{err: bufio.ErrTooLong})
					return
				}
				line = append(line, fragment...)
			}
			if errors.Is(readErr, bufio.ErrBufferFull) {
				continue
			}
			if len(line) > 0 {
				chunk := append([]byte(nil), line...)
				line = line[:0]
				if !send(nativeReadEvent{chunk: chunk}) {
					return
				}
			}
			if readErr != nil {
				_ = send(nativeReadEvent{err: readErr})
				return
			}
		}
	}()
	defer close(done)

	streamInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}
	var idleTimer *time.Timer
	var idleCh <-chan time.Time
	if streamInterval > 0 {
		idleTimer = time.NewTimer(streamInterval)
		idleCh = idleTimer.C
		defer idleTimer.Stop()
	}
	resetIdleTimer := func() {
		if idleTimer == nil {
			return
		}
		if !idleTimer.Stop() {
			select {
			case <-idleTimer.C:
			default:
			}
		}
		idleTimer.Reset(streamInterval)
	}

	firstSemanticInterval := s.firstSemanticTimeout()
	var firstSemanticTimer *time.Timer
	var firstSemanticCh <-chan time.Time
	if firstSemanticInterval > 0 {
		firstSemanticTimer = time.NewTimer(firstSemanticInterval)
		firstSemanticCh = firstSemanticTimer.C
		defer firstSemanticTimer.Stop()
	}
	var pendingUpstreamStreamError error
	pendingUpstreamStreamErrorType := ""
	pendingUpstreamStreamErrorMessage := ""
	markNativeStreamError := func(errorType, errorMessage string) {
		if errorType == "" {
			errorType = "upstream_error"
		}
		if errorMessage == "" {
			errorMessage = "Upstream stream error"
		}
		MarkGatewaySSEErrorWritten(c)
		MarkOpsStreamError(c, errorType, errorMessage, http.StatusBadGateway)
	}

	for {
		select {
		case event, ok := <-events:
			if !ok {
				if pendingUpstreamStreamError != nil {
					markNativeStreamError(pendingUpstreamStreamErrorType, pendingUpstreamStreamErrorMessage)
					return usage, firstTokenMs, clientDisconnected, pendingUpstreamStreamError
				}
				if sawTerminalEvent {
					return usage, firstTokenMs, clientDisconnected, nil
				}
				return usage, firstTokenMs, clientDisconnected, fmt.Errorf("native stream usage incomplete: missing terminal event")
			}
			if event.err != nil {
				if pendingUpstreamStreamError != nil {
					markNativeStreamError(pendingUpstreamStreamErrorType, pendingUpstreamStreamErrorMessage)
					return usage, firstTokenMs, clientDisconnected, pendingUpstreamStreamError
				}
				if errors.Is(event.err, io.EOF) && sawTerminalEvent {
					return usage, firstTokenMs, clientDisconnected, nil
				}
				if errors.Is(event.err, io.EOF) {
					return usage, firstTokenMs, clientDisconnected, fmt.Errorf("native stream usage incomplete: missing terminal event")
				}
				return usage, firstTokenMs, clientDisconnected, fmt.Errorf("native stream read error: %w", event.err)
			}
			chunk := event.chunk
			resetIdleTimer()
			wroteToClient := false
			if !clientDisconnected {
				if _, writeErr := c.Writer.Write(chunk); writeErr != nil {
					clientDisconnected = true
					logger.LegacyPrintf("service.gateway", "[Anthropic Native Passthrough] client disconnected: account=%d model=%s", account.ID, model)
				} else {
					wroteToClient = true
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
			line := strings.TrimRight(string(chunk), "\r\n")
			trimmed := strings.TrimSpace(line)
			if trimmed == "" && pendingUpstreamStreamError != nil {
				markNativeStreamError(pendingUpstreamStreamErrorType, pendingUpstreamStreamErrorMessage)
				return usage, firstTokenMs, clientDisconnected, pendingUpstreamStreamError
			}
			if strings.HasPrefix(trimmed, "event:") {
				lastEventName = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
				if anthropicStreamEventIsTerminal(lastEventName, "") {
					sawTerminalEvent = true
				}
			}
			if data, ok := extractAnthropicSSEDataLine(trimmed); ok {
				data = strings.TrimSpace(data)
				if firstTokenMs == nil && wroteToClient && data != "" && data != "[DONE]" && !strings.EqualFold(lastEventName, "error") {
					ms := int(time.Since(startTime).Milliseconds())
					firstTokenMs = &ms
					if firstSemanticTimer != nil {
						if !firstSemanticTimer.Stop() {
							select {
							case <-firstSemanticTimer.C:
							default:
							}
						}
						firstSemanticCh = nil
					}
				}
				s.parseSSEUsagePassthrough(data, usage)
				if strings.EqualFold(lastEventName, "error") {
					errorType := gjson.Get(data, "error.type").String()
					if errorType == "" {
						errorType = "upstream_error"
					}
					errorMessage := gjson.Get(data, "error.message").String()
					if errorMessage == "" {
						errorMessage = "Upstream stream error"
					}
					// Wait for the SSE record's terminating blank line so that the
					// copied response remains byte-for-byte identical.
					pendingUpstreamStreamError = fmt.Errorf("native upstream stream error: %s", sanitizeUpstreamErrorMessage(errorMessage))
					pendingUpstreamStreamErrorType = errorType
					pendingUpstreamStreamErrorMessage = errorMessage
				}
				if data == "[DONE]" || anthropicStreamEventIsTerminal(lastEventName, data) {
					sawTerminalEvent = true
				}
			}

		case <-idleCh:
			if clientDisconnected {
				return usage, firstTokenMs, true, fmt.Errorf("native stream usage incomplete after client disconnect timeout")
			}
			if firstTokenMs == nil {
				return usage, nil, false, newAnthropicFirstSemanticTimeoutFailover(account, model)
			}
			logger.LegacyPrintf("service.gateway", "[Anthropic Native Passthrough] stream data interval timeout: account=%d model=%s interval=%s", account.ID, model, streamInterval)
			if s.rateLimitService != nil {
				s.rateLimitService.HandleStreamTimeout(ctx, account, model)
			}
			return usage, firstTokenMs, false, fmt.Errorf("native stream data interval timeout")

		case <-firstSemanticCh:
			if firstTokenMs != nil {
				continue
			}
			return usage, nil, clientDisconnected, newAnthropicFirstSemanticTimeoutFailover(account, model)
		}
	}
}

func (s *GatewayService) forwardCountTokensAnthropicOAuthNativePassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
) error {
	logger.LegacyPrintf("service.gateway", "[Anthropic Native Passthrough] account=%d name=%s endpoint=count_tokens anthropic_passthrough_mode=native",
		account.ID, account.Name)
	setOpsUpstreamRequestBody(c, body)
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		s.countTokensError(c, http.StatusBadGateway, "upstream_error", "Failed to get access token")
		return err
	}
	if tokenType != "oauth" {
		s.countTokensError(c, http.StatusBadGateway, "upstream_error", "Invalid account token type")
		return fmt.Errorf("anthropic native passthrough requires oauth token, got: %s", tokenType)
	}
	req, err := s.buildAnthropicOAuthNativeCountTokensRequest(ctx, c, account, body, token)
	if err != nil {
		s.countTokensError(c, http.StatusInternalServerError, "api_error", "Failed to build request")
		return err
	}
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}
	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, nil)
	if err != nil {
		setOpsUpstreamError(c, 0, sanitizeUpstreamErrorMessage(err.Error()), "")
		s.countTokensError(c, http.StatusBadGateway, "upstream_error", "Request failed")
		return fmt.Errorf("upstream request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := ReadUpstreamResponseBody(resp.Body, s.cfg, c, func(c *gin.Context) {
		s.countTokensError(c, http.StatusBadGateway, "upstream_error", "Upstream response too large")
	})
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		if isCountTokensUnsupported404(resp.StatusCode, respBody) {
			s.countTokensError(c, http.StatusNotFound, "not_found_error", "count_tokens endpoint is not supported by upstream")
			return nil
		}
		s.handleCountTokensUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		upstreamMsg := sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
		setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, "")
		s.countTokensError(c, resp.StatusCode, "upstream_error", "Upstream request failed")
		if upstreamMsg == "" {
			return fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
	}
	writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, respBody)
	return nil
}

func (s *GatewayService) buildAnthropicOAuthNativeCountTokensRequest(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, error) {
	targetURL := claudeAPINativeCountTokensURL
	if account.IsCustomBaseURLEnabled() {
		baseURL := account.GetCustomBaseURL()
		if baseURL == "" {
			return nil, fmt.Errorf("custom_base_url is enabled but not configured for account %d", account.ID)
		}
		validatedURL, err := s.validateUpstreamBaseURL(baseURL)
		if err != nil {
			return nil, err
		}
		targetURL = strings.TrimRight(validatedURL, "/") + "/v1/messages/count_tokens"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	copyAnthropicNativeRequestHeaders(c, req)
	for _, key := range []string{
		"authorization", "x-api-key", "x-goog-api-key", "cookie", "proxy-authorization",
		"forwarded", "via", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto",
	} {
		deleteHeaderAllForms(req.Header, key)
	}
	setHeaderRaw(req.Header, "authorization", "Bearer "+token)
	return req, nil
}

// copyAnthropicNativeRequestHeaders copies the existing Claude client header
// allowlist without applying the mimicry UA, companion headers, or account
// overrides. Request IDs are included explicitly because they are useful for
// correlating a native CLI request even though they are not needed by the
// compatibility paths.
func copyAnthropicNativeRequestHeaders(c *gin.Context, req *http.Request) {
	if c == nil || c.Request == nil || req == nil {
		return
	}
	sourceHeaders := c.Request.Header
	if value, ok := c.Get(anthropicOAuthIngressHeaderKey); ok {
		if ingressHeaders, ok := value.(http.Header); ok && ingressHeaders != nil {
			sourceHeaders = ingressHeaders
		}
	}
	for key, values := range sourceHeaders {
		if !isAnthropicNativeRequestHeaderAllowed(key) {
			continue
		}
		wireKey := resolveWireCasing(key)
		for _, value := range values {
			addHeaderRaw(req.Header, wireKey, value)
		}
	}
}
