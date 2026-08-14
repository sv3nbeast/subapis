package handler

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// tryAnthropicStableIdentityCountTokens keeps every managed stable group off
// the count_tokens upstream path. Claude Code treats the local 404 as a signal
// to use its own estimator. No API-key allow-list is needed: the normal auth
// middleware has already validated this key for the group.
func (h *GatewayHandler) tryAnthropicStableIdentityCountTokens(c *gin.Context, apiKey *service.APIKey) bool {
	if h == nil || h.gatewayService == nil || c == nil || c.Request == nil || apiKey == nil ||
		apiKey.GroupID == nil {
		return false
	}
	if !service.LooksLikeAnthropicStableClaudeCode(c.GetHeader("User-Agent")) {
		return false
	}
	found, err := h.gatewayService.HasAnthropicStableIdentityGroup(c.Request.Context(), *apiKey.GroupID)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code stable identity is temporarily unavailable")
		return true
	}
	if !found {
		return false
	}
	if apiKey.Status != service.StatusAPIKeyActive || apiKey.IsExpired() || apiKey.Group == nil ||
		apiKey.Group.Platform != service.PlatformAnthropic || !apiKey.Group.IsActive() {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code stable identity group is unavailable")
		return true
	}
	if service.DetectAnthropicStableIngressProfile(c.GetHeader("User-Agent"), c.GetHeader("anthropic-beta")) == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "This Claude Code version is not approved for the configured stable identity")
		return true
	}
	h.errorResponse(c, http.StatusNotFound, "not_found_error", "Not found")
	return true
}

// tryAnthropicStableIdentityMessages handles an exact capture-backed Claude
// Code request whenever the authenticated group contains at least one stable
// account. Non-Claude clients continue through the existing scheduler.
func (h *GatewayHandler) tryAnthropicStableIdentityMessages(
	c *gin.Context,
	apiKey *service.APIKey,
	subject middleware.AuthSubject,
	requestStartedAt time.Time,
) bool {
	if h == nil || h.gatewayService == nil || c == nil || c.Request == nil || apiKey == nil || apiKey.GroupID == nil {
		return false
	}
	if !service.LooksLikeAnthropicStableClaudeCode(c.GetHeader("User-Agent")) {
		return false
	}
	found, err := h.gatewayService.HasAnthropicStableIdentityGroup(c.Request.Context(), *apiKey.GroupID)
	if err != nil {
		// A request that looks like the enrolled native client must not silently
		// fall through to mimicry while the route authority is unavailable.
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code stable identity is temporarily unavailable")
		return true
	}
	if !found {
		return false
	}
	if apiKey.Status != service.StatusAPIKeyActive || apiKey.IsExpired() || apiKey.Group == nil ||
		apiKey.Group.Platform != service.PlatformAnthropic || !apiKey.Group.IsActive() {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code stable identity group is unavailable")
		return true
	}
	profileID := service.DetectAnthropicStableIngressProfile(c.GetHeader("User-Agent"), c.GetHeader("anthropic-beta"))
	if profileID == "" {
		// A managed group must never drift to a generic account just because the
		// local Claude Code binary was upgraded before its wire profile was
		// reviewed. Non-Claude clients still fall through unchanged above.
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "This Claude Code version is not approved for the configured stable identity")
		return true
	}
	if requestStartedAt.IsZero() {
		requestStartedAt = time.Now()
	}
	ctx := c.Request.Context()
	limit := int64(service.AnthropicStableIngressMaxBodyBytes)
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
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request is not a supported Claude Code /v1/messages request")
		return true
	}
	// The exact strict path is the only branch allowed to consume the body. A
	// profile match with a malformed body is an explicit client error, not a
	// reason to retry through the compatibility translator.
	setOpsRequestContext(c, ingress.Model, ingress.Stream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(ingress.Stream, false)))
	if decision := h.checkSecurityAudit(
		c, nil, apiKey, subject, service.ContentModerationProtocolAnthropicMessages, ingress.Model, body,
	); decision != nil && !decision.AllowNextStage {
		h.anthropicSecurityAuditError(c, decision)
		return true
	}
	route, resolved, err := h.gatewayService.ResolveAnthropicStableIdentityRoute(
		ctx, *apiKey.GroupID, subject.UserID, ingress.SessionID,
	)
	if err != nil || !resolved || route == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code stable identity is temporarily unavailable")
		return true
	}
	account, err := h.gatewayService.GetAnthropicStableIdentityAccount(ctx, route)
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code stable identity is temporarily unavailable")
		return true
	}
	pricingCtx, pricingAt := service.WithGatewayTokenRequestPricing(ctx)
	c.Request = c.Request.WithContext(pricingCtx)
	ctx = pricingCtx
	if h.billingCacheService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "billing_service_error", "Billing service temporarily unavailable")
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
	subscription, _ := middleware.GetSubscriptionFromContext(c)
	if err := h.billingCacheService.CheckBillingEligibility(ctx, apiKey.User, apiKey, apiKey.Group, subscription, service.PlatformAnthropic); err != nil {
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", fmt.Sprintf("%d", retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return true
	}
	result, forwardErr := h.gatewayService.ForwardAnthropicStableIdentityRaw(ctx, c, account, route, body, apiKey.ID, subject.UserID, requestStartedAt)
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
					APIKeyService:      h.apiKeyService,
					ChannelUsageFields: service.ChannelUsageFields{OriginalModel: ingress.Model, ChannelMappedModel: ingress.Model, BillingModelSource: service.BillingModelSourceRequested},
				}); usageErr != nil {
					logger.L().With(zap.Int64("account_id", account.ID)).Warn("stable_identity.record_usage_failed", zap.Error(usageErr))
				}
			})
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
