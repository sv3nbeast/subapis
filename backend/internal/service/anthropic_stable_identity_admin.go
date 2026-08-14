package service

// Account-scoped Anthropic stable identity lifecycle.
//
// Stable mode is deliberately one account-level switch. The account's current
// Anthropic group memberships are the live pool definition; the lifecycle does
// not store a second group list, select API keys, create groups, or roll group
// membership back. This keeps normal group administration authoritative while
// the fixed device and scheduler reservation remain protected.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

var (
	ErrAnthropicStableIdentityInvalid  = infraerrors.BadRequest("ANTHROPIC_STABLE_IDENTITY_INVALID", "invalid Anthropic stable identity configuration")
	ErrAnthropicStableIdentityConflict = infraerrors.Conflict("ANTHROPIC_STABLE_IDENTITY_CONFLICT", "Anthropic stable identity configuration changed concurrently")
)

// AnthropicStableIdentityAdminService remains a narrow optional capability so
// existing AdminService doubles and alternate deployments do not need a broad
// interface change.
type AnthropicStableIdentityAdminService interface {
	GetAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error)
	ConfigureAnthropicStableIdentity(ctx context.Context, accountID int64, input *AnthropicStableIdentityConfigureInput) (*AnthropicStableIdentityStatus, error)
	PauseAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error)
	ResumeAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error)
	DisableAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error)
}

// AnthropicStableIdentityConfigureInput intentionally exposes no routing
// selectors. ProfileID and DeviceID are retained only as internal/test seams;
// the HTTP handler never accepts them from the browser.
type AnthropicStableIdentityConfigureInput struct {
	ProfileID string
	DeviceID  string
}

// AnthropicStableIdentityStatus is redacted. GroupIDs are derived from current
// account membership rather than duplicated lifecycle metadata.
type AnthropicStableIdentityStatus struct {
	AccountID           int64   `json:"account_id"`
	Enabled             bool    `json:"enabled"`
	State               string  `json:"state"`
	Blocked             bool    `json:"blocked"`
	BlockedReason       string  `json:"blocked_reason,omitempty"`
	GroupIDs            []int64 `json:"group_ids"`
	Generation          int64   `json:"generation"`
	ProfileID           string  `json:"profile_id"`
	DeviceFingerprint   string  `json:"device_fingerprint,omitempty"`
	DeviceConfigured    bool    `json:"device_configured"`
	Concurrency         int     `json:"concurrency"`
	Schedulable         bool    `json:"schedulable"`
	PreviousSchedulable *bool   `json:"previous_schedulable,omitempty"`
	RequiresRestart     bool    `json:"requires_restart"`
	ConfiguredAt        string  `json:"configured_at,omitempty"`
	UpdatedAt           string  `json:"updated_at,omitempty"`
}

var anthropicStableIdentityAdminMu sync.Mutex

func (s *adminServiceImpl) GetAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return anthropicStableIdentityStatusFromAccount(account), nil
}

