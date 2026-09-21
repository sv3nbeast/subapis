//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	nianzscooldown "github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown_nianzs"
	"github.com/stretchr/testify/require"
)

// recordingNianzsKiroCooldownStore 记录 nianzs 引擎对冷却存储的每一次写入，
// 用来断言 429 在默认配置下完全不落任何账号级冷却。
type recordingNianzsKiroCooldownStore struct {
	mark429Calls        int
	mark429WithBase     int
	lastBase            time.Duration
	markSuspendedCalls  int
	markSuccessCalls    int
	cooldownToReturn    time.Duration
	suspendedToReturn   time.Duration
	clearTransientCalls int
}

func (s *recordingNianzsKiroCooldownStore) CheckCooldown(context.Context, string) error { return nil }

func (s *recordingNianzsKiroCooldownStore) MarkSuccess(context.Context, string) error {
	s.markSuccessCalls++
	return nil
}

func (s *recordingNianzsKiroCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	s.mark429Calls++
	return s.cooldownToReturn, nil
}

func (s *recordingNianzsKiroCooldownStore) Mark429WithBase(_ context.Context, _ string, base time.Duration) (time.Duration, error) {
	s.mark429WithBase++
	s.lastBase = base
	return s.cooldownToReturn, nil
}

func (s *recordingNianzsKiroCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	s.markSuspendedCalls++
	return s.suspendedToReturn, nil
}

func (s *recordingNianzsKiroCooldownStore) GetState(context.Context, string) (*nianzscooldown.State, error) {
	return nil, nil
}

func (s *recordingNianzsKiroCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	s.clearTransientCalls++
	return false, nil
}

type kiro429RepoStub struct {
	mockAccountRepoForPlatform
	rateLimitedCalls  int
	lastRateLimitedID int64
	lastResetAt       time.Time
	clearCalls        int
}

func (r *kiro429RepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitedID = id
	r.lastResetAt = resetAt
	return nil
}

func (r *kiro429RepoStub) ClearRateLimit(context.Context, int64) error {
	r.clearCalls++
	return nil
}

func kiro429TestService(cooldown429Seconds int) (*GatewayService, *recordingNianzsKiroCooldownStore, *kiro429RepoStub) {
	svc := enforcedKiroResilienceTestService()
	svc.cfg.Gateway.KiroResilience.Cooldown429Seconds = cooldown429Seconds
	store := &recordingNianzsKiroCooldownStore{cooldownToReturn: time.Minute, suspendedToReturn: 24 * time.Hour}
	repo := &kiro429RepoStub{}
	svc.nianzsKiroCooldownStore = store
	svc.accountRepo = repo
	return svc, store, repo
}

// 默认（cooldown_429_seconds=0）下，nianzs 引擎收到 429 既不写 Redis 冷却，
// 也不写 DB rate_limit_reset_at —— 账号必须留在调度快照里，由 failover 换号。
func TestMarkKiro429NianzsDefaultConfigNeverPausesAccount(t *testing.T) {
	svc, store, repo := kiro429TestService(0)

	cooldown, err := svc.markKiro429Nianzs(context.Background(), 4291, "kiro-token-key")
	require.NoError(t, err)
	require.Zero(t, cooldown)
	require.Zero(t, store.mark429Calls)
	require.Zero(t, store.mark429WithBase)
	require.Zero(t, repo.rateLimitedCalls)
}

// 运维把开关调回 >0 时，冷却机制原样恢复，并以配置值作为首次冷却基数。
func TestMarkKiro429NianzsHonorsConfiguredCooldownBase(t *testing.T) {
	svc, store, repo := kiro429TestService(30)

	cooldown, err := svc.markKiro429Nianzs(context.Background(), 4292, "kiro-token-key")
	require.NoError(t, err)
	require.Equal(t, time.Minute, cooldown)
	require.Equal(t, 1, store.mark429WithBase)
	require.Equal(t, 30*time.Second, store.lastBase)
	require.Zero(t, store.mark429Calls)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, int64(4292), repo.lastRateLimitedID)
}

// 429 关冷却不得波及 403 suspended 标记。
func TestMarkKiroSuspendedNianzsUnaffectedBy429Switch(t *testing.T) {
	svc, store, _ := kiro429TestService(0)

	cooldown, err := svc.markKiroSuspendedNianzs(context.Background(), "kiro-token-key")
	require.NoError(t, err)
	require.Equal(t, 24*time.Hour, cooldown)
	require.Equal(t, 1, store.markSuspendedCalls)
}

// 429 关冷却也不得波及 402 月度配额耗尽标记（那是真实的长期不可用）。
func TestMarkKiroMonthlyRequestCountUnaffectedBy429Switch(t *testing.T) {
	svc, _, repo := kiro429TestService(0)
	account := &Account{ID: 4293, Platform: PlatformKiro}

	svc.markKiroMonthlyRequestCountRateLimitedNianzs(context.Background(), account, `{"reason":"MONTHLY_REQUEST_COUNT"}`)

	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, int64(4293), repo.lastRateLimitedID)
	require.True(t, repo.lastResetAt.After(time.Now()))
}

// legacy 引擎（回滚路径）同样不得在 429 时暂停账号，只返回本次请求的
// retry-after 提示。
func TestMarkKiroAccount429LegacyDefaultConfigNeverPausesAccount(t *testing.T) {
	store := &recordingKiroResilienceCooldownStore{}
	svc := enforcedKiroResilienceTestService()
	svc.cfg.Gateway.KiroResilience.Cooldown429Seconds = 0
	svc.kiroCooldownStore = store
	groupID := int64(429)
	account := &Account{ID: 4294, Platform: PlatformKiro, Credentials: map[string]any{"refresh_token": "no-cooldown"}}

	retryAfter := svc.markKiroAccount429(context.Background(), account, &groupID, nil, true)

	require.Equal(t, defaultKiro429SoftPause, retryAfter)
	require.Nil(t, account.RateLimitResetAt)
	require.Zero(t, store.mark429Calls)
	require.Zero(t, store.observe429Calls)
}
