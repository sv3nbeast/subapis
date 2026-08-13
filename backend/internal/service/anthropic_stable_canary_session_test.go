package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type stableCanarySessionRepoStub struct {
	AccountRepository
	groupID           int64
	accountID         int64
	ownerUserID       int64
	sessionHash       string
	keyFingerprint    string
	policyFingerprint string
	err               error
}

func (r *stableCanarySessionRepoStub) ClaimAnthropicStableCanarySession(
	_ context.Context,
	groupID, accountID, generation, ownerUserID int64,
	sessionHash, keyFingerprint, policyFingerprint string,
) error {
	r.groupID, r.accountID, r.ownerUserID = groupID, accountID, ownerUserID
	r.sessionHash, r.keyFingerprint = sessionHash, keyFingerprint
	r.policyFingerprint = policyFingerprint
	return r.err
}

func TestHashAnthropicStableCanarySessionForRoutingIsKeyedAndScoped(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	first, err := HashAnthropicStableCanarySessionForRouting(key, 11, 1, stableTestSession)
	require.NoError(t, err)
	require.Len(t, first, 64)
	require.NotContains(t, first, stableTestSession)

	same, err := HashAnthropicStableCanarySessionForRouting(key, 11, 1, stableTestSession)
	require.NoError(t, err)
	require.Equal(t, first, same)

	otherGroup, err := HashAnthropicStableCanarySessionForRouting(key, 12, 1, stableTestSession)
	require.NoError(t, err)
	require.NotEqual(t, first, otherGroup)

	otherKey, err := HashAnthropicStableCanarySessionForRouting("fedcba9876543210fedcba9876543210", 11, 1, stableTestSession)
	require.NoError(t, err)
	require.NotEqual(t, first, otherKey)

	otherGeneration, err := HashAnthropicStableCanarySessionForRouting(key, 11, 2, stableTestSession)
	require.NoError(t, err)
	require.NotEqual(t, first, otherGeneration)
}

func TestFingerprintAnthropicStableCanarySharedPolicyIsOrderIndependent(t *testing.T) {
	first, err := FingerprintAnthropicStableCanarySharedPolicy([]int64{91, 92, 93})
	require.NoError(t, err)
	second, err := FingerprintAnthropicStableCanarySharedPolicy([]int64{93, 91, 92})
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first, 64)

	different, err := FingerprintAnthropicStableCanarySharedPolicy([]int64{91, 92, 94})
	require.NoError(t, err)
	require.NotEqual(t, first, different)
	_, err = FingerprintAnthropicStableCanarySharedPolicy([]int64{91, 91})
	require.Error(t, err)
}

func TestAnthropicStableCanaryPrincipalAllowedPreservesD1AndRequiresSharedAllowList(t *testing.T) {
	groupID := int64(71)
	key := &APIKey{ID: 91, UserID: 1, GroupID: &groupID, Status: StatusAPIKeyActive}
	svc := &GatewayService{cfg: &config.Config{}}
	svc.cfg.Gateway.AnthropicStableCanary = config.GatewayAnthropicStableCanaryConfig{
		Enabled: true, GroupID: groupID, AccountID: 811, OwnerUserID: 1, APIKeyID: 91,
	}
	require.True(t, svc.AnthropicStableCanaryPrincipalAllowed(1, key))
	require.False(t, svc.AnthropicStableCanaryPrincipalAllowed(2, key))

	svc.cfg.Gateway.AnthropicStableCanary.OwnerUserID = 0
	svc.cfg.Gateway.AnthropicStableCanary.APIKeyID = 0
	svc.cfg.Gateway.AnthropicStableCanary.SharedUsers = true
	svc.cfg.Gateway.AnthropicStableCanary.SharedAPIKeyIDs = []int64{91, 92}
	require.True(t, svc.AnthropicStableCanaryPrincipalAllowed(1, key))

	key.ID = 93
	require.False(t, svc.AnthropicStableCanaryPrincipalAllowed(1, key), "a key is not admitted merely because it points at the group")
	key.ID = 92
	key.UserID = 2
	require.True(t, svc.AnthropicStableCanaryPrincipalAllowed(2, key))
}

func TestClaimAnthropicStableCanarySessionUsesDurableOpaqueBinding(t *testing.T) {
	repo := &stableCanarySessionRepoStub{}
	svc := &GatewayService{cfg: &config.Config{}, accountRepo: repo}
	svc.cfg.Gateway.AnthropicStableCanary = config.GatewayAnthropicStableCanaryConfig{
		Enabled: true, GroupID: 71, AccountID: 811, SharedUsers: true,
		SharedAPIKeyIDs: []int64{91}, SessionGeneration: 1, SessionHMACKey: "0123456789abcdef0123456789abcdef", MaxBodyBytes: 64 << 20,
	}

	err := svc.ClaimAnthropicStableCanarySession(context.Background(), 71, 1001, stableTestSession)
	require.NoError(t, err)
	require.Equal(t, int64(71), repo.groupID)
	require.Equal(t, int64(811), repo.accountID)
	require.Equal(t, int64(1001), repo.ownerUserID)
	require.Len(t, repo.sessionHash, 64)
	require.Len(t, repo.keyFingerprint, 64)
	require.Len(t, repo.policyFingerprint, 64)
	require.NotEqual(t, repo.sessionHash, repo.keyFingerprint)
	require.NotContains(t, repo.sessionHash, stableTestSession)
}

func TestClaimAnthropicStableCanarySessionPropagatesOwnerConflict(t *testing.T) {
	repo := &stableCanarySessionRepoStub{err: ErrAnthropicStableCanarySessionOwnerConflict}
	svc := &GatewayService{cfg: &config.Config{}, accountRepo: repo}
	svc.cfg.Gateway.AnthropicStableCanary = config.GatewayAnthropicStableCanaryConfig{
		Enabled: true, GroupID: 71, AccountID: 811, SharedUsers: true,
		SharedAPIKeyIDs: []int64{91}, SessionGeneration: 1, SessionHMACKey: "0123456789abcdef0123456789abcdef", MaxBodyBytes: 64 << 20,
	}

	err := svc.ClaimAnthropicStableCanarySession(context.Background(), 71, 1002, stableTestSession)
	require.True(t, errors.Is(err, ErrAnthropicStableCanarySessionOwnerConflict))
}