func (s *adminServiceImpl) ConfigureAnthropicStableIdentity(ctx context.Context, accountID int64, input *AnthropicStableIdentityConfigureInput) (*AnthropicStableIdentityStatus, error) {
	if s == nil || s.accountRepo == nil || s.groupRepo == nil {
		return nil, errors.New("Anthropic stable identity administration is unavailable")
	}
	if input == nil {
		input = &AnthropicStableIdentityConfigureInput{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	anthropicStableIdentityAdminMu.Lock()
	defer anthropicStableIdentityAdminMu.Unlock()
	releaseLease, err := s.acquireAnthropicStableIdentityAdminLease(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer releaseAnthropicStableIdentityAdminLease(accountID, releaseLease)

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	// Stable identity supersedes the older OAuth strict-passthrough mode. Clear
	// that mutually exclusive flag as part of the same account update so the
	// operator only needs the stable-mode switch and no intermediate save.
	if account.Extra != nil {
		delete(account.Extra, "anthropic_oauth_passthrough")
	}
	if err := ValidateAnthropicStableIdentityAccount(account); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnthropicStableIdentityInvalid, err)
	}
	if _, err := s.currentAnthropicStableIdentityGroupIDs(ctx, account); err != nil {
		return nil, err
	}

	profileID := strings.TrimSpace(input.ProfileID)
	if profileID == "" && account.IsAnthropicStableIdentityEnabled() {
		profileID = account.AnthropicStableIdentityProfileID()
	}
	if profileID == "" {
		profileID = AnthropicStableIngressProfileCLI211222V1
	}
	if !IsKnownAnthropicStableCanaryProfile(profileID) {
		return nil, fmt.Errorf("%w: profile is not an approved Claude Code capture profile", ErrAnthropicStableIdentityInvalid)
	}

	deviceID := strings.TrimSpace(input.DeviceID)
	if deviceID == "" && account.IsAnthropicStableIdentityEnabled() {
		deviceID = account.AnthropicStableIdentityDeviceID()
	}
	if deviceID == "" {
		deviceID, err = newAnthropicStableIdentityDeviceID()
		if err != nil {
			return nil, fmt.Errorf("generate stable identity device id: %w", err)
		}
	}
	if !IsValidAnthropicStableIdentityDeviceID(deviceID) {
		return nil, fmt.Errorf("%w: device id must be a 32-byte lowercase hex value", ErrAnthropicStableIdentityInvalid)
	}

	// PUT is idempotent. A browser retry must not rotate identity. Old releases
	// stored group/key selectors; their presence intentionally triggers one
	// generation bump and cleanup into the dynamic group-pool representation.
	if account.IsAnthropicStableIdentityEnabled() &&
		account.AnthropicStableIdentityState() == AnthropicStableIdentityStateActive &&
		!account.IsAnthropicStableIdentityBlocked() &&
		account.Concurrency == 1 && !account.Schedulable &&
		account.AnthropicStableIdentityDeviceID() == deviceID &&
		AnthropicStableIngressProfilesEquivalent(account.AnthropicStableIdentityProfileID(), profileID) &&
		!hasAnthropicStableIdentityLegacyRoutingMetadata(account.Extra) {
		return anthropicStableIdentityStatusFromAccount(account), nil
	}

	previousSchedulable, schedulableCaptured := account.AnthropicStableIdentityPreviousSchedulable()
	if !account.HasAnthropicStableIdentityManagedFields() || !schedulableCaptured {
		previousSchedulable = account.Schedulable
	}
	previousConcurrency, concurrencyCaptured := account.AnthropicStableIdentityPreviousConcurrency()
	if !account.HasAnthropicStableIdentityManagedFields() || !concurrencyCaptured {
		previousConcurrency = account.Concurrency
	}
	if account.IsAnthropicStableIdentityEnabled() && account.AnthropicStableIdentityGeneration() == mathMaxInt64 {
		return nil, fmt.Errorf("%w: generation exhausted; disable and re-enroll the account", ErrAnthropicStableIdentityInvalid)
	}
	generation := account.AnthropicStableIdentityGeneration() + 1
	if generation <= 0 {
		generation = 1
	}
	now := stableIdentityNow()
	configuredAt := stableIdentityExtraString(account.Extra, AnthropicStableIdentityCreatedAtExtraKey)
	if configuredAt == "" {
		configuredAt = now
	}

	// Start from non-managed account metadata so legacy group/key selectors and
	// rollback membership snapshots cannot remain authoritative accidentally.
	account.Extra = mergeAnthropicStableIdentityExtra(removeAnthropicStableIdentityManagedExtra(account.Extra), map[string]any{
		AnthropicStableIdentityEnabledExtraKey:             true,
		AnthropicStableIdentityStateExtraKey:               AnthropicStableIdentityStateActive,
		AnthropicStableIdentityDeviceIDExtraKey:            deviceID,
		AnthropicStableIdentityPreviousSchedulableExtraKey: previousSchedulable,
		AnthropicStableIdentityPreviousConcurrencyExtraKey: previousConcurrency,
		AnthropicStableIdentityProfileExtraKey:             profileID,
		AnthropicStableIdentityGenerationExtraKey:          generation,
		AnthropicStableIdentityCreatedAtExtraKey:           configuredAt,
		AnthropicStableIdentityUpdatedAtExtraKey:           now,
		AnthropicStableIdentityBlockedExtraKey:             false,
		AnthropicStableIdentityBlockedReasonExtraKey:       "",
	})
	account.Concurrency = 1
	account.Schedulable = false
	if err := s.persistAnthropicStableIdentityAccount(ctx, account); err != nil {
		return nil, err
	}
	return s.GetAnthropicStableIdentity(ctx, account.ID)
}

func (s *adminServiceImpl) PauseAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error) {
	return s.updateAnthropicStableIdentityState(ctx, accountID, AnthropicStableIdentityStatePaused, false)
}

