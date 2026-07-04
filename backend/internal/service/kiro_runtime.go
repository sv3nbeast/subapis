package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

type kiroEndpointConfig struct {
	URL       string
	Origin    string
	AmzTarget string
	Name      string
}

const kiroInvalidModelTempUnschedDuration = time.Minute

const (
	kiroRetryBaseDelay = 200 * time.Millisecond
	kiroRetryMaxDelay  = 2 * time.Second
)

var kiroRetrySleep = sleepWithContext

func kiroRetryBackoffDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := kiroRetryBaseDelay * time.Duration(1<<attempt)
	if delay > kiroRetryMaxDelay {
		delay = kiroRetryMaxDelay
	}
	jitterMax := delay / 4
	if jitterMax <= 0 {
		return delay
	}
	return delay + time.Duration(mathrand.Int63n(int64(jitterMax)+1))
}

func sleepKiroRetry(ctx context.Context, attempt int) error {
	return kiroRetrySleep(ctx, kiroRetryBackoffDelay(attempt))
}

func (s *GatewayService) forwardKiroMessages(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, startTime time.Time) (*ForwardResult, error) {
	if account == nil || parsed == nil {
		return nil, fmt.Errorf("kiro forward: missing account or request")
	}
	if msg := validateKiroRequestShape(parsed); msg != "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":  "error",
			"error": gin.H{"type": "invalid_request_error", "message": msg},
		})
		return nil, fmt.Errorf("%s", msg)
	}

	originalModel := parsed.Model
	mappedModel := originalModel
	if next := account.GetMappedModel(originalModel); next != "" {
		mappedModel = next
	}
	body := parsed.Body.Bytes()
	body = s.prepareKiroBridgeCacheEmulationBody(ctx, account, body)
	if mappedModel != originalModel {
		body = s.replaceModelInBody(body, mappedModel)
	}
	logger.L().Debug("gateway forward_kiro_messages: request prepared",
		zap.Int64("account_id", account.ID),
		zap.String("auth_method", strings.TrimSpace(account.GetCredential("auth_method"))),
		zap.String("requested_model", originalModel),
		zap.String("mapped_model", mappedModel),
		zap.Bool("has_profile_arn", strings.TrimSpace(account.GetCredential("profile_arn")) != ""),
	)

	if s.shouldEmulateWebSearch(ctx, account, parsed.GroupID, body) {
		parsedForEmulation, err := parsed.CloneForBody(body)
		if err != nil {
			return nil, err
		}
		parsedForEmulation.Model = mappedModel
		return s.handleWebSearchEmulation(ctx, c, account, parsedForEmulation)
	}

	if parsed.Stream {
		resp, _, err := s.openKiroAnthropicStreamResponse(ctx, account, parsed, body, mappedModel, originalModel, c.Request.Header, parsed.Group)
		if err != nil {
			var failoverErr *UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: failoverErr.StatusCode,
					Kind:               "failover",
					Message:            sanitizeUpstreamErrorMessage(err.Error()),
				})
				return nil, failoverErr
			}
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			if isRetryablePreResponseNetworkError(err) {
				body, _ := json.Marshal(map[string]any{
					"type": "error",
					"error": map[string]string{
						"type":    "upstream_disconnected",
						"message": "upstream request disconnected before response: " + sanitizeStreamError(err),
					},
				})
				return nil, &UpstreamFailoverError{
					StatusCode:             http.StatusBadGateway,
					ResponseBody:           body,
					RetryableOnSameAccount: true,
					Cause:                  err,
				}
			}
			return nil, fmt.Errorf("kiro upstream request failed: %s", safeErr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 400 {
			return nil, s.handleKiroHTTPError(ctx, resp, c, account, mappedModel, body)
		}
		upstreamModel := resolveKiroUpstreamModel(mappedModel)
		streamResult, err := s.handleStreamingResponse(ctx, resp, c, account, startTime, originalModel, mappedModel, false)
		if err != nil {
			if failoverErr := s.kiroStreamErrorToFailover(ctx, account, err); failoverErr != nil {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: failoverErr.StatusCode,
					Kind:               "stream_failover",
					Message:            sanitizeUpstreamErrorMessage(err.Error()),
				})
				return nil, failoverErr
			}
			return nil, err
		}
		if streamResult.usage == nil {
			streamResult.usage = &ClaudeUsage{}
		}
		requestID := buildKiroClientRequestID(resp)
		if requestID != "" {
			c.Header("x-request-id", requestID)
			c.Header("request-id", requestID)
		}
		return &ForwardResult{
			RequestID:        requestID,
			Usage:            *streamResult.usage,
			Model:            originalModel,
			UpstreamModel:    upstreamModel,
			Stream:           true,
			Duration:         time.Since(startTime),
			FirstTokenMs:     streamResult.firstTokenMs,
			ClientDisconnect: streamResult.clientDisconnect,
		}, nil
	}

	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	if !isKiroDirectTokenType(tokenType) {
		return nil, fmt.Errorf("kiro requires oauth or apikey token, got %s", tokenType)
	}
	if isOnlyWebSearchToolInBody(body) {
		webSearchResult, webSearchErr := s.executeKiroWebSearch(ctx, account, parsed.Group, body, mappedModel, originalModel, token, c.Request.Header)
		switch {
		case errors.Is(webSearchErr, errKiroWebSearchFallback):
		case webSearchErr == nil:
			upstreamModel := resolveKiroUpstreamModel(mappedModel)
			requestID := webSearchResult.RequestID
			if requestID == "" {
				requestID = kiropkg.NewClaudeRequestID()
			}
			c.Header("Content-Type", "application/json")
			c.Header("x-request-id", requestID)
			c.Header("request-id", requestID)
			c.Data(http.StatusOK, "application/json", webSearchResult.ResponseBody)
			return &ForwardResult{
				RequestID:     requestID,
				Usage:         webSearchResult.Usage,
				Model:         originalModel,
				UpstreamModel: upstreamModel,
				Stream:        false,
				Duration:      time.Since(startTime),
			}, nil
		default:
			var httpErr *kiroWebSearchHTTPError
			if errors.As(webSearchErr, &httpErr) && httpErr.Response != nil {
				return nil, s.handleKiroHTTPError(ctx, httpErr.Response, c, account, mappedModel, body)
			}
			var failoverErr *UpstreamFailoverError
			if errors.As(webSearchErr, &failoverErr) {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: failoverErr.StatusCode,
					Kind:               "failover",
					Message:            sanitizeUpstreamErrorMessage(webSearchErr.Error()),
				})
				return nil, failoverErr
			}
			safeErr := sanitizeUpstreamErrorMessage(webSearchErr.Error())
			c.JSON(http.StatusBadGateway, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "upstream_error",
					"message": "Upstream request failed",
				},
			})
			return nil, fmt.Errorf("kiro upstream request failed: %s", safeErr)
		}
	}

	inputTokens := estimateKiroInputTokens(body)
	resp, requestCtx, err := s.executeKiroUpstreamWithParsed(ctx, account, parsed, body, mappedModel, originalModel, token, c.Request.Header)
	if err != nil {
		var failoverErr *UpstreamFailoverError
		if errors.As(err, &failoverErr) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: failoverErr.StatusCode,
				Kind:               "failover",
				Message:            sanitizeUpstreamErrorMessage(err.Error()),
			})
			return nil, failoverErr
		}
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		c.JSON(http.StatusBadGateway, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "upstream_error",
				"message": "Upstream request failed",
			},
		})
		return nil, fmt.Errorf("kiro upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, s.handleKiroHTTPError(ctx, resp, c, account, mappedModel, body)
	}

	cacheUsage := s.buildKiroCacheEmulationUsage(account, parsed.Group, body, mappedModel, inputTokens)
	requestCtx.CacheEmulationUsage = cacheUsage.toKiroUsage()
	parseResult, err := kiropkg.ParseNonStreamingEventStreamWithContext(resp.Body, mappedModel, requestCtx)
	if err != nil {
		if failoverErr := s.kiroStreamErrorToFailover(ctx, account, err); failoverErr != nil {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: failoverErr.StatusCode,
				Kind:               "parse_failover",
				Message:            sanitizeUpstreamErrorMessage(err.Error()),
			})
			return nil, failoverErr
		}
		c.JSON(http.StatusBadGateway, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "upstream_error",
				"message": "Failed to parse Kiro upstream response",
			},
		})
		return nil, err
	}

	c.Header("Content-Type", "application/json")
	requestID := buildKiroClientRequestID(resp)
	c.Header("x-request-id", requestID)
	c.Header("request-id", requestID)
	c.Data(http.StatusOK, "application/json", parseResult.ResponseBody)

	upstreamModel := resolveKiroUpstreamModel(mappedModel)

	return &ForwardResult{
		RequestID:     requestID,
		Usage:         kiroUsageToClaude(parseResult.Usage, inputTokens),
		Model:         originalModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}, nil
}

