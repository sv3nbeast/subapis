package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type anthropicNoResetAccountRepo struct {
	AccountRepository
	resetAt *time.Time
}

func (r *anthropicNoResetAccountRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.resetAt = &resetAt
	return nil
}

func TestAnthropicExplicit429WithoutResetUsesAdaptiveCooldown(t *testing.T) {
	repo := &anthropicNoResetAccountRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 1936, Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	before := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"Error"}}`))

	require.NotNil(t, repo.resetAt)
	require.WithinDuration(t, before.Add(anthropicNoReset429FirstCooldown), *repo.resetAt, time.Second)
}

func TestAnthropicNoReset429CooldownEscalatesWithinTwoMinutes(t *testing.T) {
	svc := NewRateLimitService(&anthropicNoResetAccountRepo{}, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 1937, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	now := time.Now()

	require.Equal(t, 10*time.Second, svc.nextAnthropicNoReset429Cooldown(account, now, false))
	require.Equal(t, 30*time.Second, svc.nextAnthropicNoReset429Cooldown(account, now.Add(time.Second), false))
	require.Equal(t, 60*time.Second, svc.nextAnthropicNoReset429Cooldown(account, now.Add(2*time.Second), false))
	require.Equal(t, 10*time.Second, svc.nextAnthropicNoReset429Cooldown(account, now.Add(anthropicNoReset429BackoffWindow+2*time.Second), false))
}

func TestAnthropicAmbiguous429WithoutResetDoesNotCoolAccount(t *testing.T) {
	repo := &anthropicNoResetAccountRepo{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{ID: 1936, Platform: PlatformAnthropic, Type: AccountTypeOAuth}

	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"message":"Extra usage required"}}`))

	require.Nil(t, repo.resetAt)
}
