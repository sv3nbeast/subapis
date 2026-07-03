package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildKiroPayloadForParsedAccountScopesConversationIDByAccountAndAPIKey(t *testing.T) {
	svc := &GatewayService{}
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"same prompt"}]}`)
	baseAccount := &Account{
		ID:       101,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"client_id":   "client-a",
			"profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/A",
		},
	}
	otherAccount := &Account{
		ID:       202,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"client_id":   "client-a",
			"profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/A",
		},
	}

	first, err := svc.buildKiroPayloadForParsedAccountEndpoint(
		context.Background(),
		baseAccount,
		&ParsedRequest{Body: NewRequestBodyRef(body), SessionContext: &SessionContext{APIKeyID: 1}},
		body,
		"claude-sonnet-4.5",
		"token",
		"claude-sonnet-4-5",
		nil,
		kiroEndpointConfig{Origin: "AI_EDITOR"},
	)
	require.NoError(t, err)
	second, err := svc.buildKiroPayloadForParsedAccountEndpoint(
		context.Background(),
		baseAccount,
		&ParsedRequest{Body: NewRequestBodyRef(body), SessionContext: &SessionContext{APIKeyID: 1}},
		body,
		"claude-sonnet-4.5",
		"token",
		"claude-sonnet-4-5",
		nil,
		kiroEndpointConfig{Origin: "AI_EDITOR"},
	)
	require.NoError(t, err)
	otherAPIKey, err := svc.buildKiroPayloadForParsedAccountEndpoint(
		context.Background(),
		baseAccount,
		&ParsedRequest{Body: NewRequestBodyRef(body), SessionContext: &SessionContext{APIKeyID: 2}},
		body,
		"claude-sonnet-4.5",
		"token",
		"claude-sonnet-4-5",
		nil,
		kiroEndpointConfig{Origin: "AI_EDITOR"},
	)
	require.NoError(t, err)
	otherAcct, err := svc.buildKiroPayloadForParsedAccountEndpoint(
		context.Background(),
		otherAccount,
		&ParsedRequest{Body: NewRequestBodyRef(body), SessionContext: &SessionContext{APIKeyID: 1}},
		body,
		"claude-sonnet-4.5",
		"token",
		"claude-sonnet-4-5",
		nil,
		kiroEndpointConfig{Origin: "AI_EDITOR"},
	)
	require.NoError(t, err)

	firstID := gjson.GetBytes(first.Payload, "conversationState.conversationId").String()
	require.NotEmpty(t, firstID)
	require.Equal(t, firstID, gjson.GetBytes(second.Payload, "conversationState.conversationId").String())
	require.NotEqual(t, firstID, gjson.GetBytes(otherAPIKey.Payload, "conversationState.conversationId").String())
	require.NotEqual(t, firstID, gjson.GetBytes(otherAcct.Payload, "conversationState.conversationId").String())
}

func TestBuildKiroPayloadForParsedAccountUsesExplicitSessionAnchor(t *testing.T) {
	svc := &GatewayService{}
	bodyA := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"first prompt"}]}`)
	bodyB := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"different prompt"}]}`)
	account := &Account{
		ID:       303,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"client_id": "client-a",
		},
	}
	parsedA := &ParsedRequest{Body: NewRequestBodyRef(bodyA), ExplicitSessionID: "session-1"}
	parsedB := &ParsedRequest{Body: NewRequestBodyRef(bodyB), ExplicitSessionID: "session-1"}
	parsedC := &ParsedRequest{Body: NewRequestBodyRef(bodyA), ExplicitSessionID: "session-2"}

	first, err := svc.buildKiroPayloadForParsedAccountEndpoint(context.Background(), account, parsedA, bodyA, "claude-sonnet-4.5", "token", "claude-sonnet-4-5", nil, kiroEndpointConfig{Origin: "AI_EDITOR"})
	require.NoError(t, err)
	sameSession, err := svc.buildKiroPayloadForParsedAccountEndpoint(context.Background(), account, parsedB, bodyB, "claude-sonnet-4.5", "token", "claude-sonnet-4-5", nil, kiroEndpointConfig{Origin: "AI_EDITOR"})
	require.NoError(t, err)
	otherSession, err := svc.buildKiroPayloadForParsedAccountEndpoint(context.Background(), account, parsedC, bodyA, "claude-sonnet-4.5", "token", "claude-sonnet-4-5", nil, kiroEndpointConfig{Origin: "AI_EDITOR"})
	require.NoError(t, err)

	firstID := gjson.GetBytes(first.Payload, "conversationState.conversationId").String()
	require.NotEmpty(t, firstID)
	require.Equal(t, firstID, gjson.GetBytes(sameSession.Payload, "conversationState.conversationId").String())
	require.NotEqual(t, firstID, gjson.GetBytes(otherSession.Payload, "conversationState.conversationId").String())
}

func TestBuildKiroPayloadForAccount_StableConversationIDByDefault(t *testing.T) {
	t.Setenv("SUB2API_KIRO_CONVERSATION_ID_MODE", "")
	svc := &GatewayService{}
	account := &Account{
		ID:       44,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "stable-refresh",
			"profile_arn":   "arn:aws:codewhisperer:us-east-1:123456789012:profile/STABLE",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-5","system":"stable sys","messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "anthropic")
	require.NoError(t, err)
	parsed.Group = &Group{Platform: PlatformKiro}
	parsed.SessionContext = &SessionContext{APIKeyID: 9}

	first, err := svc.buildKiroPayloadForParsedAccountEndpoint(context.Background(), account, parsed, body, "claude-sonnet-4.5", "kiro-access-token", "claude-sonnet-4.5", nil, kiroEndpointConfig{Origin: "AI_EDITOR"})
	require.NoError(t, err)
	second, err := svc.buildKiroPayloadForParsedAccountEndpoint(context.Background(), account, parsed, body, "claude-sonnet-4.5", "rotated-token", "claude-sonnet-4.5", nil, kiroEndpointConfig{Origin: "AI_EDITOR"})
	require.NoError(t, err)

	firstID := gjson.GetBytes(first.Payload, "conversationState.conversationId").String()
	secondID := gjson.GetBytes(second.Payload, "conversationState.conversationId").String()
	require.NotEmpty(t, firstID)
	require.Equal(t, firstID, secondID)

	t.Setenv("SUB2API_KIRO_CONVERSATION_ID_MODE", "random")
	randomized, err := svc.buildKiroPayloadForParsedAccountEndpoint(context.Background(), account, parsed, body, "claude-sonnet-4.5", "kiro-access-token", "claude-sonnet-4.5", nil, kiroEndpointConfig{Origin: "AI_EDITOR"})
	require.NoError(t, err)
	require.NotEqual(t, firstID, gjson.GetBytes(randomized.Payload, "conversationState.conversationId").String())
}

func TestStableKiroConversationIDCanBeDisabledByEnv(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"same prompt"}]}`)
	account := &Account{ID: 404, Platform: PlatformKiro, Type: AccountTypeOAuth}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), ExplicitSessionID: "session-1"}

	require.NotEmpty(t, stableKiroConversationID(account, parsed, body, "claude-sonnet-4.5", ""))

	for _, mode := range []string{"off", "random", "uuid", "false", "0"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("SUB2API_KIRO_CONVERSATION_ID_MODE", mode)
			require.Empty(t, stableKiroConversationID(account, parsed, body, "claude-sonnet-4.5", ""))
		})
	}
}
