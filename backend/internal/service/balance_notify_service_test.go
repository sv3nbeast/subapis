package service

import (
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
