package tlsfingerprint

import (
	"fmt"
	"math/rand/v2"

	utls "github.com/refraction-networking/utls"
)

// BuiltInDefaultProfile returns the built-in Node.js 24.x profile marker.
// Empty slice fields fall back to the in-code Node.js defaults in the dialer.
func BuiltInDefaultProfile() *Profile {
	return &Profile{Name: "Built-in Default (Node.js 24.x)"}
}

// NewAccountScopedBuiltinProfile returns a deterministic built-in variant.
// It keeps the Node.js 24.x baseline, but introduces small per-account ordering
// differences so different accounts do not all share the exact same fingerprint.
func NewAccountScopedBuiltinProfile(seed uint64) *Profile {
	if seed == 0 {
		return BuiltInDefaultProfile()
	}

	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))

	cipherSuites := append([]uint16(nil), defaultCipherSuites...)
	rotateUint16Range(cipherSuites, 0, 3, r.IntN(3))   // TLS 1.3
	rotateUint16Range(cipherSuites, 3, 7, r.IntN(4))   // ECDHE + AES-GCM
	rotateUint16Range(cipherSuites, 7, 9, r.IntN(2))   // ECDHE + ChaCha20
	rotateUint16Range(cipherSuites, 9, 13, r.IntN(4))  // ECDHE + AES-CBC
	rotateUint16Range(cipherSuites, 13, 15, r.IntN(2)) // RSA + AES-GCM
	rotateUint16Range(cipherSuites, 15, 17, r.IntN(2)) // RSA + AES-CBC

	curves := curveIDsToUint16s(defaultCurves)
	rotateUint16Range(curves, 0, len(curves), r.IntN(len(curves)))

	signatureAlgorithms := signatureSchemesToUint16s(defaultSignatureAlgorithms)
	rotateUint16Range(signatureAlgorithms, 0, len(signatureAlgorithms), r.IntN(len(signatureAlgorithms)))

	return &Profile{
		Name:                fmt.Sprintf("Built-in Account Scoped (%016x)", seed),
		EnableGREASE:        r.IntN(2) == 1,
		CipherSuites:        cipherSuites,
		Curves:              curves,
		SignatureAlgorithms: signatureAlgorithms,
	}
}

// NewAntigravityAccountScopedProfile returns an account-scoped profile tuned for
// Antigravity / Cloud Code upstreams, preferring HTTP/2 ALPN while preserving
// the built-in deterministic per-account fingerprint variation.
func NewAntigravityAccountScopedProfile(seed uint64) *Profile {
	profile := NewAccountScopedBuiltinProfile(seed)
	if profile == nil {
		profile = BuiltInDefaultProfile()
	}
	profile.Name = fmt.Sprintf("Antigravity Account Scoped (%016x)", seed)
	profile.ALPNProtocols = []string{"h2", "http/1.1"}
	return profile
}

func rotateUint16Range(values []uint16, start, end, shift int) {
	if end-start <= 1 {
		return
	}
	length := end - start
	shift = shift % length
	if shift == 0 {
		return
	}

	scratch := append([]uint16(nil), values[start:end]...)
	for i := 0; i < length; i++ {
		values[start+i] = scratch[(i+shift)%length]
	}
}

func curveIDsToUint16s(values []utls.CurveID) []uint16 {
	out := make([]uint16, len(values))
	for i, v := range values {
		out[i] = uint16(v)
	}
	return out
}

func signatureSchemesToUint16s(values []utls.SignatureScheme) []uint16 {
	out := make([]uint16, len(values))
	for i, v := range values {
		out[i] = uint16(v)
	}
	return out
}
