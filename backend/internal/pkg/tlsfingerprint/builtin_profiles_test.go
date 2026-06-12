package tlsfingerprint

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"testing"

	utls "github.com/refraction-networking/utls"
)

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

// computeJA4FromSpec implements the JA4 TLS fingerprint algorithm against a ClientHelloSpec.
// ALPN field = first char + last char of first ALPN protocol ("http/1.1" → "h1").
func computeJA4FromSpec(spec *utls.ClientHelloSpec) string {
	var extIDs []uint16
	alpnField := "00"
	var sigalgs []uint16

	for _, ext := range spec.Extensions {
		switch e := ext.(type) {
		case *utls.UtlsGREASEExtension:
			// not counted
		case *utls.SNIExtension:
			extIDs = append(extIDs, 0)
		case *utls.StatusRequestExtension:
			extIDs = append(extIDs, 5)
		case *utls.SupportedCurvesExtension:
			extIDs = append(extIDs, 10)
		case *utls.SupportedPointsExtension:
			extIDs = append(extIDs, 11)
		case *utls.SignatureAlgorithmsExtension:
			extIDs = append(extIDs, 13)
			for _, sa := range e.SupportedSignatureAlgorithms {
				sigalgs = append(sigalgs, uint16(sa))
			}
		case *utls.ALPNExtension:
			extIDs = append(extIDs, 16)
			if len(e.AlpnProtocols) > 0 {
				proto := e.AlpnProtocols[0]
				if len(proto) >= 2 {
					alpnField = string(proto[0]) + string(proto[len(proto)-1])
				}
			}
		case *utls.SCTExtension:
			extIDs = append(extIDs, 18)
		case *utls.UtlsPaddingExtension:
			extIDs = append(extIDs, 21)
		case *utls.ExtendedMasterSecretExtension:
			extIDs = append(extIDs, 23)
		case *utls.SessionTicketExtension:
			extIDs = append(extIDs, 35)
		case *utls.SupportedVersionsExtension:
			extIDs = append(extIDs, 43)
		case *utls.PSKKeyExchangeModesExtension:
			extIDs = append(extIDs, 45)
		case *utls.SignatureAlgorithmsCertExtension:
			extIDs = append(extIDs, 50)
		case *utls.KeyShareExtension:
			extIDs = append(extIDs, 51)
		case *utls.GREASEEncryptedClientHelloExtension:
			extIDs = append(extIDs, 0xfe0d)
		case *utls.RenegotiationInfoExtension:
			extIDs = append(extIDs, 0xff01)
		case *utls.GenericExtension:
			extIDs = append(extIDs, e.Id)
		}
	}

	var ciphers []uint16
	for _, c := range spec.CipherSuites {
		if !(c&0x0f0f == 0x0a0a && c>>8 == c&0xff) {
			ciphers = append(ciphers, c)
		}
	}

	prefix := fmt.Sprintf("t13d%02d%02d%s", len(ciphers), len(extIDs), alpnField)

	sortedC := make([]uint16, len(ciphers))
	copy(sortedC, ciphers)
	sort.Slice(sortedC, func(i, j int) bool { return sortedC[i] < sortedC[j] })
	var cp []string
	for _, c := range sortedC {
		cp = append(cp, fmt.Sprintf("%04x", c))
	}
	h1 := sha256.Sum256([]byte(strings.Join(cp, ",")))
	bHash := fmt.Sprintf("%x", h1[:])[:12]

	var filtExts []uint16
	for _, id := range extIDs {
		if id != 0 && id != 16 {
			filtExts = append(filtExts, id)
		}
	}
	sort.Slice(filtExts, func(i, j int) bool { return filtExts[i] < filtExts[j] })
	var ep, sp []string
	for _, id := range filtExts {
		ep = append(ep, fmt.Sprintf("%04x", id))
	}
	for _, sa := range sigalgs {
		sp = append(sp, fmt.Sprintf("%04x", sa))
	}
	h2 := sha256.Sum256([]byte(strings.Join(ep, ",") + "_" + strings.Join(sp, ",")))
	cHash := fmt.Sprintf("%x", h2[:])[:12]

	return fmt.Sprintf("%s_%s_%s", prefix, bHash, cHash)
}

// TestBuiltInDefaultProfile_JA4 verifies that BuiltInDefaultProfile produces the
// target JA4 fingerprint that matches the captured Claude CLI macOS arm64 baseline.
func TestBuiltInDefaultProfile_JA4(t *testing.T) {
	profile := BuiltInDefaultProfile()
	spec := buildClientHelloSpecFromProfile(profile)
	ja4 := computeJA4FromSpec(spec)

	const want = "t13d1714h1_5b57614c22b0_43ade6aba3df"
	if ja4 != want {
		t.Errorf("BuiltInDefaultProfile JA4 mismatch:\n  got:  %s\n  want: %s", ja4, want)
	}
}
