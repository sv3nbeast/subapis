//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type grokDeviceReauthorizationRepoStub struct {
	AccountRepository
	applied             bool
	err                 error
	accountID           int64
	expectedCredentials map[string]any
	expectedProxyID     *int64
	credentials         map[string]any
	extra               map[string]any
}

func (r *grokDeviceReauthorizationRepoStub) ReauthorizeGrokOAuthIfCredentialsUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	credentials map[string]any,
	extra map[string]any,
) (bool, error) {
	r.accountID = id
	r.expectedCredentials = expectedCredentials
	r.expectedProxyID = expectedProxyID
	r.credentials = credentials
	r.extra = extra
	return r.applied, r.err
}

func TestAdminServiceReauthorizeGrokOAuthUsesCASAndClearsRuntimeBlock(t *testing.T) {
	proxyID := int64(17)
	repo := &grokDeviceReauthorizationRepoStub{applied: true}
	runtimeBlocker := &adminRuntimeBlockRecorder{}
	svc := &adminServiceImpl{accountRepo: repo, runtimeBlocker: runtimeBlocker}

	applied, err := svc.ReauthorizeGrokOAuthAccountIfUnchanged(
		context.Background(),
		42,
		map[string]any{"refresh_token": "old"},
		&proxyID,
		map[string]any{"refresh_token": "new"},
		map[string]any{"email": "user@example.com"},
	)

	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, int64(42), repo.accountID)
	require.Equal(t, "old", repo.expectedCredentials["refresh_token"])
	require.Equal(t, "new", repo.credentials["refresh_token"])
	require.Equal(t, &proxyID, repo.expectedProxyID)
	require.Equal(t, "user@example.com", repo.extra["email"])
	require.Equal(t, int64(42), runtimeBlocker.clearedAccountID)
}

func TestAdminServiceReauthorizeGrokOAuthCASMissDoesNotClearRuntimeBlock(t *testing.T) {
	repo := &grokDeviceReauthorizationRepoStub{applied: false}
	runtimeBlocker := &adminRuntimeBlockRecorder{}
	svc := &adminServiceImpl{accountRepo: repo, runtimeBlocker: runtimeBlocker}

	applied, err := svc.ReauthorizeGrokOAuthAccountIfUnchanged(
		context.Background(), 42, map[string]any{"refresh_token": "old"}, nil,
		map[string]any{"refresh_token": "stale"}, nil,
	)

	require.NoError(t, err)
	require.False(t, applied)
	require.Zero(t, runtimeBlocker.clearedAccountID)
}

func TestAdminServiceReauthorizeGrokOAuthPersistenceErrorIsReturned(t *testing.T) {
	expectedErr := errors.New("database unavailable")
	repo := &grokDeviceReauthorizationRepoStub{err: expectedErr}
	svc := &adminServiceImpl{accountRepo: repo}

	applied, err := svc.ReauthorizeGrokOAuthAccountIfUnchanged(
		context.Background(), 42, map[string]any{"refresh_token": "old"}, nil,
		map[string]any{"refresh_token": "new"}, nil,
	)

	require.False(t, applied)
	require.ErrorIs(t, err, expectedErr)
}
