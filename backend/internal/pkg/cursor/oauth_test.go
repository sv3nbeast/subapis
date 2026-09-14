package cursor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
	"time"
)

func TestGeneratePKCEProducesS256Challenge(t *testing.T) {
	verifier, challenge, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != want {
		t.Errorf("challenge = %q, want the S256 of the verifier (%q)", challenge, want)
	}
	// 两次调用必须产生不同的 verifier，否则并发登录会互相串号。
	other, _, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE (second): %v", err)
	}
	if other == verifier {
		t.Error("GeneratePKCE returned the same verifier twice")
	}
}

func TestBuildLoginURLCarriesChallengeAndUUID(t *testing.T) {
	raw := BuildLoginURL("chal+lenge/value", "uuid-1234")
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("BuildLoginURL produced an unparsable URL %q: %v", raw, err)
	}
	query := parsed.Query()
	if got := query.Get("challenge"); got != "chal+lenge/value" {
		t.Errorf("challenge = %q, want it escaped and round-tripped", got)
	}
	if got := query.Get("uuid"); got != "uuid-1234" {
		t.Errorf("uuid = %q, want %q", got, "uuid-1234")
	}
	if got := query.Get("redirectTarget"); got != "cli" {
		t.Errorf("redirectTarget = %q, want cli", got)
	}
}

func TestGenerateTelemetryIDMatchesCursorShape(t *testing.T) {
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	first, err := GenerateTelemetryID()
	if err != nil {
		t.Fatalf("GenerateTelemetryID: %v", err)
	}
	if !hex64.MatchString(first) {
		t.Errorf("telemetry id = %q, want 64 lowercase hex characters", first)
	}
	second, err := GenerateTelemetryID()
	if err != nil {
		t.Fatalf("GenerateTelemetryID (second): %v", err)
	}
	if first == second {
		t.Error("GenerateTelemetryID returned the same id twice; accounts would share a device fingerprint")
	}
}

func TestSessionStoreLifecycle(t *testing.T) {
	store := NewSessionStore()
	session := &OAuthSession{UUID: "u", Verifier: "v", CreatedAt: time.Now()}
	store.Set("sid", session)

	if got, ok := store.Get("sid"); !ok || got.UUID != "u" {
		t.Fatalf("Get = (%+v, %v), want the stored session", got, ok)
	}
	store.Delete("sid")
	if _, ok := store.Get("sid"); ok {
		t.Error("session survived Delete; credentials could be claimed twice")
	}

	// 过期会话不可用，否则一个被遗弃的登录尝试会一直可被领取。
	store.Set("stale", &OAuthSession{CreatedAt: time.Now().Add(-OAuthSessionTTL - time.Minute)})
	if _, ok := store.Get("stale"); ok {
		t.Error("expired session was returned")
	}
}

func TestPollSession(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantPending bool
		wantErr     bool
		wantToken   string
	}{
		{name: "404 means the user has not finished login", status: http.StatusNotFound, wantPending: true},
		{
			name:      "200 with tokens completes the login",
			status:    http.StatusOK,
			body:      `{"accessToken":"jwt-token","refreshToken":"refresh-token"}`,
			wantToken: "jwt-token",
		},
		{
			// 200 但没有 token 是还没就绪，继续等比报错贴合实际。
			name:        "200 without a token keeps waiting",
			status:      http.StatusOK,
			body:        `{}`,
			wantPending: true,
		},
		{name: "server errors surface", status: http.StatusInternalServerError, body: "boom", wantErr: true},
		{name: "malformed payloads surface", status: http.StatusOK, body: "not json", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotQuery url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query()
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			restore := pollURL
			pollURL = server.URL
			defer func() { pollURL = restore }()

			tokens, pending, err := PollSession(context.Background(), &OAuthSession{
				UUID: "uuid-1", Verifier: "verifier-1",
			})
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("PollSession: %v", err)
			}
			if gotQuery.Get("uuid") != "uuid-1" || gotQuery.Get("verifier") != "verifier-1" {
				t.Errorf("poll query = %v, want the session uuid and verifier", gotQuery)
			}
			if pending != tt.wantPending {
				t.Fatalf("pending = %v, want %v", pending, tt.wantPending)
			}
			if tt.wantPending {
				return
			}
			if tokens.AccessToken != tt.wantToken {
				t.Errorf("access token = %q, want %q", tokens.AccessToken, tt.wantToken)
			}
			if tokens.RefreshToken != "refresh-token" {
				t.Errorf("refresh token = %q, want %q", tokens.RefreshToken, "refresh-token")
			}
		})
	}
}

