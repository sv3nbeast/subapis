package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestAccountFromServiceShallow_IncludesAntigravityTLSFingerprint(t *testing.T) {
	account := &service.Account{
		ID:       1,
		Name:     "antigravity-1",
		Platform: service.PlatformAntigravity,
		Type:     service.AccountTypeOAuth,
		Status:   service.StatusActive,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": 9,
		},
	}

	dto := AccountFromServiceShallow(account)
	if dto == nil {
		t.Fatalf("expected dto")
	}
	if dto.EnableTLSFingerprint == nil || !*dto.EnableTLSFingerprint {
		t.Fatalf("expected enable_tls_fingerprint to be exported")
	}
	if dto.TLSFingerprintProfileID == nil || *dto.TLSFingerprintProfileID != 9 {
		t.Fatalf("expected tls_fingerprint_profile_id=9, got %#v", dto.TLSFingerprintProfileID)
	}
}
