package service

import (
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	OpenAIHTTPIngressModeOff           = "off"
	OpenAIHTTPIngressModeResponses     = "responses"
	OpenAIHTTPIngressModeResponsesChat = "responses_chat"
)

type openAIHTTPIngressEndpoint string

const (
	openAIHTTPIngressEndpointResponses openAIHTTPIngressEndpoint = "responses"
	openAIHTTPIngressEndpointChat      openAIHTTPIngressEndpoint = "chat_completions"
)

type openAIHTTPIngressWSRequest struct {
	Endpoint    openAIHTTPIngressEndpoint
	Body        []byte
	ImageIntent bool
	RawChat     bool
	Compact     bool
	InputTokens bool
}

func (s *OpenAIGatewayService) resolveOpenAIUpstreamTransport(
	c *gin.Context,
	account *Account,
	req openAIHTTPIngressWSRequest,
) OpenAIWSProtocolDecision {
	decision := s.getOpenAIWSProtocolResolver().Resolve(account)
	clientTransport := GetOpenAIClientTransport(c)
	if clientTransport != OpenAIClientTransportHTTP {
		return decision
	}
	return s.resolveOpenAIHTTPIngressTransport(account, req)
}

func (s *OpenAIGatewayService) resolveOpenAIHTTPIngressTransport(
	account *Account,
	req openAIHTTPIngressWSRequest,
) OpenAIWSProtocolDecision {
	httpDecision := func(reason string) OpenAIWSProtocolDecision {
		return openAIWSHTTPDecision("http_ingress_" + reason)
	}
	if s == nil || s.cfg == nil {
		return httpDecision("config_missing")
	}
	cfg := s.cfg.Gateway.OpenAIWS
	if cfg.ForceHTTP {
		return httpDecision("global_force_http")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.HTTPIngressMode))
	if mode == "" || mode == OpenAIHTTPIngressModeOff {
		return openAIWSHTTPDecision("client_protocol_http")
	}
	if req.Endpoint == openAIHTTPIngressEndpointChat && mode != OpenAIHTTPIngressModeResponsesChat {
		return httpDecision("chat_disabled")
	}
	if req.Endpoint != openAIHTTPIngressEndpointResponses && req.Endpoint != openAIHTTPIngressEndpointChat {
		return httpDecision("endpoint_unsupported")
	}
	if account == nil || !account.IsOpenAI() || account.IsShadow() {
		return httpDecision("account_ineligible")
	}
	if req.Compact {
		return httpDecision("compact")
	}
	if req.InputTokens {
		return httpDecision("input_tokens")
	}
	if req.RawChat || account.IsOpenAIPassthroughEnabled() {
		return httpDecision("raw_or_passthrough")
	}
	if req.ImageIntent || gjson.GetBytes(req.Body, "background").Bool() {
		return httpDecision("payload_unsupported")
	}
	if cfg.HTTPBridgeThresholdBytes > 0 && int64(len(req.Body)) > cfg.HTTPBridgeThresholdBytes {
		return httpDecision("payload_too_large")
	}
	if account.ResolveOpenAIResponsesWebSocketV2Mode(cfg.IngressModeDefault) != OpenAIWSIngressModeCtxPool {
		return httpDecision("account_mode_ineligible")
	}
	override := account.OpenAIHTTPIngressWSOverride()
	if override == OpenAIHTTPIngressWSOverrideOff {
		return httpDecision("account_override_off")
	}
	if account.IsOpenAIApiKey() && !isOfficialOpenAIAPIBaseURL(account.GetOpenAIBaseURL()) && override != OpenAIHTTPIngressWSOverrideOn {
		return httpDecision("third_party_not_opted_in")
	}
	if account.IsOpenAIApiKey() && !isOfficialOpenAIAPIBaseURL(account.GetOpenAIBaseURL()) && !hasExplicitOpenAIWSV2Capability(account) {
		return httpDecision("third_party_ws_v2_unconfirmed")
	}
	if s.isOpenAIWSFallbackCooling(account.ID) {
		return httpDecision("fallback_cooling")
	}
	if override != OpenAIHTTPIngressWSOverrideOn && !openAIHTTPIngressRolloutHit(account.ID, cfg.HTTPIngressRolloutPercent) {
		return httpDecision("rollout_miss")
	}
	if !cfg.Enabled || !cfg.ResponsesWebsocketsV2 {
		return httpDecision("ws_v2_disabled")
	}
	if account.IsOpenAIOAuth() && !cfg.OAuthEnabled {
		return httpDecision("oauth_disabled")
	}
	if account.IsOpenAIApiKey() && !cfg.APIKeyEnabled {
		return httpDecision("apikey_disabled")
	}
	return OpenAIWSProtocolDecision{
		Transport: OpenAIUpstreamTransportResponsesWebsocketV2,
		Reason:    "http_ingress_prefer_ws",
	}
}

func hasExplicitOpenAIWSV2Capability(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	if raw, ok := account.Extra["openai_apikey_responses_websockets_v2_mode"].(string); ok {
		return normalizeOpenAIWSIngressMode(raw) == OpenAIWSIngressModeCtxPool
	}
	for _, key := range []string{
		"openai_apikey_responses_websockets_v2_enabled",
		"responses_websockets_v2_enabled",
		"openai_ws_enabled",
	} {
		if enabled, ok := account.Extra[key].(bool); ok && enabled {
			return true
		}
	}
	return false
}

func openAIHTTPIngressRolloutHit(accountID int64, percent int) bool {
	if percent <= 0 || accountID <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	return int(accountID%100) < percent
}

func isOfficialOpenAIAPIBaseURL(baseURL string) bool {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return true
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return host == "api.openai.com"
}
