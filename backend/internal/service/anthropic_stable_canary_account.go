package service

import (
	"encoding/hex"
	"strings"
	"time"
)

// Stable canary account metadata is intentionally independent from the broader
// stable-identity executor. These JSON keys are managed only by a
// controlled operator enrollment procedure; ordinary account APIs must reject
// attempts to create or alter them.
const (
	AnthropicStableMessagesOriginV1                    = "https://api.anthropic.com"
	AnthropicStableMessagesPathV1                      = "/v1/messages"
	AnthropicStableOAuthTokenOriginV1                  = "https://platform.claude.com"
	AnthropicStableOAuthTokenPathV1                    = "/v1/oauth/token"
	AnthropicStableOAuthBetaV1                         = "oauth-2025-04-20"
	AnthropicStableDefaultAPIVersionV1                 = "2023-06-01"
	AnthropicStableIngressProfileClaudeCLICustomBaseV1 = "claude_cli_custom_base_v1"

	AnthropicStableCanaryEnabledExtraKey             = "anthropic_stable_canary"
	AnthropicStableCanaryDeviceIDExtraKey            = "anthropic_stable_canary_device_id"
	AnthropicStableCanaryReservedExtraKey            = "anthropic_stable_canary_reserved"
	AnthropicStableCanaryPreviousSchedulableExtraKey = "anthropic_stable_canary_previous_schedulable"
	AnthropicStableCanaryProfileExtraKey             = "anthropic_stable_canary_profile"
	AnthropicStableCanaryBlockedExtraKey             = "anthropic_stable_canary_blocked"
	AnthropicStableCanaryBlockedReasonExtraKey       = "anthropic_stable_canary_blocked_reason"
	AnthropicStableCanaryBlockedAtExtraKey           = "anthropic_stable_canary_blocked_at"
)

// IsValidAnthropicStableDeviceID accepts only the fixed 32-byte lowercase
// hexadecimal identifier captured from the known-good local client. It is
// intentionally strict so an operator cannot accidentally enroll a mutable or
// request-derived value.
func IsValidAnthropicStableDeviceID(deviceID string) bool {
	if len(deviceID) != 64 || deviceID != strings.ToLower(deviceID) {
		return false
	}
	decoded, err := hex.DecodeString(deviceID)
	return err == nil && len(decoded) == 32
}

func (a *Account) isRuntimeAvailableIgnoringLegacySchedulable() bool {
	if a == nil || !a.IsActive() {
		return false
	}
	now := time.Now()
	if a.AutoPauseOnExpired && a.ExpiresAt != nil && !now.Before(*a.ExpiresAt) {
		return false
	}
	if a.OverloadUntil != nil && now.Before(*a.OverloadUntil) {
		return false
	}
	if a.RateLimitResetAt != nil && now.Before(*a.RateLimitResetAt) {
		return false
	}
	if a.TempUnschedulableUntil != nil && now.Before(*a.TempUnschedulableUntil) {
		return false
	}
	return true
}

var anthropicStableCanaryManagedExtraKeys = [...]string{
	AnthropicStableCanaryEnabledExtraKey,
	AnthropicStableCanaryDeviceIDExtraKey,
	AnthropicStableCanaryReservedExtraKey,
	AnthropicStableCanaryPreviousSchedulableExtraKey,
	AnthropicStableCanaryProfileExtraKey,
	AnthropicStableCanaryBlockedExtraKey,
	AnthropicStableCanaryBlockedReasonExtraKey,
	AnthropicStableCanaryBlockedAtExtraKey,
}

func AnthropicStableCanaryExtraUpdateTouchesManagedFields(extra map[string]any) bool {
	for _, key := range anthropicStableCanaryManagedExtraKeys {
		if _, ok := extra[key]; ok {
			return true
		}
	}
	return false
}

func (a *Account) HasAnthropicStableCanaryManagedFields() bool {
	return a != nil && AnthropicStableCanaryExtraUpdateTouchesManagedFields(a.Extra)
}

func (a *Account) IsAnthropicStableCanaryEnabled() bool {
	if a == nil || a.Platform != PlatformAnthropic || a.Type != AccountTypeOAuth || a.Extra == nil {
		return false
	}
	enabled, ok := a.Extra[AnthropicStableCanaryEnabledExtraKey].(bool)
	return ok && enabled
}

func (a *Account) IsAnthropicStableCanaryReserved() bool {
	if a == nil || a.Extra == nil {
		return false
	}
	reserved, ok := a.Extra[AnthropicStableCanaryReservedExtraKey].(bool)
	return ok && reserved
}

func (a *Account) AnthropicStableCanaryDeviceID() string {
	if a == nil || !a.IsAnthropicStableCanaryEnabled() {
		return ""
	}
	return strings.TrimSpace(a.GetExtraString(AnthropicStableCanaryDeviceIDExtraKey))
}

func (a *Account) AnthropicStableCanaryProfileID() string {
	if a == nil || !a.IsAnthropicStableCanaryEnabled() {
		return ""
	}
	return strings.TrimSpace(a.GetExtraString(AnthropicStableCanaryProfileExtraKey))
}

func (a *Account) AnthropicStableCanaryPreviousSchedulable() (bool, bool) {
	if a == nil || a.Extra == nil {
		return false, false
	}
	value, ok := a.Extra[AnthropicStableCanaryPreviousSchedulableExtraKey].(bool)
	return value, ok
}

func (a *Account) IsAnthropicStableCanaryBlocked() bool {
	if a == nil || a.Extra == nil {
		return false
	}
	blocked, ok := a.Extra[AnthropicStableCanaryBlockedExtraKey].(bool)
	return ok && blocked
}

func (a *Account) AnthropicStableCanaryBlockedReason() string {
	if a == nil || a.Extra == nil {
		return ""
	}
	reason, _ := a.Extra[AnthropicStableCanaryBlockedReasonExtraKey].(string)
	return strings.TrimSpace(reason)
}
