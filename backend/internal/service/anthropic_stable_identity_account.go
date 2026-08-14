package service

// Account-scoped stable identity metadata.
//
// The original stable canary is intentionally tied to one process-wide
// environment configuration and one isolated group.  Stable identity mode is
// the operator-facing extension: an OAuth/SetupToken account may be enrolled
// in one or more existing Anthropic groups and only explicitly selected API
// keys enter the strict Claude Code path. The metadata lives on the account so
// it survives restarts and does not require a deployment-specific env file.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type anthropicStableIdentityMutationAuthorizationKey struct{}
type anthropicStableIdentityExpectedGroupIDsKey struct{}

// withAnthropicStableIdentityMutationAuthorization is deliberately private to
// the dedicated lifecycle. Repository mutation guards can verify the marker,
// but ordinary account/group handlers cannot manufacture it accidentally.
func withAnthropicStableIdentityMutationAuthorization(ctx context.Context, accountID int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, anthropicStableIdentityMutationAuthorizationKey{}, accountID)
}

// AnthropicStableIdentityMutationAuthorized lets repository-level group and
// delete fences distinguish the dedicated lifecycle from generic admin writes.
// It intentionally authorizes one exact account id only.
func AnthropicStableIdentityMutationAuthorized(ctx context.Context, accountID int64) bool {
	if ctx == nil || accountID <= 0 {
		return false
	}
	authorizedID, _ := ctx.Value(anthropicStableIdentityMutationAuthorizationKey{}).(int64)
	return authorizedID == accountID
}

// withAnthropicStableIdentityExpectedGroupIDs attaches the exact ordered group
// snapshot captured before a lifecycle mutation. BindGroups verifies it after
// locking the account row, preventing a concurrent ordinary membership edit
// from being silently overwritten by configure/reconfigure/disable.
func withAnthropicStableIdentityExpectedGroupIDs(ctx context.Context, accountID int64, groupIDs []int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, anthropicStableIdentityExpectedGroupIDsKey{}, struct {
		accountID int64
		groupIDs  []int64
	}{accountID: accountID, groupIDs: append([]int64(nil), groupIDs...)})
}

// AnthropicStableIdentityExpectedGroupIDs exposes the lifecycle snapshot only
// to repository guards. Callers receive a copy and cannot mutate context state.
func AnthropicStableIdentityExpectedGroupIDs(ctx context.Context, accountID int64) ([]int64, bool) {
	if ctx == nil || accountID <= 0 {
		return nil, false
	}
	value, ok := ctx.Value(anthropicStableIdentityExpectedGroupIDsKey{}).(struct {
		accountID int64
		groupIDs  []int64
	})
	if !ok || value.accountID != accountID {
		return nil, false
	}
	return append([]int64(nil), value.groupIDs...), true
}

const (
	AnthropicStableIdentityEnabledExtraKey             = "anthropic_stable_identity_enabled"
	AnthropicStableIdentityStateExtraKey               = "anthropic_stable_identity_state"
	AnthropicStableIdentityDeviceIDExtraKey            = "anthropic_stable_identity_device_id"
	AnthropicStableIdentityPreviousSchedulableExtraKey = "anthropic_stable_identity_previous_schedulable"
	AnthropicStableIdentityPreviousConcurrencyExtraKey = "anthropic_stable_identity_previous_concurrency"
	AnthropicStableIdentityPreviousGroupIDsExtraKey    = "anthropic_stable_identity_previous_group_ids"
	AnthropicStableIdentityProfileExtraKey             = "anthropic_stable_identity_profile"
	AnthropicStableIdentityGroupIDsExtraKey            = "anthropic_stable_identity_group_ids"
	AnthropicStableIdentityAPIKeyIDsExtraKey           = "anthropic_stable_identity_api_key_ids"
	AnthropicStableIdentityAPIKeyGroupIDsExtraKey      = "anthropic_stable_identity_api_key_group_ids"
	AnthropicStableIdentityGenerationExtraKey          = "anthropic_stable_identity_generation"
	AnthropicStableIdentityCreatedAtExtraKey           = "anthropic_stable_identity_created_at"
	AnthropicStableIdentityUpdatedAtExtraKey           = "anthropic_stable_identity_updated_at"
	AnthropicStableIdentityBlockedExtraKey             = "anthropic_stable_identity_blocked"
	AnthropicStableIdentityBlockedReasonExtraKey       = "anthropic_stable_identity_blocked_reason"
)

