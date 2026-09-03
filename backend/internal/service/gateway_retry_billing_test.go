package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type gatewayRetryCacheStub struct {
	markerFingerprint string
	markerLogicalID   string
}

func (s *gatewayRetryCacheStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, ErrStickySessionNotFound
}
func (s *gatewayRetryCacheStub) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}
func (s *gatewayRetryCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}
func (s *gatewayRetryCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}
func (s *gatewayRetryCacheStub) SetReasoningContent(context.Context, string, string, time.Duration) error {
	return nil
}
func (s *gatewayRetryCacheStub) GetReasoningContent(context.Context, string) (string, error) {
	return "", ErrReasoningContentNotFound
}
func (s *gatewayRetryCacheStub) MarkPreSemanticFailure(_ context.Context, fingerprint, logicalRequestID string, _ time.Duration) error {
	s.markerFingerprint = fingerprint
	s.markerLogicalID = logicalRequestID
	return nil
}
func (s *gatewayRetryCacheStub) GetPreSemanticFailure(_ context.Context, fingerprint string) (string, bool, error) {
	if s.markerFingerprint != fingerprint || s.markerLogicalID == "" {
		return "", false, nil
	}
	logicalID := s.markerLogicalID
	return logicalID, true, nil
}
func (s *gatewayRetryCacheStub) ClearPreSemanticFailure(_ context.Context, fingerprint string) error {
	if s.markerFingerprint == fingerprint {
		s.markerFingerprint = ""
		s.markerLogicalID = ""
	}
	return nil
}

