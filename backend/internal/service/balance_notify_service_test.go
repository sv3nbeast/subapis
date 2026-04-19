package service

import (
	"context"
	"strings"
	"testing"
)

func TestBuildQuotaAlertEmailBody_IncludesAccountIDAndPlatform(t *testing.T) {
	svc := &BalanceNotifyService{}

	body := svc.buildQuotaAlertEmailBody(42, "demo@example.com", "antigravity", "日限额 / Daily", 12.34, 56.78, 40, "SubAPIs")

	if !strings.Contains(body, "#42") {
		t.Fatalf("expected account id in body, got: %s", body)
	}
	if !strings.Contains(body, "antigravity") {
		t.Fatalf("expected platform in body, got: %s", body)
	}
	if !strings.Contains(body, "demo@example.com") {
		t.Fatalf("expected account name in body, got: %s", body)
	}
}

type balanceNotifySettingRepoStub struct {
	values map[string]string
}

func (s *balanceNotifySettingRepoStub) Get(context.Context, string) (*Setting, error) {
	return nil, nil
}
func (s *balanceNotifySettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}
func (s *balanceNotifySettingRepoStub) Set(context.Context, string, string) error { return nil }
func (s *balanceNotifySettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = s.values[key]
	}
	return out, nil
}
func (s *balanceNotifySettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	return nil
}
func (s *balanceNotifySettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	return s.values, nil
}
func (s *balanceNotifySettingRepoStub) Delete(context.Context, string) error { return nil }

func TestGetAccountQuotaNotifyEmails_FiltersStructuredEntries(t *testing.T) {
	svc := &BalanceNotifyService{
		settingRepo: &balanceNotifySettingRepoStub{
			values: map[string]string{
				SettingKeyAccountQuotaNotifyEmails: MarshalNotifyEmails([]NotifyEmailEntry{
					{Email: "admin-a@example.com", Disabled: false, Verified: true},
					{Email: "admin-b@example.com", Disabled: true, Verified: true},
					{Email: "admin-c@example.com", Disabled: false, Verified: false},
					{Email: "ADMIN-A@example.com", Disabled: false, Verified: true},
				}),
			},
		},
	}

	got := svc.getAccountQuotaNotifyEmails(context.Background())
	if len(got) != 1 || got[0] != "admin-a@example.com" {
		t.Fatalf("unexpected quota notify emails: %#v", got)
	}
}

func TestGetAccountQuotaNotifyEmails_SupportsLegacyStringArray(t *testing.T) {
	svc := &BalanceNotifyService{
		settingRepo: &balanceNotifySettingRepoStub{
			values: map[string]string{
				SettingKeyAccountQuotaNotifyEmails: `["legacy-a@example.com","legacy-b@example.com"]`,
			},
		},
	}

	got := svc.getAccountQuotaNotifyEmails(context.Background())
	if len(got) != 2 || got[0] != "legacy-a@example.com" || got[1] != "legacy-b@example.com" {
		t.Fatalf("unexpected legacy quota notify emails: %#v", got)
	}
}