var ErrAnthropicStableIdentityManaged = infraerrors.Conflict(
	"ANTHROPIC_STABLE_IDENTITY_MANAGED",
	"account is managed by Anthropic stable identity mode; disable the mode before changing identity or routing fields",
)

var ErrAnthropicStableIdentityOutboundBlocked = errors.New("Anthropic stable identity account cannot use a generic outbound path")

const (
	AnthropicStableIdentityStateActive = "active"
	AnthropicStableIdentityStatePaused = "paused"
	AnthropicStableIdentityStateOff    = "off"
)

var anthropicStableIdentityManagedExtraKeys = [...]string{
	AnthropicStableIdentityEnabledExtraKey,
	AnthropicStableIdentityStateExtraKey,
	AnthropicStableIdentityDeviceIDExtraKey,
	AnthropicStableIdentityPreviousSchedulableExtraKey,
	AnthropicStableIdentityPreviousConcurrencyExtraKey,
	AnthropicStableIdentityPreviousGroupIDsExtraKey,
	AnthropicStableIdentityProfileExtraKey,
	AnthropicStableIdentityGroupIDsExtraKey,
	AnthropicStableIdentityAPIKeyIDsExtraKey,
	AnthropicStableIdentityAPIKeyGroupIDsExtraKey,
	AnthropicStableIdentityGenerationExtraKey,
	AnthropicStableIdentityCreatedAtExtraKey,
	AnthropicStableIdentityUpdatedAtExtraKey,
	AnthropicStableIdentityBlockedExtraKey,
	AnthropicStableIdentityBlockedReasonExtraKey,
}

