package service

import "context"

// adaptKiroAccountForNianzs converts the production-only "OAuth account with
// attached CLI key" representation into the canonical API-key account shape
// understood by the pinned nianzs runtime. The returned value is a detached
// shallow copy; persisted credentials are never rewritten.
//
// This compatibility seam preserves existing imported CLI accounts while the
// request itself still follows nianzs' API-key path: Q endpoint, API_KEY token
// type, API-key-derived machine identity, and no OAuth/profile headers.
func adaptKiroAccountForNianzs(account *Account) *Account {
	if account == nil || !account.IsKiro() {
		return account
	}
	apiKey := account.KiroAPIKey()
	if apiKey == "" {
		return account
	}

	adapted := *account
	adapted.Type = AccountTypeAPIKey
	adapted.Credentials = make(map[string]any, len(account.Credentials)+1)
	for key, value := range account.Credentials {
		adapted.Credentials[key] = value
	}
	for _, key := range []string{
		"access_token", "refresh_token", "client_id", "client_id_hash", "client_secret",
		"profile_arn", "auth_method", "provider", "token_endpoint", "issuer_url",
		"start_url", "sso_region", "idc_region",
	} {
		delete(adapted.Credentials, key)
	}
	adapted.Credentials["api_key"] = apiKey
	// OAuth accounts are always native in the production representation. Do not
	// let a stale relay URL turn the adapted CLI credential into an Anthropic
	// relay account.
	adapted.Credentials["base_url"] = ""
	return &adapted
}

// isStickyAccountUpstreamRestricted is present in the pinned nianzs scheduler
// and composed from restriction helpers already carried by this production
// baseline.
func (s *GatewayService) isStickyAccountUpstreamRestricted(ctx context.Context, groupID *int64, account *Account, requestedModel string) bool {
	if groupID == nil || !s.needsUpstreamChannelRestrictionCheck(ctx, groupID) {
		return false
	}
	return s.isUpstreamModelRestrictedByChannel(ctx, *groupID, account, requestedModel)
}
