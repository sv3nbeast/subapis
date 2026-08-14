package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const anthropicStableIdentityRouteRefreshInterval = 5 * time.Second

var errAnthropicStableIdentityRouteUnavailable = errors.New("stable identity route is unavailable")

type anthropicStableIdentityRouteKey struct {
	groupID  int64
	apiKeyID int64
}

// AnthropicStableIdentityRoute is an in-memory, redacted runtime policy. The
// derived HMAC key never leaves the process and is regenerated from the server
// secret plus the account generation after every restart.
type AnthropicStableIdentityRoute struct {
	GroupID           int64
	AccountID         int64
	Generation        int64
	APIKeyIDs         []int64
	APIKeyGroupIDs    map[int64]int64
	DeviceID          string
	ProfileID         string
	SessionHMACKey    string
	SessionScopeID    int64
	KeyFingerprint    string
	PolicyFingerprint string
	MaxBodyBytes      int64
	State             string
	PausedReason      string
}

// deriveAnthropicStableIdentitySessionScope allocates an opaque positive
// namespace in the upper quarter of BIGINT. The durable canary tables use a
// column historically named group_id, but existing-group mode can have many
// accounts (and repeated enrollments) in one real group. Scoping by account,
// group, generation and device prevents those independent identities from
// colliding without changing the established canary schema. Normal sequence
// group IDs remain in the low range and therefore cannot overlap this domain.
func deriveAnthropicStableIdentitySessionScope(route *AnthropicStableIdentityRoute, groupID int64) (int64, error) {
	if route == nil || groupID <= 0 || route.AccountID <= 0 || route.Generation <= 0 ||
		!IsValidAnthropicStableIdentityDeviceID(route.DeviceID) || len(route.SessionHMACKey) < 32 {
		return 0, errors.New("stable identity session scope inputs are incomplete")
	}
	mac := hmac.New(sha256.New, []byte(route.SessionHMACKey))
	_, _ = fmt.Fprintf(mac, "sub2api:anthropic-stable-identity:scope:v1:%d:%d:%d:%s",
		route.AccountID, groupID, route.Generation, route.DeviceID)
	value := binary.BigEndian.Uint64(mac.Sum(nil)[:8]) & ((uint64(1) << 62) - 1)
	value |= uint64(1) << 62
	return int64(value), nil
}

func bindAnthropicStableIdentityRouteGroup(route *AnthropicStableIdentityRoute, groupID int64) error {
	scopeID, err := deriveAnthropicStableIdentitySessionScope(route, groupID)
	if err != nil {
		return err
	}
	route.GroupID = groupID
	route.SessionScopeID = scopeID
	return nil
}

type anthropicStableIdentityRouteDirectory struct {
	mu          sync.RWMutex
	refreshMu   sync.Mutex
	routes      map[anthropicStableIdentityRouteKey]*AnthropicStableIdentityRoute
	ambiguous   map[anthropicStableIdentityRouteKey]struct{}
	managedKeys map[int64]struct{}
	loadedAt    time.Time
	loaded      bool
}

func newAnthropicStableIdentityRouteDirectory() *anthropicStableIdentityRouteDirectory {
	return &anthropicStableIdentityRouteDirectory{
		routes:      make(map[anthropicStableIdentityRouteKey]*AnthropicStableIdentityRoute),
		ambiguous:   make(map[anthropicStableIdentityRouteKey]struct{}),
		managedKeys: make(map[int64]struct{}),
	}
}

func (d *anthropicStableIdentityRouteDirectory) invalidate() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.loadedAt = time.Time{}
	d.loaded = false
	d.mu.Unlock()
}

func (d *anthropicStableIdentityRouteDirectory) snapshotFresh(now time.Time) bool {
	return d != nil && d.loaded && !d.loadedAt.IsZero() && now.Sub(d.loadedAt) < anthropicStableIdentityRouteRefreshInterval
}

