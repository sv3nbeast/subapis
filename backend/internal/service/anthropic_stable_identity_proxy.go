package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyurl"
	xproxy "golang.org/x/net/proxy"
)

const anthropicStableIdentityTransportSnapshotVersion = "stable-identity-transport-v1"

// AnthropicStableIdentityTransportHash returns the lifecycle-owned digest of
// the account's fixed outbound route. The digest binds every dialer-affecting
// proxy field without persisting or exposing proxy credentials in plaintext.
func (a *Account) AnthropicStableIdentityTransportHash() string {
	if a == nil || a.Extra == nil {
		return ""
	}
	value, _ := a.Extra[AnthropicStableIdentityTransportHashExtraKey].(string)
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != sha256.Size*2 {
		return ""
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return ""
	}
	return value
}

// ValidateAnthropicStableIdentityProxy verifies that a stable account has one
// complete, non-fallback outbound route. Direct mode remains accepted for
// stable accounts enrolled by older releases; a configured proxy is always
// fail-closed and can never fall back to a backup proxy or direct egress.
func ValidateAnthropicStableIdentityProxy(account *Account) error {
	if account == nil {
		return errors.New("stable identity account is missing")
	}
	if account.ProxyID == nil {
		if account.Proxy != nil || account.ProxyFallbackOriginID != nil {
			return errors.New("stable identity direct route has inconsistent proxy metadata")
		}
		return nil
	}
	if *account.ProxyID <= 0 || account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
		return errors.New("stable identity proxy is not fully hydrated")
	}
	proxy := account.Proxy
	if !proxy.IsActive() {
		return errors.New("stable identity proxy is not active")
	}
	if proxy.IsExpired(time.Now()) {
		return errors.New("stable identity proxy is expired")
	}
	if account.ProxyFallbackOriginID != nil {
		return errors.New("stable identity proxy fallback origin must be empty")
	}
	mode := strings.ToLower(strings.TrimSpace(proxy.FallbackMode))
	if mode != "" && mode != FallbackModeNone {
		return errors.New("stable identity proxy fallback must be disabled")
	}
	if proxy.BackupProxyID != nil {
		return errors.New("stable identity proxy backup must be empty")
	}
	if (proxy.Username == "") != (proxy.Password == "") {
		return errors.New("stable identity proxy authentication must include both username and password")
	}
	scheme := proxy.EffectiveProtocol()
	switch scheme {
	case "http", "https", "socks5h":
	default:
		return errors.New("stable identity proxy protocol is unsupported")
	}
	if normalizeProxyHost(proxy.Host) == "" || proxy.Port <= 0 || proxy.Port > 65535 {
		return errors.New("stable identity proxy host or port is invalid")
	}
	_, parsed, err := proxyurl.Parse(proxy.URL())
	if err != nil || parsed == nil {
		return errors.New("stable identity proxy URL is invalid")
	}
	return nil
}

// ExpectedAnthropicStableIdentityTransportHash computes the immutable route
// snapshot expected by the current hydrated account. It includes the proxy ID,
// endpoint, authentication, status, expiry and fallback configuration. Name
// and timestamps are intentionally excluded because they do not affect egress.
func ExpectedAnthropicStableIdentityTransportHash(account *Account) (string, error) {
	if err := ValidateAnthropicStableIdentityProxy(account); err != nil {
		return "", err
	}
	canonical := anthropicStableIdentityTransportSnapshotVersion + "\x00direct"
	if account.ProxyID != nil {
		canonical = anthropicStableIdentityProxyCanonical(account.Proxy)
	}
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:]), nil
}

func anthropicStableIdentityProxyCanonical(proxy *Proxy) string {
	if proxy == nil {
		return anthropicStableIdentityTransportSnapshotVersion + "\x00invalid"
	}
	expiresAt := ""
	if proxy.ExpiresAt != nil {
		expiresAt = proxy.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}
	backupID := ""
	if proxy.BackupProxyID != nil {
		backupID = strconv.FormatInt(*proxy.BackupProxyID, 10)
	}
	return strings.Join([]string{
		anthropicStableIdentityTransportSnapshotVersion,
		"proxy",
		strconv.FormatInt(proxy.ID, 10),
		proxy.EffectiveProtocol(),
		normalizeProxyHost(proxy.Host),
		strconv.Itoa(proxy.Port),
		proxy.Username,
		proxy.Password,
		strings.ToLower(strings.TrimSpace(proxy.Status)),
		expiresAt,
		anthropicStableIdentityFallbackMode(proxy.FallbackMode),
		backupID,
	}, "\x00")
}

func anthropicStableIdentityFallbackMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return FallbackModeNone
	}
	return mode
}

func anthropicStableIdentityProxyOperationallyEqual(left, right *Proxy) bool {
	return left != nil && right != nil && anthropicStableIdentityProxyCanonical(left) == anthropicStableIdentityProxyCanonical(right)
}

// ensureAnthropicStableIdentityProxyUpdateAllowed protects the proxy row as
// well as the account.proxy_id field. Display-name changes are allowed, but a
// stable account's endpoint, credentials, status, expiry or fallback policy can
// only change after stable mode is disabled and the account has drained.
func (s *adminServiceImpl) ensureAnthropicStableIdentityProxyUpdateAllowed(
	ctx context.Context,
	current *Proxy,
	candidate *Proxy,
) error {
	if s == nil || current == nil || candidate == nil || anthropicStableIdentityProxyOperationallyEqual(current, candidate) {
		return nil
	}
	// Narrow unit services may omit accountRepo entirely. Production admin
	// wiring always provides it; runtime snapshot validation remains the final
	// fail-closed boundary even for out-of-band database mutations.
	if s.accountRepo == nil || s.proxyRepo == nil {
		return nil
	}
	accounts, err := s.proxyRepo.ListAccountSummariesByProxyID(ctx, current.ID)
	if err != nil {
		return err
	}
	for _, summary := range accounts {
		account, loadErr := s.accountRepo.GetByID(ctx, summary.ID)
		if loadErr != nil {
			return loadErr
		}
		if account != nil && account.HasAnthropicStableIdentityManagedFields() &&
			account.ProxyID != nil && *account.ProxyID == current.ID {
			return ErrAnthropicStableIdentityManaged
		}
	}
	return nil
}

func anthropicStableIdentityTransportSnapshotMatches(account *Account, expected string) bool {
	if account == nil || expected == "" {
		return false
	}
	stored := account.AnthropicStableIdentityTransportHash()
	if stored != "" {
		return stored == expected
	}
	// Stable accounts created before proxy support were necessarily direct.
	// Accept their missing snapshot without forcing a production-wide rotation.
	return account.ProxyID == nil && account.Proxy == nil && account.ProxyFallbackOriginID == nil
}

func configureAnthropicStableIdentityTransport(transport *http.Transport, account *Account) error {
	if transport == nil || account == nil {
		return errors.New("stable identity transport is incomplete")
	}
	if err := ValidateAnthropicStableIdentityProxy(account); err != nil {
		return err
	}
	// Never inherit HTTP_PROXY/HTTPS_PROXY/NO_PROXY. A proxy failure therefore
	// terminates the request instead of silently changing the account's egress.
	transport.Proxy = nil
	if account.ProxyID == nil {
		return nil
	}
	_, parsed, err := proxyurl.Parse(account.Proxy.URL())
	if err != nil || parsed == nil {
		return errors.New("stable identity proxy URL is invalid")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsed)
		return nil
	case "socks5", "socks5h":
		baseDialContext := transport.DialContext
		if baseDialContext == nil {
			return errors.New("stable identity base dialer is unavailable")
		}
		forward := &anthropicStableIdentityForwardDialer{dialContext: baseDialContext}
		dialer, dialErr := xproxy.FromURL(parsed, forward)
		if dialErr != nil {
			return fmt.Errorf("create stable identity SOCKS5 dialer: %w", dialErr)
		}
		contextDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return errors.New("stable identity SOCKS5 dialer does not support cancellation")
		}
		transport.Proxy = nil
		transport.DialContext = contextDialer.DialContext
		return nil
	default:
		return errors.New("stable identity proxy protocol is unsupported")
	}
}

type anthropicStableIdentityForwardDialer struct {
	dialContext func(context.Context, string, string) (net.Conn, error)
}

func (d *anthropicStableIdentityForwardDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *anthropicStableIdentityForwardDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if d == nil || d.dialContext == nil {
		return nil, errors.New("stable identity forward dialer is unavailable")
	}
	return d.dialContext(ctx, network, address)
}
