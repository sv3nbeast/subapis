package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
	"go.uber.org/zap"
)

// isGrokCredentialRecoveryCandidate is deliberately narrow: quota and generic
// policy 403s must not rotate a valid refresh token. The two explicit denial
// messages are the account-scoped failures observed from xAI Build/API.
func isGrokCredentialRecoveryCandidate(statusCode int, body []byte) bool {
	if statusCode == http.StatusUnauthorized {
		return true
	}
	if statusCode != http.StatusForbidden {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		extractUpstreamErrorMessage(body),
		string(body),
	)))
	if strings.Contains(message, "access to the chat endpoint is denied") {
		return true
	}
	return strings.Trim(message, " .!\t\r\n\"") == "access denied"
}

// retryGrokAfterCredentialRefresh forces one OAuth refresh and retries the same
// account before failover/quarantine. If refresh itself fails, the original
// response is restored so the normal error classifier remains authoritative.
func (s *OpenAIGatewayService) retryGrokAfterCredentialRefresh(
	ctx context.Context,
	account *Account,
	response *http.Response,
	retry func(accessToken string) (*http.Response, error),
) (*http.Response, bool, error) {
	if response == nil || (response.StatusCode != http.StatusUnauthorized && response.StatusCode != http.StatusForbidden) {
		return response, false, nil
	}
	if s == nil || account == nil || response.Body == nil || retry == nil ||
		s.grokTokenProvider == nil || strings.TrimSpace(account.GetGrokRefreshToken()) == "" {
		return response, false, nil
	}
	body := s.readUpstreamErrorBody(response)
	if !isGrokCredentialRecoveryCandidate(response.StatusCode, body) {
		response.Body = io.NopCloser(bytes.NewReader(body))
		return response, false, nil
	}
	_ = response.Body.Close()

	accessToken, err := s.grokTokenProvider.ForceRefreshAccessToken(ctx, account)
	if err != nil {
		response.Body = io.NopCloser(bytes.NewReader(body))
		logger.FromContext(ctx).Warn("grok.credential_recovery_refresh_failed",
			zap.Int64("account_id", account.ID),
			zap.Int("status_code", response.StatusCode),
			zap.String("error", logredact.RedactText(err.Error())),
		)
		return response, false, nil
	}
	retried, err := retry(accessToken)
	if err != nil {
		return nil, true, err
	}
	if retried == nil {
		return nil, true, fmt.Errorf("Grok credential recovery retry returned no response")
	}
	logger.FromContext(ctx).Info("grok.credential_recovery_retried",
		zap.Int64("account_id", account.ID),
		zap.Int("original_status_code", response.StatusCode),
		zap.Int("retry_status_code", retried.StatusCode),
	)
	return retried, true, nil
}

func (s *OpenAIGatewayService) markGrokUpstreamSuccess(ctx context.Context, account *Account) {
	if s == nil || s.rateLimitService == nil || account == nil || account.Platform != PlatformGrok {
		return
	}
	s.rateLimitService.ResetOpenAI403Counter(ctx, account.ID)
}

// commitGrokUpstreamSuccess persists health and quota signals only after the
// caller has completely consumed a successful upstream response. Keeping this
// off the pre-output path avoids adding Redis/DB latency to Grok TTFT.
func (s *OpenAIGatewayService) commitGrokUpstreamSuccess(ctx context.Context, account *Account, headers http.Header, statusCode int) {
	if s == nil || account == nil || account.Platform != PlatformGrok {
		return
	}
	stateCtx, cancel := openAIAccountStateContext(ctx)
	defer cancel()

	s.markGrokUpstreamSuccess(stateCtx, account)
	s.updateGrokUsageSnapshot(stateCtx, account.ID, xai.ParseQuotaHeaders(headers, statusCode))
}
