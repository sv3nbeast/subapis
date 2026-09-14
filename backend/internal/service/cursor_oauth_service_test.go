package service

import (
	"regexp"
	"strings"
	"testing"
)

func TestCursorGenerateAuthURLProducesDistinctSessions(t *testing.T) {
	svc := NewCursorOAuthService(nil)

	first, err := svc.GenerateAuthURL(t.Context(), nil)
	if err != nil {
		t.Fatalf("GenerateAuthURL: %v", err)
	}
	if !strings.Contains(first.AuthURL, "challenge=") || !strings.Contains(first.AuthURL, "uuid=") {
		t.Errorf("auth url = %q, want challenge and uuid query parameters", first.AuthURL)
	}
	if first.SessionID == "" {
		t.Error("session id is empty")
	}

	second, err := svc.GenerateAuthURL(t.Context(), nil)
	if err != nil {
		t.Fatalf("GenerateAuthURL (second): %v", err)
	}
	// 并发添加两个账号时会同时存在两次登录尝试，它们必须互不干扰。
	if second.SessionID == first.SessionID {
		t.Error("two authorization attempts share a session id")
	}
	if second.AuthURL == first.AuthURL {
		t.Error("two authorization attempts share an authorization URL")
	}
}

func TestCursorPollRejectsUnknownSession(t *testing.T) {
	svc := NewCursorOAuthService(nil)
	if _, err := svc.Poll(t.Context(), "", nil); err == nil {
		t.Error("empty session id was accepted")
	}
	if _, err := svc.Poll(t.Context(), "does-not-exist", nil); err == nil {
		t.Error("unknown session id was accepted")
	}
}

func TestCursorBuildAccountCredentials(t *testing.T) {
	svc := NewCursorOAuthService(nil)
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)

	full := svc.BuildAccountCredentials(&CursorTokenInfo{
		AccessToken:  "jwt-token",
		RefreshToken: "refresh-token",
		MachineID:    strings.Repeat("a", 64),
		MacMachineID: strings.Repeat("b", 64),
		ExpiresAt:    "2026-09-15T00:00:00Z",
		UserID:       "user_01H",
		Email:        "dev@example.com",
	})
	for key, want := range map[string]string{
		"access_token":   "jwt-token",
		"refresh_token":  "refresh-token",
		"machine_id":     strings.Repeat("a", 64),
		"mac_machine_id": strings.Repeat("b", 64),
		"expires_at":     "2026-09-15T00:00:00Z",
		"user_id":        "user_01H",
		"email":          "dev@example.com",
	} {
		if got, _ := full[key].(string); got != want {
			t.Errorf("credentials[%q] = %q, want %q", key, got, want)
		}
	}

	// 空的可选字段不写入，避免在凭证里留下空串。
	minimal := svc.BuildAccountCredentials(&CursorTokenInfo{
		AccessToken:  "jwt-token",
		MachineID:    strings.Repeat("a", 64),
		MacMachineID: strings.Repeat("b", 64),
	})
	for _, key := range []string{"refresh_token", "expires_at", "user_id", "email"} {
		if _, ok := minimal[key]; ok {
			t.Errorf("credentials carry empty %q", key)
		}
	}
	if got := svc.BuildAccountCredentials(nil); len(got) != 0 {
		t.Errorf("nil token info produced %v, want an empty map", got)
	}

	// 生成的设备标识必须是 Cursor 形态，否则 checksum 头会被上游拒绝。
	if id, _ := full["machine_id"].(string); !hex64.MatchString(id) {
		t.Errorf("machine_id = %q, want 64 hex characters", id)
	}
}

func TestCursorOAuthCredentialsFeedTheGatewayUnchanged(t *testing.T) {
	// 授权产出的凭证必须能被网关直接读成上游认证数据——两边字段名对不上
	// 会让账号建出来就不可用，而这只有在真实请求时才会暴露。
	svc := NewCursorOAuthService(nil)
	credentials := svc.BuildAccountCredentials(&CursorTokenInfo{
		AccessToken:  "user_01H::jwt-token",
		MachineID:    strings.Repeat("a", 64),
		MacMachineID: strings.Repeat("b", 64),
	})
	account := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: credentials}

	creds := CursorCredentialsFromAccount(account)
	if creds.AccessToken != "jwt-token" {
		t.Errorf("AccessToken = %q, want the prefix stripped", creds.AccessToken)
	}
	if creds.MachineID != strings.Repeat("a", 64) || creds.MacMachineID != strings.Repeat("b", 64) {
		t.Errorf("telemetry ids did not survive the round trip: %+v", creds)
	}
}
