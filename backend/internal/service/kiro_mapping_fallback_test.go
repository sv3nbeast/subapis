package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountKiroDefaultMappingRestrictsUnsupportedModels(t *testing.T) {
	account := &Account{Platform: PlatformKiro}

	require.False(t, account.IsModelSupported("gpt-4o"))
	require.False(t, account.IsModelSupported("kiro-gpt-4o"))
	require.False(t, account.IsModelSupported("auto"))
	require.Equal(t, "claude-sonnet-4.6", account.GetMappedModel("claude-sonnet-4-6"))
}

func TestGatewayServiceCalculateTokenCost_KiroAutoUsesConservativeFallback(t *testing.T) {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1

	svc := NewGatewayService(
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		cfg,
		nil,
		nil,
		NewBillingService(cfg, nil),
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil, // userPlatformQuotaRepo
	)

	result := &ForwardResult{
		Model:         "auto",
		UpstreamModel: "auto",
		Usage: ClaudeUsage{
			InputTokens:  20,
			OutputTokens: 10,
		},
	}

	expected, err := svc.billingService.CalculateCost(kiroConservativeFallbackBillingModel, UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	cost := svc.calculateTokenCost(context.Background(), result, &APIKey{}, "auto", 1.1, &recordUsageOpts{IsKiroAccount: true})
	require.NotNil(t, cost)
	require.InDelta(t, expected.ActualCost, cost.ActualCost, 1e-12)
	require.InDelta(t, expected.TotalCost, cost.TotalCost, 1e-12)
}

func TestKiroRuntimeEndpointModeRequiresProfileArn(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"api_region":         "us-west-2",
			"kiro_endpoint_mode": "runtime",
		},
	}

	endpoints := buildKiroEndpoints(account)
	require.Len(t, endpoints, 1)
	require.Equal(t, "AmazonQ", endpoints[0].Name)
	require.Equal(t, "https://q.us-west-2.amazonaws.com/generateAssistantResponse", endpoints[0].URL)
}

func TestKiroRuntimeEndpointModeUsesRuntimeHostWithProfileArn(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"kiro_endpoint_mode": "runtime",
			"profile_arn":        "arn:aws:codewhisperer:eu-west-1:123456789012:profile/KIRO",
		},
	}

	endpoints := buildKiroEndpoints(account)
	require.Len(t, endpoints, 1)
	require.Equal(t, "KiroRuntime", endpoints[0].Name)
	require.Equal(t, "https://runtime.eu-west-1.kiro.dev/", endpoints[0].URL)
	require.Equal(t, kiroGenerateAssistantResponseTarget, endpoints[0].AmzTarget)
}

func TestNewKiroJSONRequestCLIWireHeaders(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"kiro_wire_mode": "cli",
		},
	}

	req, err := newKiroJSONRequestWithAttempt(
		context.Background(),
		"https://runtime.us-east-1.kiro.dev/",
		[]byte(`{"ok":true}`),
		"access-token",
		"account-key",
		"machine-id",
		kiroGenerateAssistantResponseTarget,
		account,
		2,
		3,
	)
	require.NoError(t, err)
	require.Equal(t, kiroAWSJSONContentType, req.Header.Get("Content-Type"))
	require.Equal(t, kiroGenerateAssistantResponseTarget, req.Header.Get("X-Amz-Target"))
	require.Equal(t, "false", req.Header.Get("x-amzn-codewhisperer-optout"))
	require.Equal(t, "attempt=2; max=3", req.Header.Get("Amz-Sdk-Request"))
	require.Equal(t, "*/*", req.Header.Get("Accept"))
	require.Equal(t, "Bearer access-token", req.Header.Get("Authorization"))
}

func TestBuildKiroPayloadForAccountCLIWireMode(t *testing.T) {
	account := &Account{
		ID:       50,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"kiro_wire_mode": "cli",
			"profile_arn":    "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO",
		},
	}
	body := []byte(`{
		"model":"claude-opus-4-8-thinking",
		"system":"<env>\nWorking directory: /tmp/work\nPlatform: win32\n</env>",
		"messages":[{"role":"user","content":"hello"}]
	}`)

	result, err := (&GatewayService{}).buildKiroPayloadForAccount(
		context.Background(),
		account,
		body,
		"claude-opus-4.8",
		"access-token",
		"claude-opus-4-8-thinking",
		http.Header{},
	)
	require.NoError(t, err)

	payload := result.Payload
	require.Equal(t, "KIRO_CLI", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.origin").String())
	require.Equal(t, "high", gjson.GetBytes(payload, "additionalModelRequestFields.output_config.effort").String())
	require.Equal(t, "windows", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.envState.operatingSystem").String())
	require.Equal(t, "/tmp/work", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.envState.currentWorkingDirectory").String())
	systemContent := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	require.NotContains(t, systemContent, "<thinking_mode>")
}

func TestBuildKiroPayloadForAccountRuntimeEndpointModeUsesCLIWireWithProfileArn(t *testing.T) {
	account := &Account{
		ID:       51,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"kiro_endpoint_mode": "runtime",
			"profile_arn":        "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO",
		},
	}
	body := []byte(`{
		"model":"claude-opus-4-8",
		"output_config":{"effort":"max"},
		"messages":[{"role":"user","content":"hello"}]
	}`)

	result, err := (&GatewayService{}).buildKiroPayloadForAccount(
		context.Background(),
		account,
		body,
		"claude-opus-4.8",
		"access-token",
		"claude-opus-4-8",
		http.Header{},
	)
	require.NoError(t, err)
	require.Equal(t, "KIRO_CLI", gjson.GetBytes(result.Payload, "conversationState.currentMessage.userInputMessage.origin").String())
	require.Equal(t, "max", gjson.GetBytes(result.Payload, "additionalModelRequestFields.output_config.effort").String())
}
