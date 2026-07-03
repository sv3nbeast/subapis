package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func kiroStickyGroup() *Group {
	return &Group{Platform: PlatformKiro, KiroAutoStickyEnabled: true}
}

func nonKiroStickyGroup() *Group {
	return &Group{Platform: PlatformAnthropic}
}

func newKiroStickyRequestBody(systemPrompt string, turns int) []byte {
	msgs := `[{"role":"user","content":"hello"}`
	for i := 1; i < turns; i++ {
		msgs += `,{"role":"assistant","content":"reply"},{"role":"user","content":"next turn"}`
	}
	msgs += `]`
	return []byte(`{"model":"claude-sonnet-4-5","system":"` + systemPrompt + `","messages":` + msgs + `}`)
}

func newKiroStickyRequestBodyWithoutSystem(firstUserMessage string, turns int) []byte {
	msgs := `[{"role":"user","content":"` + firstUserMessage + `"}`
	for i := 1; i < turns; i++ {
		msgs += `,{"role":"assistant","content":"reply"},{"role":"user","content":"next turn"}`
	}
	msgs += `]`
	return []byte(`{"model":"claude-sonnet-4-5","messages":` + msgs + `}`)
}

func newKiroStickyRequestBodyWithBodySession(systemPrompt, sessionField, sessionID string) []byte {
	return []byte(`{"model":"claude-sonnet-4-5","` + sessionField + `":"` + sessionID + `","system":"` + systemPrompt + `","messages":[{"role":"user","content":"hello"}]}`)
}

func TestKiroStickySession_SystemPromptHashStableAcrossRounds(t *testing.T) {
	svc := &GatewayService{}
	systemPrompt := "You are a helpful assistant for Kiro."
	ctx := &SessionContext{APIKeyID: 42}

	makeHash := func(turns int) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBody(systemPrompt, turns)), "anthropic")
		require.NoError(t, err)
		parsed.Group = kiroStickyGroup()
		parsed.SessionContext = ctx
		return svc.GenerateSessionHash(parsed)
	}

	hash1 := makeHash(1)
	require.NotEmpty(t, hash1)
	require.Equal(t, hash1, makeHash(2))
	require.Equal(t, hash1, makeHash(5))
}

func TestKiroStickySession_DifferentSystemPromptsDifferentHash(t *testing.T) {
	svc := &GatewayService{}
	ctx := &SessionContext{APIKeyID: 42}

	makeHash := func(systemPrompt string) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBody(systemPrompt, 1)), "anthropic")
		require.NoError(t, err)
		parsed.Group = kiroStickyGroup()
		parsed.SessionContext = ctx
		return svc.GenerateSessionHash(parsed)
	}

	hashA := makeHash("System prompt A")
	hashB := makeHash("System prompt B")
	require.NotEmpty(t, hashA)
	require.NotEmpty(t, hashB)
	require.NotEqual(t, hashA, hashB)
}

func TestKiroStickySession_NonKiroGroupFallsBackToMessageHash(t *testing.T) {
	svc := &GatewayService{}
	ctx := &SessionContext{APIKeyID: 42}

	makeHash := func(turns int) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBody("same system", turns)), "anthropic")
		require.NoError(t, err)
		parsed.Group = nonKiroStickyGroup()
		parsed.SessionContext = ctx
		return svc.GenerateSessionHash(parsed)
	}

	require.NotEqual(t, makeHash(1), makeHash(2))
}

func TestKiroStickySession_DifferentAPIKeysDifferentHash(t *testing.T) {
	svc := &GatewayService{}

	makeHash := func(apiKeyID int64) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBody("shared system", 1)), "anthropic")
		require.NoError(t, err)
		parsed.Group = kiroStickyGroup()
		parsed.SessionContext = &SessionContext{APIKeyID: apiKeyID}
		return svc.GenerateSessionHash(parsed)
	}

	require.NotEqual(t, makeHash(1), makeHash(2))
}

func TestKiroStickySession_ExplicitSessionIDHeaderTakesPrecedence(t *testing.T) {
	svc := &GatewayService{}
	ctx := &SessionContext{APIKeyID: 42}

	makeHash := func(systemPrompt, explicitID string) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBody(systemPrompt, 1)), "anthropic")
		require.NoError(t, err)
		parsed.Group = kiroStickyGroup()
		parsed.SessionContext = ctx
		parsed.ExplicitSessionID = explicitID
		return svc.GenerateSessionHash(parsed)
	}

	hashA := makeHash("System prompt A", "my-session-123")
	require.NotEmpty(t, hashA)
	require.Equal(t, hashA, makeHash("System prompt B", "my-session-123"))
	require.NotEqual(t, hashA, makeHash("System prompt A", "other-session-456"))
}