func TestBuildGatewayRetryFingerprintIsStableAndTenantScoped(t *testing.T) {
	groupID := int64(29)
	body := []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"hello"}]}`)

	fingerprintA, logicalIDA := BuildGatewayRetryFingerprint(742, 1188, &groupID, PlatformAnthropic, "claude-opus-5", "session-1", body)
	fingerprintB, logicalIDB := BuildGatewayRetryFingerprint(742, 1188, &groupID, PlatformAnthropic, "claude-opus-5", "session-1", body)
	fingerprintOtherUser, _ := BuildGatewayRetryFingerprint(743, 1188, &groupID, PlatformAnthropic, "claude-opus-5", "session-1", body)
	fingerprintOtherBody, _ := BuildGatewayRetryFingerprint(742, 1188, &groupID, PlatformAnthropic, "claude-opus-5", "session-1", []byte(`{"model":"claude-opus-5","messages":[{"role":"user","content":"different"}]}`))

	require.NotEmpty(t, fingerprintA)
	require.Equal(t, fingerprintA, fingerprintB)
	require.Equal(t, logicalIDA, logicalIDB)
	require.Equal(t, "gateway-retry:v1:"+fingerprintA, logicalIDA)
	require.True(t, IsGatewayRetryLogicalRequestID(logicalIDA))
	require.False(t, IsGatewayRetryLogicalRequestID("client:ordinary-request"))
	require.NotEqual(t, fingerprintA, fingerprintOtherUser)
	require.NotEqual(t, fingerprintA, fingerprintOtherBody)
}

func TestIsGatewayPreSemanticRetryableFailure(t *testing.T) {
	require.True(t, IsGatewayPreSemanticRetryableFailure(&UpstreamFailoverError{
		StatusCode:  504,
		FailureKind: UpstreamFailureFirstSemanticTimeout,
	}))
	require.True(t, IsGatewayPreSemanticRetryableFailure(&UpstreamFailoverError{
		StatusCode: http.StatusTooManyRequests,
	}))
	require.False(t, IsGatewayPreSemanticRetryableFailure(&UpstreamFailoverError{
		StatusCode:         503,
		FailureKind:        UpstreamFailureIncompleteStream,
		FailoverProhibited: true,
	}))
	require.False(t, IsGatewayPreSemanticRetryableFailure(&UpstreamFailoverError{
		StatusCode:  503,
		FailureKind: UpstreamFailureIncompleteStream,
		Reason:      GatewayFailureReasonKiroContentProcessingFailed,
	}))
	require.False(t, IsGatewayPreSemanticRetryableFailure(&UpstreamFailoverError{StatusCode: 400}))
	require.True(t, IsGatewayPreSemanticNetworkError(io.ErrUnexpectedEOF))
	require.False(t, IsGatewayPreSemanticNetworkError(context.Canceled))
	require.False(t, IsGatewayPreSemanticNetworkError(errors.New("invalid request body")))
}

func TestGatewayServiceRetryMarkerDelegatesToSharedLedger(t *testing.T) {
	cache := &gatewayRetryCacheStub{}
	cfg := &config.Config{}
	svc := NewGatewayService(
		nil, nil, nil, nil, nil, nil, nil, cache, cfg, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)

	fingerprint, logicalID := BuildGatewayRetryFingerprint(742, 1188, nil, PlatformAnthropic, "claude-opus-5", "session-1", []byte("body"))
	require.NoError(t, svc.MarkGatewayPreSemanticFailure(context.Background(), fingerprint, logicalID))
	got, ok, err := svc.GetGatewayPreSemanticRetry(context.Background(), fingerprint)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, logicalID, got)
	got, ok, err = svc.GetGatewayPreSemanticRetry(context.Background(), fingerprint)
	require.NoError(t, err)
	require.True(t, ok, "concurrent/repeated retries must share the marker until settlement")
	require.Equal(t, logicalID, got)
	require.NoError(t, svc.ClearGatewayPreSemanticRetry(context.Background(), fingerprint))
	_, ok, err = svc.GetGatewayPreSemanticRetry(context.Background(), fingerprint)
	require.NoError(t, err)
	require.False(t, ok, "a settled retry marker must be cleared")
}

func TestGatewayServiceRecordUsageUsesLogicalRetryRequestID(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.0
	svc := NewGatewayService(
		nil, nil, usageRepo, billingRepo,
		&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil, nil,
		cfg, nil, nil, NewBillingService(cfg, nil), nil, &BillingCacheService{}, nil,
		nil, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)

	logicalID := "gateway-retry:v1:logical-once"
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "client:new-request-id",
			Model:     "claude-sonnet-4",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 5,
			},
			Duration: time.Second,
		},
		LogicalRequestID:   logicalID,
		RequestPayloadHash: HashUsageRequestPayload([]byte("same-body")),
		APIKey:             &APIKey{ID: 1188, Quota: 100},
		User:               &User{ID: 742},
		Account:            &Account{ID: 2553},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, logicalID, usageRepo.lastLog.RequestID)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, logicalID, billingRepo.lastCmd.RequestID)
	require.Equal(t, BuildGatewayRetryBillingFingerprint(logicalID), billingRepo.lastCmd.RequestFingerprint)
}

func TestGatewayServiceRecordUsageSuppressesDuplicateLogicalRetryUsageRow(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.0
	svc := NewGatewayService(
		nil, nil, usageRepo, billingRepo,
		&openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil, nil,
		cfg, nil, nil, NewBillingService(cfg, nil), nil, &BillingCacheService{}, nil,
		nil, &DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "client:retry-duplicate",
			Model:     "claude-sonnet-4",
			Usage:     ClaudeUsage{InputTokens: 10, OutputTokens: 5, CacheCreationInputTokens: 1000},
			Duration:  time.Second,
		},
		LogicalRequestID:   gatewayRetryLogicalRequestPrefix + "duplicate",
		RequestPayloadHash: HashUsageRequestPayload([]byte("same-body")),
		APIKey:             &APIKey{ID: 1188, Quota: 100},
		User:               &User{ID: 742},
		Account:            &Account{ID: 2553},
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 0, usageRepo.calls, "a duplicate logical retry must not create a second usage row")
}

func TestGatewayRetryBillingFingerprintIgnoresPhysicalAccount(t *testing.T) {
	logicalID := gatewayRetryLogicalRequestPrefix + "cross-account"
	first := &UsageBillingCommand{
		RequestID:          logicalID,
		APIKeyID:           1188,
		UserID:             742,
		AccountID:          2553,
		Model:              "claude-sonnet-4",
		RequestPayloadHash: HashUsageRequestPayload([]byte("same-body")),
		BalanceCost:        0.49,
	}
	first.Normalize()
	second := &UsageBillingCommand{
		RequestID:          logicalID,
		APIKeyID:           1188,
		UserID:             742,
		AccountID:          2572,
		Model:              "claude-sonnet-4",
		RequestPayloadHash: HashUsageRequestPayload([]byte("same-body")),
		BalanceCost:        0.47,
	}
	second.Normalize()

	require.Equal(t, BuildGatewayRetryBillingFingerprint(logicalID), first.RequestFingerprint)
	require.Equal(t, first.RequestFingerprint, second.RequestFingerprint)
}
