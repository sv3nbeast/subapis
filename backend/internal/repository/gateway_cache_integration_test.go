//go:build integration

package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GatewayCacheSuite struct {
	IntegrationRedisSuite
	cache service.GatewayCache
}

func (s *GatewayCacheSuite) SetupTest() {
	s.IntegrationRedisSuite.SetupTest()
	s.cache = NewGatewayCache(s.rdb)
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_Missing() {
	_, err := s.cache.GetSessionAccountID(s.ctx, 1, "nonexistent")
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil for missing session")
}

func (s *GatewayCacheSuite) TestSetAndGetSessionAccountID() {
	sessionID := "s1"
	accountID := int64(99)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sid, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.NoError(s.T(), err, "GetSessionAccountID")
	require.Equal(s.T(), accountID, sid, "session id mismatch")
}

func (s *GatewayCacheSuite) TestSessionAccountID_TTL() {
	sessionID := "s2"
	accountID := int64(100)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL sessionKey after Set")
	s.AssertTTLWithin(ttl, 1*time.Second, sessionTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL() {
	sessionID := "s3"
	accountID := int64(101)
	groupID := int64(1)
	initialTTL := 1 * time.Minute
	refreshTTL := 3 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, initialTTL), "SetSessionAccountID")

	require.NoError(s.T(), s.cache.RefreshSessionTTL(s.ctx, groupID, sessionID, refreshTTL), "RefreshSessionTTL")

	sessionKey := buildSessionKey(groupID, sessionID)
	ttl, err := s.rdb.TTL(s.ctx, sessionKey).Result()
	require.NoError(s.T(), err, "TTL after Refresh")
	s.AssertTTLWithin(ttl, 1*time.Second, refreshTTL)
}

func (s *GatewayCacheSuite) TestRefreshSessionTTL_MissingKey() {
	// RefreshSessionTTL on a missing key should not error (no-op)
	err := s.cache.RefreshSessionTTL(s.ctx, 1, "missing-session", 1*time.Minute)
	require.NoError(s.T(), err, "RefreshSessionTTL on missing key should not error")
}

func (s *GatewayCacheSuite) TestDeleteSessionAccountID() {
	sessionID := "openai:s4"
	accountID := int64(102)
	groupID := int64(1)
	sessionTTL := 1 * time.Minute

	require.NoError(s.T(), s.cache.SetSessionAccountID(s.ctx, groupID, sessionID, accountID, sessionTTL), "SetSessionAccountID")
	require.NoError(s.T(), s.cache.DeleteSessionAccountID(s.ctx, groupID, sessionID), "DeleteSessionAccountID")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.True(s.T(), errors.Is(err, redis.Nil), "expected redis.Nil after delete")
}

func (s *GatewayCacheSuite) TestGetSessionAccountID_CorruptedValue() {
	sessionID := "corrupted"
	groupID := int64(1)
	sessionKey := buildSessionKey(groupID, sessionID)

	// Set a non-integer value
	require.NoError(s.T(), s.rdb.Set(s.ctx, sessionKey, "not-a-number", 1*time.Minute).Err(), "Set invalid value")

	_, err := s.cache.GetSessionAccountID(s.ctx, groupID, sessionID)
	require.Error(s.T(), err, "expected error for corrupted value")
	require.False(s.T(), errors.Is(err, redis.Nil), "expected parsing error, not redis.Nil")
}

func (s *GatewayCacheSuite) TestKiroCacheFingerprints_UpsertAndGet() {
	store := s.cache.(service.KiroCachePersistenceStore)
	stableKey := "kiro:credential:test-upsert"

	require.NoError(s.T(), store.UpsertKiroCacheFingerprints(s.ctx, stableKey, map[string]time.Duration{
		"fp-a": time.Minute,
		"fp-b": time.Minute,
	}))

	got, err := store.GetKiroCacheFingerprints(s.ctx, stableKey, []string{"fp-a", "fp-b", "fp-missing"})
	require.NoError(s.T(), err)
	require.True(s.T(), got["fp-a"])
	require.True(s.T(), got["fp-b"])
	require.False(s.T(), got["fp-missing"])
}

func (s *GatewayCacheSuite) TestKiroCacheFingerprints_UpsertExtendsButDoesNotShortenTTL() {
	store := s.cache.(service.KiroCachePersistenceStore)
	stableKey := "kiro:credential:test-ttl"
	fingerprint := "fp-ttl"
	key := buildKiroCacheFingerprintKey(stableKey, fingerprint)

	require.NoError(s.T(), store.UpsertKiroCacheFingerprints(s.ctx, stableKey, map[string]time.Duration{
		fingerprint: 15 * time.Second,
	}))
	initialTTL, err := s.rdb.PTTL(s.ctx, key).Result()
	require.NoError(s.T(), err)
	s.AssertTTLWithin(initialTTL, 10*time.Second, 15*time.Second)

	require.NoError(s.T(), store.UpsertKiroCacheFingerprints(s.ctx, stableKey, map[string]time.Duration{
		fingerprint: 2 * time.Minute,
	}))
	extendedTTL, err := s.rdb.PTTL(s.ctx, key).Result()
	require.NoError(s.T(), err)
	s.AssertTTLWithin(extendedTTL, 110*time.Second, 2*time.Minute)

	require.NoError(s.T(), store.UpsertKiroCacheFingerprints(s.ctx, stableKey, map[string]time.Duration{
		fingerprint: 30 * time.Second,
	}))
	afterShortTTL, err := s.rdb.PTTL(s.ctx, key).Result()
	require.NoError(s.T(), err)
	require.Greater(s.T(), afterShortTTL, 100*time.Second, "shorter upsert must not reduce existing Kiro cache TTL")
	require.LessOrEqual(s.T(), afterShortTTL, extendedTTL)
}

func (s *GatewayCacheSuite) TestKiroCacheFingerprints_UpsertAddsTTLToUnexpectedPersistentKey() {
	store := s.cache.(service.KiroCachePersistenceStore)
	stableKey := "kiro:credential:test-no-ttl"
	fingerprint := "fp-no-ttl"
	key := buildKiroCacheFingerprintKey(stableKey, fingerprint)

	require.NoError(s.T(), s.rdb.Set(s.ctx, key, "1", 0).Err())
	beforeTTL, err := s.rdb.PTTL(s.ctx, key).Result()
	require.NoError(s.T(), err)
	require.Equal(s.T(), time.Duration(-1), beforeTTL, "fixture should start as persistent Redis key")

	require.NoError(s.T(), store.UpsertKiroCacheFingerprints(s.ctx, stableKey, map[string]time.Duration{
		fingerprint: time.Minute,
	}))
	afterTTL, err := s.rdb.PTTL(s.ctx, key).Result()
	require.NoError(s.T(), err)
	s.AssertTTLWithin(afterTTL, 55*time.Second, time.Minute)
}

func TestGatewayCacheSuite(t *testing.T) {
	suite.Run(t, new(GatewayCacheSuite))
}
