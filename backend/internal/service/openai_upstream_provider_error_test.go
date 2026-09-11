package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const providerCapacityTestMessage = "Our servers are currently overloaded. Please try again later."

func TestOpenAIUpstreamCapacityClientMessage(t *testing.T) {
	attributed := OpenAIUpstreamCapacityClientMessage(providerCapacityTestMessage)
	require.True(t, strings.HasPrefix(attributed, openAIUpstreamCapacityClientMessagePrefix))
	require.Contains(t, attributed, "OpenAI 官方服务当前算力过载")
	require.Contains(t, attributed, "not this platform")
	require.True(t, strings.HasSuffix(attributed, providerCapacityTestMessage), "provider wording is quoted verbatim")

	require.Equal(t, attributed, OpenAIUpstreamCapacityClientMessage(attributed), "idempotent when applied twice")
	require.Equal(t, openAIUpstreamCapacityClientMessagePrefix, OpenAIUpstreamCapacityClientMessage("   "))
	require.NotContains(t, attributed, "server_is_overloaded")
}

// The relayed error/response.failed events carry the attribution in their
// message (Codex shows the first error message it parses) while keeping the
// retryable server_error code; re-sanitizing the same event is a no-op.
func TestSanitizeOpenAICapacityShedErrorAttributesMessageIdempotently(t *testing.T) {
	payload := []byte(`{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"` + providerCapacityTestMessage + `","param":null},"sequence_number":2}`)

	first, changed := sanitizeOpenAICapacityShedErrorCodeForClient(payload)
	require.True(t, changed)
	require.Equal(t, "server_error", gjson.GetBytes(first, "error.code").String())
	message := gjson.GetBytes(first, "error.message").String()
	require.Contains(t, message, "OpenAI 官方服务当前算力过载")
	require.Contains(t, message, providerCapacityTestMessage)
	require.Equal(t, int64(2), gjson.GetBytes(first, "sequence_number").Int(), "unrelated fields survive")
	require.NotContains(t, string(first), "server_is_overloaded")

	second, changedAgain := sanitizeOpenAICapacityShedErrorCodeForClient(first)
	require.False(t, changedAgain)
	require.Equal(t, string(first), string(second))

	failed := []byte(`{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_is_overloaded","message":"` + providerCapacityTestMessage + `"}}}`)
	rewritten, changed := sanitizeOpenAICapacityShedErrorCodeForClient(failed)
	require.True(t, changed)
	require.Equal(t, "server_error", gjson.GetBytes(rewritten, "response.error.code").String())
	require.Contains(t, gjson.GetBytes(rewritten, "response.error.message").String(), "OpenAI 官方服务当前算力过载")

	other := []byte(`{"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"try again in 3s"}}}`)
	same, changed := sanitizeOpenAICapacityShedErrorCodeForClient(other)
	require.False(t, changed)
	require.Equal(t, string(other), string(same))
}

func TestOpenAICapacityShedClientMessageIsAttributed(t *testing.T) {
	body := []byte(`{"error":{"code":"server_is_overloaded","message":"` + providerCapacityTestMessage + `"}}`)
	message := openAICapacityShedClientMessage("", body)
	require.Contains(t, message, "OpenAI 官方服务当前算力过载")
	require.Contains(t, message, providerCapacityTestMessage)

	require.Equal(t, openAIUpstreamCapacityClientMessagePrefix, openAICapacityShedClientMessage("", []byte(`{"error":{"code":"slow_down"}}`)))
}

// newProviderErrorWSTestService mirrors the WS v2 success-test harness: an API
// key account whose upstream is a scripted WebSocket connection.
func newProviderErrorWSTestService(t *testing.T, events [][]byte) (*OpenAIGatewayService, *Account) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	copied := make([][]byte, 0, len(events))
	for _, event := range events {
		copied = append(copied, append([]byte(nil), event...))
	}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: copied}})

	svc := &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     &httpUpstreamRecorder{},
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
		openaiWSPool:     pool,
	}
	account := &Account{
		ID: 2613, Name: "openai-ws-capacity", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true},
	}
	return svc, account
}

func newProviderErrorWSTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")
	groupID := int64(5)
	c.Set("api_key", &APIKey{GroupID: &groupID})
	return c, rec
}

var providerCapacityWSErrorEvent = []byte(`{"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"` + providerCapacityTestMessage + `","param":null},"sequence_number":2}`)

// Production request 737b7ce6: the ChatGPT WebSocket answered a streaming
// Codex request with a server_is_overloaded error event. The relayed event must
// carry the retryable code plus the provider attribution, the service must hand
// the handler a classified terminal instead of writing a generic one, and ops
// must keep the provider's own wording.
func TestOpenAIGatewayService_Forward_WSv2_CapacityErrorEventStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, account := newProviderErrorWSTestService(t, [][]byte{providerCapacityWSErrorEvent})
	c, rec := newProviderErrorWSTestContext(t)
	body := []byte(`{"model":"gpt-5.6-sol","stream":true,"instructions":"be brief","input":[{"type":"message","role":"user","content":"hello"}]}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.Error(t, err)
	require.Nil(t, result)
	var terminal *OpenAIUpstreamTerminalError
	require.ErrorAs(t, err, &terminal)
	require.Equal(t, http.StatusServiceUnavailable, terminal.ClientStatus)
	require.Equal(t, "server_error", terminal.ErrType)
	require.Equal(t, "server_error", terminal.Code)
	require.Contains(t, terminal.Message, "OpenAI 官方服务当前算力过载")
	require.Contains(t, terminal.Message, providerCapacityTestMessage)
	require.Equal(t, providerCapacityTestMessage, terminal.UpstreamMessage)
	require.Equal(t, http.StatusBadGateway, terminal.UpstreamStatus)

	relayed := rec.Body.String()
	require.Contains(t, relayed, "data: ")
	require.Contains(t, relayed, `"code":"server_error"`)
	require.Contains(t, relayed, "OpenAI 官方服务当前算力过载")
	require.Contains(t, relayed, providerCapacityTestMessage)
	require.NotContains(t, relayed, "server_is_overloaded")
	require.NotContains(t, relayed, "response.failed", "the terminal is rendered by the handler, not the service")
	require.NotContains(t, relayed, "Upstream request failed")

	recorded, ok := c.Get(OpsUpstreamErrorMessageKey)
	require.True(t, ok)
	require.Equal(t, providerCapacityTestMessage, recorded)
}

// A non-streaming client used to receive a JSON envelope from the service and a
// stray SSE frame from the handler fallback. The service still writes the one
// envelope (now attributed), marks the response committed, and flags the typed
// error as rendered so handlers only log.
func TestOpenAIGatewayService_Forward_WSv2_CapacityErrorEventNonStreamingWritesSingleAttributedEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, account := newProviderErrorWSTestService(t, [][]byte{providerCapacityWSErrorEvent})
	c, rec := newProviderErrorWSTestContext(t)
	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"instructions":"be brief","input":[{"type":"message","role":"user","content":"hello"}]}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.Error(t, err)
	require.Nil(t, result)
	var terminal *OpenAIUpstreamTerminalError
	require.ErrorAs(t, err, &terminal)
	require.True(t, terminal.Rendered)
	require.Equal(t, http.StatusServiceUnavailable, terminal.ClientStatus)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.True(t, IsResponseCommitted(c))
	envelope := rec.Body.String()
	require.Equal(t, "server_error", gjson.Get(envelope, "error.type").String())
	require.Equal(t, "server_error", gjson.Get(envelope, "error.code").String())
	require.Contains(t, gjson.Get(envelope, "error.message").String(), "OpenAI 官方服务当前算力过载")
	require.Contains(t, gjson.Get(envelope, "error.message").String(), providerCapacityTestMessage)
	require.NotContains(t, envelope, "event: ")
	require.NotContains(t, envelope, "server_is_overloaded")
	require.Equal(t, 1, strings.Count(envelope, `"error"`), "exactly one envelope, no trailing SSE frame")
}

// Other non-fallback error events keep the upstream-mapped status and the
// provider's sanitized message without inventing a capacity attribution.
func TestOpenAIGatewayService_Forward_WSv2_NonCapacityErrorEventKeepsUpstreamSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, account := newProviderErrorWSTestService(t, [][]byte{
		[]byte(`{"type":"error","error":{"type":"invalid_request_error","code":"unusable_input","message":"input item 3 is not usable"}}`),
	})
	c, rec := newProviderErrorWSTestContext(t)
	body := []byte(`{"model":"gpt-5.6-sol","stream":false,"instructions":"be brief","input":[{"type":"message","role":"user","content":"hello"}]}`)

	result, err := svc.Forward(context.Background(), c, account, body)

	require.Error(t, err)
	require.Nil(t, result)
	var terminal *OpenAIUpstreamTerminalError
	require.ErrorAs(t, err, &terminal)
	require.Equal(t, http.StatusBadRequest, terminal.ClientStatus)
	require.Equal(t, "upstream_error", terminal.ErrType)
	require.Empty(t, terminal.Code)
	require.Equal(t, "input item 3 is not usable", terminal.Message)
	require.NotContains(t, terminal.Message, "OpenAI 官方服务当前算力过载")
	require.True(t, terminal.Rendered)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "upstream_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "input item 3 is not usable", gjson.Get(rec.Body.String(), "error.message").String())
	require.False(t, gjson.Get(rec.Body.String(), "error.code").Exists())
}

func newProviderErrorHTTPTestService() *OpenAIGatewayService {
	repo := &capacityShedAccountRepoStub{}
	return &OpenAIGatewayService{
		cfg:              &config.Config{},
		rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
	}
}

func providerCapacityHTTPResponse(status int) *http.Response {
	header := http.Header{}
	header.Set("Content-Type", "application/json")
	header.Set("x-request-id", "rid_capacity")
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"server_is_overloaded","type":"service_unavailable_error","message":"` + providerCapacityTestMessage + `"}}`)),
	}
}

