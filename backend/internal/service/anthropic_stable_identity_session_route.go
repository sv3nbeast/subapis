package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

var ErrAnthropicStableIdentitySessionRouteUnavailable = errors.New("anthropic stable identity session route is unavailable")

// AnthropicStableIdentitySessionRouteBinding is the durable result of a
// session's first stable-pool selection. Raw Claude session IDs are never
// persisted; SessionHash is an HMAC derived with the deployment secret.
type AnthropicStableIdentitySessionRouteBinding struct {
	GroupID             int64
	OwnerUserID         int64
	SessionHash         string
	AccountID           int64
	AccountGeneration   int64
	IdentityFingerprint string
	// CandidateDeviceID and CandidateProfileID are admission-only values. They
	// are not persisted in the route table; the repository uses them to prove
	// that a new binding still targets the exact live identity represented by a
	// possibly cached route snapshot. Existing bindings ignore these fields.
	CandidateDeviceID  string
	CandidateProfileID string
}

// AnthropicStableIdentitySessionRouteRepository atomically inserts the first
// candidate for a session or returns the already bound identity. Implementors
// must never replace an existing account binding when pool membership changes.
type AnthropicStableIdentitySessionRouteRepository interface {
	ResolveAnthropicStableIdentitySessionRoute(
		ctx context.Context,
		candidate AnthropicStableIdentitySessionRouteBinding,
	) (*AnthropicStableIdentitySessionRouteBinding, error)
}

func anthropicStableIdentityDeploymentSecret(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", errors.New("stable identity server configuration is unavailable")
	}
	secret := strings.TrimSpace(cfg.JWT.Secret)
	if len(secret) < 32 {
		secret = strings.TrimSpace(cfg.Totp.EncryptionKey)
	}
	if len(secret) < 32 {
		return "", errors.New("stable identity requires a configured server secret")
	}
	return secret, nil
}

// hashAnthropicStableIdentityPoolSession creates one cross-instance lookup key
// before an account has been selected. ownerUserID is in the MAC domain, so
// users with the same client session UUID remain independent conversations.
func hashAnthropicStableIdentityPoolSession(cfg *config.Config, groupID, ownerUserID int64, sessionID string) (string, error) {
	if groupID <= 0 || ownerUserID <= 0 || strings.TrimSpace(sessionID) == "" {
		return "", errors.New("stable identity pool session inputs are incomplete")
	}
	secret, err := anthropicStableIdentityDeploymentSecret(cfg)
	if err != nil {
		return "", err
	}
	keyMAC := hmac.New(sha256.New, []byte(secret))
	_, _ = keyMAC.Write([]byte("sub2api:anthropic-stable-identity:pool-key:v1"))
	poolKey := keyMAC.Sum(nil)

	mac := hmac.New(sha256.New, poolKey)
	_, _ = fmt.Fprintf(mac, "sub2api:anthropic-stable-identity:pool-session:v1:%d:%d:", groupID, ownerUserID)
	_, _ = mac.Write([]byte(strings.TrimSpace(sessionID)))
	return hex.EncodeToString(mac.Sum(nil)), nil
}
