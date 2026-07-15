package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIHTTPIngressTransport(t *testing.T) {
	newService := func(mode string, rollout int) *OpenAIGatewayService {
		cfg := &config.Config{}
		cfg.Gateway.OpenAIWS.Enabled = true
		cfg.Gateway.OpenAIWS.OAuthEnabled = true
		cfg.Gateway.OpenAIWS.APIKeyEnabled = true
		cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
		cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
		cfg.Gateway.OpenAIWS.HTTPIngressMode = mode
		cfg.Gateway.OpenAIWS.HTTPIngressRolloutPercent = rollout
		cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1024
		return &OpenAIGatewayService{cfg: cfg}
	}
	newOAuth := func(id int64, extra map[string]any) *Account {
		return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
	}
	responses := openAIHTTPIngressWSRequest{Endpoint: openAIHTTPIngressEndpointResponses, Body: []byte(`{"model":"gpt-5.6-sol"}`)}

	t.Run("default off remains http", func(t *testing.T) {
		decision := newService(OpenAIHTTPIngressModeOff, 100).resolveOpenAIHTTPIngressTransport(newOAuth(1, nil), responses)
		require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "client_protocol_http", decision.Reason)
	})

	t.Run("oauth rollout hit uses ws", func(t *testing.T) {
		decision := newService(OpenAIHTTPIngressModeResponses, 100).resolveOpenAIHTTPIngressTransport(newOAuth(1, nil), responses)
		require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
		require.Equal(t, "http_ingress_prefer_ws", decision.Reason)
	})

	t.Run("chat requires responses chat mode", func(t *testing.T) {
		req := responses
		req.Endpoint = openAIHTTPIngressEndpointChat
		decision := newService(OpenAIHTTPIngressModeResponses, 100).resolveOpenAIHTTPIngressTransport(newOAuth(1, nil), req)
		require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "http_ingress_chat_disabled", decision.Reason)
	})

	t.Run("override on bypasses rollout", func(t *testing.T) {
		account := newOAuth(99, map[string]any{"openai_http_ingress_ws_override": "on"})
		decision := newService(OpenAIHTTPIngressModeResponses, 0).resolveOpenAIHTTPIngressTransport(account, responses)
		require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
	})

	t.Run("explicit off wins", func(t *testing.T) {
		account := newOAuth(1, map[string]any{"openai_http_ingress_ws_override": "off"})
		decision := newService(OpenAIHTTPIngressModeResponses, 100).resolveOpenAIHTTPIngressTransport(account, responses)
		require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "http_ingress_account_override_off", decision.Reason)
	})

	t.Run("legacy force http wins", func(t *testing.T) {
		account := newOAuth(1, map[string]any{
			"openai_http_ingress_ws_override": "on",
			"openai_ws_force_http":            true,
		})
		decision := newService(OpenAIHTTPIngressModeResponses, 100).resolveOpenAIHTTPIngressTransport(account, responses)
		require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
	})

	for _, tc := range []struct {
		name string
		req  openAIHTTPIngressWSRequest
	}{
		{name: "background", req: openAIHTTPIngressWSRequest{Endpoint: openAIHTTPIngressEndpointResponses, Body: []byte(`{"background":true}`)}},
		{name: "image", req: openAIHTTPIngressWSRequest{Endpoint: openAIHTTPIngressEndpointResponses, ImageIntent: true}},
		{name: "chat image", req: openAIHTTPIngressWSRequest{Endpoint: openAIHTTPIngressEndpointChat, ImageIntent: true}},
		{name: "input tokens", req: openAIHTTPIngressWSRequest{Endpoint: openAIHTTPIngressEndpointResponses, InputTokens: true}},
		{name: "raw chat", req: openAIHTTPIngressWSRequest{Endpoint: openAIHTTPIngressEndpointChat, RawChat: true}},
		{name: "large payload", req: openAIHTTPIngressWSRequest{Endpoint: openAIHTTPIngressEndpointResponses, Body: make([]byte, 1025)}},
	} {
		t.Run(tc.name+" stays http", func(t *testing.T) {
			decision := newService(OpenAIHTTPIngressModeResponsesChat, 100).resolveOpenAIHTTPIngressTransport(newOAuth(1, nil), tc.req)
			require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		})
	}

	t.Run("third party api key requires override", func(t *testing.T) {
		account := &Account{
			ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
			Credentials: map[string]any{"base_url": "https://third-party.example/v1"},
		}
		decision := newService(OpenAIHTTPIngressModeResponses, 100).resolveOpenAIHTTPIngressTransport(account, responses)
		require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)

		account.Extra = map[string]any{"openai_http_ingress_ws_override": "on"}
		decision = newService(OpenAIHTTPIngressModeResponses, 0).resolveOpenAIHTTPIngressTransport(account, responses)
		require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "http_ingress_third_party_ws_v2_unconfirmed", decision.Reason)

		account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
		decision = newService(OpenAIHTTPIngressModeResponses, 0).resolveOpenAIHTTPIngressTransport(account, responses)
		require.Equal(t, OpenAIUpstreamTransportResponsesWebsocketV2, decision.Transport)
	})

	t.Run("explicit non pool mode is ineligible", func(t *testing.T) {
		account := newOAuth(1, map[string]any{
			"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModePassthrough,
			"openai_http_ingress_ws_override":           "on",
		})
		decision := newService(OpenAIHTTPIngressModeResponses, 100).resolveOpenAIHTTPIngressTransport(account, responses)
		require.Equal(t, OpenAIUpstreamTransportHTTPSSE, decision.Transport)
		require.Equal(t, "http_ingress_account_mode_ineligible", decision.Reason)
	})
}

func TestIsOpenAIResponsesInputTokensPath(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/input_tokens", nil)
	require.True(t, isOpenAIResponsesInputTokensPath(c))

	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	require.False(t, isOpenAIResponsesInputTokensPath(c))
}

func TestOpenAIHTTPIngressRolloutHit(t *testing.T) {
	require.False(t, openAIHTTPIngressRolloutHit(15, 0))
	require.True(t, openAIHTTPIngressRolloutHit(15, 16))
	require.False(t, openAIHTTPIngressRolloutHit(15, 15))
	require.True(t, openAIHTTPIngressRolloutHit(115, 100))
}

func TestIsOfficialOpenAIAPIBaseURL(t *testing.T) {
	require.True(t, isOfficialOpenAIAPIBaseURL(""))
	require.True(t, isOfficialOpenAIAPIBaseURL("https://api.openai.com/v1"))
	require.False(t, isOfficialOpenAIAPIBaseURL("https://openai.example/v1"))
	require.False(t, isOfficialOpenAIAPIBaseURL("://bad"))
}