// Non-passthrough HTTP path: a capacity shed the account keeps handling locally
// becomes a retryable 503 with the provider attribution, not "Upstream request
// failed".
func TestOpenAIHandleErrorResponse_CapacityShedIsAttributedRetryable503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newProviderErrorHTTPTestService()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := &Account{ID: 2613, Name: "acc", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}

	result, err := svc.handleErrorResponse(context.Background(), providerCapacityHTTPResponse(http.StatusServiceUnavailable), c, account, []byte(`{"model":"gpt-5.6-sol","input":"hello"}`))

	require.Error(t, err)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		// A request-scoped capacity shed may also be surfaced as a same-account
		// retry; its client message must then already be attributed.
		require.Contains(t, failoverErr.ClientMessage, "OpenAI 官方服务当前算力过载")
		return
	}
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	body := rec.Body.String()
	require.Equal(t, "server_error", gjson.Get(body, "error.type").String())
	require.Equal(t, "server_error", gjson.Get(body, "error.code").String())
	require.Contains(t, gjson.Get(body, "error.message").String(), "OpenAI 官方服务当前算力过载")
	require.Contains(t, gjson.Get(body, "error.message").String(), providerCapacityTestMessage)
	require.NotContains(t, body, "Upstream request failed")
}

// Passthrough path: the sanitized envelope names OpenAI's overload instead of
// "Upstream service temporarily unavailable".
func TestOpenAIHandleErrorResponsePassthrough_CapacityShedIsAttributed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newProviderErrorHTTPTestService()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := &Account{ID: 2613, Name: "acc", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Extra: map[string]any{"openai_passthrough": true}}
	resp := providerCapacityHTTPResponse(http.StatusBadGateway)
	responseBody, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)

	err := svc.handleErrorResponsePassthrough(context.Background(), resp, c, account, []byte(`{"model":"gpt-5.6-sol","input":"hello"}`), responseBody)

	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	body := rec.Body.String()
	require.Equal(t, "upstream_error", gjson.Get(body, "error.type").String())
	require.Contains(t, gjson.Get(body, "error.message").String(), "OpenAI 官方服务当前算力过载")
	require.Contains(t, gjson.Get(body, "error.message").String(), providerCapacityTestMessage)
	require.NotContains(t, body, "Upstream service temporarily unavailable")
	require.NotContains(t, body, "server_is_overloaded")
}
