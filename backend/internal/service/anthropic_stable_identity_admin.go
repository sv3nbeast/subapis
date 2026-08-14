package service

// Admin lifecycle for account-scoped Anthropic stable identity mode.
//
// This deliberately does not create or mutate a special group.  The account
// remains in the operator's existing Anthropic groups; the extra metadata only
// narrows which existing group/API-key pairs are allowed to enter the strict
// Claude Code path.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

var (
	ErrAnthropicStableIdentityInvalid  = infraerrors.BadRequest("ANTHROPIC_STABLE_IDENTITY_INVALID", "invalid Anthropic stable identity configuration")
	ErrAnthropicStableIdentityConflict = infraerrors.Conflict("ANTHROPIC_STABLE_IDENTITY_CONFLICT", "Anthropic stable identity configuration changed concurrently")
)

// AnthropicStableIdentityAdminService is intentionally optional on the broad
// AdminService interface.  Keeping it as a narrow capability means existing
// admin test doubles and alternate deployments do not need a breaking method
// addition, while the production admin implementation exposes the lifecycle.
type AnthropicStableIdentityAdminService interface {
	GetAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error)
	ConfigureAnthropicStableIdentity(ctx context.Context, accountID int64, input *AnthropicStableIdentityConfigureInput) (*AnthropicStableIdentityStatus, error)
	PauseAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error)
	ResumeAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error)
	DisableAnthropicStableIdentity(ctx context.Context, accountID int64) (*AnthropicStableIdentityStatus, error)
}

type AnthropicStableIdentityConfigureInput struct {
	GroupIDs  []int64
	APIKeyIDs []int64
	ProfileID string
	DeviceID  string
}

