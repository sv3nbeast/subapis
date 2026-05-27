//go:build unit

package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type rateLimit429AccountRepoStub struct {
	mockAccountRepoForGemini
	rateLimitCalls  int
	rateLimitIDs    []int64
	rateLimitResets map[int64]time.Time
	extraMatches    []Account
}

func (r *rateLimit429AccountRepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitCalls++
	r.rateLimitIDs = append(r.rateLimitIDs, id)
	if r.rateLimitResets == nil {
		r.rateLimitResets = map[int64]time.Time{}
	}
	r.rateLimitResets[id] = resetAt
	return nil
}

func (r *rateLimit429AccountRepoStub) FindByExtraField(_ context.Context, key string, value any) ([]Account, error) {
	if key == "org_uuid" && value == "org-1" {
		return r.extraMatches, nil
	}
	return nil, nil
}

func TestGetRateLimit429CooldownSettings_DefaultsWhenNotSet(t *testing.T) {
	repo := newMockSettingRepo()
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetRateLimit429CooldownSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, 5, settings.CooldownSeconds)
}

func TestGetRateLimit429CooldownSettings_ReadsFromDB(t *testing.T) {
	repo := newMockSettingRepo()
	data, _ := json.Marshal(RateLimit429CooldownSettings{Enabled: false, CooldownSeconds: 12})
	repo.data[SettingKeyRateLimit429CooldownSettings] = string(data)
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetRateLimit429CooldownSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Enabled)
	require.Equal(t, 12, settings.CooldownSeconds)
}

func TestSetRateLimit429CooldownSettings_EnabledRejectsOutOfRange(t *testing.T) {
	svc := NewSettingService(newMockSettingRepo(), &config.Config{})

	for _, seconds := range []int{0, -1, 7201, 99999} {
		err := svc.SetRateLimit429CooldownSettings(context.Background(), &RateLimit429CooldownSettings{
			Enabled: true, CooldownSeconds: seconds,
		})
		require.Error(t, err, "should reject enabled=true + cooldown_seconds=%d", seconds)
		require.Contains(t, err.Error(), "cooldown_seconds must be between 1-7200")
	}
}

func TestHandle429_FallbackUsesDBSeconds(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newMockSettingRepo()
	data, _ := json.Marshal(RateLimit429CooldownSettings{Enabled: true, CooldownSeconds: 12})
	settingRepo.data[SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := NewSettingService(settingRepo, &config.Config{})
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(settingSvc)

	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	before := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
	after := time.Now()

	require.Equal(t, 1, accountRepo.rateLimitCalls)
	require.Equal(t, []int64{42}, accountRepo.rateLimitIDs)
	require.True(t, !accountRepo.rateLimitResets[42].Before(before.Add(12*time.Second)) && !accountRepo.rateLimitResets[42].After(after.Add(12*time.Second)))
}

func TestHandle429_FallbackDisabledSkipsLocalMark(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newMockSettingRepo()
	data, _ := json.Marshal(RateLimit429CooldownSettings{Enabled: false, CooldownSeconds: 12})
	settingRepo.data[SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := NewSettingService(settingRepo, &config.Config{})
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(settingSvc)

	account := &Account{ID: 43, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))

	require.Zero(t, accountRepo.rateLimitCalls)
}

func TestHandle429_FallbackUsesDefaultSecondsWhenSettingServiceMissing(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	cfg := &config.Config{}
	svc := NewRateLimitService(accountRepo, nil, cfg, nil, nil)

	account := &Account{ID: 44, Platform: PlatformGemini, Type: AccountTypeAPIKey}
	before := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"message":"slow down"}}`))
	after := time.Now()

	require.Equal(t, 1, accountRepo.rateLimitCalls)
	require.Equal(t, []int64{44}, accountRepo.rateLimitIDs)
	require.True(t, !accountRepo.rateLimitResets[44].Before(before.Add(5*time.Second)) && !accountRepo.rateLimitResets[44].After(after.Add(5*time.Second)))
}

func TestHandle429_AnthropicRateLimitWithoutResetUsesAdaptiveInitialCooldown(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newMockSettingRepo()
	data, _ := json.Marshal(RateLimit429CooldownSettings{Enabled: true, CooldownSeconds: 5})
	settingRepo.data[SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := NewSettingService(settingRepo, &config.Config{})
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(settingSvc)

	account := &Account{ID: 45, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	before := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"Error"}}`))
	after := time.Now()

	require.Equal(t, 1, accountRepo.rateLimitCalls)
	require.Equal(t, []int64{45}, accountRepo.rateLimitIDs)
	require.True(t, !accountRepo.rateLimitResets[45].Before(before.Add(10*time.Second)) && !accountRepo.rateLimitResets[45].After(after.Add(10*time.Second)))
}