func deriveAnthropicStableIdentitySessionKey(cfg *config.Config, accountID, generation int64) (string, error) {
	if cfg == nil || accountID <= 0 || generation <= 0 {
		return "", errors.New("stable identity session key inputs are incomplete")
	}
	secret := strings.TrimSpace(cfg.JWT.Secret)
	if len(secret) < 32 {
		// TOTP's encryption key is also a deployment-scoped high entropy secret.
		// It is only a fallback for installations that intentionally leave JWT
		// configuration to the bootstrap wizard; the domain separator keeps the
		// derived value independent from TOTP ciphertext.
		secret = strings.TrimSpace(cfg.Totp.EncryptionKey)
	}
	if len(secret) < 32 {
		return "", errors.New("stable identity requires a configured server secret")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "sub2api:anthropic-stable-identity:session:v1:%d:%d", accountID, generation)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func stableIdentityPolicyFingerprint(groupIDs, apiKeyIDs []int64, keyGroups map[int64]int64) (string, error) {
	groups, err := normalizeStableIdentityIDs(groupIDs)
	if err != nil {
		return "", err
	}
	keys, err := normalizeStableIdentityIDs(apiKeyIDs)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("sub2api:anthropic-stable-identity:policy:v1\x00"))
	for _, id := range groups {
		_, _ = fmt.Fprintf(hash, "g:%d\x00", id)
	}
	for _, id := range keys {
		groupID := keyGroups[id]
		if groupID <= 0 || !containsStableIdentityID(groups, groupID) {
			return "", errors.New("stable identity API-key route mapping is incomplete")
		}
		_, _ = fmt.Fprintf(hash, "k:%d:g:%d\x00", id, groupID)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func stableIdentityRouteFromAccount(cfg *config.Config, account Account) (*AnthropicStableIdentityRoute, error) {
	if !account.IsAnthropicStableIdentityEnabled() {
		return nil, errors.New("stable identity is not active")
	}
	if account.IsAnthropicStableIdentityBlocked() {
		return nil, errors.New("stable identity is blocked")
	}
	if err := ValidateAnthropicStableIdentityEnrolledAccount(&account); err != nil {
		return nil, err
	}
	groupIDs, _ := normalizeStableIdentityIDs(account.AnthropicStableIdentityGroupIDs())
	keyIDs, err := normalizeStableIdentityIDs(account.AnthropicStableIdentityAPIKeyIDs())
	if err != nil {
		return nil, err
	}
	keyGroups := account.AnthropicStableIdentityAPIKeyGroupIDs()
	deviceID := account.AnthropicStableIdentityDeviceID()
	if !IsValidAnthropicStableIdentityDeviceID(deviceID) {
		return nil, errors.New("stable identity device id is invalid")
	}
	profileID := account.AnthropicStableIdentityProfileID()
	if !IsKnownAnthropicStableCanaryProfile(profileID) {
		return nil, errors.New("stable identity profile is not reviewed")
	}
	generation := account.AnthropicStableIdentityGeneration()
	if generation <= 0 {
		return nil, errors.New("stable identity generation is invalid")
	}
	secret, err := deriveAnthropicStableIdentitySessionKey(cfg, account.ID, generation)
	if err != nil {
		return nil, err
	}
	keyFingerprint, err := FingerprintAnthropicStableCanarySessionKey(secret)
	if err != nil {
		return nil, err
	}
	policyFingerprint, err := stableIdentityPolicyFingerprint(groupIDs, keyIDs, keyGroups)
	if err != nil {
		return nil, err
	}
	return &AnthropicStableIdentityRoute{
		AccountID:         account.ID,
		Generation:        generation,
		APIKeyIDs:         keyIDs,
		APIKeyGroupIDs:    keyGroups,
		DeviceID:          deviceID,
		ProfileID:         profileID,
		SessionHMACKey:    secret,
		KeyFingerprint:    keyFingerprint,
		PolicyFingerprint: policyFingerprint,
		MaxBodyBytes:      AnthropicStableIngressMaxBodyBytes,
		State:             account.AnthropicStableIdentityState(),
		PausedReason:      account.AnthropicStableIdentityBlockedReason(),
	}, nil
}

func (d *anthropicStableIdentityRouteDirectory) replace(routes map[anthropicStableIdentityRouteKey]*AnthropicStableIdentityRoute, ambiguous map[anthropicStableIdentityRouteKey]struct{}, managedKeys map[int64]struct{}) {
	d.mu.Lock()
	d.routes = routes
	d.ambiguous = ambiguous
	d.managedKeys = managedKeys
	d.loadedAt = time.Now()
	d.loaded = true
	d.mu.Unlock()
}

func (s *GatewayService) RefreshAnthropicStableIdentityRoutes(ctx context.Context) error {
	if s == nil || s.accountRepo == nil || s.anthropicStableIdentityRoutes == nil {
		return errors.New("stable identity route directory is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d := s.anthropicStableIdentityRoutes
	d.refreshMu.Lock()
	defer d.refreshMu.Unlock()
	d.mu.RLock()
	fresh := d.snapshotFresh(time.Now())
	d.mu.RUnlock()
	if fresh {
		return nil
	}
	accounts, err := s.accountRepo.FindByExtraField(ctx, AnthropicStableIdentityEnabledExtraKey, true)
	if err != nil {
		return fmt.Errorf("load stable identity accounts: %w", err)
	}
	routes := make(map[anthropicStableIdentityRouteKey]*AnthropicStableIdentityRoute)
	ambiguous := make(map[anthropicStableIdentityRouteKey]struct{})
	managedKeys := make(map[int64]struct{})
	for _, account := range accounts {
		for _, keyID := range account.AnthropicStableIdentityAPIKeyIDs() {
			if keyID > 0 {
				managedKeys[keyID] = struct{}{}
			}
		}
		route, routeErr := stableIdentityRouteFromAccount(s.cfg, account)
		if routeErr != nil {
			logger.LegacyPrintf("service.gateway", "[Anthropic Stable Identity] skip account=%d reason=%v", account.ID, routeErr)
			continue
		}
		for _, groupID := range account.AnthropicStableIdentityGroupIDs() {
			if !accountBelongsToGroup(&account, groupID) {
				logger.LegacyPrintf("service.gateway", "[Anthropic Stable Identity] skip account=%d group=%d: account is not bound to group", account.ID, groupID)
				continue
			}
			for _, keyID := range route.APIKeyIDs {
				if route.APIKeyGroupIDs[keyID] != groupID {
					continue
				}
				key := anthropicStableIdentityRouteKey{groupID: groupID, apiKeyID: keyID}
				if _, exists := routes[key]; exists {
					delete(routes, key)
					ambiguous[key] = struct{}{}
					continue
				}
				if _, exists := ambiguous[key]; exists {
					continue
				}
				copyRoute := *route
				copyRoute.APIKeyIDs = append([]int64(nil), route.APIKeyIDs...)
				copyRoute.APIKeyGroupIDs = cloneStableIdentityKeyGroups(route.APIKeyGroupIDs)
				if bindErr := bindAnthropicStableIdentityRouteGroup(&copyRoute, groupID); bindErr != nil {
					logger.LegacyPrintf("service.gateway", "[Anthropic Stable Identity] skip account=%d group=%d: %v", account.ID, groupID, bindErr)
					continue
				}
				routes[key] = &copyRoute
			}
		}
	}
	s.anthropicStableIdentityRoutes.replace(routes, ambiguous, managedKeys)
	return nil
}

// GetAnthropicStableIdentityAccount reloads the account behind a route from
// the repository.  Route entries are intentionally short-lived; the fresh
// read closes the window where an operator rotates credentials, pauses the
// account, or changes the selected API-key set while a request is waiting for
// a local/cross-process lease.
func (s *GatewayService) GetAnthropicStableIdentityAccount(ctx context.Context, route *AnthropicStableIdentityRoute) (*Account, error) {
	if s == nil || s.accountRepo == nil || route == nil || route.AccountID <= 0 || route.GroupID <= 0 {
		return nil, errAnthropicStableIdentityRouteUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	account, err := s.accountRepo.GetByID(ctx, route.AccountID)
	if err != nil {
		return nil, err
	}
	if account == nil || !account.IsAnthropicStableIdentityEnabled() || account.IsAnthropicStableIdentityPaused() || account.IsAnthropicStableIdentityBlocked() {
		return nil, errAnthropicStableIdentityRouteUnavailable
	}
	if err := ValidateAnthropicStableIdentityEnrolledAccount(account); err != nil {
		return nil, errAnthropicStableIdentityRouteUnavailable
	}
	if account.AnthropicStableIdentityGeneration() != route.Generation ||
		account.AnthropicStableIdentityDeviceID() != route.DeviceID ||
		!AnthropicStableIngressProfilesEquivalent(account.AnthropicStableIdentityProfileID(), route.ProfileID) ||
		!accountBelongsToGroup(account, route.GroupID) {
		return nil, errAnthropicStableIdentityRouteUnavailable
	}
	return account, nil
}

func accountBelongsToGroup(account *Account, groupID int64) bool {
	if account == nil || groupID <= 0 {
		return false
	}
	for _, id := range account.GroupIDs {
		if id == groupID {
			return true
		}
	}
	for _, binding := range account.AccountGroups {
		if binding.GroupID == groupID {
			return true
		}
	}
	return false
}

// LookupAnthropicStableIdentityRoute returns (route, true, nil) only for an
// explicitly allow-listed API key. Unknown clients/keys stay on the existing
// scheduler path. Ambiguous enrollment fails closed rather than choosing an
// arbitrary account.
func (s *GatewayService) LookupAnthropicStableIdentityRoute(ctx context.Context, groupID, apiKeyID int64) (*AnthropicStableIdentityRoute, bool, error) {
	// Alternate/simple-mode GatewayService instances may intentionally omit an
	// account repository. In that shape stable identity is not installed, so it
	// must remain an optional no-op rather than intercepting every Claude Code
	// request as an uninitialized fail-closed directory. Production instances
	// have a repository; once installed, load/refresh failures still fail managed
	// and cold-start Claude traffic closed below.
	if s == nil || s.accountRepo == nil || groupID <= 0 || apiKeyID <= 0 || s.anthropicStableIdentityRoutes == nil {
		return nil, false, nil
	}
	d := s.anthropicStableIdentityRoutes
	now := time.Now()
	d.mu.RLock()
	fresh := d.snapshotFresh(now)
	if fresh {
		key := anthropicStableIdentityRouteKey{groupID: groupID, apiKeyID: apiKeyID}
		if _, blocked := d.ambiguous[key]; blocked {
			d.mu.RUnlock()
			return nil, true, errors.New("stable identity route is ambiguous")
		}
		route := d.routes[key]
		if route == nil {
			_, managed := d.managedKeys[apiKeyID]
			d.mu.RUnlock()
			if managed {
				return nil, true, errAnthropicStableIdentityRouteUnavailable
			}
			return nil, false, nil
		}
		copyRoute := *route
		copyRoute.APIKeyIDs = append([]int64(nil), route.APIKeyIDs...)
		copyRoute.APIKeyGroupIDs = cloneStableIdentityKeyGroups(route.APIKeyGroupIDs)
		d.mu.RUnlock()
		return &copyRoute, true, nil
	}
	d.mu.RUnlock()
	if err := s.RefreshAnthropicStableIdentityRoutes(ctx); err != nil {
		// Preserve the last known deny-only set on transient repository errors.
		// Ordinary keys must not suffer a global Anthropic outage merely because
		// the optional stable directory could not refresh; a key that was already
		// known as managed still fails closed.
		d.mu.RLock()
		loaded := d.loaded
		_, managed := d.managedKeys[apiKeyID]
		d.mu.RUnlock()
		if !loaded || managed {
			// With no authoritative snapshot at all, availability and identity
			// cannot both be proven. Fail exact Claude Code traffic closed rather
			// than accidentally sending an enrolled key through another account.
			return nil, true, err
		}
		return nil, false, nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	key := anthropicStableIdentityRouteKey{groupID: groupID, apiKeyID: apiKeyID}
	if _, blocked := d.ambiguous[key]; blocked {
		return nil, true, errors.New("stable identity route is ambiguous")
	}
	route := d.routes[key]
	if route == nil {
		if _, managed := d.managedKeys[apiKeyID]; managed {
			return nil, true, errAnthropicStableIdentityRouteUnavailable
		}
		return nil, false, nil
	}
	copyRoute := *route
	copyRoute.APIKeyIDs = append([]int64(nil), route.APIKeyIDs...)
	copyRoute.APIKeyGroupIDs = cloneStableIdentityKeyGroups(route.APIKeyGroupIDs)
	return &copyRoute, true, nil
}

// InvalidateAnthropicStableIdentityRoutes is called by the admin lifecycle
// after a mutation. It deliberately does not synchronously touch the gateway
// hot path; the next lookup performs one bounded refresh.
func (s *GatewayService) InvalidateAnthropicStableIdentityRoutes() {
	if s != nil && s.anthropicStableIdentityRoutes != nil {
		s.anthropicStableIdentityRoutes.invalidate()
	}
}

// ClearAnthropicStableIdentityRuntimeBlock is called only after an explicit
// successful configure/resume/disable lifecycle action. Durable state remains
// authoritative; this merely removes the process-local fast-fail copy so an
// operator recovery does not require a service restart.
func (s *GatewayService) ClearAnthropicStableIdentityRuntimeBlock(accountID int64) {
	if s != nil && s.anthropicStableCanary != nil {
		s.anthropicStableCanary.clearBlock(accountID)
	}
}

func (r *AnthropicStableIdentityRoute) AllowsAPIKey(apiKeyID int64) bool {
	if r == nil || apiKeyID <= 0 {
		return false
	}
	for _, id := range r.APIKeyIDs {
		if id == apiKeyID {
			return true
		}
	}
	return false
}

func (r *AnthropicStableIdentityRoute) IsPaused() bool {
	return r != nil && NormalizeAnthropicStableIdentityState(r.State) == AnthropicStableIdentityStatePaused
}

func cloneStableIdentityKeyGroups(in map[int64]int64) map[int64]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int64]int64, len(in))
	for keyID, groupID := range in {
		out[keyID] = groupID
	}
	return out
}
