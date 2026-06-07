//go:build unit

package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildKiroAccountKeyIgnoresAccessToken(t *testing.T) {
	accountA := &Account{
		ID: 99,
		Credentials: map[string]any{
			"access_token": "token-a",
		},
	}
	accountB := &Account{
		ID: 99,
		Credentials: map[string]any{
			"access_token": "token-b",
		},
	}

	require.Equal(t, buildKiroAccountKey(accountA), buildKiroAccountKey(accountB))
}

func TestBuildKiroMachineIDPrefersExplicitCredential(t *testing.T) {
	account := &Account{
		ID:       101,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"machineId":     "2582956e-cc88-4669-b546-07adbffcb894",
			"refresh_token": "refresh-token",
		},
	}

	require.Equal(t, "2582956ecc884669b54607adbffcb8942582956ecc884669b54607adbffcb894", buildKiroMachineID(account))
}

func TestBuildKiroMachineIDDerivesFromRefreshToken(t *testing.T) {
	account := &Account{
		ID:       102,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "refresh-token",
		},
	}

	require.Equal(t, kiropkg.BuildMachineID("refresh-token", "", "account:102"), buildKiroMachineID(account))
}

func TestBuildKiroMachineIDDerivesFromAPIKeyAccount(t *testing.T) {
	account := &Account{
		ID:       103,
		Platform: PlatformKiro,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"kiroApiKey": "kiro-api-key",
		},
	}

	require.Equal(t, kiropkg.BuildMachineID("", "kiro-api-key", "account:103"), buildKiroMachineID(account))
}

func TestNewKiroJSONRequestAddsConditionalHeaders(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"auth_method": "external_idp",
			"provider":    "Internal",
			"profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/HEADER",
		},
	}

	req, err := newKiroJSONRequest(
		context.Background(),
		"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
		[]byte(`{"ok":true}`),
		"access-token",
		"account-key",
		buildKiroMachineID(account),
		"",
		account,
	)
	require.NoError(t, err)
	require.Equal(t, "EXTERNAL_IDP", req.Header.Get("TokenType"))
	require.Equal(t, "true", req.Header.Get("redirect-for-internal"))
	require.Empty(t, req.Header.Get("x-amzn-kiro-profile-arn"))
	require.Equal(t, "vibe", req.Header.Get("x-amzn-kiro-agent-mode"))
	require.Equal(t, "true", req.Header.Get("x-amzn-codewhisperer-optout"))
	require.Equal(t, "application/x-amz-json-1.0", req.Header.Get("Content-Type"))
	require.Equal(t, "AmazonCodeWhispererStreamingService.GenerateAssistantResponse", req.Header.Get("X-Amz-Target"))
	require.Contains(t, req.Header.Get("User-Agent"), "aws-sdk-js/1.0.34")
	require.Contains(t, req.Header.Get("User-Agent"), "md/nodejs#22.22.0")
	require.Contains(t, req.Header.Get("User-Agent"), buildKiroMachineID(account))
	require.Contains(t, req.Header.Get("X-Amz-User-Agent"), buildKiroMachineID(account))
	require.True(t, strings.Contains(req.Header.Get("User-Agent"), "api/codewhispererstreaming#1.0.34"))
	require.Empty(t, req.Header.Get("Anthropic-Beta"))
}

