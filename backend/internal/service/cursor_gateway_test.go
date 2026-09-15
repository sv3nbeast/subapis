package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
)

func TestNormalizeCursorAccessToken(t *testing.T) {
	tests := map[string]string{
		"":                         "",
		"  jwt-token  ":            "jwt-token",
		"user_01H::jwt-token":      "jwt-token",
		"user_01H::  jwt-token  ":  "jwt-token",
		"jwt::with::more-segments": "with::more-segments",
	}
	for in, want := range tests {
		if got := normalizeCursorAccessToken(in); got != want {
			t.Errorf("normalizeCursorAccessToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCursorCredentialsFromAccount(t *testing.T) {
	account := &Account{
		Platform: PlatformCursor,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":    "user_01H::jwt-token",
			"machine_id":      strings.Repeat("a", 64),
			"mac_machine_id":  strings.Repeat("b", 64),
			"client_version":  "3.16.17",
			"client_commit":   "deadbeef",
			"ghost_mode":      "true",
			"unrelated_field": "ignored",
		},
	}

	creds := CursorCredentialsFromAccount(account)
	if creds.AccessToken != "jwt-token" {
		t.Errorf("AccessToken = %q, want the bare JWT", creds.AccessToken)
	}
	if creds.MachineID != strings.Repeat("a", 64) || creds.MacMachineID != strings.Repeat("b", 64) {
		t.Errorf("telemetry ids not carried: %+v", creds)
	}
	if creds.ClientVersion != "3.16.17" || creds.ClientCommit != "deadbeef" {
		t.Errorf("client identity not carried: %+v", creds)
	}
	if !creds.GhostMode {
		t.Error("GhostMode = false, want true")
	}

	if got := CursorCredentialsFromAccount(nil); got != (cursor.Credentials{}) {
		t.Errorf("nil account produced %+v, want the zero value", got)
	}
}

func TestCursorTokenCacheKeySharesCredentialAcrossAccounts(t *testing.T) {
	withRefresh := func(id int64) *Account {
		return &Account{ID: id, Credentials: map[string]any{"refresh_token": "shared-refresh"}}
	}
	if a, b := CursorTokenCacheKey(withRefresh(1)), CursorTokenCacheKey(withRefresh(2)); a != b {
		t.Errorf("accounts sharing a refresh token got different keys: %q vs %q", a, b)
	}
	// Without a refresh token the key must stay account-scoped, otherwise two
	// unrelated static-token accounts would share one cached access token.
	a := CursorTokenCacheKey(&Account{ID: 1})
	b := CursorTokenCacheKey(&Account{ID: 2})
	if a == b {
		t.Errorf("accounts without refresh tokens collided on key %q", a)
	}
}

func TestCursorUnsupportedCapability(t *testing.T) {
	if got := cursorUnsupportedCapability(cursorRunRequest{}); got != "" {
		t.Errorf("plain text request reported %q, want it servable", got)
	}
	// Silently dropping tools would leave Claude Code / Codex waiting forever
	// for a tool call the Ask-mode upstream can never emit.
	if got := cursorUnsupportedCapability(cursorRunRequest{HasTools: true}); got != "tool use" {
		t.Errorf("tools reported %q, want %q", got, "tool use")
	}
	// Dropping images silently is worse than failing: the model answers
	// confidently about an image it never received.
	if got := cursorUnsupportedCapability(cursorRunRequest{HasImages: true}); got != "image input" {
		t.Errorf("images reported %q, want %q", got, "image input")
	}
}

func TestCursorCapabilityErrorStaysFailoverableWhenMixedScheduled(t *testing.T) {
	// Mixed scheduling: the group has other accounts that can serve this, so the
	// error must be failoverable. A plain error would end the request instead.
	var failoverErr *UpstreamFailoverError
	if !errors.As(cursorCapabilityError("tool use", true), &failoverErr) {
		t.Fatal("mixed-scheduled capability error is not an UpstreamFailoverError; the request could not move to another account")
	}
	if !failoverErr.RequestScopedTransient {
		t.Error("RequestScopedTransient = false; the account would be penalized for a request-shape mismatch")
	}

	// A dedicated Cursor group has nobody to defer to; failing over would only
	// burn a cycle, so the client should be told why.
	if errors.As(cursorCapabilityError("tool use", false), &failoverErr) {
		t.Error("dedicated-group capability error is failoverable; it should terminate with a reason")
	}
}

func TestCursorMixedScheduled(t *testing.T) {
	cursorAccount := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth}

	hydrated := func(platform string) *Group {
		return &Group{ID: 1, Platform: platform, Status: StatusActive, Hydrated: true}
	}

	tests := []struct {
		name    string
		ctx     context.Context
		account *Account
		want    bool
	}{
		{
			name:    "anthropic group means the account was picked as a stand-in",
			ctx:     context.WithValue(context.Background(), ctxkey.Group, hydrated(PlatformAnthropic)),
			account: cursorAccount,
			want:    true,
		},
		{
			name:    "dedicated cursor group is a direct choice",
			ctx:     context.WithValue(context.Background(), ctxkey.Group, hydrated(PlatformCursor)),
			account: cursorAccount,
		},
		{
			// Guessing "dedicated" here would 400 a request another account in
			// the group could have served; staying failoverable costs only the
			// explanatory message.
			name:    "missing group context stays failoverable",
			ctx:     context.Background(),
			account: cursorAccount,
			want:    true,
		},
		{
			name:    "unhydrated group is treated as unknown, not dedicated",
			ctx:     context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 1, Platform: PlatformCursor}),
			account: cursorAccount,
			want:    true,
		},
		{
			name:    "non-cursor account",
			ctx:     context.WithValue(context.Background(), ctxkey.Group, hydrated(PlatformAnthropic)),
			account: &Account{Platform: PlatformKiro},
		},
		{
			name: "nil account",
			ctx:  context.Background(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cursorMixedScheduled(tt.ctx, tt.account); got != tt.want {
				t.Errorf("cursorMixedScheduled = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCursorSupportsMixedScheduling(t *testing.T) {
	account := &Account{Platform: PlatformCursor, Type: AccountTypeOAuth}
	if !account.SupportsMixedScheduling() {
		t.Fatal("cursor accounts cannot opt into mixed scheduling")
	}
	// Opt-in only: a cursor account must not be pulled into an anthropic group
	// until the operator says so.
	if account.IsMixedSchedulingEnabled() {
		t.Error("mixed scheduling is on without the account opting in")
	}
	account.Extra = map[string]any{"mixed_scheduling": true}
	if !account.IsMixedSchedulingEnabled() {
		t.Error("mixed scheduling stayed off after opting in")
	}
	if !isAccountAllowedInMixedScheduling(account, PlatformAnthropic) {
		t.Error("an opted-in cursor account is not allowed in an anthropic group")
	}
	// Cursor serves no Gemini-shaped traffic.
	if isAccountAllowedInMixedScheduling(account, PlatformGemini) {
		t.Error("cursor was allowed into a gemini group")
	}
	if !containsString(mixedSchedulingQueryPlatforms(PlatformAnthropic), PlatformCursor) {
		t.Error("anthropic scheduling does not query cursor accounts")
	}
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestCursorRunOptsFromAnthropic(t *testing.T) {
	enabled := true
	disabled := false
	tests := []struct {
		name         string
		req          *apicompat.AnthropicRequest
		wantEffort   string
		wantThinking *bool
	}{
		{name: "nil request", req: nil},
		{
			name:       "output_config effort is normalized",
			req:        &apicompat.AnthropicRequest{OutputConfig: &apicompat.AnthropicOutputConfig{Effort: "extra-high"}},
			wantEffort: "xhigh",
		},
		{
			name:         "thinking enabled",
			req:          &apicompat.AnthropicRequest{Thinking: &apicompat.AnthropicThinking{Type: "enabled"}},
			wantThinking: &enabled,
		},
		{
			name:         "thinking adaptive counts as enabled",
			req:          &apicompat.AnthropicRequest{Thinking: &apicompat.AnthropicThinking{Type: "adaptive"}},
			wantThinking: &enabled,
		},
		{
			name:         "thinking disabled is explicit, not absent",
			req:          &apicompat.AnthropicRequest{Thinking: &apicompat.AnthropicThinking{Type: "disabled"}},
			wantThinking: &disabled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := cursorRunOptsFromAnthropic(tt.req)
			if opts.Effort != tt.wantEffort {
				t.Errorf("Effort = %q, want %q", opts.Effort, tt.wantEffort)
			}
			switch {
			case tt.wantThinking == nil && opts.Thinking != nil:
				t.Errorf("Thinking = %v, want nil", *opts.Thinking)
			case tt.wantThinking != nil && opts.Thinking == nil:
				t.Errorf("Thinking = nil, want %v", *tt.wantThinking)
			case tt.wantThinking != nil && *opts.Thinking != *tt.wantThinking:
				t.Errorf("Thinking = %v, want %v", *opts.Thinking, *tt.wantThinking)
			}
		})
	}
}

func TestCursorRunOptsFromResponsesAndChat(t *testing.T) {
	if got := cursorRunOptsFromResponses(&apicompat.ResponsesRequest{
		Reasoning: &apicompat.ResponsesReasoning{Effort: "HIGH"},
	}); got.Effort != "high" {
		t.Errorf("responses effort = %q, want %q", got.Effort, "high")
	}
	if got := cursorRunOptsFromResponses(&apicompat.ResponsesRequest{}); got.Effort != "" {
		t.Errorf("responses without reasoning = %q, want empty", got.Effort)
	}
	if got := cursorRunOptsFromChat(&apicompat.ChatCompletionsRequest{
		ReasoningEffort: "minimal",
	}); got.Effort != "minimal" {
		t.Errorf("chat effort = %q, want %q", got.Effort, "minimal")
	}
	if got := cursorRunOptsFromChat(nil); got.Effort != "" {
		t.Errorf("nil chat request = %q, want empty", got.Effort)
	}
}

func TestCursorContentText(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		want      string
		wantImage bool
	}{
		{name: "plain string", raw: `"hello"`, want: "hello"},
		{name: "null collapses to empty", raw: `null`, want: ""},
		{name: "absent collapses to empty", raw: ``, want: ""},
		{name: "content parts are concatenated", raw: `[{"type":"text","text":"a"},{"type":"text","text":"b"}]`, want: "ab"},
		{name: "untyped parts count as text", raw: `[{"text":"a"}]`, want: "a"},
		{
			name:      "image parts are reported, not silently dropped",
			raw:       `[{"type":"image_url"},{"type":"text","text":"keep"}]`,
			want:      "keep",
			wantImage: true,
		},
		{
			name:      "responses-style input_image is reported too",
			raw:       `[{"type":"input_image"},{"type":"text","text":"keep"}]`,
			want:      "keep",
			wantImage: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, hadImage := cursorContentText(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Errorf("cursorContentText(%s) = %q, want %q", tt.raw, got, tt.want)
			}
			if hadImage != tt.wantImage {
				t.Errorf("hadImage = %v, want %v", hadImage, tt.wantImage)
			}
		})
	}
}

func TestCursorMessagesFromChatFlattensContent(t *testing.T) {
	got, droppedImages := cursorMessagesFromChat([]apicompat.ChatMessage{
		{Role: "system", Content: json.RawMessage(`"be terse"`)},
		{Role: "user", Content: json.RawMessage(`[{"type":"text","text":"hi "},{"type":"text","text":"there"}]`)},
	})
	if droppedImages {
		t.Error("droppedImages = true for a text-only conversation")
	}
	want := []cursor.ChatMessage{
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "hi there"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestClaudeUsageFromCursor(t *testing.T) {
	got := claudeUsageFromCursor(cursor.TokenUsage{
		InputTokens: 10, OutputTokens: 20, CacheReadTokens: 3, CacheWriteTokens: 4,
	})
	want := ClaudeUsage{
		InputTokens: 10, OutputTokens: 20, CacheCreationInputTokens: 4, CacheReadInputTokens: 3,
	}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}

	// A reasoning-only turn produced real output; billing it as 0 would give
	// the model's work away for free.
	reasoningOnly := claudeUsageFromCursor(cursor.TokenUsage{InputTokens: 5, ReasoningTokens: 9})
	if reasoningOnly.OutputTokens != 9 {
		t.Errorf("reasoning-only output = %d, want 9", reasoningOnly.OutputTokens)
	}
}

func TestChatUsageFromCursor(t *testing.T) {
	if got := chatUsageFromCursor(cursor.TokenUsage{}); got != nil {
		t.Fatalf("empty usage = %+v, want nil so the chunk is omitted", got)
	}

	got := chatUsageFromCursor(cursor.TokenUsage{
		InputTokens: 10, OutputTokens: 20, CacheReadTokens: 3, CacheWriteTokens: 4, ReasoningTokens: 5,
	})
	if got == nil {
		t.Fatal("usage = nil, want a populated ChatUsage")
	}
	// OpenAI's prompt_tokens is inclusive of cached reads/writes; Cursor reports
	// them separately, so they have to be folded back in.
	if got.PromptTokens != 17 {
		t.Errorf("PromptTokens = %d, want 17 (10 + 3 + 4)", got.PromptTokens)
	}
	if got.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want 20", got.CompletionTokens)
	}
	if got.TotalTokens != 37 {
		t.Errorf("TotalTokens = %d, want 37", got.TotalTokens)
	}
	if got.PromptTokensDetails == nil || got.PromptTokensDetails.CachedTokens != 3 ||
		got.PromptTokensDetails.CacheWriteTokens != 4 {
		t.Errorf("PromptTokensDetails = %+v, want cached=3 write=4", got.PromptTokensDetails)
	}
	if got.CompletionTokensDetails == nil || got.CompletionTokensDetails.ReasoningTokens != 5 {
		t.Errorf("CompletionTokensDetails = %+v, want reasoning=5", got.CompletionTokensDetails)
	}
}

func TestClassifyCursorConnectError(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantStatus  int
		wantType    string
		wantMessage string
	}{
		{
			name:        "bad model name is a client error",
			raw:         `{"code":"invalid_argument","message":"ERROR_BAD_MODEL_NAME"}`,
			wantStatus:  http.StatusBadRequest,
			wantType:    "invalid_request_error",
			wantMessage: "ERROR_BAD_MODEL_NAME",
		},
		{
			name:        "anything else is an upstream failure",
			raw:         `{"code":"internal","message":"boom"}`,
			wantStatus:  http.StatusBadGateway,
			wantType:    "upstream_error",
			wantMessage: "boom",
		},
		{
			name:        "unparsable payloads still fail closed",
			raw:         "not json at all",
			wantStatus:  http.StatusBadGateway,
			wantType:    "upstream_error",
			wantMessage: "Cursor upstream error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, errType, message := classifyCursorConnectError(tt.raw)
			if status != tt.wantStatus || errType != tt.wantType || message != tt.wantMessage {
				t.Errorf("got (%d, %q, %q), want (%d, %q, %q)",
					status, errType, message, tt.wantStatus, tt.wantType, tt.wantMessage)
			}
		})
	}
}