func TestPollSessionRejectsMissingSession(t *testing.T) {
	if _, _, err := PollSession(context.Background(), nil); err == nil {
		t.Error("PollSession(nil) = nil error, want a failure")
	}
}

// jwtWithClaims builds an unsigned JWT carrying the given payload.
func jwtWithClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func TestUserID(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "provider-prefixed sub keeps the trailing id",
			token: jwtWithClaims(t, map[string]any{"sub": "auth0|user_01HXYZ"}),
			want:  "user_01HXYZ",
		},
		{
			name:  "plain sub passes through",
			token: jwtWithClaims(t, map[string]any{"sub": "user_01HXYZ"}),
			want:  "user_01HXYZ",
		},
		{
			name:  "the userId:: prefix is stripped before decoding",
			token: "user_01H::" + jwtWithClaims(t, map[string]any{"sub": "auth0|user_01HXYZ"}),
			want:  "user_01HXYZ",
		},
		{name: "not a jwt", token: "opaque-token"},
		{name: "empty", token: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UserID(tt.token); got != tt.want {
				t.Errorf("UserID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAccountEmail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("authorization"); got != "Bearer jwt-token" {
			t.Errorf("authorization = %q, want the bare JWT as a bearer token", got)
		}
		_, _ = w.Write([]byte(`{"email":"dev@example.com"}`))
	}))
	defer server.Close()

	restore := authMeEndpoint
	authMeEndpoint = server.URL
	defer func() { authMeEndpoint = restore }()

	// 前缀形式的 token 也要能取到邮箱。
	if got := AccountEmail(context.Background(), "user_01H::jwt-token", ""); got != "dev@example.com" {
		t.Errorf("AccountEmail = %q, want %q", got, "dev@example.com")
	}
}

func TestAccountEmailFailsSoft(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	restore := authMeEndpoint
	authMeEndpoint = server.URL
	defer func() { authMeEndpoint = restore }()

	// 邮箱只是展示用，取不到不能让整条授权失败。
	if got := AccountEmail(context.Background(), "jwt-token", ""); got != "" {
		t.Errorf("AccountEmail = %q, want empty on failure", got)
	}
}

func TestRefreshFallsBackToOAuthTokenEndpoint(t *testing.T) {
	var exchangeCalls, oauthCalls int
	exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exchangeCalls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer exchange.Close()
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oauthCalls++
		_, _ = w.Write([]byte(`{"access_token":"new-jwt","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer oauth.Close()

	restoreExchange, restoreOAuth := exchangeTokenURL, oauthTokenURL
	exchangeTokenURL, oauthTokenURL = exchange.URL, oauth.URL
	defer func() { exchangeTokenURL, oauthTokenURL = restoreExchange, restoreOAuth }()

	result, err := RefreshSession(context.Background(), exchange.Client(), "old-refresh")
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if exchangeCalls != 1 || oauthCalls != 1 {
		t.Errorf("calls: exchange=%d oauth=%d, want one each", exchangeCalls, oauthCalls)
	}
	if result.AccessToken != "new-jwt" || result.RefreshToken != "new-refresh" || result.ExpiresIn != 3600 {
		t.Errorf("result = %+v, want the oauth endpoint's tokens", result)
	}
}

func TestRefreshPrefersExchangeEndpoint(t *testing.T) {
	var oauthCalls int
	exchange := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer old-refresh" {
			t.Errorf("authorization = %q, want the refresh token as a bearer token", got)
		}
		_, _ = w.Write([]byte(`{"accessToken":"new-jwt"}`))
	}))
	defer exchange.Close()
	oauth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oauthCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer oauth.Close()

	restoreExchange, restoreOAuth := exchangeTokenURL, oauthTokenURL
	exchangeTokenURL, oauthTokenURL = exchange.URL, oauth.URL
	defer func() { exchangeTokenURL, oauthTokenURL = restoreExchange, restoreOAuth }()

	result, err := RefreshSession(context.Background(), exchange.Client(), "old-refresh")
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if oauthCalls != 0 {
		t.Error("the oauth endpoint was called even though exchange succeeded")
	}
	// 该端点不轮换 refresh token，必须沿用原值而不是把它清空。
	if result.RefreshToken != "old-refresh" {
		t.Errorf("refresh token = %q, want the original to be retained", result.RefreshToken)
	}
	if result.AccessToken != "new-jwt" {
		t.Errorf("access token = %q, want %q", result.AccessToken, "new-jwt")
	}
}