// AnthropicStableIdentityExtraUpdateTouchesManagedFields is used by ordinary
// account update paths to prevent a stale edit form from deleting or replacing
// the identity policy.  The dedicated admin lifecycle is the only writer of
// these keys.
func AnthropicStableIdentityExtraUpdateTouchesManagedFields(extra map[string]any) bool {
	for _, key := range anthropicStableIdentityManagedExtraKeys {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

func IsAnthropicStableIdentityManagedExtraKey(key string) bool {
	for _, managed := range anthropicStableIdentityManagedExtraKeys {
		if key == managed {
			return true
		}
	}
	return false
}

func (a *Account) HasAnthropicStableIdentityManagedFields() bool {
	return a != nil && AnthropicStableIdentityExtraUpdateTouchesManagedFields(a.Extra)
}

func (a *Account) IsAnthropicStableIdentityEnabled() bool {
	if a == nil || !a.IsAnthropicOAuthOrSetupToken() || a.Extra == nil {
		return false
	}
	enabled, _ := a.Extra[AnthropicStableIdentityEnabledExtraKey].(bool)
	return enabled && a.AnthropicStableIdentityState() != AnthropicStableIdentityStateOff
}

func (a *Account) AnthropicStableIdentityState() string {
	if a == nil || a.Extra == nil {
		return AnthropicStableIdentityStateOff
	}
	state, _ := a.Extra[AnthropicStableIdentityStateExtraKey].(string)
	switch strings.ToLower(strings.TrimSpace(state)) {
	case AnthropicStableIdentityStateActive:
		return AnthropicStableIdentityStateActive
	case AnthropicStableIdentityStatePaused:
		return AnthropicStableIdentityStatePaused
	default:
		if enabled, _ := a.Extra[AnthropicStableIdentityEnabledExtraKey].(bool); enabled {
			return AnthropicStableIdentityStateActive
		}
		return AnthropicStableIdentityStateOff
	}
}

func (a *Account) IsAnthropicStableIdentityPaused() bool {
	return a != nil && a.IsAnthropicStableIdentityEnabled() && a.AnthropicStableIdentityState() == AnthropicStableIdentityStatePaused
}

func (a *Account) AnthropicStableIdentityDeviceID() string {
	if a == nil || a.Extra == nil {
		return ""
	}
	value, _ := a.Extra[AnthropicStableIdentityDeviceIDExtraKey].(string)
	return strings.TrimSpace(value)
}

func (a *Account) AnthropicStableIdentityProfileID() string {
	if a == nil || a.Extra == nil {
		return ""
	}
	value, _ := a.Extra[AnthropicStableIdentityProfileExtraKey].(string)
	return strings.TrimSpace(value)
}

func (a *Account) AnthropicStableIdentityGeneration() int64 {
	if a == nil || a.Extra == nil {
		return 0
	}
	return stableIdentityInt64(a.Extra[AnthropicStableIdentityGenerationExtraKey])
}

// AnthropicStableIdentityUpdatedAt returns the lifecycle revision marker used
// to reject an ordinary account edit that was loaded before a configure,
// reconfigure, pause, block, resume, or disable operation committed. It is not
// a wall-clock concurrency primitive by itself; the repository compares it
// while holding the account row lock.
func (a *Account) AnthropicStableIdentityUpdatedAt() string {
	if a == nil {
		return ""
	}
	return stableIdentityExtraString(a.Extra, AnthropicStableIdentityUpdatedAtExtraKey)
}

func (a *Account) AnthropicStableIdentityPreviousSchedulable() (bool, bool) {
	if a == nil || a.Extra == nil {
		return false, false
	}
	value, ok := a.Extra[AnthropicStableIdentityPreviousSchedulableExtraKey].(bool)
	return value, ok
}

func (a *Account) AnthropicStableIdentityPreviousConcurrency() (int, bool) {
	if a == nil || a.Extra == nil {
		return 0, false
	}
	if _, ok := a.Extra[AnthropicStableIdentityPreviousConcurrencyExtraKey]; !ok {
		return 0, false
	}
	value := stableIdentityInt64(a.Extra[AnthropicStableIdentityPreviousConcurrencyExtraKey])
	if value < 0 || value > int64(^uint(0)>>1) {
		return 0, false
	}
	return int(value), true
}

func (a *Account) AnthropicStableIdentityPreviousGroupIDs() ([]int64, bool) {
	if a == nil || a.Extra == nil {
		return nil, false
	}
	value, ok := a.Extra[AnthropicStableIdentityPreviousGroupIDsExtraKey]
	if !ok {
		return nil, false
	}
	return stableIdentityOrderedInt64Slice(value)
}

func (a *Account) IsAnthropicStableIdentityBlocked() bool {
	if a == nil || a.Extra == nil {
		return false
	}
	value, _ := a.Extra[AnthropicStableIdentityBlockedExtraKey].(bool)
	return value
}

func (a *Account) AnthropicStableIdentityBlockedReason() string {
	if a == nil || a.Extra == nil {
		return ""
	}
	value, _ := a.Extra[AnthropicStableIdentityBlockedReasonExtraKey].(string)
	return strings.TrimSpace(value)
}

func (a *Account) AnthropicStableIdentityGroupIDs() []int64 {
	if a == nil || a.Extra == nil {
		return nil
	}
	return stableIdentityInt64Slice(a.Extra[AnthropicStableIdentityGroupIDsExtraKey])
}

func (a *Account) AnthropicStableIdentityAPIKeyIDs() []int64 {
	if a == nil || a.Extra == nil {
		return nil
	}
	return stableIdentityInt64Slice(a.Extra[AnthropicStableIdentityAPIKeyIDsExtraKey])
}

func (a *Account) AnthropicStableIdentityAPIKeyGroupIDs() map[int64]int64 {
	if a == nil || a.Extra == nil {
		return nil
	}
	raw, ok := a.Extra[AnthropicStableIdentityAPIKeyGroupIDsExtraKey].(map[string]any)
	if !ok {
		if typed, typedOK := a.Extra[AnthropicStableIdentityAPIKeyGroupIDsExtraKey].(map[string]int64); typedOK {
			out := make(map[int64]int64, len(typed))
			for key, groupID := range typed {
				keyID, err := strconv.ParseInt(strings.TrimSpace(key), 10, 64)
				if err == nil && keyID > 0 && groupID > 0 {
					out[keyID] = groupID
				}
			}
			return out
		}
		return nil
	}
	out := make(map[int64]int64, len(raw))
	for key, value := range raw {
		keyID, err := strconv.ParseInt(strings.TrimSpace(key), 10, 64)
		groupID := stableIdentityInt64(value)
		if err == nil && keyID > 0 && groupID > 0 {
			out[keyID] = groupID
		}
	}
	return out
}

func IsValidAnthropicStableIdentityDeviceID(deviceID string) bool {
	deviceID = strings.TrimSpace(deviceID)
	if len(deviceID) != 64 || deviceID != strings.ToLower(deviceID) {
		return false
	}
	decoded, err := hex.DecodeString(deviceID)
	return err == nil && len(decoded) == 32
}

func NormalizeAnthropicStableIdentityState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case AnthropicStableIdentityStateActive:
		return AnthropicStableIdentityStateActive
	case AnthropicStableIdentityStatePaused:
		return AnthropicStableIdentityStatePaused
	default:
		return AnthropicStableIdentityStateOff
	}
}

