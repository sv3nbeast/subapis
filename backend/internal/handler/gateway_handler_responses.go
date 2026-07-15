package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Responses handles OpenAI Responses API endpoint for Anthropic platform groups.
// POST /v1/responses
// This converts Responses API requests to Anthropic format, forwards to Anthropic
// upstream, and converts responses back to Responses format.
func (h *GatewayHandler) Responses(c *gin.Context) {
	streamStarted := false

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.responsesErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.responsesErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.gateway.responses",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// Read request body
	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.responsesErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	setOpsRequestContext(c, "", false, body)

	// Validate JSON
	if !gjson.ValidBytes(body) {
		logRequestBodyParseFailure(reqLog, body, nil)
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	// Extract model and stream using gjson (like OpenAI handler)
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	reqStream, ok := parseOpenAICompatibleStream(body)
	if !ok {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", invalidStreamFieldTypeMessage)
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	setOpsRequestContext(c, reqModel, reqStream, body)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(reqStream, false)))
	requestCtx := c.Request.Context()
	imageGenerationIntent := service.IsImageGenerationIntent("/v1/responses", reqModel, body)
	if imageGenerationIntent {
		requestCtx = service.WithOpenAIImageGenerationIntent(requestCtx)
	}

	// 解析渠道级模型映射
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(requestCtx, apiKey.GroupID, reqModel)

	// Claude Code only restriction:
	// /v1/responses is never a Claude Code endpoint.
	// When claude_code_only is enabled, this endpoint is rejected.
	// The existing service-layer checkClaudeCodeRestriction handles degradation
	// to fallback groups when the Forward path calls SelectAccountForModelWithExclusions.
	// Here we just reject at handler level since /v1/responses clients can't be Claude Code.
	if apiKey.Group != nil && apiKey.Group.ClaudeCodeOnly {
		h.responsesErrorResponse(c, http.StatusForbidden, "permission_error",
			"This group is restricted to Claude Code clients (/v1/messages only)")
		return
	}

	if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIResponses, reqModel, body); decision != nil && decision.Blocked {
		h.responsesErrorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
		return
	}

	// Error passthrough binding
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	userReleaseFunc, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		reqLog.Warn("gateway.responses.user_slot_acquire_failed", zap.Error(err))
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
	}
	userReleaseFunc = wrapReleaseOnDone(c.Request.Context(), userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. Re-check billing
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription); err != nil {
		reqLog.Info("gateway.responses.billing_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.responsesErrorResponse(c, status, code, message)
		return
	}

	// Parse request for session hash
	bodyRef := service.NewRequestBodyRef(body)
	parsedReq, _ := service.ParseGatewayRequest(bodyRef, "responses")
	if parsedReq == nil {
		parsedReq = &service.ParsedRequest{Model: reqModel, Stream: reqStream, Body: bodyRef}
	}
	attachAPIKeyGroupToParsedRequest(parsedReq, apiKey)
	parsedReq.SessionContext = &service.SessionContext{
		ClientIP:  ip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		APIKeyID:  apiKey.ID,
	}
	parsedReq.ExplicitSessionID = explicitStickySessionIDFromHeaders(c)
	sessionHash := h.gatewayService.GenerateSessionHash(parsedReq)
	sessionBoundAccountID, _ := h.gatewayService.GetCachedSessionAccountID(c.Request.Context(), apiKey.GroupID, sessionHash)

	// 3. Account selection + failover loop
	fs := NewFailoverState(h.maxAccountSwitches, false)
	fs.KiroResilienceEnforced = h.gatewayService.KiroResilienceEnforced(apiKey.GroupID)
	c.Request = c.Request.WithContext(service.WithModelCapacityRetryState(c.Request.Context(), fs.ModelCapacityRetryState, h.metadataBridgeEnabled()))

	for {
		selectionCtx := service.WithAvoidEmailDomainSuffixes(c.Request.Context(), fs.AvoidEmailDomainSuffixesList(), h.metadataBridgeEnabled())
		selectionCtx = fs.SelectionContext(selectionCtx, apiKey.GroupID, h.metadataBridgeEnabled())
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(selectionCtx, apiKey.GroupID, sessionHash, reqModel, fs.FailedAccountIDs, "", int64(0))
		if err != nil {
			if len(fs.FailedAccountIDs) == 0 {
				if cls := classifySelectionError(err); cls.Handled {
					applySelectionErrorMonitoringClassification(c, cls)
					h.responsesErrorResponse(c, cls.StatusCode, cls.ErrorType, cls.Message)
					return
				}
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, reqModel, reqModel, service.PlatformAnthropic)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				message := cls.Message
				if !cls.ModelNotFound {
					message = "No available accounts: " + err.Error()
				}
				h.responsesErrorResponse(c, cls.Status, cls.ErrType, message)
				return
			}
			action := fs.HandleSelectionExhausted(c.Request.Context(), err)
			switch action {
			case FailoverContinue:
				continue
			case FailoverCanceled:
				return
			default:
				if fs.LastFailoverErr != nil {
					h.handleResponsesFailoverExhausted(c, fs.LastFailoverErr, streamStarted)
				} else {
					h.responsesErrorResponse(c, http.StatusBadGateway, "server_error", "All available accounts exhausted")
				}
				return
			}
		}
		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)
		if budgetErr := prepareKiroAccountAttempt(c, h.gatewayService, apiKey.GroupID, account); budgetErr != nil {
			if selection.Acquired && selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			h.responsesErrorResponse(c, http.StatusServiceUnavailable, "upstream_error", "Kiro upstream failover budget exhausted")
			return
		}

		// 4. Acquire account concurrency slot
		accountReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				h.responsesErrorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts")
				return
			}
			waitTimeout, budgetErr := kiroAccountWaitTimeout(h.gatewayService, c.Request.Context(), apiKey.GroupID, account, selection.WaitPlan.Timeout)
			if budgetErr != nil {
				applyKiroBudgetExhaustedRetryAfter(c)
				h.responsesErrorResponse(c, http.StatusServiceUnavailable, "upstream_error", "Kiro upstream failover budget exhausted")
				return
			}
			accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
				c,
				account.ID,
				selection.WaitPlan.MaxConcurrency,
				waitTimeout,
				reqStream,
				&streamStarted,
			)
			if err != nil {
				reqLog.Warn("gateway.responses.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				if account.Platform == service.PlatformKiro && fs.KiroResilienceEnforced && isAccountConcurrencyWaitTimeout(err) {
					if _, remainingErr := h.gatewayService.KiroWaitTimeoutWithinBudget(c.Request.Context(), time.Nanosecond); remainingErr != nil {
						applyKiroBudgetExhaustedRetryAfter(c)
						h.responsesErrorResponse(c, http.StatusServiceUnavailable, "upstream_error", "Kiro upstream failover budget exhausted")
						return
					}
					if !fs.KiroWaitReselectUsed {
						fs.KiroWaitReselectUsed = true
						reqLog.Info("gateway.responses.kiro_account_wait_timeout_reselect", zap.Int64("account_id", account.ID))
						continue
					}
				}
				h.handleConcurrencyError(c, err, "account", streamStarted)
				return
			}
		}
		if account.Platform == service.PlatformKiro && h.gatewayService.KiroResilienceEnforced(apiKey.GroupID) {
			accountReleaseFunc = wrapReleaseOnce(accountReleaseFunc)
		} else {
			accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)
		}

		// 5. Forward request
		writerSizeBeforeForward := c.Writer.Size()
		forwardBody := body
		if channelMapping.Mapped {
			forwardBody = h.gatewayService.ReplaceModelInBody(body, channelMapping.MappedModel)
		}
		forwardCtx := c.Request.Context()
		if imageGenerationIntent {
			forwardCtx = service.WithOpenAIImageGenerationIntent(forwardCtx)
		}
		if fs.SwitchCount > 0 {
			forwardCtx = service.WithAccountSwitchCount(forwardCtx, fs.SwitchCount, h.metadataBridgeEnabled())
		}
		result, err := h.gatewayService.ForwardAsResponses(forwardCtx, c, account, forwardBody, parsedReq)

		releaseAccountSlotAfterForward(accountReleaseFunc, err)

		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				// Can't failover if streaming content already sent
				if c.Writer.Size() != writerSizeBeforeForward {
					h.handleResponsesFailoverExhausted(c, failoverErr, true)
					return
				}
				if service.ShouldPreferDifferentEmailDomainSuffixForFailover(account.Platform, failoverErr) {
					fs.RecordAvoidEmailDomainSuffix(account.EmailDomainSuffix())
				}
				action := fs.HandleFailoverError(c.Request.Context(), h.gatewayService, account.ID, account.Platform, failoverErr)
				switch action {
				case FailoverContinue:
					continue
				case FailoverExhausted:
					h.handleResponsesFailoverExhausted(c, fs.LastFailoverErr, streamStarted)
					return
				case FailoverCanceled:
					return
				}
			}
			upstreamErrorAlreadyCommunicated := gatewayForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err)
			wroteFallback := false
			if !upstreamErrorAlreadyCommunicated {
				wroteFallback = h.ensureForwardErrorResponse(c, streamStarted)
			}
			reqLog.Error("gateway.responses.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Bool("fallback_error_response_written", wroteFallback),
				zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
				zap.Error(err),
			)
			return
		}
		if fs.KiroResilienceEnforced && sessionHash != "" &&
			(selection.DeferStickyMigration || fs.HasFailedAccountID(sessionBoundAccountID) || fs.HasKiro429Retries() ||
				(account.Platform == service.PlatformKiro && (sessionBoundAccountID == 0 || sessionBoundAccountID == account.ID || !selection.PreserveStickyBinding))) {
			stateCtx, stateCancel := gatewayPostForwardStateContext(c.Request.Context())
			if bindErr := h.gatewayService.BindStickySession(stateCtx, apiKey.GroupID, sessionHash, account.ID); bindErr != nil {
				reqLog.Warn("gateway.responses.kiro_bind_sticky_after_success_failed", zap.Int64("account_id", account.ID), zap.Error(bindErr))
			}
			stateCancel()
		}

		// 6. Record usage
		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload(body)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)

		h.submitUsageRecordTask(func(ctx context.Context) {
			if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:             result,
				APIKey:             apiKey,
				User:               apiKey.User,
				Account:            account,
				Subscription:       subscription,
				InboundEndpoint:    inboundEndpoint,
				UpstreamEndpoint:   upstreamEndpoint,
				UserAgent:          userAgent,
				IPAddress:          clientIP,
				RequestPayloadHash: requestPayloadHash,
				APIKeyService:      h.apiKeyService,
				ChannelUsageFields: channelMapping.ToUsageFields(reqModel, result.UpstreamModel),
			}); err != nil {
				reqLog.Error("gateway.responses.record_usage_failed",
					zap.Int64("account_id", account.ID),
					zap.Error(err),
				)
			}
		})
		return
	}
}

// responsesErrorResponse writes an error in OpenAI Responses API format.
func (h *GatewayHandler) responsesErrorResponse(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"code":    code,
			"message": message,
		},
	})
}

// handleResponsesFailoverExhausted writes a failover-exhausted error in Responses format.
func (h *GatewayHandler) handleResponsesFailoverExhausted(c *gin.Context, lastErr *service.UpstreamFailoverError, streamStarted bool) {
	applyFailoverRetryAfter(c, lastErr)
	if streamStarted {
		return // Can't write error after stream started
	}
	status, code, message := h.resolveFailoverExhaustedError(c, lastErr, service.PlatformAnthropic)
	h.responsesErrorResponse(c, status, code, message)
}