func (s *GatewayService) kiroStreamErrorToFailover(ctx context.Context, account *Account, err error) *UpstreamFailoverError {
	if err == nil || account == nil {
		return nil
	}

	var exceptionErr *kiropkg.UpstreamExceptionError
	if errors.As(err, &exceptionErr) {
		statusCode := http.StatusBadGateway
		retryableOnSameAccount := true
		exceptionType := strings.ToLower(strings.TrimSpace(exceptionErr.ExceptionType))
		exceptionMsg := strings.ToLower(strings.TrimSpace(exceptionErr.Message))
		if strings.Contains(exceptionType, "throttl") ||
			strings.Contains(exceptionType, "ratelimit") ||
			strings.Contains(exceptionType, "rate_limit") ||
			strings.Contains(exceptionMsg, "too many requests") ||
			strings.Contains(exceptionMsg, "rate limit") {
			statusCode = http.StatusTooManyRequests
			retryableOnSameAccount = false
		}
		body, _ := json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    "upstream_error",
				"message": sanitizeUpstreamErrorMessage(exceptionErr.Error()),
			},
		})
		return &UpstreamFailoverError{
			StatusCode:             statusCode,
			ResponseBody:           body,
			RetryableOnSameAccount: retryableOnSameAccount,
			KiroRateLimited:        statusCode == http.StatusTooManyRequests,
			Cause:                  err,
		}
	}

	var incompleteStreamErr *kiropkg.IncompleteStreamError
	if errors.As(err, &incompleteStreamErr) {
		body, _ := json.Marshal(map[string]any{
			"type": "error",
			"error": map[string]string{
				"type":    "upstream_error",
				"message": sanitizeUpstreamErrorMessage(incompleteStreamErr.Error()),
			},
		})
		return &UpstreamFailoverError{
			StatusCode:             http.StatusBadGateway,
			ResponseBody:           body,
			RetryableOnSameAccount: true,
			SuppressTempUnschedule: true,
			Cause:                  err,
		}
	}

	emptyStreamMsg := kiroEmptyEventStreamMessage(err)
	if emptyStreamMsg == "" {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    "upstream_error",
			"message": sanitizeUpstreamErrorMessage(emptyStreamMsg),
		},
	})
	return &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		ResponseBody:           body,
		RetryableOnSameAccount: true,
		SuppressTempUnschedule: true,
		Cause:                  err,
	}
}