func ValidateAnthropicStableIdentityAccount(account *Account) error {
	if account == nil || !account.IsAnthropicOAuthOrSetupToken() {
		return errors.New("stable identity requires an Anthropic OAuth or SetupToken account")
	}
	if account.Status != StatusActive {
		return errors.New("stable identity requires an active account")
	}
	if account.ProxyID != nil || account.Proxy != nil || account.ProxyFallbackOriginID != nil {
		return errors.New("stable identity does not support an account proxy")
	}
	if account.ParentAccountID != nil || account.HasAnthropicStableCanaryManagedFields() {
		return errors.New("stable identity requires an independent account outside the static canary")
	}
	if account.IsAnthropicOAuthPassthroughEnabled() || account.IsCustomBaseURLEnabled() ||
		account.IsCacheTTLOverrideEnabled() || account.IsSessionIDMaskingEnabled() || account.IsTLSFingerprintEnabled() {
		return errors.New("disable OAuth passthrough, custom base URL, cache TTL, session masking, and TLS fingerprint overrides first")
	}
	if strings.TrimSpace(account.GetCredential("base_url")) != "" ||
		len(account.GetModelMapping()) > 0 || len(account.GetCompactModelMapping()) > 0 {
		return errors.New("stable identity requires the native Anthropic endpoint and no model mapping")
	}
	if raw, ok := account.Credentials["header_overrides"]; ok && raw != nil {
		switch value := raw.(type) {
		case map[string]any:
			if len(value) > 0 {
				return errors.New("stable identity does not allow custom request headers")
			}
		case map[string]string:
			if len(value) > 0 {
				return errors.New("stable identity does not allow custom request headers")
			}
		default:
			return errors.New("stable identity request header configuration is malformed")
		}
	}
	if validateAnthropicStableOAuthAccessToken(account.GetCredential("access_token")) != nil ||
		validateAnthropicStableOAuthRefreshToken(account.GetCredential("refresh_token")) != nil {
		return errors.New("stable identity requires stored OAuth access and refresh tokens")
	}
	return nil
}

