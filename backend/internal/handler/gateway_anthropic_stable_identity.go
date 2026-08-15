package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
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

func restoreAnthropicStableRawBody(c *gin.Context, body []byte) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
}

// parseAnthropicStableIdentityProbeRequest extracts only the fields used by
// SetClaudeCodeClientContext's Electron connection-probe exception. It never
// becomes the forwarded body and deliberately ignores all other fields.
func parseAnthropicStableIdentityProbeRequest(body []byte) *service.ParsedRequest {
	if len(body) == 0 {
		return nil
	}
	var shape struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Stream    bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &shape); err != nil {
		return nil
	}
	return &service.ParsedRequest{Model: shape.Model, MaxTokens: shape.MaxTokens, Stream: shape.Stream}
}

func stableIdentityJSONContentType(c *gin.Context) bool {
	if c == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(c.GetHeader("Content-Type")))
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

// enforceAnthropicStableIdentityGroupRestriction applies the same effective
// group/fallback resolution as ordinary scheduler requests. It returns
// (useStable, handledResponse). A false useStable with no handled response
// means the caller must restore the body and continue through compatibility.
func (h *GatewayHandler) enforceAnthropicStableIdentityGroupRestriction(
	c *gin.Context,
	apiKey *service.APIKey,
	body []byte,
	parsedReq *service.ParsedRequest,
) (bool, bool) {
	if h == nil || h.gatewayService == nil || c == nil || c.Request == nil || apiKey == nil || apiKey.GroupID == nil {
		return false, true
	}
	SetClaudeCodeClientContext(c, body, parsedReq)
	_, effectiveGroupID, err := h.gatewayService.ResolveClaudeCodeRestriction(c.Request.Context(), apiKey.GroupID)
	if err != nil {
		if errors.Is(err, service.ErrClaudeCodeOnly) {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
			h.errorResponse(c, http.StatusForbidden, "permission_error", "This group only allows Claude Code clients")
			return false, true
		}
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "The configured Claude Code stable identity group is temporarily unavailable")
		return false, true
	}
	if effectiveGroupID == nil || *effectiveGroupID != *apiKey.GroupID {
		return false, false
	}
	return true, false
}

