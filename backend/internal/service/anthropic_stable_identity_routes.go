package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

const anthropicStableIdentityRouteRefreshInterval = 5 * time.Second

var errAnthropicStableIdentityRouteUnavailable = errors.New("stable identity route is unavailable")

// AnthropicStableIdentityRoute is an in-memory, redacted fixed-account policy.
// GroupID is bound from the account's current ordinary membership. No API-key
// allow-list is stored: authentication and group membership are already
// enforced by the normal API-key middleware.
type AnthropicStableIdentityRoute struct {
	GroupID           int64
	AccountID         int64
	Generation        int64
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

// deriveAnthropicStableIdentitySessionScope allocates an opaque namespace in
// the upper quarter of BIGINT for the existing durable owner-binding table.
// Account, group, generation, and device are all in the domain so independent
// stable identities cannot collide.
func deriveAnthropicStableIdentitySessionScope(route *AnthropicStableIdentityRoute, groupID int64) (int64, error) {
	if route == nil || groupID <= 0 || route.AccountID <= 0 || route.Generation <= 0 ||
		!IsValidAnthropicStableIdentityDeviceID(route.DeviceID) || len(route.SessionHMACKey) < 32 {
		return 0, errors.New("stable identity session scope inputs are incomplete")
	}
	mac := hmac.New(sha256.New, []byte(route.SessionHMACKey))
	_, _ = fmt.Fprintf(mac, "sub2api:anthropic-stable-identity:scope:v2:%d:%d:%d:%s",
		route.AccountID, groupID, route.Generation, route.DeviceID)
	value := binary.BigEndian.Uint64(mac.Sum(nil)[:8]) & ((uint64(1) << 62) - 1)
	value |= uint64(1) << 62
	return int64(value), nil
}

func stableIdentityRoutePolicyFingerprint(route *AnthropicStableIdentityRoute, groupID int64) (string, error) {
	if route == nil || route.AccountID <= 0 || route.Generation <= 0 || groupID <= 0 ||
		len(route.KeyFingerprint) != 64 {
		return "", errors.New("stable identity route policy inputs are incomplete")
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "sub2api:anthropic-stable-identity:account-policy:v2:%d:%d:%d:%s",
		route.AccountID, route.Generation, groupID, route.KeyFingerprint)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func bindAnthropicStableIdentityRouteGroup(route *AnthropicStableIdentityRoute, groupID int64) error {
	scopeID, err := deriveAnthropicStableIdentitySessionScope(route, groupID)
	if err != nil {
		return err
	}
	policyFingerprint, err := stableIdentityRoutePolicyFingerprint(route, groupID)
	if err != nil {
		return err
	}
	route.GroupID = groupID
	route.SessionScopeID = scopeID
	route.PolicyFingerprint = policyFingerprint
	return nil
}

type anthropicStableIdentityRouteDirectory struct {
	mu            sync.RWMutex
	refreshMu     sync.Mutex
	pools         map[int64][]*AnthropicStableIdentityRoute
	managedGroups map[int64]struct{}
	loadedAt      time.Time
	loaded        bool
}

func newAnthropicStableIdentityRouteDirectory() *anthropicStableIdentityRouteDirectory {
	return &anthropicStableIdentityRouteDirectory{
		pools:         make(map[int64][]*AnthropicStableIdentityRoute),
		managedGroups: make(map[int64]struct{}),
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
	if accountID <= 0 || generation <= 0 {
		return "", errors.New("stable identity session key inputs are incomplete")
	}
	secret, err := anthropicStableIdentityDeploymentSecret(cfg)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "sub2api:anthropic-stable-identity:session:v2:%d:%d", accountID, generation)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func stableIdentityRouteFromAccount(cfg *config.Config, account Account) (*AnthropicStableIdentityRoute, error) {
	if !account.IsAnthropicStableIdentityEnabled() || account.IsAnthropicStableIdentityPaused() || account.IsAnthropicStableIdentityBlocked() {
		return nil, errors.New("stable identity is not active")
	}
	if err := ValidateAnthropicStableIdentityEnrolledAccount(&account); err != nil {
		return nil, err
	}
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
	// Generation can restart at one after a deliberate disable/re-enrol. Bind
	// durable conversations to the complete fixed identity, not just the
	// generation-derived session key, so a newly generated device can never
	// inherit an old route tombstone.
	identityHash := sha256.New()
	_, _ = fmt.Fprintf(identityHash,
		"sub2api:anthropic-stable-identity:identity:v1:%d:%d:%s:%s:%s",
		account.ID, generation, deviceID, profileID, keyFingerprint,
	)
	identityFingerprint := hex.EncodeToString(identityHash.Sum(nil))
	return &AnthropicStableIdentityRoute{
		AccountID:      account.ID,
		Generation:     generation,
		DeviceID:       deviceID,
		ProfileID:      profileID,
		SessionHMACKey: secret,
		KeyFingerprint: identityFingerprint,
		MaxBodyBytes:   AnthropicStableIngressMaxBodyBytes,
		State:          account.AnthropicStableIdentityState(),
		PausedReason:   account.AnthropicStableIdentityBlockedReason(),
	}, nil
}

func (d *anthropicStableIdentityRouteDirectory) replace(pools map[int64][]*AnthropicStableIdentityRoute, managedGroups map[int64]struct{}) {
	d.mu.Lock()
	d.pools = pools
	d.managedGroups = managedGroups
	d.loadedAt = time.Now()
	d.loaded = true
	d.mu.Unlock()
}

// currentAnthropicStableRouteGroupIDs uses eagerly loaded group entities when
// available, excluding inactive or non-Anthropic memberships. Narrow test
// repositories may provide only GroupIDs; the forwarder still performs an
// authoritative group read immediately before upstream egress.
func currentAnthropicStableRouteGroupIDs(account *Account) []int64 {
	if account == nil {
		return nil
	}
	allowed := make(map[int64]struct{}, len(account.GroupIDs))
	if len(account.Groups) > 0 {
		for _, group := range account.Groups {
			if group != nil && group.ID > 0 && group.Platform == PlatformAnthropic && group.IsActive() {
				allowed[group.ID] = struct{}{}
			}
		}
	} else {
		for _, groupID := range account.GroupIDs {
			if groupID > 0 {
				allowed[groupID] = struct{}{}
			}
		}
	}
	ids := make([]int64, 0, len(allowed))
	for groupID := range allowed {
		ids = append(ids, groupID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
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
	pools := make(map[int64][]*AnthropicStableIdentityRoute)
	managedGroups := make(map[int64]struct{})
	for _, account := range accounts {
		if account.Platform != PlatformAnthropic || !account.IsAnthropicStableIdentityEnabled() {
			continue
		}
		groupIDs := currentAnthropicStableRouteGroupIDs(&account)
		for _, groupID := range groupIDs {
			managedGroups[groupID] = struct{}{}
		}
		route, routeErr := stableIdentityRouteFromAccount(s.cfg, account)
		if routeErr != nil {
			logger.LegacyPrintf("service.gateway", "[Anthropic Stable Identity] keep group managed but skip account=%d reason=%v", account.ID, routeErr)
			continue
		}
		for _, groupID := range groupIDs {
			copyRoute := *route
			if bindErr := bindAnthropicStableIdentityRouteGroup(&copyRoute, groupID); bindErr != nil {
				logger.LegacyPrintf("service.gateway", "[Anthropic Stable Identity] skip account=%d group=%d: %v", account.ID, groupID, bindErr)
				continue
			}
			pools[groupID] = append(pools[groupID], &copyRoute)
		}
	}
	for groupID := range pools {
		sort.Slice(pools[groupID], func(i, j int) bool {
			if pools[groupID][i].AccountID == pools[groupID][j].AccountID {
				return pools[groupID][i].Generation < pools[groupID][j].Generation
			}
			return pools[groupID][i].AccountID < pools[groupID][j].AccountID
		})
	}
	d.replace(pools, managedGroups)
	return nil
}

func cloneAnthropicStableIdentityRoute(route *AnthropicStableIdentityRoute) *AnthropicStableIdentityRoute {
	if route == nil {
		return nil
	}
	copyRoute := *route
	return &copyRoute
}

func cloneAnthropicStableIdentityPool(routes []*AnthropicStableIdentityRoute) []*AnthropicStableIdentityRoute {
	out := make([]*AnthropicStableIdentityRoute, 0, len(routes))
	for _, route := range routes {
		if route != nil {
			out = append(out, cloneAnthropicStableIdentityRoute(route))
		}
	}
	return out
}

// lookupAnthropicStableIdentityPool returns managed=true for a stable group
// even when all accounts are paused/blocked. Such traffic must fail closed and
// must never drift into the generic scheduler.
func (s *GatewayService) lookupAnthropicStableIdentityPool(ctx context.Context, groupID int64) ([]*AnthropicStableIdentityRoute, bool, error) {
	if s == nil || s.accountRepo == nil || groupID <= 0 || s.anthropicStableIdentityRoutes == nil {
		return nil, false, nil
	}
	d := s.anthropicStableIdentityRoutes
	d.mu.RLock()
	if d.snapshotFresh(time.Now()) {
		_, managed := d.managedGroups[groupID]
		pool := cloneAnthropicStableIdentityPool(d.pools[groupID])
		d.mu.RUnlock()
		return pool, managed, nil
	}
	d.mu.RUnlock()

	if err := s.RefreshAnthropicStableIdentityRoutes(ctx); err != nil {
		d.mu.RLock()
		loaded := d.loaded
		_, managed := d.managedGroups[groupID]
		d.mu.RUnlock()
		if !loaded || managed {
			return nil, true, err
		}
		return nil, false, nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, managed := d.managedGroups[groupID]
	return cloneAnthropicStableIdentityPool(d.pools[groupID]), managed, nil
}

func (s *GatewayService) HasAnthropicStableIdentityGroup(ctx context.Context, groupID int64) (bool, error) {
	_, managed, err := s.lookupAnthropicStableIdentityPool(ctx, groupID)
	return managed, err
}

func selectAnthropicStableIdentityCandidate(sessionHash string, pool []*AnthropicStableIdentityRoute) *AnthropicStableIdentityRoute {
	var selected *AnthropicStableIdentityRoute
	var selectedScore [sha256.Size]byte
	for _, route := range pool {
		if route == nil || route.AccountID <= 0 || route.Generation <= 0 {
			continue
		}
		score := sha256.Sum256([]byte(fmt.Sprintf(
			"sub2api:anthropic-stable-identity:rendezvous:v1:%s:%d:%d:%s",
			sessionHash, route.AccountID, route.Generation, route.KeyFingerprint,
		)))
		if selected == nil || bytes.Compare(score[:], selectedScore[:]) > 0 {
			selected = route
			selectedScore = score
		}
	}
	return selected
}

// ResolveAnthropicStableIdentityRoute selects one account only for a new
// logical session. The repository returns the first durable binding for later
// turns, so adding/removing pool accounts cannot migrate an existing session.
func (s *GatewayService) ResolveAnthropicStableIdentityRoute(
	ctx context.Context,
	groupID, ownerUserID int64,
	sessionID string,
) (*AnthropicStableIdentityRoute, bool, error) {
	pool, managed, err := s.lookupAnthropicStableIdentityPool(ctx, groupID)
	if err != nil || !managed {
		return nil, managed, err
	}
	if len(pool) == 0 {
		return nil, true, errAnthropicStableIdentityRouteUnavailable
	}
	sessionHash, err := hashAnthropicStableIdentityPoolSession(s.cfg, groupID, ownerUserID, sessionID)
	if err != nil {
		return nil, true, err
	}
	candidate := selectAnthropicStableIdentityCandidate(sessionHash, pool)
	if candidate == nil {
		return nil, true, errAnthropicStableIdentityRouteUnavailable
	}
	repo, ok := s.accountRepo.(AnthropicStableIdentitySessionRouteRepository)
	if !ok || repo == nil {
		return nil, true, ErrAnthropicStableIdentitySessionRouteUnavailable
	}
	bound, err := repo.ResolveAnthropicStableIdentitySessionRoute(ctx, AnthropicStableIdentitySessionRouteBinding{
		GroupID:             groupID,
		OwnerUserID:         ownerUserID,
		SessionHash:         sessionHash,
		AccountID:           candidate.AccountID,
		AccountGeneration:   candidate.Generation,
		IdentityFingerprint: candidate.KeyFingerprint,
		CandidateDeviceID:   candidate.DeviceID,
		CandidateProfileID:  candidate.ProfileID,
	})
	if err != nil || bound == nil {
		if err == nil {
			err = ErrAnthropicStableIdentitySessionRouteUnavailable
		}
		return nil, true, err
	}
	for _, route := range pool {
		if route != nil && route.AccountID == bound.AccountID &&
			route.Generation == bound.AccountGeneration &&
			route.KeyFingerprint == bound.IdentityFingerprint {
			return cloneAnthropicStableIdentityRoute(route), true, nil
		}
	}
	// Existing sessions fail closed if their account is paused, blocked,
	// disabled, re-enrolled, or removed from the group. Never switch identity.
	return nil, true, errAnthropicStableIdentityRouteUnavailable
}

// GetAnthropicStableIdentityAccount reloads the account after durable routing
// and again under the forwarder's cross-process account lease.
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

func (s *GatewayService) InvalidateAnthropicStableIdentityRoutes() {
	if s != nil && s.anthropicStableIdentityRoutes != nil {
		s.anthropicStableIdentityRoutes.invalidate()
	}
}

func (s *GatewayService) ClearAnthropicStableIdentityRuntimeBlock(accountID int64) {
	if s != nil && s.anthropicStableCanary != nil {
		s.anthropicStableCanary.clearBlock(accountID)
	}
}

func (r *AnthropicStableIdentityRoute) IsPaused() bool {
	return r != nil && NormalizeAnthropicStableIdentityState(r.State) == AnthropicStableIdentityStatePaused
}