func ValidateAnthropicStableIdentityEnrolledAccount(account *Account) error {
	if err := ValidateAnthropicStableIdentityAccount(account); err != nil {
		return err
	}
	if !account.IsAnthropicStableIdentityEnabled() || account.Schedulable || account.Concurrency != 1 {
		return errors.New("stable identity account reservation is inconsistent")
	}
	if account.AnthropicStableIdentityGeneration() <= 0 ||
		!IsValidAnthropicStableIdentityDeviceID(account.AnthropicStableIdentityDeviceID()) ||
		!IsKnownAnthropicStableCanaryProfile(account.AnthropicStableIdentityProfileID()) {
		return errors.New("stable identity metadata is incomplete")
	}
	if _, ok := account.AnthropicStableIdentityPreviousSchedulable(); !ok {
		return errors.New("stable identity scheduler rollback state is incomplete")
	}
	if _, ok := account.AnthropicStableIdentityPreviousConcurrency(); !ok {
		return errors.New("stable identity concurrency rollback state is incomplete")
	}
	if _, ok := account.AnthropicStableIdentityPreviousGroupIDs(); !ok {
		return errors.New("stable identity group rollback state is incomplete")
	}
	groups, err := normalizeStableIdentityIDs(account.AnthropicStableIdentityGroupIDs())
	if err != nil {
		return err
	}
	keys, err := normalizeStableIdentityIDs(account.AnthropicStableIdentityAPIKeyIDs())
	if err != nil {
		return err
	}
	mapping := account.AnthropicStableIdentityAPIKeyGroupIDs()
	if len(mapping) != len(keys) {
		return errors.New("stable identity API-key route mapping is incomplete")
	}
	for _, keyID := range keys {
		groupID := mapping[keyID]
		if groupID <= 0 || !containsStableIdentityID(groups, groupID) || !accountBelongsToGroup(account, groupID) {
			return errors.New("stable identity API-key route mapping is inconsistent")
		}
	}
	return nil
}

func validateAnthropicStableIdentityAdminUpdate(account *Account, input *UpdateAccountInput) error {
	if input == nil {
		return nil
	}
	if AnthropicStableIdentityExtraUpdateTouchesManagedFields(input.Extra) {
		return ErrAnthropicStableIdentityManaged
	}
	if account == nil || !account.HasAnthropicStableIdentityManagedFields() {
		return nil
	}
	if (input.ProxyID != nil && *input.ProxyID != 0) ||
		(input.Type != "" && input.Type != account.Type) ||
		(input.Concurrency != nil && *input.Concurrency != 1) ||
		(input.Status != "" && input.Status != StatusActive) {
		return ErrAnthropicStableIdentityManaged
	}
	if len(input.Credentials) > 0 {
		candidate := *account
		candidate.Credentials = MergePreservingSensitiveCreds(account.Credentials, input.Credentials)
		if candidate.GetCredential("access_token") != account.GetCredential("access_token") ||
			candidate.GetCredential("refresh_token") != account.GetCredential("refresh_token") {
			return ErrAnthropicStableIdentityManaged
		}
		if err := NormalizeHeaderOverrideCredentials(candidate.Credentials); err != nil {
			return fmt.Errorf("%w: %v", ErrAnthropicStableIdentityManaged, err)
		}
		if err := ValidateAnthropicStableIdentityAccount(&candidate); err != nil {
			return fmt.Errorf("%w: %v", ErrAnthropicStableIdentityManaged, err)
		}
	}
	if input.GroupIDs != nil && !sameStableIdentityIDSet(*input.GroupIDs, account.GroupIDs) {
		// Group membership is part of the captured rollback state.  Letting an
		// ordinary edit add a group would make Disable silently discard that
		// later edit; removing a group could reopen a selected route elsewhere.
		// Reconfigure through the dedicated lifecycle instead.
		return ErrAnthropicStableIdentityManaged
	}
	if input.Extra != nil {
		candidate := *account
		candidate.Extra = preserveAnthropicStableIdentityManagedExtra(account.Extra, input.Extra)
		if err := ValidateAnthropicStableIdentityAccount(&candidate); err != nil {
			return fmt.Errorf("%w: %v", ErrAnthropicStableIdentityManaged, err)
		}
	}
	return nil
}

