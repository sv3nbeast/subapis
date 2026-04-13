package service

import (
	"strings"
	"time"
)

const (
	antigravityOAuth401TempUnschedDuration = 3 * time.Minute
	antigravityOAuth401RefreshAtKey        = "_ag_oauth_401_refresh_at"
)

func antigravityOAuth401RefreshAt(account *Account) *time.Time {
	if account == nil {
		return nil
	}
	return account.GetCredentialAsTime(antigravityOAuth401RefreshAtKey)
}

func antigravityOAuth401RefreshPending(account *Account, now time.Time) bool {
	refreshAt := antigravityOAuth401RefreshAt(account)
	return refreshAt != nil && now.Before(*refreshAt)
}

func antigravityOAuth401RefreshDue(account *Account, now time.Time) bool {
	refreshAt := antigravityOAuth401RefreshAt(account)
	return refreshAt != nil && !now.Before(*refreshAt)
}

func scheduleAntigravityOAuth401RefreshCredentials(account *Account, refreshAt time.Time) map[string]any {
	credentials := cloneCredentials(account.Credentials)
	credentials[antigravityOAuth401RefreshAtKey] = refreshAt.UTC().Format(time.RFC3339)
	return credentials
}

func ClearAntigravityOAuth401RefreshCredentials(credentials map[string]any) {
	delete(credentials, antigravityOAuth401RefreshAtKey)
}

func IsRecoverableAntigravityAuthErrorMessage(msg string) bool {
	msg = strings.ToLower(strings.TrimSpace(msg))
	if msg == "" {
		return false
	}
	return strings.Contains(msg, "authentication failed (401)") ||
		strings.Contains(msg, "invalid bearer token")
}