// AnthropicStableIdentityStatus never contains credentials, the derived HMAC,
// or the full device id.  API-key IDs are administrative identifiers, not key
// material, and are returned so the panel can render the current selection.
type AnthropicStableIdentityStatus struct {
	AccountID           int64   `json:"account_id"`
	Enabled             bool    `json:"enabled"`
	State               string  `json:"state"`
	Blocked             bool    `json:"blocked"`
	BlockedReason       string  `json:"blocked_reason,omitempty"`
	GroupIDs            []int64 `json:"group_ids"`
	APIKeyIDs           []int64 `json:"api_key_ids"`
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
	if s == nil || s.accountRepo == nil || s.groupRepo == nil || s.apiKeyRepo == nil {
		return nil, errors.New("Anthropic stable identity administration is unavailable")
	}
	if input == nil {
		return nil, ErrAnthropicStableIdentityInvalid
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
	if err := ValidateAnthropicStableIdentityAccount(account); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAnthropicStableIdentityInvalid, err)
	}

	groupIDs, err := normalizeStableIdentityIDs(input.GroupIDs)
	if err != nil {
		return nil, fmt.Errorf("%w: at least one existing Anthropic group is required", ErrAnthropicStableIdentityInvalid)
	}
	if err := s.validateAnthropicStableIdentityGroups(ctx, groupIDs); err != nil {
		return nil, err
	}

	apiKeyIDs, apiKeyGroupIDs, err := s.resolveAnthropicStableIdentityAPIKeys(ctx, groupIDs, input.APIKeyIDs)
	if err != nil {
		return nil, err
	}
	if err := s.ensureAnthropicStableIdentityRouteOwnership(ctx, accountID, apiKeyGroupIDs); err != nil {
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

	previousSchedulable, captured := account.AnthropicStableIdentityPreviousSchedulable()
	if !account.HasAnthropicStableIdentityManagedFields() || !captured {
		previousSchedulable = account.Schedulable
	}
	previousConcurrency, concurrencyCaptured := account.AnthropicStableIdentityPreviousConcurrency()
	if !account.HasAnthropicStableIdentityManagedFields() || !concurrencyCaptured {
		previousConcurrency = account.Concurrency
	}
	previousGroupIDs, groupsCaptured := account.AnthropicStableIdentityPreviousGroupIDs()
	if !account.HasAnthropicStableIdentityManagedFields() || !groupsCaptured {
		previousGroupIDs = append([]int64{}, account.GroupIDs...)
	}
	// Stable membership is always the captured original membership plus the
	// current selection. This preserves unrelated existing groups while letting
	// an operator deselect a group that stable mode added earlier; using the
	// account's current union here would make those additions accumulate forever.
	// No dedicated group is created or required.
	newAccountGroups := unionAnthropicStableIdentityGroups(previousGroupIDs, groupIDs)
	expectedCurrentGroups := append([]int64(nil), account.GroupIDs...)
	// Retrying an already successful configure request must not rotate the
	// generation or identity.  This also makes a browser/network retry safe when
	// the first response was lost after the transaction committed.
	if account.IsAnthropicStableIdentityEnabled() &&
		account.AnthropicStableIdentityState() == AnthropicStableIdentityStateActive &&
		!account.IsAnthropicStableIdentityBlocked() &&
		account.Concurrency == 1 && !account.Schedulable &&
		account.AnthropicStableIdentityDeviceID() == deviceID &&
		AnthropicStableIngressProfilesEquivalent(account.AnthropicStableIdentityProfileID(), profileID) &&
		sameStableIdentityIDSet(account.AnthropicStableIdentityGroupIDs(), groupIDs) &&
		sameStableIdentityIDSet(account.AnthropicStableIdentityAPIKeyIDs(), apiKeyIDs) &&
		reflect.DeepEqual(account.AnthropicStableIdentityAPIKeyGroupIDs(), apiKeyGroupIDs) &&
		sameStableIdentityIDSet(account.GroupIDs, newAccountGroups) {
		return anthropicStableIdentityStatusFromAccount(account), nil
	}
	if account.IsAnthropicStableIdentityEnabled() && account.AnthropicStableIdentityGeneration() == mathMaxInt64 {
		return nil, fmt.Errorf("%w: generation exhausted; disable and re-enroll the account", ErrAnthropicStableIdentityInvalid)
	}
	generation := account.AnthropicStableIdentityGeneration() + 1
	if generation <= 0 {
		generation = 1
	}
	now := stableIdentityNow()
	extra := map[string]any{
		AnthropicStableIdentityEnabledExtraKey:             true,
		AnthropicStableIdentityStateExtraKey:               AnthropicStableIdentityStateActive,
		AnthropicStableIdentityDeviceIDExtraKey:            deviceID,
		AnthropicStableIdentityPreviousSchedulableExtraKey: previousSchedulable,
		AnthropicStableIdentityPreviousConcurrencyExtraKey: previousConcurrency,
		AnthropicStableIdentityPreviousGroupIDsExtraKey:    previousGroupIDs,
		AnthropicStableIdentityProfileExtraKey:             profileID,
		AnthropicStableIdentityGroupIDsExtraKey:            groupIDs,
		AnthropicStableIdentityAPIKeyIDsExtraKey:           apiKeyIDs,
		AnthropicStableIdentityAPIKeyGroupIDsExtraKey:      stableIdentityKeyGroupJSON(apiKeyGroupIDs),
		AnthropicStableIdentityGenerationExtraKey:          generation,
		AnthropicStableIdentityUpdatedAtExtraKey:           now,
		AnthropicStableIdentityBlockedExtraKey:             false,
		AnthropicStableIdentityBlockedReasonExtraKey:       "",
	}
	if !account.IsAnthropicStableIdentityEnabled() {
		extra[AnthropicStableIdentityCreatedAtExtraKey] = now
	} else if value, ok := account.Extra[AnthropicStableIdentityCreatedAtExtraKey]; ok {
		extra[AnthropicStableIdentityCreatedAtExtraKey] = value
	}

	account.Extra = mergeAnthropicStableIdentityExtra(account.Extra, extra)
	account.GroupIDs = newAccountGroups
	account.Concurrency = 1
	account.Schedulable = false
	if err := s.persistAnthropicStableIdentityAccount(ctx, account, newAccountGroups, expectedCurrentGroups); err != nil {
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
	previous, schedulerCaptured := account.AnthropicStableIdentityPreviousSchedulable()
	previousConcurrency, concurrencyCaptured := account.AnthropicStableIdentityPreviousConcurrency()
	previousGroups, groupsCaptured := account.AnthropicStableIdentityPreviousGroupIDs()
	if !schedulerCaptured || !concurrencyCaptured || !groupsCaptured {
		return nil, fmt.Errorf("%w: captured rollback state is incomplete", ErrAnthropicStableIdentityInvalid)
	}
	expectedCurrentGroups := append([]int64(nil), account.GroupIDs...)
	account.Extra = removeAnthropicStableIdentityManagedExtra(account.Extra)
	account.GroupIDs = append([]int64(nil), previousGroups...)
	account.Concurrency = previousConcurrency
	account.Schedulable = previous && account.Status == StatusActive
	if err := s.persistAnthropicStableIdentityAccount(ctx, account, account.GroupIDs, expectedCurrentGroups); err != nil {
		return nil, err
	}
	return s.GetAnthropicStableIdentity(ctx, accountID)
}

func (s *adminServiceImpl) updateAnthropicStableIdentityState(ctx context.Context, accountID int64, state string, clearBlock bool) (*AnthropicStableIdentityStatus, error) {
	if s == nil || s.accountRepo == nil {
		return nil, errors.New("Anthropic stable identity administration is unavailable")
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
		if err := s.validateAnthropicStableIdentityGroups(ctx, account.AnthropicStableIdentityGroupIDs()); err != nil {
			return nil, fmt.Errorf("%w: reconfigure the account before resuming: %v", ErrAnthropicStableIdentityInvalid, err)
		}
		resolvedKeys, resolvedMapping, resolveErr := s.resolveAnthropicStableIdentityAPIKeys(
			ctx,
			account.AnthropicStableIdentityGroupIDs(),
			account.AnthropicStableIdentityAPIKeyIDs(),
		)
		if resolveErr != nil || !reflect.DeepEqual(resolvedKeys, account.AnthropicStableIdentityAPIKeyIDs()) ||
			!reflect.DeepEqual(resolvedMapping, account.AnthropicStableIdentityAPIKeyGroupIDs()) {
			if resolveErr == nil {
				resolveErr = errors.New("selected API-key group binding changed")
			}
			return nil, fmt.Errorf("%w: reconfigure the account before resuming: %v", ErrAnthropicStableIdentityInvalid, resolveErr)
		}
		if err := s.ensureAnthropicStableIdentityRouteOwnership(ctx, accountID, resolvedMapping); err != nil {
			return nil, err
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
	if state == AnthropicStableIdentityStateActive {
		if _, err := s.SetAccountSchedulable(ctx, accountID, false); err != nil {
			return nil, err
		}
	}
	return s.GetAnthropicStableIdentity(ctx, accountID)
}

func (s *adminServiceImpl) validateAnthropicStableIdentityGroups(ctx context.Context, groupIDs []int64) error {
	if s == nil || s.groupRepo == nil {
		return errors.New("Anthropic stable identity group validation is unavailable")
	}
	for _, groupID := range groupIDs {
		group, err := s.groupRepo.GetByID(ctx, groupID)
		if err != nil {
			return err
		}
		if group == nil || !group.IsActive() || group.Platform != PlatformAnthropic {
			return fmt.Errorf("%w: group %d must be an active Anthropic group", ErrAnthropicStableIdentityInvalid, groupID)
		}
	}
	return nil
}

func (s *adminServiceImpl) resolveAnthropicStableIdentityAPIKeys(ctx context.Context, groupIDs, requested []int64) ([]int64, map[int64]int64, error) {
	allowed := make(map[int64]int64)
	for _, groupID := range groupIDs {
		page := 1
		for {
			keys, result, err := s.apiKeyRepo.ListByGroupID(ctx, groupID, pagination.PaginationParams{Page: page, PageSize: 1000})
			if err != nil {
				return nil, nil, err
			}
			for _, key := range keys {
				if key.ID > 0 && key.Status == StatusAPIKeyActive && !key.IsExpired() && key.GroupID != nil && *key.GroupID == groupID {
					if previous, exists := allowed[key.ID]; exists && previous != groupID {
						return nil, nil, fmt.Errorf("%w: API key %d belongs to more than one selected group", ErrAnthropicStableIdentityConflict, key.ID)
					}
					allowed[key.ID] = groupID
				}
			}
			if len(keys) == 0 || result == nil || int64(page*1000) >= result.Total {
				break
			}
			page++
		}
	}
	if len(allowed) == 0 {
		return nil, nil, fmt.Errorf("%w: selected groups have no active API keys", ErrAnthropicStableIdentityInvalid)
	}
	if len(requested) == 0 {
		requested = make([]int64, 0, len(allowed))
		for id := range allowed {
			requested = append(requested, id)
		}
	}
	keyGroups := make(map[int64]int64, len(requested))
	for _, id := range requested {
		groupID, ok := allowed[id]
		if !ok {
			return nil, nil, fmt.Errorf("%w: API key %d is not active in a selected existing group", ErrAnthropicStableIdentityInvalid, id)
		}
		keyGroups[id] = groupID
	}
	ids, err := normalizeStableIdentityIDs(requested)
	if err != nil {
		return nil, nil, err
	}
	return ids, keyGroups, nil
}

func (s *adminServiceImpl) ensureAnthropicStableIdentityRouteOwnership(ctx context.Context, accountID int64, requested map[int64]int64) error {
	if s == nil || s.accountRepo == nil {
		return errors.New("Anthropic stable identity route ownership is unavailable")
	}
	accounts, err := s.accountRepo.FindByExtraField(ctx, AnthropicStableIdentityEnabledExtraKey, true)
	if err != nil {
		return err
	}
	return validateAnthropicStableIdentityRouteOwnership(accountID, requested, accounts)
}

func validateAnthropicStableIdentityRouteOwnership(accountID int64, requested map[int64]int64, accounts []Account) error {
	for _, other := range accounts {
		if other.ID <= 0 || other.ID == accountID || !other.IsAnthropicStableIdentityEnabled() {
			continue
		}
		owned := other.AnthropicStableIdentityAPIKeyGroupIDs()
		for keyID, groupID := range requested {
			if owned[keyID] == groupID {
				return fmt.Errorf(
					"%w: API key %d in group %d is already reserved by stable account %d",
					ErrAnthropicStableIdentityConflict, keyID, groupID, other.ID,
				)
			}
		}
	}
	return nil
}

// The same PostgreSQL session lease used by the strict forwarder serializes
// lifecycle writes across gateway instances. Besides preventing two admin
// requests from overwriting each other's generation, it guarantees that a
// pause, reconfiguration, or disable cannot change the fixed identity while an
// accepted upstream request is still using it.
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

func unionAnthropicStableIdentityGroups(existing, selected []int64) []int64 {
	seen := make(map[int64]struct{}, len(existing)+len(selected))
	out := make([]int64, 0, len(existing)+len(selected))
	for _, id := range existing {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range selected {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// persistAnthropicStableIdentityAccount commits group membership, the account
// reservation, and managed identity metadata in one database transaction.  No
// scheduler can observe a stable policy whose account is still schedulable (or
// the inverse).  The post-commit SetSchedulable call is an idempotent snapshot
// nudge for repository implementations whose in-memory scheduler sync is
// intentionally deferred while participating in an external transaction.
func (s *adminServiceImpl) persistAnthropicStableIdentityAccount(ctx context.Context, account *Account, groupIDs, expectedCurrentGroupIDs []int64) error {
	if s == nil || s.entClient == nil || s.accountRepo == nil || account == nil || account.ID <= 0 {
		return errors.New("Anthropic stable identity transactional persistence is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	txCtx = withAnthropicStableIdentityMutationAuthorization(txCtx, account.ID)
	txCtx = withAnthropicStableIdentityExpectedGroupIDs(txCtx, account.ID, expectedCurrentGroupIDs)
	// BindGroups uses the repository's established group->account lock order.
	// Keeping it before the full account update avoids a reverse-order deadlock
	// with concurrent ordinary group edits.
	if err := s.accountRepo.BindGroups(txCtx, account.ID, groupIDs); err != nil {
		return err
	}
	if account.IsAnthropicStableIdentityEnabled() {
		if err := s.validateAnthropicStableIdentityGroups(txCtx, account.AnthropicStableIdentityGroupIDs()); err != nil {
			return err
		}
	}
	// BindGroups has now locked every old/new group row. Recheck route
	// ownership inside the same transaction so two processes cannot both pass
	// the earlier optimistic check and enroll different accounts for one
	// existing (group, API-key) route. FindByExtraField must honor txCtx for this
	// statement to observe the latest committed owner after a group-lock wait.
	if account.IsAnthropicStableIdentityEnabled() {
		if err := s.ensureAnthropicStableIdentityRouteOwnership(
			txCtx,
			account.ID,
			account.AnthropicStableIdentityAPIKeyGroupIDs(),
		); err != nil {
			return err
		}
	}
	if err := s.accountRepo.Update(txCtx, account); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.accountRepo.SetSchedulable(ctx, account.ID, account.Schedulable); err != nil {
		return fmt.Errorf("stable identity committed but scheduler snapshot refresh failed: %w", err)
	}
	return nil
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

func stableIdentityKeyGroupJSON(mapping map[int64]int64) map[string]any {
	out := make(map[string]any, len(mapping))
	for keyID, groupID := range mapping {
		out[fmt.Sprintf("%d", keyID)] = groupID
	}
	return out
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
		GroupIDs:            account.AnthropicStableIdentityGroupIDs(),
		APIKeyIDs:           account.AnthropicStableIdentityAPIKeyIDs(),
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

func stableIdentityExtraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	value, _ := extra[key].(string)
	return strings.TrimSpace(value)
}

// Avoid importing math solely for MaxInt64 in the hot service file.
const mathMaxInt64 = int64(^uint64(0) >> 1)

var _ AnthropicStableIdentityAdminService = (*adminServiceImpl)(nil)