func kiroEmptyEventStreamMessage(err error) string {
	for err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "empty kiro event stream") {
			return err.Error()
		}
		err = errors.Unwrap(err)
	}
	return ""
}

func (s *GatewayService) shouldPrepareKiroBridgeCacheEmulation(ctx context.Context, account *Account, body []byte) bool {
	_ = s
	_ = body
	if account == nil || !account.IsKiro() || !isKiroDirectModeAccount(account) {
		return false
	}
	return IsClaudeCodeXMLInvokeBridgeUserAgent(ClaudeCodeUserAgent(ctx))
}

// prepareKiroBridgeCacheEmulationBody aligns Kiro's local cache-emulation
// breakpoints with Claude Desktop / Agent SDK traffic.
//
// Kiro upstream does not consume Anthropic cache_control blocks directly, but
// the local Kiro billing emulation uses the Anthropic request body to decide
// which prefixes should be treated as cache read/write. Claude Desktop 3P
// agent-sdk clients often place a drifting message cache_control on the current
// tail (or omit one entirely for sub-agent turns), so using the body as-is makes
// the emulated prefix unstable. Reuse the bridge strategy from the Anthropic
// path: strip message cache_control and add stable + trailing message anchors.
//
// Unlike injectBridgeCacheBreakpoints, this helper intentionally does not rename
// tools; Kiro's translator has its own tool-name mapping and response restore
// context.
func (s *GatewayService) prepareKiroBridgeCacheEmulationBody(ctx context.Context, account *Account, body []byte) []byte {
	if !s.shouldPrepareKiroBridgeCacheEmulation(ctx, account, body) {
		return body
	}
	body = stripMessageCacheControl(body)
	body = addBridgeMessageCacheBreakpointsWithTTL(body, cacheTTLTarget1h)
	body = enforceCacheControlLimit(body)
	body = normalizeCacheControlTTLOrder(body)
	return body
}

func isKiroDirectTokenType(tokenType string) bool {
	switch strings.ToLower(strings.TrimSpace(tokenType)) {
	case "oauth", "apikey":
		return true
	default:
		return false
	}
}

func resolveKiroUpstreamModel(mappedModel string) string {
	upstreamModel := kiropkg.MapModel(mappedModel)
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = strings.TrimSpace(mappedModel)
	}
	return upstreamModel
}

func (s *GatewayService) openKiroAnthropicStreamResponse(ctx context.Context, account *Account, parsed *ParsedRequest, anthropicBody []byte, mappedModel, requestModel string, headers http.Header, group *Group) (*http.Response, int, error) {
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, 0, err
	}
	if !isKiroDirectTokenType(tokenType) {
		return nil, 0, fmt.Errorf("kiro requires oauth or apikey token, got %s", tokenType)
	}

	inputTokens := estimateKiroInputTokens(anthropicBody)
	if isOnlyWebSearchToolInBody(anthropicBody) {
		cacheUsage := s.buildKiroCacheEmulationUsage(account, group, anthropicBody, mappedModel, inputTokens)
		pr, pw := io.Pipe()
		headers := make(http.Header)
		headers.Set("Content-Type", "text/event-stream")
		go func() {
			streamErr := s.streamKiroWebSearchAsAnthropic(ctx, account, anthropicBody, mappedModel, requestModel, token, inputTokens, headers, pw, cacheUsage)
			if streamErr != nil {
				_ = pw.CloseWithError(streamErr)
				return
			}
			_ = pw.Close()
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     headers,
			Body:       pr,
		}, inputTokens, nil
	}

	streamCtx, releaseStreamCtx := detachStreamUpstreamContext(ctx, true)
	resp, requestCtx, err := s.executeKiroUpstreamWithParsed(streamCtx, account, parsed, anthropicBody, mappedModel, requestModel, token, headers)
	if err != nil {
		releaseStreamCtx()
		var failoverErr *UpstreamFailoverError
		if errors.As(err, &failoverErr) {
			return nil, inputTokens, err
		}
		return nil, inputTokens, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, inputTokens, nil
	}
	cacheUsage := s.buildKiroCacheEmulationUsage(account, group, anthropicBody, mappedModel, inputTokens)
	requestCtx.CacheEmulationUsage = cacheUsage.toKiroUsage()
	requestCtx.RequireTerminalEvent = true

	pr, pw := io.Pipe()
	wrappedHeaders := resp.Header.Clone()
	wrappedHeaders.Set("Content-Type", "text/event-stream")
	requestID := buildKiroClientRequestID(resp)
	wrappedHeaders.Set("x-request-id", requestID)
	wrappedHeaders.Set("request-id", requestID)

	go func() {
		defer releaseStreamCtx()
		defer func() { _ = resp.Body.Close() }()
		_, streamErr := kiropkg.StreamEventStreamAsAnthropicWithContext(streamCtx, resp.Body, pw, mappedModel, inputTokens, requestCtx)
		if streamErr != nil {
			_ = pw.CloseWithError(streamErr)
			return
		}
		_ = pw.Close()
	}()

	return &http.Response{
		StatusCode: resp.StatusCode,
		Header:     wrappedHeaders,
		Body:       pr,
	}, inputTokens, nil
}

func (s *GatewayService) executeKiroUpstream(ctx context.Context, account *Account, anthropicBody []byte, mappedModel, requestModel, token string, headers http.Header) (*http.Response, kiropkg.KiroRequestContext, error) {
	return s.executeKiroUpstreamWithParsed(ctx, account, nil, anthropicBody, mappedModel, requestModel, token, headers)
}