func TestNewKiroJSONRequestKeepsMCPAsPlainJSON(t *testing.T) {
	req, err := newKiroJSONRequest(
		context.Background(),
		"https://q.us-east-1.amazonaws.com/mcp",
		[]byte(`{"jsonrpc":"2.0"}`),
		"access-token",
		"account-key",
		"machine-id",
		"",
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Empty(t, req.Header.Get("X-Amz-Target"))
}

func TestIsKiroInvalidModelIDBodyRecognizesKnownForms(t *testing.T) {
	tests := []string{
		`{"message":"Invalid model ID. Please select a different model to continue.","reason":"INVALID_MODEL_ID"}`,
		`{"message":"Invalid model. Please select a different model to continue."}`,
		`API Error: 400 {"error":{"message":"Invalid model. Please select a different model to continue.","type":"upstream_error"}}`,
	}

	for _, body := range tests {
		classification := classifyKiroHTTPError(http.StatusBadRequest, body)
		require.Equal(t, kiroErrorBadRequestInvalidModel, classification.Category, body)
	}
}

func TestBuildKiroPayloadForAccountPropagatesThinkingHeaders(t *testing.T) {
	account := &Account{
		ID:       7,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
		},
	}
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"messages":[{"role":"user","content":"hello"}]
	}`)
	headers := http.Header{}
	headers.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(
		context.Background(),
		account,
		body,
		"claude-sonnet-4.6",
		"kiro-access-token",
		"claude-sonnet-4-6",
		headers,
	)
	require.NoError(t, err)
	payload := buildResult.Payload
	require.NotContains(t, string(payload), "CHUNKED WRITE PROTOCOL")
	require.Contains(t, string(payload), "\\u003cthinking_mode\\u003eenabled\\u003c/thinking_mode\\u003e")
}

func TestBuildKiroPayloadForAccountPreservesThinkingAliasAfterMapping(t *testing.T) {
	account := &Account{
		ID:       8,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
	}
	body := []byte(`{
		"model":"claude-opus-4.6",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(
		context.Background(),
		account,
		body,
		"claude-opus-4.6",
		"kiro-access-token",
		"claude-opus-4-6-thinking",
		nil,
	)
	require.NoError(t, err)
	payload := buildResult.Payload

	require.Equal(t, "claude-opus-4.6", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.modelId").String())
	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.Contains(t, systemContent, "<thinking_mode>adaptive</thinking_mode>")
	require.Contains(t, systemContent, "<thinking_effort>high</thinking_effort>")
}

func TestBuildKiroPayloadForAccountMapsOpus47ToDottedModelID(t *testing.T) {
	account := &Account{
		ID:       10,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-7": "claude-opus-4.7",
			},
		},
	}
	body := []byte(`{
		"model":"claude-opus-4.7",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	mappedModel := account.GetMappedModel("claude-opus-4-7")
	modelID := kiropkg.MapModel(mappedModel)
	require.Equal(t, "claude-opus-4.7", modelID)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(
		context.Background(),
		account,
		body,
		modelID,
		"kiro-access-token",
		"claude-opus-4-7",
		nil,
	)
	require.NoError(t, err)
	payload := buildResult.Payload

	require.Equal(t, "claude-opus-4.7", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.modelId").String())
}

func TestBuildKiroPayloadForAccountDoesNotEnableThinkingForNonThinkingAlias(t *testing.T) {
	account := &Account{
		ID:       9,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
	}
	body := []byte(`{
		"model":"claude-opus-4.6",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(
		context.Background(),
		account,
		body,
		"claude-opus-4.6",
		"kiro-access-token",
		"claude-opus-4-6",
		nil,
	)
	require.NoError(t, err)
	payload := buildResult.Payload

	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.NotContains(t, systemContent, "<thinking_mode>")
}

func TestKiroAPIRegionPrefersAPIRegionOverProfileARN(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"api_region":  "eu-west-1",
			"profile_arn": "arn:aws:codewhisperer:us-west-2:123456789012:profile/test",
			"region":      "ap-northeast-1",
		},
	}

	require.Equal(t, "eu-west-1", kiroAPIRegion(account))
}

func TestKiroAPIRegionIgnoresProfileARNRegionFallback(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"profile_arn": "arn:aws:codewhisperer:us-west-2:123456789012:profile/test",
		},
	}

	require.Equal(t, kiroDefaultRegion, kiroAPIRegion(account))
}

func TestKiroAPIRegionIgnoresOIDCRegionFallback(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"region": "ap-northeast-2",
		},
	}

	require.Equal(t, kiroDefaultRegion, kiroAPIRegion(account))
}

func TestBuildKiroEndpointsUsesKiroGoDefaultFallbackOrder(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"api_region":         "us-west-2",
			"preferred_endpoint": "cw",
		},
	}

	endpoints := buildKiroEndpoints(account)
	require.Len(t, endpoints, 3)
	require.Equal(t, "KiroIDE", endpoints[0].Name)
	require.Equal(t, "q.us-west-2.amazonaws.com/generateAssistantResponse", endpoints[0].URL[8:])
	require.Equal(t, "AI_EDITOR", endpoints[0].Origin)
	require.Empty(t, endpoints[0].AmzTarget)

	require.Equal(t, "CodeWhisperer", endpoints[1].Name)
	require.Equal(t, "codewhisperer.us-west-2.amazonaws.com/generateAssistantResponse", endpoints[1].URL[8:])
	require.Equal(t, "AI_EDITOR", endpoints[1].Origin)
	require.Equal(t, "AmazonCodeWhispererStreamingService.GenerateAssistantResponse", endpoints[1].AmzTarget)

	require.Equal(t, "AmazonQ", endpoints[2].Name)
	require.Equal(t, "q.us-west-2.amazonaws.com/generateAssistantResponse", endpoints[2].URL[8:])
	require.Equal(t, "AI_EDITOR", endpoints[2].Origin)
	require.Equal(t, "AmazonQDeveloperStreamingService.SendMessage", endpoints[2].AmzTarget)
}

func TestBuildKiroEndpointsKeepsDefaultOrderForPreferredEndpoint(t *testing.T) {
	for _, preferred := range []string{"codewhisperer", "cw", "unknown"} {
		account := &Account{
			Credentials: map[string]any{
				"api_region":         "us-west-2",
				"preferred_endpoint": preferred,
			},
		}

		endpoints := buildKiroEndpoints(account)
		require.Len(t, endpoints, 3)
		require.Equal(t, []string{"KiroIDE", "CodeWhisperer", "AmazonQ"}, []string{endpoints[0].Name, endpoints[1].Name, endpoints[2].Name})
	}
}
