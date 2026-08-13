package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func readAnthropicStableRawBody(c *gin.Context, limit int64) ([]byte, error) {
	if c == nil || c.Request == nil || c.Request.Body == nil || limit <= 0 {
		return nil, errors.New("stable raw body reader is not configured")
	}
	if encoding := strings.TrimSpace(c.GetHeader("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return nil, fmt.Errorf("unsupported content encoding")
	}
	if c.Request.ContentLength > limit {
		return nil, &http.MaxBytesError{Limit: limit}
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, &http.MaxBytesError{Limit: limit}
	}
	return body, nil
}

func (h *GatewayHandler) tryAnthropicStableCanaryMessages(c *gin.Context, apiKey *service.APIKey, subject middleware.AuthSubject, requestStartedAt time.Time) bool {
	if h == nil || h.gatewayService == nil || apiKey == nil || apiKey.GroupID == nil ||
		!h.gatewayService.IsAnthropicStableCanaryGroup(apiKey.GroupID) {
		return false
	}
	if requestStartedAt.IsZero() {
		requestStartedAt = time.Now()
	}
	ctx := c.Request.Context()
	if !h.gatewayService.AnthropicStableCanaryOwnerAllowed(subject.UserID) ||
		!h.gatewayService.AnthropicStableCanaryAPIKeyAllowed(apiKey.ID) {
		h.errorResponse(c, http.StatusForbidden, "permission_error", "This Claude Code canary is restricted to its registered user")
		return true
	}
	if apiKey.Group == nil || apiKey.Group.Platform != service.PlatformAnthropic {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code canary is temporarily unavailable")
		return true
	}
	pricingCtx, pricingAt := service.WithGatewayTokenRequestPricing(ctx)
	c.Request = c.Request.WithContext(pricingCtx)
	ctx = pricingCtx
	if h.billingCacheService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "billing_service_error", "Billing service temporarily unavailable")
		return true
	}
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	limit := h.gatewayService.AnthropicStableCanaryMaxBodyBytes()
	body, err := readAnthropicStableRawBody(c, limit)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
		} else {
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		}
		return true
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return true
	}
	ingress, err := service.InspectAnthropicStableCanaryIngress(c, body)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, service.ErrAnthropicStableIngressMalformed) && len(body) > int(limit) {
			status = http.StatusRequestEntityTooLarge
		}
		h.errorResponse(c, status, "invalid_request_error", "Request is not a supported Claude Code /v1/messages request")
		return true
	}
	setOpsRequestContext(c, ingress.Model, ingress.Stream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(ingress.Stream, false)))
	if decision := h.checkSecurityAudit(
		c, nil, apiKey, subject, service.ContentModerationProtocolAnthropicMessages, ingress.Model, body,
	); decision != nil && !decision.AllowNextStage {
		h.anthropicSecurityAuditError(c, decision)
		return true
	}
	account, err := h.gatewayService.GetAnthropicStableCanaryAccount(ctx, *apiKey.GroupID)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code canary is temporarily unavailable")
		return true
	}
	var userRelease func()
	if h.concurrencyHelper != nil {
		userRelease, err = h.acquireAnthropicStableCanaryUserSlot(c, subject.UserID, subject.Concurrency)
		if err != nil {
			h.handleConcurrencyError(c, err, "user", false)
			return true
		}
		if release := wrapReleaseOnDone(ctx, userRelease); release != nil {
			defer release()
		}
	}
	// Eligibility is intentionally checked once, after the potentially long
	// user/API-key queue wait. CheckBillingEligibility also consumes RPM, so a
	// pre-wait check plus a recheck would double-count one physical request.
	if err := h.billingCacheService.CheckBillingEligibility(ctx, apiKey.User, apiKey, apiKey.Group, subscription, service.PlatformAnthropic); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return true
	}
	result, forwardErr := h.gatewayService.ForwardAnthropicStableCanaryRaw(ctx, c, account, body, requestStartedAt)
	service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, time.Since(requestStartedAt).Milliseconds())
	if result != nil {
		if result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
		}
		if shouldRecordAnthropicStableCanaryUsage(result, forwardErr) {
			requestHash := service.HashUsageRequestPayload(body)
			userAgent := c.GetHeader("User-Agent")
			clientIP := ip.GetClientIP(c)
			quotaPlatform := service.QuotaPlatform(ctx, apiKey)
			sessionID := service.HashAnthropicStableCanarySessionID(ingress.SessionID)
			inboundEndpoint := GetInboundEndpoint(c)
			upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)
			h.submitMandatoryUsageRecordTask(ctx, func(recordCtx context.Context) {
				if usageErr := h.gatewayService.RecordUsage(recordCtx, &service.RecordUsageInput{
					Result: result, APIKey: apiKey, User: apiKey.User, Account: account,
					Subscription: subscription, PricingAt: pricingAt,
					InboundEndpoint: inboundEndpoint, UpstreamEndpoint: upstreamEndpoint,
					UserAgent: userAgent, IPAddress: clientIP, SessionID: sessionID,
					RequestPayloadHash: requestHash, QuotaPlatform: quotaPlatform,
					APIKeyService: h.apiKeyService, ChannelUsageFields: service.ChannelUsageFields{OriginalModel: ingress.Model, ChannelMappedModel: ingress.Model, BillingModelSource: service.BillingModelSourceRequested},
				}); usageErr != nil {
					logger.L().With(zap.Int64("account_id", account.ID)).Warn("stable_canary.record_usage_failed", zap.Error(usageErr))
				}
			})
		} else {
			logger.L().With(zap.Int64("account_id", account.ID)).Warn("stable_canary.usage_skipped_unproven")
		}
	}
	if forwardErr != nil && ((result != nil && result.ClientDisconnect) || context.Cause(ctx) != nil) {
		markClientClosedRequest(c)
		return true
	}
	if forwardErr != nil && !service.IsResponseCommitted(c) {
		h.errorResponse(c, http.StatusBadGateway, "upstream_error", "The upstream Claude response was interrupted")
	}
	return true
}

// acquireAnthropicStableCanaryUserSlot retains ordinary user/API-key limits but
// never emits a gateway-generated wait ping. Any byte written before Anthropic's
// response would violate the raw-response contract and could prevent a later
// upstream status from reaching the client.
func (h *GatewayHandler) acquireAnthropicStableCanaryUserSlot(c *gin.Context, userID int64, maxConcurrency int) (func(), error) {
	if h == nil || h.concurrencyHelper == nil {
		return nil, nil
	}
	silent := NewConcurrencyHelper(h.concurrencyHelper.concurrencyService, SSEPingFormatNone, 0)
	streamStarted := false
	return silent.AcquireUserSlotWithWait(c, userID, maxConcurrency, false, &streamStarted)
}

func shouldRecordAnthropicStableCanaryUsage(result *service.ForwardResult, forwardErr error) bool {
	if result == nil {
		return false
	}
	if forwardErr == nil || result.FirstTokenMs != nil {
		return true
	}
	usage := result.Usage
	return usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0
}