func (s *adminServiceImpl) ResumeAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error) {
	return s.updateAnthropicStableIdentityState(ctx, accountID, AnthropicStableIdentityStateActive, true)
}

func (s *adminServiceImpl) DisableAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error) {
	if s == nil || s.accountRepo == nil {
		return nil, errors.New("Anthropic stable identity administration is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	anthropicStableIdentityAdminMu.Lock()
	defer anthropicStableIdentityAdminMu.Unlock()
	releaseLease, err := s.acquireAnthropicStableIdentityAdminLease(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer releaseAnthropicStableIdentityAdminLease(accountID, releaseLease)

	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !account.HasAnthropicStableIdentityManagedFields() {
		return anthropicStableIdentityStatusFromAccount(account), nil
	}
	previousSchedulable, schedulableCaptured := account.AnthropicStableIdentityPreviousSchedulable()
	previousConcurrency, concurrencyCaptured := account.AnthropicStableIdentityPreviousConcurrency()
	if !schedulableCaptured || !concurrencyCaptured {
		return nil, fmt.Errorf("%w: captured rollback state is incomplete", ErrAnthropicStableIdentityInvalid)
	}
	wasBlocked := account.IsAnthropicStableIdentityBlocked()
	account.Extra = removeAnthropicStableIdentityManagedExtra(account.Extra)
	account.Concurrency = previousConcurrency
	// A credential rejected by upstream must not silently re-enter the generic
	// scheduler merely because stable mode was disabled. Manual account recovery
	// remains required, matching the static stable-canary lifecycle.
	account.Schedulable = previousSchedulable && account.Status == StatusActive && !wasBlocked
	// Current group membership is deliberately untouched.
	if err := s.persistAnthropicStableIdentityAccount(ctx, account); err != nil {
		return nil, err
	}
	return s.GetAnthropicStableIdentity(ctx, accountID)
}

func (s *adminServiceImpl) updateAnthropicStableIdentityState(ctx context.Context, accountID int64, state string, clearBlock bool) (*AnthropicStableIdentityStatus, error) {
	if s == nil || s.accountRepo == nil {
		return nil, errors.New("Anthropic stable identity administration is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	anthropicStableIdentityAdminMu.Lock()
	defer anthropicStableIdentityAdminMu.Unlock()
	releaseLease, err := s.acquireAnthropicStableIdentityAdminLease(ctx, accountID)
	if err != nil {
		return nil, err
	}
	defer releaseAnthropicStableIdentityAdminLease(accountID, releaseLease)
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !account.IsAnthropicStableIdentityEnabled() {
		return nil, fmt.Errorf("%w: account is not enrolled", ErrAnthropicStableIdentityInvalid)
	}
	if err := ValidateAnthropicStableIdentityEnrolledAccount(account); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnthropicStableIdentityInvalid, err)
	}
	if state == AnthropicStableIdentityStateActive {
		if _, err := s.currentAnthropicStableIdentityGroupIDs(ctx, account); err != nil {
			return nil, fmt.Errorf("%w: update group membership before resuming: %v", ErrAnthropicStableIdentityInvalid, err)
		}
	}
	updates := map[string]any{
		AnthropicStableIdentityStateExtraKey:     state,
		AnthropicStableIdentityUpdatedAtExtraKey: stableIdentityNow(),
	}
	if clearBlock {
		updates[AnthropicStableIdentityBlockedExtraKey] = false
		updates[AnthropicStableIdentityBlockedReasonExtraKey] = ""
	}
	mutationCtx := withAnthropicStableIdentityMutationAuthorization(ctx, accountID)
	if err := s.accountRepo.UpdateExtra(mutationCtx, accountID, updates); err != nil {
		return nil, err
	}
	return s.GetAnthropicStableIdentity(ctx, accountID)
}

// currentAnthropicStableIdentityGroupIDs validates the account's ordinary live
// group memberships. At least one active Anthropic group is required, but
// unrelated memberships do not become stable routes.
func (s *adminServiceImpl) currentAnthropicStableIdentityGroupIDs(ctx context.Context, account *Account) ([]int64, error) {
	if s == nil || s.groupRepo == nil || account == nil {
		return nil, errors.New("Anthropic stable identity group validation is unavailable")
	}
	seen := make(map[int64]struct{}, len(account.GroupIDs))
	groupIDs := make([]int64, 0, len(account.GroupIDs))
	for _, groupID := range account.GroupIDs {
		if groupID <= 0 {
			continue
		}
		group, err := s.groupRepo.GetByID(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if group == nil || !group.IsActive() || group.Platform != PlatformAnthropic {
			continue
		}
		if _, exists := seen[groupID]; exists {
			continue
		}
		seen[groupID] = struct{}{}
		groupIDs = append(groupIDs, groupID)
	}
	if len(groupIDs) == 0 {
		return nil, fmt.Errorf("%w: add the account to at least one active Anthropic group first", ErrAnthropicStableIdentityInvalid)
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	return groupIDs, nil
}

func (s *adminServiceImpl) validateAnthropicStableIdentityCreateGroups(ctx context.Context, groupIDs []int64) ([]int64, error) {
	normalized, err := normalizeStableIdentityIDs(groupIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: select at least one active Anthropic group", ErrAnthropicStableIdentityInvalid)
	}
	if s == nil || s.groupRepo == nil {
		return nil, errors.New("Anthropic stable identity group validation is unavailable")
	}
	for _, groupID := range normalized {
		group, loadErr := s.groupRepo.GetByID(ctx, groupID)
		if loadErr != nil {
			return nil, loadErr
		}
		if group == nil || !group.IsActive() || group.Platform != PlatformAnthropic {
			return nil, fmt.Errorf("%w: group %d is not an active Anthropic group", ErrAnthropicStableIdentityInvalid, groupID)
		}
	}
	return normalized, nil
}

// prepareAnthropicStableIdentityAccountForCreate applies the same lifecycle
// invariants as Configure, but before the account is visible. The repository
// persists this account and all group memberships in one transaction, so a
// credential requested in stable mode never spends even a short interval in
// the generic scheduler.
func prepareAnthropicStableIdentityAccountForCreate(account *Account, groupIDs []int64) error {
	if account == nil {
		return ErrAccountNilInput
	}
	account.GroupIDs = append([]int64(nil), groupIDs...)
	if err := ValidateAnthropicStableIdentityAccount(account); err != nil {
		return fmt.Errorf("%w: %v", ErrAnthropicStableIdentityInvalid, err)
	}
	deviceID, err := newAnthropicStableIdentityDeviceID()
	if err != nil {
		return fmt.Errorf("generate stable identity device id: %w", err)
	}
	previousSchedulable := account.Schedulable
	previousConcurrency := account.Concurrency
	now := stableIdentityNow()
	account.Extra = mergeAnthropicStableIdentityExtra(removeAnthropicStableIdentityManagedExtra(account.Extra), map[string]any{
		AnthropicStableIdentityEnabledExtraKey:             true,
		AnthropicStableIdentityStateExtraKey:               AnthropicStableIdentityStateActive,
		AnthropicStableIdentityDeviceIDExtraKey:            deviceID,
		AnthropicStableIdentityPreviousSchedulableExtraKey: previousSchedulable,
		AnthropicStableIdentityPreviousConcurrencyExtraKey: previousConcurrency,
		AnthropicStableIdentityProfileExtraKey:             AnthropicStableIngressProfileCLI211222V1,
		AnthropicStableIdentityGenerationExtraKey:          int64(1),
		AnthropicStableIdentityCreatedAtExtraKey:           now,
		AnthropicStableIdentityUpdatedAtExtraKey:           now,
		AnthropicStableIdentityBlockedExtraKey:             false,
		AnthropicStableIdentityBlockedReasonExtraKey:       "",
	})
	account.Concurrency = 1
	account.Schedulable = false
	if err := ValidateAnthropicStableIdentityEnrolledAccount(account); err != nil {
		return fmt.Errorf("%w: %v", ErrAnthropicStableIdentityInvalid, err)
	}
	return nil
}

// The same PostgreSQL session lease used by the strict forwarder serializes
// lifecycle writes across gateway instances and waits for accepted upstream
// work to finish before changing the fixed identity.
func (s *adminServiceImpl) acquireAnthropicStableIdentityAdminLease(ctx context.Context, accountID int64) (func() error, error) {
	if s == nil || s.accountRepo == nil || accountID <= 0 {
		return nil, errors.New("Anthropic stable identity lease is unavailable")
	}
	repo, ok := s.accountRepo.(AnthropicStableCanaryLeaseRepository)
	if !ok {
		return nil, errors.New("Anthropic stable identity requires a cross-process account lease")
	}
	release, err := repo.AcquireAnthropicStableCanaryLease(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("acquire Anthropic stable identity account lease: %w", err)
	}
	if release == nil {
		return nil, errors.New("Anthropic stable identity account lease is incomplete")
	}
	return release, nil
}

func releaseAnthropicStableIdentityAdminLease(accountID int64, release func() error) {
	if release == nil {
		return
	}
	if err := release(); err != nil {
		logger.LegacyPrintf("service.admin", "[Anthropic Stable Identity] release admin lease failed account=%d err=%v", accountID, err)
	}
}

func (s *adminServiceImpl) persistAnthropicStableIdentityAccount(ctx context.Context, account *Account) error {
	if s == nil || s.accountRepo == nil || account == nil || account.ID <= 0 {
		return errors.New("Anthropic stable identity persistence is unavailable")
	}
	mutationCtx := withAnthropicStableIdentityMutationAuthorization(ctx, account.ID)
	return s.accountRepo.Update(mutationCtx, account)
}

func mergeAnthropicStableIdentityExtra(current, managed map[string]any) map[string]any {
	out := shallowCopyMap(current)
	if out == nil {
		out = make(map[string]any, len(managed))
	}
	for key, value := range managed {
		out[key] = value
	}
	return out
}

func removeAnthropicStableIdentityManagedExtra(current map[string]any) map[string]any {
	out := shallowCopyMap(current)
	for _, key := range anthropicStableIdentityManagedExtraKeys {
		delete(out, key)
	}
	return out
}

func hasAnthropicStableIdentityLegacyRoutingMetadata(extra map[string]any) bool {
	if extra == nil {
		return false
	}
	for _, key := range []string{
		AnthropicStableIdentityPreviousGroupIDsExtraKey,
		AnthropicStableIdentityGroupIDsExtraKey,
		AnthropicStableIdentityAPIKeyIDsExtraKey,
		AnthropicStableIdentityAPIKeyGroupIDsExtraKey,
	} {
		if _, exists := extra[key]; exists {
			return true
		}
	}
	return false
}

func newAnthropicStableIdentityDeviceID() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func anthropicStableIdentityStatusFromAccount(account *Account) *AnthropicStableIdentityStatus {
	if account == nil {
		return nil
	}
	previous, hasPrevious := account.AnthropicStableIdentityPreviousSchedulable()
	var previousPtr *bool
	if hasPrevious {
		previousPtr = &previous
	}
	device := account.AnthropicStableIdentityDeviceID()
	fingerprint := ""
	if len(device) >= 12 {
		fingerprint = device[:12]
	}
	return &AnthropicStableIdentityStatus{
		AccountID:           account.ID,
		Enabled:             account.IsAnthropicStableIdentityEnabled(),
		State:               account.AnthropicStableIdentityState(),
		Blocked:             account.IsAnthropicStableIdentityBlocked(),
		BlockedReason:       account.AnthropicStableIdentityBlockedReason(),
		GroupIDs:            currentAnthropicStableIdentityMembershipIDs(account),
		Generation:          account.AnthropicStableIdentityGeneration(),
		ProfileID:           account.AnthropicStableIdentityProfileID(),
		DeviceFingerprint:   fingerprint,
		DeviceConfigured:    IsValidAnthropicStableIdentityDeviceID(device),
		Concurrency:         account.Concurrency,
		Schedulable:         account.Schedulable,
		PreviousSchedulable: previousPtr,
		RequiresRestart:     false,
		ConfiguredAt:        stableIdentityExtraString(account.Extra, AnthropicStableIdentityCreatedAtExtraKey),
		UpdatedAt:           stableIdentityExtraString(account.Extra, AnthropicStableIdentityUpdatedAtExtraKey),
	}
}

func currentAnthropicStableIdentityMembershipIDs(account *Account) []int64 {
	if account == nil {
		return []int64{}
	}
	seen := make(map[int64]struct{}, len(account.GroupIDs))
	ids := make([]int64, 0, len(account.GroupIDs))
	for _, id := range account.GroupIDs {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func stableIdentityExtraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	value, _ := extra[key].(string)
	return strings.TrimSpace(value)
}

const mathMaxInt64 = int64(^uint64(0) >> 1)

var _ AnthropicStableIdentityAdminService = (*adminServiceImpl)(nil)