func TestIsCursorAuthError(t *testing.T) {
	authErrors := []string{
		"cursor: stream chat: host: status 401: unauthorized",
		"connect error: UNAUTHENTICATED",
		"invalid_token",
		"token expired",
	}
	for _, raw := range authErrors {
		if !isCursorAuthError(cursorTestError(raw)) {
			t.Errorf("isCursorAuthError(%q) = false, want true", raw)
		}
	}
	for _, raw := range []string{"status 500: boom", "context deadline exceeded"} {
		if isCursorAuthError(cursorTestError(raw)) {
			t.Errorf("isCursorAuthError(%q) = true, want false", raw)
		}
	}
	if isCursorAuthError(nil) {
		t.Error("isCursorAuthError(nil) = true, want false")
	}
}

type cursorTestError string

func (e cursorTestError) Error() string { return string(e) }

func TestCursorForwardResultRecordsUpstreamModelOnlyWhenRemapped(t *testing.T) {
	run := cursorRunRequest{
		RequestModel:  "grok-4.6",
		RequestStream: true,
		StartTime:     time.Now().Add(-time.Second),
	}
	usage := cursor.TokenUsage{InputTokens: 1, OutputTokens: 2}

	remapped := cursorForwardResult(run, "cursor-grok-4.6-medium", nil, usage)
	if remapped.Model != "grok-4.6" {
		t.Errorf("Model = %q, want the client-visible id", remapped.Model)
	}
	if remapped.UpstreamModel != "cursor-grok-4.6-medium" {
		t.Errorf("UpstreamModel = %q, want the resolved run slug", remapped.UpstreamModel)
	}
	if !remapped.Stream || remapped.Duration <= 0 {
		t.Errorf("stream/duration not carried: %+v", remapped)
	}
	if remapped.Usage.OutputTokens != 2 {
		t.Errorf("Usage.OutputTokens = %d, want 2", remapped.Usage.OutputTokens)
	}

	// Persistence normalizes equal values away, so an unchanged model must be
	// reported as empty rather than duplicated.
	same := cursorForwardResult(run, "grok-4.6", nil, usage)
	if same.UpstreamModel != "" {
		t.Errorf("UpstreamModel = %q, want empty when the model was not remapped", same.UpstreamModel)
	}
}