// tryAnthropicStableIdentityCountTokens keeps every managed stable group off
// the count_tokens upstream path. Claude Code treats the local 404 as a signal
// to use its own estimator. The decision is made from the authenticated group;
// it is not an additional Claude CLI version/profile gate.
func (h *GatewayHandler) tryAnthropicStableIdentityCountTokens(c *gin.Context, apiKey *service.APIKey) bool {
	if h == nil || h.gatewayService == nil || c == nil || c.Request == nil || apiKey == nil ||
		apiKey.GroupID == nil {
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
	if encoding := strings.TrimSpace(c.GetHeader("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		// Leave compressed/encoded traffic to the ordinary parser rather than
		// turning the stable route into a new client-specific rejection point.
		return false
	}
	// Preserve the generic scheduler's ClaudeCodeOnly/fallback semantics even
	// though this endpoint is answered locally. Most Claude CLI count_tokens
	// requests need no body read: the shared classifier recognizes the endpoint
	// from its UA. Desktop's connection probe is the one exception because its
	// Electron UA is only trusted for max_tokens=1, non-stream requests.
	var probeBody []byte
	var probeReq *service.ParsedRequest
	probeBodyRead := false
	if service.IsClaudeCodeDesktopProbeUserAgent(c.GetHeader("User-Agent")) {
		probeBody, err = readAnthropicStableRawBody(c, service.AnthropicStableIngressMaxBodyBytes)
		if err != nil {
			if maxErr, ok := extractMaxBytesError(err); ok {
				h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			} else {
				h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
			}
			return true
		}
		probeBodyRead = true
		restoreAnthropicStableRawBody(c, probeBody)
		probeReq = parseAnthropicStableIdentityProbeRequest(probeBody)
	}
	if useStable, handled := h.enforceAnthropicStableIdentityGroupRestriction(c, apiKey, probeBody, probeReq); handled {
		return true
	} else if !useStable {
		if probeBodyRead {
			restoreAnthropicStableRawBody(c, probeBody)
		}
		return false
	}
	h.errorResponse(c, http.StatusNotFound, "not_found_error", "Not found")
	return true
}

// tryAnthropicStableIdentityMessages handles a request whenever the
// authenticated group contains at least one stable account and the request has
// the identity fields needed for safe session routing. Client UA/version/query
// values are not an admission allow-list; group ClaudeCodeOnly remains the
// single configurable client restriction.
func (h *GatewayHandler) tryAnthropicStableIdentityMessages(
	c *gin.Context,
	apiKey *service.APIKey,
	subject middleware.AuthSubject,
	requestStartedAt time.Time,
) bool {
	if h == nil || h.gatewayService == nil || c == nil || c.Request == nil || apiKey == nil || apiKey.GroupID == nil {
		return false
	}
	found, err := h.gatewayService.HasAnthropicStableIdentityGroup(c.Request.Context(), *apiKey.GroupID)
	if err != nil {
		// Do not silently use a generic account while the stable route authority
		// is unavailable for a group that explicitly enrolled stable accounts.
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
	if encoding := strings.TrimSpace(c.GetHeader("Content-Encoding")); encoding != "" && !strings.EqualFold(encoding, "identity") {
		// Leave compressed/encoded traffic to the ordinary parser rather than
		// turning the stable route into a new client-specific rejection point.
		return false
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
		restoreAnthropicStableRawBody(c, body)
		return false
	}
	if !stableIdentityJSONContentType(c) {
		restoreAnthropicStableRawBody(c, body)
		return false
	}
	ingress, err := service.InspectAnthropicStableIdentityIngress(c, body)
	if err != nil {
		if !service.HasAnthropicStableIdentityEnvelope(body) {
			// A client that does not carry the stable session/device envelope
			// belongs to the existing compatibility path. Restore the body before
			// returning so ordinary SDK/Desktop traffic keeps its old route.
			restoreAnthropicStableRawBody(c, body)
			return false
		}
		// Once a request advertises the stable envelope, malformed/duplicate
		// identity data fails closed instead of being normalized by the generic
		// parser and silently changing session/account semantics.
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request is not a valid Anthropic /v1/messages request")
		return true
	}
	// Classify the request exactly as the generic handler does before applying
	// the group restriction. For Electron probes only, the minimal parsed shape
	// preserves the existing max_tokens=1/non-stream exception without making
	// Desktop UA a stable-route allow-list.
	var classifierReq *service.ParsedRequest
	if service.IsClaudeCodeDesktopProbeUserAgent(c.GetHeader("User-Agent")) {
		classifierReq = &service.ParsedRequest{Model: ingress.Model, Stream: ingress.Stream, MaxTokens: int(ingress.MaxTokens)}
	}
	// Match the generic handler's probe context before classification. Without
	// this, a valid Claude Code connection probe (max_tokens=1, non-stream)
	// would be mistaken for a non-Claude client solely because it omits the
	// normal system prompt.
	if isClaudeCodeConnectionProbeRequest(int(ingress.MaxTokens), ingress.Stream) {
		probeCtx := service.WithIsClaudeCodeConnectionProbeRequest(c.Request.Context(), true, h.metadataBridgeEnabled())
		c.Request = c.Request.WithContext(probeCtx)
	}
	if isMaxTokensOneHaikuRequest(ingress.Model, int(ingress.MaxTokens)) {
		probeCtx := service.WithIsMaxTokensOneHaikuRequest(c.Request.Context(), true, h.metadataBridgeEnabled())
		c.Request = c.Request.WithContext(probeCtx)
	}
	if useStable, handled := h.enforceAnthropicStableIdentityGroupRestriction(c, apiKey, body, classifierReq); handled {
		return true
	} else if !useStable {
		restoreAnthropicStableRawBody(c, body)
		return false
	}
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
