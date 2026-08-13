package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// These errors are intentionally stable so the HTTP layer can distinguish a
// session ownership collision from a temporary binding-store outage without
// exposing storage details to the caller.
var (
	ErrAnthropicStableCanarySessionOwnerConflict      = errors.New("anthropic stable canary session belongs to another owner")
	ErrAnthropicStableCanarySessionBindingUnavailable = errors.New("anthropic stable canary session binding is unavailable")
)

// AnthropicStableCanarySessionBindingRepository is the durable routing seam for
// shared stable accounts. Implementations must atomically insert-or-verify a
// binding and must never persist the raw Claude session UUID.
type AnthropicStableCanarySessionBindingRepository interface {
	ClaimAnthropicStableCanarySession(ctx context.Context, groupID, accountID, generation, ownerUserID int64, sessionHash, keyFingerprint, policyFingerprint string) error
}

// HashAnthropicStableCanarySessionForRouting derives a stable, non-reversible
// routing key. The group id is part of the MAC domain so the same client UUID
// cannot collide across canary groups. The raw session id is never persisted.
func HashAnthropicStableCanarySessionForRouting(secret string, groupID, generation int64, sessionID string) (string, error) {
	secret = strings.TrimSpace(secret)
	sessionID = strings.TrimSpace(sessionID)
	if secret == "" || groupID <= 0 || generation <= 0 || sessionID == "" {
		return "", errors.New("stable session routing inputs are incomplete")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "sub2api:anthropic-stable-canary:session:v2:%d:%d:", groupID, generation)
	_, _ = mac.Write([]byte(sessionID))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func FingerprintAnthropicStableCanarySessionKey(secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 32 {
		return "", errors.New("stable session routing key must contain at least 32 characters")
	}
	sum := sha256.Sum256([]byte("sub2api:anthropic-stable-canary:key-fingerprint:v1\x00" + secret))
	return hex.EncodeToString(sum[:]), nil
}

// FingerprintAnthropicStableCanarySharedPolicy locks the complete API-key
// allow-list without persisting any API key values. Ordering is normalized,
// while invalid or duplicate identifiers fail closed.
func FingerprintAnthropicStableCanarySharedPolicy(apiKeyIDs []int64) (string, error) {
	if len(apiKeyIDs) == 0 || len(apiKeyIDs) > 32 {
		return "", errors.New("stable shared API key allow-list is invalid")
	}
	ids := append([]int64(nil), apiKeyIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	hash := sha256.New()
	_, _ = hash.Write([]byte("sub2api:anthropic-stable-canary:shared-policy:v1\x00"))
	for i, id := range ids {
		if id <= 0 || (i > 0 && id == ids[i-1]) {
			return "", errors.New("stable shared API key allow-list is invalid")
		}
		_, _ = hash.Write([]byte(strconv.FormatInt(id, 10)))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// AnthropicStableCanarySharedUsersEnabled reports the explicit D2 opt-in. It
// is separate from the D1 owner/key fields so enabling it cannot silently
// broaden an existing single-owner canary.
func (s *GatewayService) AnthropicStableCanarySharedUsersEnabled() bool {
	return s != nil && s.cfg != nil && s.cfg.Gateway.AnthropicStableCanary.Enabled &&
		s.cfg.Gateway.AnthropicStableCanary.SharedUsers
}

// AnthropicStableCanaryPrincipalAllowed applies the route-level principal
// gate. In shared mode the normal API-key middleware remains authoritative;
// this check only confirms that the authenticated subject owns the presented
// key. In D1 mode the exact configured owner/key pair is still required.
func (s *GatewayService) AnthropicStableCanaryPrincipalAllowed(userID int64, apiKey *APIKey) bool {
	if s == nil || s.cfg == nil || !s.cfg.Gateway.AnthropicStableCanary.Enabled || userID <= 0 || apiKey == nil {
		return false
	}
	canary := s.cfg.Gateway.AnthropicStableCanary
	if apiKey.ID <= 0 || apiKey.UserID != userID || apiKey.Status != StatusAPIKeyActive || apiKey.IsExpired() {
		return false
	}
	if apiKey.GroupID == nil || *apiKey.GroupID != canary.GroupID {
		return false
	}
	if canary.SharedUsers {
		for _, allowedID := range canary.SharedAPIKeyIDs {
			if apiKey.ID == allowedID {
				return true
			}
		}
		return false
	}
	return canary.OwnerUserID > 0 && canary.APIKeyID > 0 && userID == canary.OwnerUserID && apiKey.ID == canary.APIKeyID
}

// ClaimAnthropicStableCanarySession binds one client session to one user and
// the configured account before the first upstream write. Existing bindings
// may be reused by the same owner only; a different owner is rejected. D1 is
// deliberately a no-op because its owner gate already provides isolation.
func (s *GatewayService) ClaimAnthropicStableCanarySession(ctx context.Context, groupID, ownerUserID int64, sessionID string) error {
	if !s.AnthropicStableCanarySharedUsersEnabled() {
		return nil
	}
	if ownerUserID <= 0 || groupID <= 0 || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("%w: invalid session identity", ErrAnthropicStableCanarySessionBindingUnavailable)
	}
	canary, err := s.anthropicStableCanaryConfig()
	if err != nil || canary.GroupID != groupID {
		if err == nil {
			err = errors.New("stable canary group mismatch")
		}
		return fmt.Errorf("%w: %v", ErrAnthropicStableCanarySessionBindingUnavailable, err)
	}
	secret := canary.SessionHMACKey
	sessionHash, err := HashAnthropicStableCanarySessionForRouting(secret, groupID, canary.SessionGeneration, sessionID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAnthropicStableCanarySessionBindingUnavailable, err)
	}
	keyFingerprint, err := FingerprintAnthropicStableCanarySessionKey(secret)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAnthropicStableCanarySessionBindingUnavailable, err)
	}
	policyFingerprint, err := FingerprintAnthropicStableCanarySharedPolicy(canary.SharedAPIKeyIDs)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAnthropicStableCanarySessionBindingUnavailable, err)
	}
	repo, ok := s.accountRepo.(AnthropicStableCanarySessionBindingRepository)
	if !ok || repo == nil {
		return ErrAnthropicStableCanarySessionBindingUnavailable
	}
	if err := repo.ClaimAnthropicStableCanarySession(ctx, groupID, canary.AccountID, canary.SessionGeneration, ownerUserID, sessionHash, keyFingerprint, policyFingerprint); err != nil {
		return err
	}
	return nil
}
