//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountUsageService_GetUsage_KiroAPIKeySupported(t *testing.T) {
	account := &Account{
		ID:       9101,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	svc := NewAccountUsageService(repo, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)

	usage, err := svc.GetUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, "active", usage.Source)
}

func TestAccountUsageService_GetPassiveUsage_KiroAPIKeySupported(t *testing.T) {
	account := &Account{
		ID:       9102,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	svc := NewAccountUsageService(repo, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)

	usage, err := svc.GetPassiveUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, "passive", usage.Source)
}

func TestAccountUsageService_GetUsage_KiroAPIKeyWithBaseURLSkipsDirectUsage(t *testing.T) {
	account := &Account{
		ID:       9103,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "kiro-api-key",
			"base_url": "https://kiro-upstream.example.com",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	svc := NewAccountUsageService(repo, nil, nil, nil, nil, nil, nil, NewUsageCache(), nil, nil)

	usage, err := svc.GetUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, "active", usage.Source)
	require.Empty(t, usage.Error)
	require.Empty(t, usage.ErrorCode)
	require.Nil(t, usage.KiroCredit)
}