func validateAnthropicStableIdentityAccountServiceUpdate(account *Account, input UpdateAccountRequest) error {
	if input.Extra != nil && AnthropicStableIdentityExtraUpdateTouchesManagedFields(*input.Extra) {
		return ErrAnthropicStableIdentityManaged
	}
	if account == nil || !account.HasAnthropicStableIdentityManagedFields() {
		return nil
	}
	if input.Credentials != nil || input.ProxyID != nil ||
		(input.Concurrency != nil && *input.Concurrency != 1) ||
		(input.Status != nil && *input.Status != StatusActive) {
		return ErrAnthropicStableIdentityManaged
	}
	if input.GroupIDs != nil && !sameStableIdentityIDSet(*input.GroupIDs, account.GroupIDs) {
		return ErrAnthropicStableIdentityManaged
	}
	return nil
}

func preserveAnthropicStableIdentityManagedExtra(current, replacement map[string]any) map[string]any {
	out := shallowCopyMap(replacement)
	if out == nil {
		out = make(map[string]any)
	}
	for _, key := range anthropicStableIdentityManagedExtraKeys {
		if value, ok := current[key]; ok {
			out[key] = value
		}
	}
	return out
}

func anthropicStableIdentityBulkUpdateTouchesProtectedFields(input *BulkUpdateAccountsInput) bool {
	if input == nil {
		return false
	}
	return input.ProxyID != nil || input.Concurrency != nil || input.Status != "" ||
		input.Schedulable != nil || input.GroupIDs != nil || len(input.Credentials) > 0 || len(input.Extra) > 0
}

func normalizeStableIdentityIDs(values []int64) ([]int64, error) {
	seen := make(map[int64]struct{}, len(values))
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			return nil, errors.New("stable identity ids must be positive")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, errors.New("at least one stable identity id is required")
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func stableIdentityInt64(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case json.Number:
		parsed, _ := strconv.ParseInt(string(v), 10, 64)
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return parsed
	default:
		return 0
	}
}

func stableIdentityInt64Slice(value any) []int64 {
	var raw []any
	switch v := value.(type) {
	case []any:
		raw = v
	case []int64:
		return append([]int64(nil), v...)
	case []int:
		out := make([]int64, 0, len(v))
		for _, id := range v {
			out = append(out, int64(id))
		}
		return out
	default:
		return nil
	}
	out := make([]int64, 0, len(raw))
	seen := map[int64]struct{}{}
	for _, item := range raw {
		id := stableIdentityInt64(item)
		if id > 0 {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// stableIdentityOrderedInt64Slice is used only for rollback state, where group
// order carries account-group priority and therefore must not be sorted. The
// boolean distinguishes a deliberately captured empty list from missing or
// malformed metadata.
func stableIdentityOrderedInt64Slice(value any) ([]int64, bool) {
	if value == nil {
		return []int64{}, true
	}
	var values []int64
	switch raw := value.(type) {
	case []any:
		values = make([]int64, 0, len(raw))
		for _, item := range raw {
			values = append(values, stableIdentityInt64(item))
		}
	case []int64:
		values = append([]int64{}, raw...)
	case []int:
		values = make([]int64, 0, len(raw))
		for _, item := range raw {
			values = append(values, int64(item))
		}
	default:
		return nil, false
	}
	seen := make(map[int64]struct{}, len(values))
	for _, id := range values {
		if id <= 0 {
			return nil, false
		}
		if _, exists := seen[id]; exists {
			return nil, false
		}
		seen[id] = struct{}{}
	}
	return values, true
}

func containsStableIdentityID(values []int64, wanted int64) bool {
	if wanted <= 0 {
		return false
	}
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func sameStableIdentityIDSet(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	if len(left) == 0 {
		return true
	}
	a := append([]int64(nil), left...)
	b := append([]int64(nil), right...)
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	for i := range a {
		if a[i] <= 0 || a[i] != b[i] || (i > 0 && a[i] == a[i-1]) || (i > 0 && b[i] == b[i-1]) {
			return false
		}
	}
	return true
}

func stableIdentityNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }
