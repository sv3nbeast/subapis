package service

// Account-scoped strict forwarding.  This path deliberately reuses the
// capture-backed ingress parser, dedicated transport and raw response copier
// from the stable canary, but obtains its policy from the account route
// directory instead of the process-wide canary environment.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
)

var errAnthropicStableIdentityInvalid = errors.New("anthropic stable identity account is not eligible")

func stableIdentityReject(c *gin.Context, status int, cause error) error {
	if c != nil && !IsResponseCommitted(c) {
		message := "The configured Claude Code stable identity is temporarily unavailable"
		switch {
		case errors.Is(cause, ErrAnthropicStableCanarySessionOwnerConflict):
			message = "This Claude Code session belongs to another user"
		case errors.Is(cause, ErrAnthropicStableCanarySessionBindingUnavailable):
			message = "The Claude Code session is temporarily unavailable"
		case errors.Is(cause, ErrAnthropicStableIngressNotClaudeCode),
			errors.Is(cause, ErrAnthropicStableIngressMalformed),
			errors.Is(cause, ErrAnthropicStableIngressDuplicateKey):
			message = "Request is not a supported Claude Code /v1/messages request"
		}
		c.JSON(status, gin.H{"type": "error", "error": gin.H{
			"type": "invalid_request_error", "message": message,
		}})
		MarkResponseCommitted(c)
	}
	return fmt.Errorf("anthropic stable identity rejected request: %w", cause)
}

func (s *GatewayService) blockAnthropicStableIdentity(ctx context.Context, accountID int64, reason string) {
	if s == nil || accountID <= 0 {
		return
	}
	if s.anthropicStableCanary != nil {
		s.anthropicStableCanary.block(accountID, reason)
	}
	if s.accountRepo == nil {
		return
	}
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	persistCtx, cancel := context.WithTimeout(base, anthropicStableCanaryDurableBlockTimeout)
	defer cancel()
	persistCtx = withAnthropicStableIdentityMutationAuthorization(persistCtx, accountID)
	if err := s.accountRepo.UpdateExtra(persistCtx, accountID, map[string]any{
		AnthropicStableIdentityBlockedExtraKey:       true,
		AnthropicStableIdentityBlockedReasonExtraKey: NormalizeAnthropicStableCanaryBlockReason(reason),
		AnthropicStableIdentityUpdatedAtExtraKey:     stableIdentityNow(),
	}); err != nil {
		logger.LegacyPrintf("service.gateway", "[Anthropic Stable Identity] durable block failed account=%d err=%v", accountID, err)
	}
	s.InvalidateAnthropicStableIdentityRoutes()
}

func claimAnthropicStableIdentitySession(ctx context.Context, repo AnthropicStableCanarySessionBindingRepository, route *AnthropicStableIdentityRoute, ownerUserID int64, sessionID string) error {
	if route == nil || repo == nil || ownerUserID <= 0 || route.GroupID <= 0 || route.SessionScopeID <= 0 || route.AccountID <= 0 || route.Generation <= 0 {
		return ErrAnthropicStableCanarySessionBindingUnavailable
	}
	sessionHash, err := HashAnthropicStableCanarySessionForRouting(route.SessionHMACKey, route.SessionScopeID, route.Generation, sessionID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAnthropicStableCanarySessionBindingUnavailable, err)
	}
	return repo.ClaimAnthropicStableCanarySession(ctx, route.SessionScopeID, route.AccountID, route.Generation, ownerUserID, sessionHash, route.KeyFingerprint, route.PolicyFingerprint)
}

// ForwardAnthropicStableIdentityRaw forwards one native Claude Code request.
// It is called only after the route directory has authenticated the API key;
// non-Claude requests never enter this function and continue through the
// existing OAuth mimicry path.
func (s *GatewayService) ForwardAnthropicStableIdentityRaw(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	route *AnthropicStableIdentityRoute,
	rawBody []byte,
	apiKeyID int64,
	ownerUserID int64,
	startTime time.Time,
) (*ForwardResult, error) {
	if s == nil || c == nil || c.Request == nil || c.Writer == nil || route == nil || account == nil {
		return nil, errors.New("stable identity requires an HTTP handler context")
	}
	if startTime.IsZero() {
		startTime = time.Now()
	}
	if route.AccountID != account.ID || route.GroupID <= 0 || !route.AllowsAPIKey(apiKeyID) {
		// A missing API-key id fails closed; raw key material is never passed.
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errAnthropicStableIdentityInvalid)
	}
	if route.IsPaused() || account.IsAnthropicStableIdentityPaused() || account.IsAnthropicStableIdentityBlocked() {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errAnthropicStableIdentityInvalid)
	}
	if int64(len(rawBody)) > route.MaxBodyBytes {
		return nil, stableIdentityReject(c, http.StatusRequestEntityTooLarge, ErrAnthropicStableIngressMalformed)
	}
	if err := ValidateAnthropicStableIdentityEnrolledAccount(account); err != nil {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, err)
	}
	if !accountBelongsToGroup(account, route.GroupID) {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errAnthropicStableIdentityInvalid)
	}
	if account.AnthropicStableIdentityGeneration() != route.Generation ||
		account.AnthropicStableIdentityDeviceID() != route.DeviceID ||
		!AnthropicStableIngressProfilesEquivalent(account.AnthropicStableIdentityProfileID(), route.ProfileID) {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errAnthropicStableIdentityInvalid)
	}
	header := stableCanaryIngressHeaders(c)
	ingress, err := parseAnthropicStableCanaryIngress(c, rawBody)
	if err != nil {
		return nil, stableIdentityReject(c, http.StatusBadRequest, err)
	}
	if err := s.validateAnthropicStableCanaryModelAccess(ctx, route.GroupID, ingress.Model); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, errAnthropicStableCanaryModelRestricted) {
			status = http.StatusBadRequest
		}
		return nil, stableIdentityReject(c, status, err)
	}
	if reason := s.anthropicStableCanary.blockReason(account.ID); reason != "" {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errAnthropicStableIdentityInvalid)
	}
	releaseSlot, err := s.anthropicStableCanary.acquire(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	defer releaseSlot()
	leaseRepo, ok := s.accountRepo.(AnthropicStableCanaryLeaseRepository)
	if !ok {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errors.New("stable identity lease is unavailable"))
	}
	releaseLease, err := leaseRepo.AcquireAnthropicStableCanaryLease(ctx, account.ID)
	if err != nil || releaseLease == nil {
		if err == nil {
			err = errors.New("stable identity lease is incomplete")
		}
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, err)
	}
	defer func() {
		if releaseErr := releaseLease(); releaseErr != nil {
			logger.LegacyPrintf("service.gateway", "[Anthropic Stable Identity] release lease failed account=%d err=%v", account.ID, releaseErr)
		}
	}()
	if s.accountRepo == nil {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errAnthropicStableIdentityInvalid)
	}
	// Reload under the account lease so an admin pause/rotation or a preceding
	// 401 refresh cannot race a stale credential/device snapshot.
	freshAccount, loadErr := s.accountRepo.GetByID(ctx, account.ID)
	if loadErr != nil || freshAccount == nil {
		if loadErr == nil {
			loadErr = errAnthropicStableIdentityInvalid
		}
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, loadErr)
	}
	if err := ValidateAnthropicStableIdentityEnrolledAccount(freshAccount); err != nil ||
		!freshAccount.IsAnthropicStableIdentityEnabled() || freshAccount.IsAnthropicStableIdentityPaused() ||
		freshAccount.IsAnthropicStableIdentityBlocked() || !accountBelongsToGroup(freshAccount, route.GroupID) ||
		!containsStableIdentityID(freshAccount.AnthropicStableIdentityAPIKeyIDs(), apiKeyID) ||
		freshAccount.AnthropicStableIdentityAPIKeyGroupIDs()[apiKeyID] != route.GroupID ||
		freshAccount.AnthropicStableIdentityGeneration() != route.Generation ||
		freshAccount.AnthropicStableIdentityDeviceID() != route.DeviceID {
		if err == nil {
			err = errAnthropicStableIdentityInvalid
		}
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, err)
	}
	if s.groupRepo == nil {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errAnthropicStableIdentityInvalid)
	}
	freshGroup, groupErr := s.groupRepo.GetByID(ctx, route.GroupID)
	if groupErr != nil || freshGroup == nil || !freshGroup.IsActive() || freshGroup.Platform != PlatformAnthropic {
		if groupErr == nil {
			groupErr = errAnthropicStableIdentityInvalid
		}
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, groupErr)
	}
	account = freshAccount
	if !AnthropicStableIngressProfilesEquivalent(ingress.ProfileID, account.AnthropicStableIdentityProfileID()) {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errAnthropicStableIdentityInvalid)
	}
	upstreamBody, err := ingress.PatchDevice(account.AnthropicStableIdentityDeviceID())
	if err != nil {
		return nil, stableIdentityReject(c, http.StatusBadRequest, err)
	}
	claimCtx, claimCancel := context.WithTimeout(ctx, anthropicStableCanarySessionClaimTimeout)
	defer claimCancel()
	if repo, ok := s.accountRepo.(AnthropicStableCanarySessionBindingRepository); !ok {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, ErrAnthropicStableCanarySessionBindingUnavailable)
	} else if err := claimAnthropicStableIdentitySession(claimCtx, repo, route, ownerUserID, ingress.SessionID); err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, ErrAnthropicStableCanarySessionOwnerConflict) {
			status = http.StatusConflict
		}
		return nil, stableIdentityReject(c, status, err)
	}
	setOpsAnthropicRequestShape(c, ingress.Stream, ingress.Stream, "stable_identity", "")
	c.Set("anthropic_passthrough_mode", "stable_identity")
	c.Set("anthropic_passthrough_fallback", "")
	c.Set("anthropic_stable_session_generation", route.Generation)
	loggerFromStableCanary(c, account, ingress)

	token := account.GetCredential("access_token")
	if err := validateAnthropicStableOAuthAccessToken(token); err != nil {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, err)
	}
	client, err := s.anthropicStableCanaryHTTPClient(account)
	if err != nil {
		return nil, stableIdentityReject(c, http.StatusServiceUnavailable, err)
	}
	var resp *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		request, buildErr := BuildAnthropicStableMessageRequest(ctx, AnthropicStableMessagesOriginV1, header, upstreamBody, token)
		if buildErr != nil {
			return nil, buildErr
		}
		resp, err = roundTripAnthropicStableCanary(client, request)
		if err != nil {
			return nil, fmt.Errorf("stable identity upstream request failed: %w", err)
		}
		if resp == nil {
			return nil, errors.New("stable identity upstream returned an empty response")
		}
		if resp.StatusCode != http.StatusUnauthorized || attempt != 0 {
			break
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		refreshCtx := withAnthropicStableCanaryRefreshAuthorization(ctx, account.ID)
		refreshed, refreshErr := s.refreshAnthropicStableCanaryToken(refreshCtx, account, client)
		if refreshErr != nil {
			failureClass := anthropicStableCanaryRefreshFailureClassOf(refreshErr)
			if failureClass != anthropicStableCanaryRefreshFailureTransient {
				s.blockAnthropicStableIdentity(ctx, account.ID, anthropicStableCanaryBlockReasonRefreshFailed)
			}
			if cause := context.Cause(ctx); cause != nil {
				return nil, cause
			}
			if failureClass == anthropicStableCanaryRefreshFailureTransient {
				return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errors.New("stable identity token refresh is temporarily unavailable"))
			}
			return nil, stableIdentityReject(c, http.StatusServiceUnavailable, errors.New("stable identity credential requires recovery"))
		}
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		token = refreshed
	}
	if resp == nil || resp.Body == nil {
		return nil, errors.New("stable identity upstream returned an empty response")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		s.blockAnthropicStableIdentity(ctx, account.ID, anthropicStableCanaryBlockReasonCredentialRejected)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, s.writeAnthropicStableUpstreamError(
			c, resp, account, ingress.Model, "anthropic_stable_identity_http_error", "stable identity",
		)
	}
	writeAnthropicPassthroughResponseHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	downstream := &stableCanaryResponseWriter{ctx: c, status: resp.StatusCode}
	result := &ForwardResult{RequestID: resp.Header.Get("x-request-id"), Model: ingress.Model, Stream: ingress.Stream, UpstreamModel: ingress.Model, Duration: time.Since(startTime)}
	observer := NewAnthropicStableSSEObserver(time.Now)
	flushed := false
	flush := func() error {
		if !downstream.committed {
			return nil
		}
		if flusher, ok := c.Writer.(http.Flusher); ok {
			flusher.Flush()
			flushed = true
		}
		return nil
	}
	metrics, copyErr := CopyAnthropicStableResponse(ctx, downstream, resp.Body, ingress.Stream, flush, observer)
	if !flushed {
		metrics.FirstDownstreamFlushAt = time.Time{}
	}
	result.Usage = stableCanaryUsage(metrics)
	result.RequestID = firstNonEmptyStableCanary(result.RequestID, metrics.UpstreamRequestID)
	result.ResponseID = metrics.UpstreamRequestID
	result.FirstTokenMs = stableCanaryFirstSemanticOutput(startTime, metrics)
	result.Duration = time.Since(startTime)
	if copyErr != nil {
		result.ClientDisconnect = context.Cause(ctx) != nil || metrics.DownstreamError
		if metrics.ErrorEventSeen {
			MarkGatewaySSEErrorWritten(c)
		}
		return result, copyErr
	}
	downstream.CommitEmpty()
	return result, nil
}
