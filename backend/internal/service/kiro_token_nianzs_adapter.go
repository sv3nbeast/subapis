package service

import (
	"context"
	"errors"
	"fmt"
)

// getNianzsKiroAccessToken mirrors the pinned nianzs GatewayService token
// selection: the persisted access token is used for the first OAuth request;
// the nianzs token provider is invoked only by its 401 refresh path.
func (s *GatewayService) getNianzsKiroAccessToken(_ context.Context, account *Account) (string, string, error) {
	if account == nil {
		return "", "", errors.New("account is nil")
	}
	switch account.Type {
	case AccountTypeOAuth, AccountTypeSetupToken:
		accessToken := account.GetCredential("access_token")
		if accessToken == "" {
			return "", "", errors.New("access_token not found in credentials")
		}
		return accessToken, "oauth", nil
	case AccountTypeAPIKey:
		apiKey := account.GetCredential("api_key")
		if apiKey == "" {
			return "", "", errors.New("api_key not found in credentials")
		}
		return apiKey, "apikey", nil
	default:
		return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
	}
}
