package service

import (
	"fmt"
	"strings"
)

const (
	GrokOAuthRefreshFailureCodeCredentialKey = "_oauth_refresh_failure_code"
	GrokOAuthRefreshFailureAtCredentialKey   = "_oauth_refresh_failure_at"
)

func grokOAuthStoredPermanentRefreshFailure(account *Account) string {
	if account == nil || !account.IsGrokOAuth() {
		return ""
	}
	return strings.TrimSpace(account.GetCredential(GrokOAuthRefreshFailureCodeCredentialKey))
}

func grokOAuthPermanentRefreshFailureCode(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	for _, code := range []string{
		"invalid_grant",
		"invalid_refresh_token",
		"token_expired",
		"refresh_token_reused",
		"refresh_token_invalidated",
		"app_session_terminated",
		"access_denied",
		"entitlement_denied",
		"subscription_required",
	} {
		if strings.Contains(message, code) {
			return code
		}
	}
	return "credential_rejected"
}

func grokOAuthStoredRefreshFailureError(account *Account) error {
	code := grokOAuthStoredPermanentRefreshFailure(account)
	if code == "" {
		return nil
	}
	return fmt.Errorf("grok oauth refresh token permanently rejected: %s", code)
}

func clearGrokOAuthRefreshFailure(credentials map[string]any) map[string]any {
	delete(credentials, GrokOAuthRefreshFailureCodeCredentialKey)
	delete(credentials, GrokOAuthRefreshFailureAtCredentialKey)
	return credentials
}
