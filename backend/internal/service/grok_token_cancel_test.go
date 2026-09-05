//go:build unit

package service

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type grokCancelCommitRepo struct {
	AccountRepository
	mu        sync.Mutex
	account   *Account
	committed chan struct{}
}

func (r *grokCancelCommitRepo) GetByID(context.Context, int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return snapshotOAuthRefreshAccount(r.account), nil
}

func (r *grokCancelCommitRepo) UpdateGrokOAuthCredentialsIfUnchanged(_ context.Context, _ int64, expected map[string]any, proxyID *int64, next map[string]any) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !reflect.DeepEqual(expected, r.account.Credentials) || !reflect.DeepEqual(proxyID, r.account.ProxyID) {
		return false, nil
	}
	r.account.Credentials = cloneCredentials(next)
	close(r.committed)
	return true, nil
}

type grokCancelCommitExecutor struct {
	refreshAPIExecutorStub
	started chan context.Context
	release <-chan struct{}
}

func (e *grokCancelCommitExecutor) Refresh(ctx context.Context, _ *Account) (map[string]any, error) {
	e.started <- ctx
	select {
	case <-e.release:
		return map[string]any{"access_token": "successor-access", "refresh_token": "successor-refresh", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestGrokRequestCancellationDoesNotLoseInFlightRotation(t *testing.T) {
	account := &Account{ID: 9401, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh", "expires_at": time.Now().Add(-time.Minute).Format(time.RFC3339)}}
	repo := &grokCancelCommitRepo{account: snapshotOAuthRefreshAccount(account), committed: make(chan struct{})}
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	executor := &grokCancelCommitExecutor{refreshAPIExecutorStub: refreshAPIExecutorStub{needsRefresh: true}, started: make(chan context.Context, 1), release: release}
	provider := NewGrokTokenProvider(repo, nil)
	provider.SetRefreshAPI(NewOAuthRefreshAPI(repo, nil), executor)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := provider.GetAccessToken(ctx, account); done <- err }()
	var criticalCtx context.Context
	select {
	case criticalCtx = <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("request kept waiting after cancellation")
	}
	require.NoError(t, criticalCtx.Err(), "the already-started rotation owns its bounded commit lifetime")
	finish()
	select {
	case <-repo.committed:
	case <-time.After(time.Second):
		t.Fatal("successor token was not persisted")
	}
	stored, err := repo.GetByID(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "successor-refresh", stored.GetGrokRefreshToken())
	require.Equal(t, "old-refresh", account.GetGrokRefreshToken(), "caller snapshot remains immutable")
}