func TestCursorCatalogCache(t *testing.T) {
	cache := &cursorCatalogCache{entries: make(map[int64]cursorCatalogEntry)}
	models := []cursor.AvailableModel{{Name: "grok-4.6"}}

	if _, ok := cache.get(7); ok {
		t.Fatal("empty cache reported a hit")
	}
	cache.put(7, models)
	got, ok := cache.get(7)
	if !ok || len(got) != 1 || got[0].Name != "grok-4.6" {
		t.Fatalf("cache.get = (%+v, %v), want the stored catalog", got, ok)
	}
	if _, ok := cache.get(8); ok {
		t.Error("cache leaked an entry across accounts")
	}

	// An expired entry must miss so the next request refetches.
	cache.entries[7] = cursorCatalogEntry{models: models, expiry: time.Now().Add(-time.Minute)}
	if _, ok := cache.get(7); ok {
		t.Error("expired entry reported a hit")
	}
}

func TestCursorTokenRefresherCanRefresh(t *testing.T) {
	refresher := NewCursorTokenRefresher()
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{name: "nil account", account: nil},
		{
			name:    "no refresh token",
			account: &Account{Platform: PlatformCursor, Type: AccountTypeOAuth},
		},
		{
			name: "wrong platform",
			account: &Account{Platform: PlatformKiro, Type: AccountTypeOAuth,
				Credentials: map[string]any{"refresh_token": "r"}},
		},
		{
			name: "cursor oauth with a refresh token",
			account: &Account{Platform: PlatformCursor, Type: AccountTypeOAuth,
				Credentials: map[string]any{"refresh_token": "r"}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := refresher.CanRefresh(tt.account); got != tt.want {
				t.Errorf("CanRefresh = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCursorTokenRefresherNeedsRefresh(t *testing.T) {
	refresher := NewCursorTokenRefresher()
	cursorAccount := func(credentials map[string]any) *Account {
		credentials["refresh_token"] = "r"
		return &Account{Platform: PlatformCursor, Type: AccountTypeOAuth, Credentials: credentials}
	}

	if !refresher.NeedsRefresh(cursorAccount(map[string]any{}), 0) {
		t.Error("missing access_token should force a refresh")
	}
	expiring := cursorAccount(map[string]any{
		"access_token": "jwt",
		"expires_at":   time.Now().Add(time.Minute).Format(time.RFC3339),
	})
	if !refresher.NeedsRefresh(expiring, cursorTokenRefreshSkew) {
		t.Error("token inside the refresh window should be rotated")
	}
	fresh := cursorAccount(map[string]any{
		"access_token": "jwt",
		"expires_at":   time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	})
	if refresher.NeedsRefresh(fresh, cursorTokenRefreshSkew) {
		t.Error("a long-lived token should not be rotated")
	}
	// An opaque token with no derivable expiry must not be rotated eagerly:
	// the 401 path handles it instead, so a working token is never discarded.
	unknown := cursorAccount(map[string]any{"access_token": "opaque"})
	if refresher.NeedsRefresh(unknown, cursorTokenRefreshSkew) {
		t.Error("token with unknown expiry should be left to the 401 path")
	}
}

func TestBuildCursorCredentialsKeepsExistingRefreshToken(t *testing.T) {
	// Cursor's rotation is optional; a response without refresh_token must not
	// erase the one already on the account.
	got := buildCursorCredentials(&cursor.TokenRefreshResult{AccessToken: "new-jwt", ExpiresIn: 3600})
	if _, ok := got["refresh_token"]; ok {
		t.Errorf("credentials carry refresh_token = %v, want it left untouched", got["refresh_token"])
	}
	if got["access_token"] != "new-jwt" {
		t.Errorf("access_token = %v, want %q", got["access_token"], "new-jwt")
	}
	if _, err := time.Parse(time.RFC3339, got["expires_at"].(string)); err != nil {
		t.Errorf("expires_at = %v, want an RFC3339 timestamp: %v", got["expires_at"], err)
	}

	rotated := buildCursorCredentials(&cursor.TokenRefreshResult{AccessToken: "new-jwt", RefreshToken: "new-refresh"})
	if rotated["refresh_token"] != "new-refresh" {
		t.Errorf("rotated refresh_token = %v, want %q", rotated["refresh_token"], "new-refresh")
	}

	if got := buildCursorCredentials(nil); got != nil {
		t.Errorf("nil result produced %v, want nil", got)
	}
}

func TestCursorAccountProxyURLRequiresHydratedProxy(t *testing.T) {
	id := int64(3)
	if got := cursorAccountProxyURL(&Account{ProxyID: &id}); got != "" {
		t.Errorf("unhydrated proxy produced %q, want empty", got)
	}
	if got := cursorAccountProxyURL(&Account{}); got != "" {
		t.Errorf("account without a proxy produced %q, want empty", got)
	}
	if got := cursorAccountProxyURL(nil); got != "" {
		t.Errorf("nil account produced %q, want empty", got)
	}
}