func (s *GatewayService) executeKiroUpstreamWithParsed(ctx context.Context, account *Account, parsed *ParsedRequest, anthropicBody []byte, mappedModel, requestModel, token string, headers http.Header) (*http.Response, kiropkg.KiroRequestContext, error) {
	var requestCtx kiropkg.KiroRequestContext
	mode := kiroEndpointModeForRequest(account, parsed)
	if mode == KiroEndpointModeKRS || mode == KiroEndpointModeAuto {
		s.ensureKiroProfileArnForRequest(ctx, account, token, mode)
	}
	accountKey := buildKiroAccountKey(account)
	if err := s.checkAndWaitKiroCooldown(ctx, accountKey); err != nil {
		if failoverErr := asKiroCooldownFailoverError(err); failoverErr != nil {
			return nil, requestCtx, failoverErr
		}
		return nil, requestCtx, err
	}

	modelID := kiropkg.MapModel(mappedModel)
	currentToken := token
	endpoints := buildKiroEndpointsForMode(account, mode)
	proxyURL := kiroProxyURL(account)
	tlsProfile := s.tlsFPProfileService.ResolveTLSProfile(account)
	maxRetries := 2
	var lastKiro429 *UpstreamFailoverError

	for idx, endpoint := range endpoints {
		buildResult, err := s.buildKiroPayloadForParsedAccountEndpoint(ctx, account, parsed, anthropicBody, modelID, currentToken, requestModel, headers, endpoint)
		if err != nil {
			return nil, requestCtx, err
		}
		payload := buildResult.Payload
		requestCtx = buildResult.Context
		logKiroStatelessReplay(account, buildResult.Payload)

		for attempt := 0; attempt <= maxRetries; attempt++ {
			req, err := newKiroJSONRequestWithExplicitTarget(ctx, endpoint.URL, payload, currentToken, accountKey, buildKiroMachineID(account), endpoint.AmzTarget, account, attempt+1, maxRetries+1)
			if err != nil {
				return nil, requestCtx, err
			}

			resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
			if err != nil {
				if attempt < maxRetries {
					if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
						return nil, requestCtx, sleepErr
					}
					continue
				}
				return nil, requestCtx, err
			}

			if resp.StatusCode == http.StatusTooManyRequests {
				respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, requestCtx, readErr
				}
				if len(respBody) == 0 {
					respBody = []byte(`{"message":"kiro upstream rate limited"}`)
				}
				lastKiro429 = &UpstreamFailoverError{
					StatusCode:      http.StatusTooManyRequests,
					ResponseBody:    respBody,
					ResponseHeaders: resp.Header.Clone(),
					KiroRateLimited: true,
				}
				if idx+1 < len(endpoints) {
					logger.L().Warn("kiro endpoint rate limited; trying next endpoint",
						zap.Int64("account_id", account.ID),
						zap.String("endpoint", endpoint.Name),
						zap.String("next_endpoint", endpoints[idx+1].Name),
						zap.String("request_id", buildKiroRequestID(resp)),
					)
					break
				}
				return nil, requestCtx, lastKiro429
			}

			if resp.StatusCode == http.StatusRequestTimeout || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
				if attempt < maxRetries {
					_ = resp.Body.Close()
					if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
						return nil, requestCtx, sleepErr
					}
					continue
				}
				if idx+1 < len(endpoints) {
					_ = resp.Body.Close()
					if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
						return nil, requestCtx, sleepErr
					}
					break
				}
				return resp, requestCtx, nil
			}

			if resp.StatusCode == http.StatusPaymentRequired {
				respBody, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, requestCtx, readErr
				}
				classification := classifyKiroHTTPError(resp.StatusCode, string(respBody))
				if classification.Category == kiroErrorMonthlyRequest {
					s.markKiroMonthlyRequestCountRateLimited(ctx, account, string(respBody))
				}
				return nil, requestCtx, &UpstreamFailoverError{
					StatusCode:      resp.StatusCode,
					ResponseBody:    respBody,
					ResponseHeaders: resp.Header.Clone(),
				}
			}

			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				respBody, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, requestCtx, readErr
				}

				if resp.StatusCode == http.StatusForbidden && isKiroSuspendedBody(respBody) {
					if _, err := s.markKiroSuspended(ctx, accountKey); err != nil {
						return nil, requestCtx, err
					}
					resetHTTPResponseBody(resp, respBody)
					return resp, requestCtx, nil
				}

				if s.kiroTokenProvider != nil && (resp.StatusCode == http.StatusUnauthorized || isKiroTokenErrorBody(respBody)) && attempt < maxRetries {
					refreshedToken, refreshErr := s.kiroTokenProvider.ForceRefreshAccessToken(ctx, account)
					if refreshErr == nil && strings.TrimSpace(refreshedToken) != "" {
						currentToken = refreshedToken
						accountKey = buildKiroAccountKey(account)
						buildResult, err = s.buildKiroPayloadForParsedAccountEndpoint(ctx, account, parsed, anthropicBody, modelID, currentToken, requestModel, headers, endpoint)
						if err != nil {
							return nil, requestCtx, err
						}
						payload = buildResult.Payload
						requestCtx = buildResult.Context
						logKiroStatelessReplay(account, buildResult.Payload)
						if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
							return nil, requestCtx, sleepErr
						}
						continue
					}
					if refreshErr != nil && isNonRetryableRefreshError(refreshErr) {
						resetHTTPResponseBody(resp, respBody)
						return resp, requestCtx, nil
					}
				}

				if classifyKiroHTTPError(resp.StatusCode, string(respBody)).Category == kiroErrorAuthError {
					s.markKiroAuthTemporarilyUnavailable(ctx, account, resp.StatusCode, string(respBody))
				}

				resetHTTPResponseBody(resp, respBody)
				return resp, requestCtx, nil
			}

			if resp.StatusCode == http.StatusBadRequest {
				respBody, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, requestCtx, readErr
				}
				classification := classifyKiroHTTPError(resp.StatusCode, string(respBody))
				logKiroBadRequestClassification(classification, account, mappedModel, resp.Header, respBody)
				resetHTTPResponseBody(resp, respBody)
				return resp, requestCtx, nil
			}

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if err := s.markKiroSuccess(ctx, accountKey); err != nil {
					_ = resp.Body.Close()
					return nil, requestCtx, err
				}
			}
			return resp, requestCtx, nil
		}
	}
	if lastKiro429 != nil {
		return nil, requestCtx, lastKiro429
	}
	return nil, requestCtx, fmt.Errorf("kiro upstream endpoints exhausted")
}

