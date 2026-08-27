package handler

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newKiroMetadataOnlyEOFFailover() *service.UpstreamFailoverError {
	return &service.UpstreamFailoverError{
		StatusCode:             http.StatusServiceUnavailable,
		FailureKind:            service.UpstreamFailureIncompleteStream,
		Reason:                 service.GatewayFailureReasonKiroMetadataOnlyEOF,
		Scope:                  service.GatewayFailureScopeRequest,
		RequestScopedTransient: true,
		RetryableOnSameAccount: false,
		SuppressTempUnschedule: true,
	}
}

func TestHandleFailoverErrorKiroMetadataOnlyEOFProbesAtMostTwoAccounts(t *testing.T) {
	mock := &mockTempUnscheduler{}
	fs := NewFailoverState(10, true)

	first := newKiroMetadataOnlyEOFFailover()
	action := fs.HandleFailoverError(context.Background(), mock, 2596, service.PlatformKiro, first)
	require.Equal(t, FailoverContinue, action)
	require.Contains(t, fs.FailedAccountIDs, int64(2596))
	require.Equal(t, 1, fs.SwitchCount)
	require.Len(t, fs.KiroMetadataOnlyEOFAccounts, 1)
	require.Zero(t, fs.SameAccountRetryCount[2596])
	require.Empty(t, mock.calls, "request-shaped metadata EOF must not cool a healthy account")

	second := newKiroMetadataOnlyEOFFailover()
	action = fs.HandleFailoverError(context.Background(), mock, 2597, service.PlatformKiro, second)
	require.Equal(t, FailoverExhausted, action)
	require.Contains(t, fs.FailedAccountIDs, int64(2597))
	require.Equal(t, 1, fs.SwitchCount, "the second deterministic rejection must not scan a third account")
	require.Len(t, fs.KiroMetadataOnlyEOFAccounts, 2)
	require.Empty(t, mock.calls)
}

func TestHandleSelectionExhaustedDoesNotReplayMetadataOnlyEOFOnSoleAccount(t *testing.T) {
	mock := &mockTempUnscheduler{}
	fs := NewFailoverState(10, false)
	err := newKiroMetadataOnlyEOFFailover()
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), mock, 2596, service.PlatformKiro, err))
	require.Equal(t, FailoverExhausted, fs.HandleSelectionExhausted(context.Background()))
	require.Contains(t, fs.FailedAccountIDs, int64(2596), "the sole rejected account must remain excluded")
}

func TestHandleFailoverErrorKiroMetadataOnlyEOFRejectsRepeatedCredential(t *testing.T) {
	mock := &mockTempUnscheduler{}
	fs := NewFailoverState(10, false)
	require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), mock, 2596, service.PlatformKiro, newKiroMetadataOnlyEOFFailover()))
	require.Equal(t, FailoverExhausted, fs.HandleFailoverError(context.Background(), mock, 2596, service.PlatformKiro, newKiroMetadataOnlyEOFFailover()))
	require.Equal(t, 1, fs.SwitchCount)
	require.Len(t, fs.KiroMetadataOnlyEOFAccounts, 1)
}

func TestHandleFailoverErrorOrdinaryIncompleteStreamRetainsGenericPeerBudget(t *testing.T) {
	mock := &mockTempUnscheduler{}
	fs := NewFailoverState(4, false)
	for accountID := int64(1); accountID <= 3; accountID++ {
		err := &service.UpstreamFailoverError{
			StatusCode:             http.StatusServiceUnavailable,
			FailureKind:            service.UpstreamFailureIncompleteStream,
			RetryableOnSameAccount: false,
			SuppressTempUnschedule: true,
		}
		require.Equal(t, FailoverContinue, fs.HandleFailoverError(context.Background(), mock, accountID, service.PlatformKiro, err))
	}
	require.Equal(t, 3, fs.SwitchCount, "zero-frame/transport EOF keeps the normal peer failover budget")
	require.Empty(t, fs.KiroMetadataOnlyEOFAccounts)
}