func TestKiroStickySession_ExplicitSessionIDDifferentAPIKeys(t *testing.T) {
	svc := &GatewayService{}

	makeHash := func(apiKeyID int64) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBody("shared prompt", 1)), "anthropic")
		require.NoError(t, err)
		parsed.Group = kiroStickyGroup()
		parsed.SessionContext = &SessionContext{APIKeyID: apiKeyID}
		parsed.ExplicitSessionID = "default"
		return svc.GenerateSessionHash(parsed)
	}

	require.NotEqual(t, makeHash(1), makeHash(2))
}

func TestKiroStickySession_EmptySystemPromptUsesFirstUserMessage(t *testing.T) {
	svc := &GatewayService{}
	ctx := &SessionContext{APIKeyID: 42}

	makeHash := func(firstUserMessage string, turns int) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBodyWithoutSystem(firstUserMessage, turns)), "anthropic")
		require.NoError(t, err)
		parsed.Group = kiroStickyGroup()
		parsed.SessionContext = ctx
		return svc.GenerateSessionHash(parsed)
	}

	hash1 := makeHash("hello", 1)
	require.NotEmpty(t, hash1)
	require.Equal(t, hash1, makeHash("hello", 2))
	require.NotEqual(t, hash1, makeHash("different first prompt", 2))
}

func TestKiroStickySession_DisabledSkipsAutoInference(t *testing.T) {
	svc := &GatewayService{}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBody("stable system prompt", 1)), "anthropic")
	require.NoError(t, err)
	parsed.Group = &Group{Platform: PlatformKiro, KiroAutoStickyEnabled: false}
	parsed.SessionContext = &SessionContext{APIKeyID: 42}

	require.Empty(t, svc.GenerateSessionHash(parsed))
}

func TestKiroStickySession_ExplicitSessionIDWorksWhenAutoInferenceDisabled(t *testing.T) {
	svc := &GatewayService{}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBody("stable system prompt", 1)), "anthropic")
	require.NoError(t, err)
	parsed.Group = &Group{Platform: PlatformKiro, KiroAutoStickyEnabled: false}
	parsed.SessionContext = &SessionContext{APIKeyID: 42}
	parsed.ExplicitSessionID = "manual-session"

	require.NotEmpty(t, svc.GenerateSessionHash(parsed))
}

func TestKiroStickySession_BodySessionIDTakesPrecedence(t *testing.T) {
	svc := &GatewayService{}
	ctx := &SessionContext{APIKeyID: 42}

	makeHash := func(systemPrompt, conversationID string) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBodyWithBodySession(systemPrompt, "conversation_id", conversationID)), "anthropic")
		require.NoError(t, err)
		parsed.Group = kiroStickyGroup()
		parsed.SessionContext = ctx
		return svc.GenerateSessionHash(parsed)
	}

	hashA := makeHash("System prompt A", "conv-stable")
	require.NotEmpty(t, hashA)
	require.Equal(t, hashA, makeHash("System prompt B", "conv-stable"))
	require.NotEqual(t, hashA, makeHash("System prompt A", "conv-other"))
}

func TestKiroStickySession_BodySessionIDDifferentAPIKeys(t *testing.T) {
	svc := &GatewayService{}

	makeHash := func(apiKeyID int64) string {
		parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBodyWithBodySession("stable system prompt", "session_id", "shared-session")), "anthropic")
		require.NoError(t, err)
		parsed.Group = kiroStickyGroup()
		parsed.SessionContext = &SessionContext{APIKeyID: apiKeyID}
		return svc.GenerateSessionHash(parsed)
	}

	require.NotEqual(t, makeHash(1), makeHash(2))
}

func TestKiroStickySession_BodySessionIDWorksWhenAutoInferenceDisabled(t *testing.T) {
	svc := &GatewayService{}
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(newKiroStickyRequestBodyWithBodySession("stable system prompt", "thread_id", "thread-123")), "anthropic")
	require.NoError(t, err)
	parsed.Group = &Group{Platform: PlatformKiro, KiroAutoStickyEnabled: false}
	parsed.SessionContext = &SessionContext{APIKeyID: 42}

	require.NotEmpty(t, svc.GenerateSessionHash(parsed))
}

func TestExtractBodySessionID(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "prompt cache key", body: `{"prompt_cache_key":"pcache-1","conversation_id":"conv-1"}`, want: "pcache-1"},
		{name: "conversation id", body: `{"conversation_id":"conv-1"}`, want: "conv-1"},
		{name: "camel session id", body: `{"sessionId":"sess-1"}`, want: "sess-1"},
		{name: "metadata session id", body: `{"metadata":{"session_id":"meta-sess-1"}}`, want: "meta-sess-1"},
		{name: "blank ignored", body: `{"session_id":"   ","thread_id":"thread-1"}`, want: "thread-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, extractBodySessionID(tc.body))
		})
	}
}

func TestIsKiroGroup(t *testing.T) {
	require.True(t, isKiroGroup(&Group{Platform: PlatformKiro}))
	require.False(t, isKiroGroup(&Group{Platform: PlatformAnthropic}))
	require.False(t, isKiroGroup(&Group{Platform: PlatformOpenAI}))
	require.False(t, isKiroGroup(nil))
}
