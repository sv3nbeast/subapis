package service

import "testing"

func TestAccountSupportsTLSFingerprint_Antigravity(t *testing.T) {
	account := &Account{
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint":     true,
			"tls_fingerprint_profile_id": 7,
		},
	}

	if !account.SupportsTLSFingerprint() {
		t.Fatalf("expected antigravity account to support tls fingerprint")
	}
	if !account.IsTLSFingerprintEnabled() {
		t.Fatalf("expected antigravity account tls fingerprint to be enabled")
	}
	if got := account.GetTLSFingerprintProfileID(); got != 7 {
		t.Fatalf("expected tls fingerprint profile id 7, got %d", got)
	}
}

func TestAccountSupportsTLSFingerprint_AntigravityDisabledByDefault(t *testing.T) {
	account := &Account{
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
	}

	if !account.SupportsTLSFingerprint() {
		t.Fatalf("expected antigravity account to support tls fingerprint")
	}
	if account.IsTLSFingerprintEnabled() {
		t.Fatalf("expected antigravity account tls fingerprint to be disabled by default")
	}
}

func TestResolveTLSProfile_AntigravityUsesAccountScopedBuiltinVariant(t *testing.T) {
	service := &TLSFingerprintProfileService{}
	accountA := &Account{
		ID:       101,
		Name:     "ag-101",
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint": true,
		},
	}
	accountB := &Account{
		ID:       102,
		Name:     "ag-102",
		Platform: PlatformAntigravity,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"enable_tls_fingerprint": true,
		},
	}

	profileA := service.ResolveTLSProfile(accountA)
	profileA2 := service.ResolveTLSProfile(accountA)
	profileB := service.ResolveTLSProfile(accountB)

	if profileA == nil || profileA2 == nil || profileB == nil {
		t.Fatalf("expected resolved profiles")
	}
	if profileA.Name != profileA2.Name {
		t.Fatalf("expected deterministic account-scoped profile")
	}
	if profileA.Name == profileB.Name {
		t.Fatalf("expected different antigravity accounts to get different built-in variants")
	}
	if len(profileA.ALPNProtocols) != 2 || profileA.ALPNProtocols[0] != "h2" {
		t.Fatalf("expected antigravity profile to prefer h2 ALPN, got %v", profileA.ALPNProtocols)
	}
}