func TestHandle429_AnthropicRateLimitWithoutResetBacksOffWithinWindow(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newMockSettingRepo()
	data, _ := json.Marshal(RateLimit429CooldownSettings{Enabled: true, CooldownSeconds: 5})
	settingRepo.data[SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := NewSettingService(settingRepo, &config.Config{})
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(settingSvc)

	account := &Account{ID: 45, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"org_uuid": "org-1"}}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Error"}}`)

	beforeFirst := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, body)
	afterFirst := time.Now()
	firstReset := accountRepo.rateLimitResets[45]

	beforeSecond := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, body)
	afterSecond := time.Now()
	secondReset := accountRepo.rateLimitResets[45]

	beforeThird := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, body)
	afterThird := time.Now()
	thirdReset := accountRepo.rateLimitResets[45]

	require.Equal(t, 3, accountRepo.rateLimitCalls)
	require.True(t, !firstReset.Before(beforeFirst.Add(10*time.Second)) && !firstReset.After(afterFirst.Add(10*time.Second)))
	require.True(t, !secondReset.Before(beforeSecond.Add(15*time.Second)) && !secondReset.After(afterSecond.Add(15*time.Second)))
	require.True(t, !thirdReset.Before(beforeThird.Add(30*time.Second)) && !thirdReset.After(afterThird.Add(30*time.Second)))
}

func TestHandle429_AnthropicRateLimitWithoutResetBackoffIsSharedByOrg(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newMockSettingRepo()
	data, _ := json.Marshal(RateLimit429CooldownSettings{Enabled: true, CooldownSeconds: 5})
	settingRepo.data[SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := NewSettingService(settingRepo, &config.Config{})
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(settingSvc)

	firstAccount := &Account{ID: 45, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"org_uuid": "org-1"}}
	secondAccount := &Account{ID: 46, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"org_uuid": "org-1"}}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"Error"}}`)

	svc.handle429(context.Background(), firstAccount, http.Header{}, body)
	beforeSecond := time.Now()
	svc.handle429(context.Background(), secondAccount, http.Header{}, body)
	afterSecond := time.Now()

	require.Equal(t, 2, accountRepo.rateLimitCalls)
	require.True(t, !accountRepo.rateLimitResets[46].Before(beforeSecond.Add(15*time.Second)) && !accountRepo.rateLimitResets[46].After(afterSecond.Add(15*time.Second)))
}

func TestHandle429_AnthropicRateLimitWithoutResetKeepsLargerConfiguredCooldown(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newMockSettingRepo()
	data, _ := json.Marshal(RateLimit429CooldownSettings{Enabled: true, CooldownSeconds: 60})
	settingRepo.data[SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := NewSettingService(settingRepo, &config.Config{})
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)
	svc.SetSettingService(settingSvc)

	account := &Account{ID: 45, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	before := time.Now()
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"Error"}}`))
	after := time.Now()

	require.Equal(t, 1, accountRepo.rateLimitCalls)
	require.True(t, !accountRepo.rateLimitResets[45].Before(before.Add(60*time.Second)) && !accountRepo.rateLimitResets[45].After(after.Add(60*time.Second)))
}

func TestHandle429_AnthropicRateLimitWithoutResetCoolsSameOrgPeers(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{
		extraMatches: []Account{
			{ID: 45, Platform: PlatformAnthropic, Extra: map[string]any{"org_uuid": "org-1"}},
			{ID: 46, Platform: PlatformAnthropic, Extra: map[string]any{"org_uuid": "org-1"}},
			{ID: 47, Platform: PlatformOpenAI, Extra: map[string]any{"org_uuid": "org-1"}},
		},
	}
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)

	account := &Account{ID: 45, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"org_uuid": "org-1"}}
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"Error"}}`))

	require.Equal(t, []int64{45, 46}, accountRepo.rateLimitIDs)
	require.WithinDuration(t, accountRepo.rateLimitResets[45], accountRepo.rateLimitResets[46], time.Second)
}

func TestHandle429_AnthropicAmbiguousWithoutResetSkipsFallback(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	svc := NewRateLimitService(accountRepo, nil, &config.Config{}, nil, nil)

	account := &Account{ID: 46, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	svc.handle429(context.Background(), account, http.Header{}, []byte(`{"error":{"message":"extra usage required"}}`))

	require.Zero(t, accountRepo.rateLimitCalls)
}
