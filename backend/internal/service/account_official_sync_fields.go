package service

import (
	"context"
	"maps"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const openAILongContextBillingEnabledKey = "openai_long_context_billing_enabled"

func (a *Account) IsOpenAILongContextBillingEnabled() bool {
	if a == nil || !a.IsOpenAI() || a.Extra == nil {
		return false
	}
	enabled, ok := a.Extra[openAILongContextBillingEnabledKey].(bool)
	return ok && enabled
}

func ValidateOpenAILongContextBillingExtra(platform string, extra map[string]any) error {
	if platform != PlatformOpenAI {
		return nil
	}
	raw, exists := extra[openAILongContextBillingEnabledKey]
	if !exists {
		return nil
	}
	if _, ok := raw.(bool); !ok {
		return infraerrors.BadRequest(
			"OPENAI_LONG_CONTEXT_BILLING_INVALID",
			"openai_long_context_billing_enabled must be a boolean",
		)
	}
	return nil
}

func normalizeOpenAILongContextBillingExtra(platform string, extra map[string]any) (map[string]any, error) {
	if platform != PlatformOpenAI {
		return extra, nil
	}
	if err := ValidateOpenAILongContextBillingExtra(platform, extra); err != nil {
		return nil, err
	}
	normalized := maps.Clone(extra)
	if normalized == nil {
		normalized = make(map[string]any, 1)
	}
	if _, exists := normalized[openAILongContextBillingEnabledKey]; !exists {
		normalized[openAILongContextBillingEnabledKey] = false
	}
	return normalized, nil
}

func normalizeOpenAILongContextBillingUpdateExtra(account *Account, input *UpdateAccountInput) (map[string]any, error) {
	normalized, err := normalizeOpenAILongContextBillingExtra(account.Platform, input.Extra)
	if err != nil || account.Platform != PlatformOpenAI {
		return normalized, err
	}

	_, provided := input.Extra[openAILongContextBillingEnabledKey]
	current, hasCurrent := account.Extra[openAILongContextBillingEnabledKey].(bool)
	if !provided && hasCurrent {
		normalized[openAILongContextBillingEnabledKey] = current
	}
	return normalized, nil
}

type accountProbeEnabledAtomicUpdater interface {
	UpdateWithUpstreamBillingProbeEnabled(context.Context, *Account, bool) error
}

func upstreamBillingProbeIdentity(account *Account) map[string]any {
	if account == nil {
		return nil
	}
	identity := map[string]any{"platform": account.Platform, "type": account.Type, "proxy_id": nil}
	if account.ProxyID != nil {
		identity["proxy_id"] = *account.ProxyID
	}
	for _, key := range []string{"api_key", "base_url", credKeyHeaderOverrideEnabled, credKeyHeaderOverrides} {
		if value, ok := account.Credentials[key]; ok {
			identity[key] = value
		}
	}
	return identity
}