func buildKiroEndpoints(account *Account) []kiroEndpointConfig {
	return buildKiroEndpointsForMode(account, KiroEndpointModeQ)
}

func buildKiroEndpointsForMode(account *Account, mode string) []kiroEndpointConfig {
	if isKiroRuntimeEndpointMode(account) && strings.TrimSpace(account.GetCredential("profile_arn")) != "" {
		region := kiroRuntimeAPIRegion(account)
		return []kiroEndpointConfig{
			{
				URL:       fmt.Sprintf("https://runtime.%s.kiro.dev/", region),
				Origin:    "KIRO_CLI",
				AmzTarget: kiroGenerateAssistantResponseTarget,
				Name:      "KiroRuntime",
			},
		}
	}
	if mode == KiroEndpointModeKRS {
		return []kiroEndpointConfig{kiroKRSEndpointConfig()}
	}
	region := kiroAPIRegion(account)
	endpoints := []kiroEndpointConfig{
		{
			URL:       fmt.Sprintf("https://q.%s.amazonaws.com/generateAssistantResponse", region),
			Origin:    "AI_EDITOR",
			AmzTarget: "",
			Name:      "KiroIDE",
		},
		{
			URL:       fmt.Sprintf("https://codewhisperer.%s.amazonaws.com/generateAssistantResponse", region),
			Origin:    "AI_EDITOR",
			AmzTarget: kiroGenerateAssistantResponseTarget,
			Name:      "CodeWhisperer",
		},
		{
			URL:       fmt.Sprintf("https://q.%s.amazonaws.com/generateAssistantResponse", region),
			Origin:    "AI_EDITOR",
			AmzTarget: "AmazonQDeveloperStreamingService.SendMessage",
			Name:      "AmazonQ",
		},
	}
	endpoints = sortKiroEndpointsByPreference(endpoints, account)
	if mode == KiroEndpointModeAuto {
		endpoints = append(endpoints, kiroKRSEndpointConfig())
	}
	return endpoints
}

func kiroKRSEndpointConfig() kiroEndpointConfig {
	return kiroEndpointConfig{
		URL:    kiroKRSEndpointURL,
		Origin: "AI_EDITOR",
		Name:   "KiroRuntime",
	}
}

func kiroEndpointModeForRequest(account *Account, parsed *ParsedRequest) string {
	if account != nil {
		if account.Type == AccountTypeAPIKey {
			return KiroEndpointModeQ
		}
		if hasExplicitKiroEndpointPreference(account) {
			return KiroEndpointModeQ
		}
	}
	if parsed == nil || parsed.Group == nil {
		return KiroEndpointModeQ
	}
	return parsed.Group.EffectiveKiroEndpointMode()
}

func sortKiroEndpointsByPreference(endpoints []kiroEndpointConfig, account *Account) []kiroEndpointConfig {
	if len(endpoints) == 0 || account == nil {
		return endpoints
	}
	var primaryName string
	switch strings.ToLower(strings.TrimSpace(account.GetCredential("preferred_endpoint"))) {
	case "kiro", "kiroide", "kiro_ide", "ide":
		primaryName = "KiroIDE"
	case "codewhisperer", "cw":
		primaryName = "CodeWhisperer"
	case "amazonq", "amazon_q", "q":
		primaryName = "AmazonQ"
	default:
		return endpoints
	}
	primary := -1
	for i, endpoint := range endpoints {
		if endpoint.Name == primaryName {
			primary = i
			break
		}
	}
	if primary <= 0 {
		return endpoints
	}
	sorted := make([]kiroEndpointConfig, 0, len(endpoints))
	sorted = append(sorted, endpoints[primary])
	for i, endpoint := range endpoints {
		if i != primary {
			sorted = append(sorted, endpoint)
		}
	}
	return sorted
}

func (s *GatewayService) buildKiroPayloadForAccount(ctx context.Context, account *Account, anthropicBody []byte, modelID, token, requestModel string, headers http.Header) (*kiropkg.KiroBuildResult, error) {
	return s.buildKiroPayloadForAccountEndpoint(ctx, account, anthropicBody, modelID, token, requestModel, headers, kiroEndpointConfig{Origin: "AI_EDITOR"})
}

func (s *GatewayService) buildKiroPayloadForAccountEndpoint(ctx context.Context, account *Account, anthropicBody []byte, modelID, token, requestModel string, headers http.Header, endpoint kiroEndpointConfig) (*kiropkg.KiroBuildResult, error) {
	return s.buildKiroPayloadForParsedAccountEndpoint(ctx, account, nil, anthropicBody, modelID, token, requestModel, headers, endpoint)
}

func (s *GatewayService) buildKiroPayloadForParsedAccountEndpoint(ctx context.Context, account *Account, parsed *ParsedRequest, anthropicBody []byte, modelID, token, requestModel string, headers http.Header, endpoint kiroEndpointConfig) (*kiropkg.KiroBuildResult, error) {
	_ = s
	_ = ctx
	_ = token
	var profileArn string
	if endpoint.Name == "KiroRuntime" {
		profileArn = s.resolveKiroPayloadProfileArn(ctx, account, token)
		if profileArn == "" {
			profileArn = kiroResolveProfileArnForKRS(account)
		}
	}
	if isKiroCLIWireMode(account) && profileArn == "" {
		profileArn = kiroResolveProfileArnForKRS(account)
	}
	anthropicBody = prepareKiroPayloadBodyForRequestModel(anthropicBody, requestModel)
	if isKiroCLIWireMode(account) {
		buildResult, err := kiropkg.BuildKiroPayloadWithOptions(anthropicBody, modelID, profileArn, headers, kiropkg.KiroPayloadOptions{
			Origin:                     "KIRO_CLI",
			UseNativeEffort:            true,
			AttachEnvState:             true,
			InjectThinkingSystemPrompt: false,
		})
		if err != nil {
			return nil, err
		}
		return applyStableKiroConversationID(account, parsed, anthropicBody, modelID, profileArn, buildResult), nil
	}
	origin := strings.TrimSpace(endpoint.Origin)
	if origin == "" {
		origin = "AI_EDITOR"
	}
	buildResult, err := kiropkg.BuildKiroPayloadWithContext(anthropicBody, modelID, profileArn, origin, headers)
	if err != nil {
		return nil, err
	}
	return applyStableKiroConversationID(account, parsed, anthropicBody, modelID, profileArn, buildResult), nil
}

func applyStableKiroConversationID(account *Account, parsed *ParsedRequest, anthropicBody []byte, modelID, profileArn string, buildResult *kiropkg.KiroBuildResult) *kiropkg.KiroBuildResult {
	if buildResult == nil {
		return nil
	}
	if stableID := stableKiroConversationID(account, parsed, anthropicBody, modelID, profileArn); stableID != "" {
		if next, err := sjson.SetBytes(buildResult.Payload, "conversationState.conversationId", stableID); err == nil {
			buildResult.Payload = next
		}
	}
	return buildResult
}

func stableKiroConversationID(account *Account, parsed *ParsedRequest, anthropicBody []byte, modelID, profileArn string) string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_CONVERSATION_ID_MODE"))) {
	case "random", "uuid", "off", "false", "0":
		return ""
	}
	seed := stableKiroConversationSeed(account, parsed, anthropicBody, modelID, profileArn)
	if seed == "" {
		return ""
	}
	return generateSessionUUID(seed)
}

func stableKiroConversationSeed(account *Account, parsed *ParsedRequest, anthropicBody []byte, modelID, profileArn string) string {
	anchorType, anchor := stableKiroConversationAnchor(parsed, anthropicBody)
	if anchor == "" {
		return ""
	}

	var sb strings.Builder
	_, _ = sb.WriteString("kiro-conversation-v1|")
	if account != nil {
		_, _ = sb.WriteString("account:")
		_, _ = sb.WriteString(strconv.FormatInt(account.ID, 10))
		_, _ = sb.WriteString("|credential:")
		_, _ = sb.WriteString(kiroCacheCredentialIdentity(account))
		_, _ = sb.WriteString("|")
	}
	if parsed != nil && parsed.SessionContext != nil {
		_, _ = sb.WriteString("api_key:")
		_, _ = sb.WriteString(strconv.FormatInt(parsed.SessionContext.APIKeyID, 10))
		_, _ = sb.WriteString("|")
	}
	_, _ = sb.WriteString("model:")
	_, _ = sb.WriteString(strings.TrimSpace(modelID))
	_, _ = sb.WriteString("|profile:")
	_, _ = sb.WriteString(strings.TrimSpace(profileArn))
	_, _ = sb.WriteString("|anchor:")
	_, _ = sb.WriteString(anchorType)
	_, _ = sb.WriteString(":")
	_, _ = sb.WriteString(anchor)
	return sb.String()
}

func stableKiroConversationAnchor(parsed *ParsedRequest, anthropicBody []byte) (string, string) {
	if parsed != nil {
		if explicitID := strings.TrimSpace(parsed.ExplicitSessionID); explicitID != "" {
			return "explicit", explicitID
		}
		if metadataUserID := strings.TrimSpace(parsed.MetadataUserID); metadataUserID != "" {
			return "metadata", metadataUserID
		}
		if bodySessionID := strings.TrimSpace(parsed.BodySessionID); bodySessionID != "" {
			return "body_session", bodySessionID
		}
		if systemText := extractTextFromSystemRaw(parsed.SystemRaw()); systemText != "" {
			return "system", systemText
		}
	}
	if len(anthropicBody) > 0 {
		if systemText := extractTextFromSystemRaw([]byte(gjson.GetBytes(anthropicBody, "system").Raw)); systemText != "" {
			return "system", systemText
		}
		if firstUserText := extractFirstUserText(anthropicBody); firstUserText != "" {
			return "first_user", firstUserText
		}
	}
	return "", ""
}

func logKiroStatelessReplay(account *Account, payload []byte) {
	if account == nil {
		return
	}
	conversationID := gjson.GetBytes(payload, "conversationState.conversationId").String()
	systemPrompt := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	currentContent := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String()
	logger.L().Info("kiro.stateless_replay",
		zap.Int64("selected_account_id", account.ID),
		zap.Bool("stateless_replay", true),
		zap.Int("history_count", len(gjson.GetBytes(payload, "conversationState.history").Array())),
		zap.Bool("has_agent_continuation_id", gjson.GetBytes(payload, "conversationState.agentContinuationId").Exists()),
		zap.String("conversation_id_hash", hashKiroLogString(conversationID)),
		zap.String("payload_hash_no_conversation_id", hashKiroPayloadWithoutConversationID(payload)),
		zap.String("system_prompt_hash", hashKiroLogString(systemPrompt)),
		zap.Int("system_prompt_len", len(systemPrompt)),
		zap.String("current_content_hash", hashKiroLogString(currentContent)),
		zap.Int("tool_count", len(gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array())),
	)
}

func hashKiroPayloadWithoutConversationID(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	normalized := payload
	if next, err := sjson.DeleteBytes(payload, "conversationState.conversationId"); err == nil {
		normalized = next
	}
	return strconv.FormatUint(xxhash.Sum64(normalized), 36)
}

func hashKiroLogString(value string) string {
	if value == "" {
		return ""
	}
	return strconv.FormatUint(xxhash.Sum64String(value), 36)
}

func prepareKiroPayloadBodyForRequestModel(anthropicBody []byte, requestModel string) []byte {
	requestModel = strings.TrimSpace(requestModel)
	if requestModel == "" || !strings.Contains(strings.ToLower(requestModel), "thinking") {
		return anthropicBody
	}
	bodyModel := strings.TrimSpace(gjson.GetBytes(anthropicBody, "model").String())
	if bodyModel == "" || strings.EqualFold(bodyModel, requestModel) || strings.Contains(strings.ToLower(bodyModel), "thinking") {
		return anthropicBody
	}
	if next, ok := setJSONValueBytes(anthropicBody, "model", requestModel); ok {
		return next
	}
	return anthropicBody
}

func (s *GatewayService) markKiroAuthTemporarilyUnavailable(ctx context.Context, account *Account, statusCode int, body string) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	until := time.Now().Add(10 * time.Minute)
	reason := fmt.Sprintf("kiro auth failure (%d): %s", statusCode, strings.TrimSpace(body))
	_ = s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason)
}

func (s *GatewayService) markKiroMonthlyRequestCountRateLimited(ctx context.Context, account *Account, body string) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	resetAt := nextKiroMonthlyResetUTC(time.Now())
	if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetAt); err != nil {
		logger.L().Warn("kiro monthly request count rate-limit failed",
			zap.Int64("account_id", account.ID),
			zap.Time("reset_at", resetAt),
			zap.Error(err),
		)
		return
	}
	reason := "kiro monthly request count exhausted (402): MONTHLY_REQUEST_COUNT"
	if trimmed := strings.TrimSpace(body); trimmed != "" {
		reason = fmt.Sprintf("%s body=%s", reason, truncateForLog([]byte(trimmed), 512))
	}
	logger.L().Warn("kiro monthly request count rate-limited",
		zap.Int64("account_id", account.ID),
		zap.Time("reset_at", resetAt),
		zap.String("reason", reason),
	)
}

func nextKiroMonthlyResetUTC(now time.Time) time.Time {
	utc := now.UTC()
	year, month, _ := utc.Date()
	return time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC)
}

func resetHTTPResponseBody(resp *http.Response, body []byte) {
	if resp == nil {
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
}

func estimateKiroInputTokens(body []byte) int {
	if len(body) == 0 {
		return 0
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err == nil {
		return countKiroInputTokensFromPayload(payload)
	}
	tokens := len(body) / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}

func kiroUsageToClaude(usage kiropkg.Usage, fallbackInput int) ClaudeUsage {
	inputTokens := usage.InputTokens
	if inputTokens == 0 {
		inputTokens = fallbackInput
	}
	return ClaudeUsage{
		InputTokens:              inputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheCreation5mTokens:    usage.CacheCreation5mInputTokens,
		CacheCreation1hTokens:    usage.CacheCreation1hInputTokens,
		KiroCredits:              usage.KiroCredits,
	}
}

func (s *GatewayService) markKiroInvalidModelRateLimited(ctx context.Context, account *Account, mappedModel string) {
	if s == nil || s.accountRepo == nil || account == nil || account.Type != AccountTypeOAuth {
		return
	}
	resetAt := time.Now().Add(kiroInvalidModelTempUnschedDuration)
	if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetAt); err != nil {
		logger.L().Warn("kiro invalid model rate-limit failed",
			zap.Int64("account_id", account.ID),
			zap.String("mapped_model", strings.TrimSpace(mappedModel)),
			zap.Time("reset_at", resetAt),
			zap.Error(err),
		)
		return
	}
	logger.L().Warn("kiro invalid model rate-limited",
		zap.Int64("account_id", account.ID),
		zap.String("mapped_model", strings.TrimSpace(mappedModel)),
		zap.Time("reset_at", resetAt),
	)
}

func (s *GatewayService) handleKiroHTTPError(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, mappedModel string, requestBody []byte) error {
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	if upstreamMsg == "" {
		upstreamMsg = strings.TrimSpace(string(respBody))
	}
	classification := classifyKiroHTTPError(resp.StatusCode, string(respBody))
	if resp.StatusCode == http.StatusBadRequest {
		logKiroBadRequestClassification(classification, account, "", resp.Header, respBody)
	}
	if classification.Category == kiroErrorMonthlyRequest {
		s.markKiroMonthlyRequestCountRateLimited(ctx, account, string(respBody))
	}
	if classification.Category == kiroErrorBadRequestInvalidModel && account != nil && account.Type == AccountTypeOAuth {
		s.markKiroInvalidModelRateLimited(ctx, account, mappedModel)
		event := s.buildKiroInvalidModelUpstreamEvent(account, resp, upstreamMsg, mappedModel, requestBody, c)
		appendOpsUpstreamError(c, event)
		return &UpstreamFailoverError{
			StatusCode:      resp.StatusCode,
			ResponseBody:    respBody,
			ResponseHeaders: resp.Header.Clone(),
		}
	}

	if resp.StatusCode == http.StatusPaymentRequired || s.shouldFailoverUpstreamError(resp.StatusCode) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  resp.Header.Get("x-request-id"),
			Kind:               "failover",
			Message:            upstreamMsg,
		})
		if s.rateLimitService != nil {
			s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		}
		return &UpstreamFailoverError{
			StatusCode:      resp.StatusCode,
			ResponseBody:    respBody,
			ResponseHeaders: resp.Header.Clone(),
		}
	}

	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "http_error",
		Message:            upstreamMsg,
	})
	c.JSON(mapUpstreamStatusCode(resp.StatusCode), gin.H{
		"type": "error",
		"error": gin.H{
			"type":    "upstream_error",
			"message": coalesceKiroErrorMessage(resp.StatusCode, upstreamMsg),
		},
	})
	return fmt.Errorf("kiro upstream error: %d %s", resp.StatusCode, upstreamMsg)
}

