package tlsfingerprint

import "testing"

func TestNewAccountScopedBuiltinProfile_Deterministic(t *testing.T) {
	p1 := NewAccountScopedBuiltinProfile(12345)
	p2 := NewAccountScopedBuiltinProfile(12345)

	if p1 == nil || p2 == nil {
		t.Fatalf("expected profiles")
	}
	if p1.Name != p2.Name {
		t.Fatalf("expected identical names for same seed, got %q vs %q", p1.Name, p2.Name)
	}
	if len(p1.CipherSuites) != len(p2.CipherSuites) {
		t.Fatalf("expected same cipher suite length")
	}
	for i := range p1.CipherSuites {
		if p1.CipherSuites[i] != p2.CipherSuites[i] {
			t.Fatalf("expected deterministic cipher suites")
		}
	}
}

func TestNewAccountScopedBuiltinProfile_DifferentSeedsDiffer(t *testing.T) {
	p1 := NewAccountScopedBuiltinProfile(111)
	p2 := NewAccountScopedBuiltinProfile(222)

	if p1 == nil || p2 == nil {
		t.Fatalf("expected profiles")
	}

	same := p1.EnableGREASE == p2.EnableGREASE &&
		slicesEqual(p1.CipherSuites, p2.CipherSuites) &&
		slicesEqual(p1.Curves, p2.Curves) &&
		slicesEqual(p1.SignatureAlgorithms, p2.SignatureAlgorithms)
	if same {
		t.Fatalf("expected different seeds to produce different built-in variants")
	}
}

func TestNewAntigravityAccountScopedProfile_PrefersHTTP2ALPN(t *testing.T) {
	profile := NewAntigravityAccountScopedProfile(12345)
	if profile == nil {
		t.Fatalf("expected profile")
	}
	if len(profile.ALPNProtocols) != 2 || profile.ALPNProtocols[0] != "h2" || profile.ALPNProtocols[1] != "http/1.1" {
		t.Fatalf("expected ALPN [h2 http/1.1], got %v", profile.ALPNProtocols)
	}
}

func slicesEqual(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