func (s *GatewayService) buildKiroInvalidModelUpstreamEvent(account *Account, resp *http.Response, upstreamMsg, mappedModel string, requestBody []byte, c *gin.Context) OpsUpstreamErrorEvent {
	_ = s
	requestedModel := strings.TrimSpace(gjson.GetBytes(requestBody, "model").String())
	hasTools := gjson.GetBytes(requestBody, "tools").Exists()
	hasAdaptiveThinking := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(requestBody, "thinking.type").String()), "adaptive")
	hasContext1MBeta := false
	if c != nil {
		hasContext1MBeta = strings.Contains(c.GetHeader("Anthropic-Beta"), "context-1m")
	}
	return OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "failover",
		Message:            upstreamMsg,
		Detail: fmt.Sprintf("requested_model=%s mapped_model=%s kiro_model_id=%s has_tools=%t has_adaptive_thinking=%t has_context_1m_beta=%t",
			requestedModel,
			strings.TrimSpace(mappedModel),
			kiropkg.MapModel(mappedModel),
			hasTools,
			hasAdaptiveThinking,
			hasContext1MBeta,
		),
	}
}

func logKiroBadRequestClassification(classification kiroErrorClassification, account *Account, model string, headers http.Header, body []byte) {
	if classification.StatusCode != http.StatusBadRequest {
		return
	}
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	logger.L().Warn("kiro upstream bad request classified",
		zap.String("category", classification.Category),
		zap.Int("status", classification.StatusCode),
		zap.Int64("account_id", accountID),
		zap.String("model", strings.TrimSpace(model)),
		zap.String("request_id", headers.Get("x-request-id")),
		zap.String("body_excerpt", truncateForLog(body, 512)),
	)
}

func coalesceKiroErrorMessage(statusCode int, upstreamMsg string) string {
	if upstreamMsg != "" {
		return upstreamMsg
	}
	switch statusCode {
	case http.StatusTooManyRequests:
		return "Rate limit exceeded"
	case http.StatusForbidden:
		return "Access denied"
	case http.StatusUnauthorized:
		return "Authentication failed"
	default:
		return "Upstream request failed"
	}
}

func validateKiroRequestShape(parsed *ParsedRequest) string {
	if parsed == nil {
		return "messages must not be empty"
	}

	var messages []any
	_ = parsed.DecodeMessages(&messages)
	if len(messages) == 0 && parsed.Body.Len() > 0 {
		if bodyMessages := gjson.GetBytes(parsed.Body.Bytes(), "messages"); bodyMessages.Exists() && bodyMessages.IsArray() {
			var decoded []any
			if err := json.Unmarshal([]byte(bodyMessages.Raw), &decoded); err == nil {
				messages = decoded
			}
		}
	}
	if len(messages) == 0 {
		return "messages must not be empty"
	}

	lastRole := ""
	hasUserContext := false
	for _, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := stringFromKiroRequestShapeValue(msg["role"])
		if role == "" {
			continue
		}
		lastRole = role
		if role == "user" && kiroUserMessageHasContext(msg["content"]) {
			hasUserContext = true
		}
	}

	if lastRole == "assistant" {
		return "assistant-prefill final message is not supported; last message must be user"
	}
	if !hasUserContext {
		return "at least one non-empty user message is required"
	}
	return ""
}

func kiroUserMessageHasContext(content any) bool {
	switch v := content.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case []any:
		for _, rawBlock := range v {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				continue
			}
			blockType := stringFromKiroRequestShapeValue(block["type"])
			switch blockType {
			case "text", "input_text":
				if stringFromKiroRequestShapeValue(block["text"]) != "" {
					return true
				}
			case "image", "image_url", "input_image", "file", "input_file", "tool_result":
				return true
			}
		}
	case map[string]any:
		if stringFromKiroRequestShapeValue(v["text"]) != "" {
			return true
		}
		switch stringFromKiroRequestShapeValue(v["type"]) {
		case "image", "image_url", "input_image", "file", "input_file", "tool_result":
			return true
		}
	}
	return false
}

func stringFromKiroRequestShapeValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}
