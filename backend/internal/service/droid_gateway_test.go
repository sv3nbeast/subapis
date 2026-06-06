package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type droidHTTPUpstreamRecorder struct {
	lastReq  *http.Request
	lastBody []byte
	resp     *http.Response
	err      error
}

func (u *droidHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.lastReq = req
	if req != nil && req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		u.lastBody = b
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	if u.err != nil {
		return nil, u.err
	}
	return u.resp, nil
}

func (u *droidHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func newDroidAccountForTest() *Account {
	return &Account{
		ID:          701,
		Name:        "droid-test",
		Platform:    PlatformDroid,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "factory-key",
		},
		Status:      StatusActive,
		Schedulable: true,
	}
}

func TestForwardDroidMessages_UsesFactoryAnthropicEndpointAndInjectsSystem(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/droid/claude/v1/messages", nil)
	c.Request.Header.Set("Authorization", "Bearer inbound")

	upstream := &droidHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"req_droid"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-sonnet-4-20250514","usage":{"input_tokens":11,"output_tokens":3}}`)),
	}}
	svc := &GatewayService{httpUpstream: upstream}

	parsed := &ParsedRequest{
		Body:   []byte(`{"model":"claude-3-5-haiku-latest","stream":false,"messages":[{"role":"user","content":"hi"}]}`),
		Model:  "claude-3-5-haiku-latest",
		Stream: false,
	}
	result, err := svc.forwardDroidMessages(context.Background(), c, newDroidAccountForTest(), parsed, time.Now())

	require.NoError(t, err)
	require.Equal(t, "https://api.factory.ai/api/llm/a/v1/messages", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer factory-key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "placeholder", upstream.lastReq.Header.Get("X-Api-Key"))
	require.Equal(t, "anthropic", upstream.lastReq.Header.Get("X-Api-Provider"))
	require.Equal(t, "2023-06-01", upstream.lastReq.Header.Get("Anthropic-Version"))
	require.Equal(t, "cli", upstream.lastReq.Header.Get("X-Factory-Client"))
	require.Equal(t, "factory-cli/0.32.1", upstream.lastReq.Header.Get("User-Agent"))
	require.NotEmpty(t, upstream.lastReq.Header.Get("X-Session-Id"))
	require.Equal(t, "claude-sonnet-4-20250514", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, droidSystemPrompt, gjson.GetBytes(upstream.lastBody, "system.0.text").String())
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, "req_droid", result.RequestID)
}

func TestForwardDroidOpenAI_UsesFactoryResponsesEndpointAndParsesUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/droid/openai/v1/responses", nil)

	upstream := &droidHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_1","usage":{"input_tokens":17,"output_tokens":5,"input_tokens_details":{"cached_tokens":9}}}`)),
	}}
	svc := &GatewayService{httpUpstream: upstream}

	body := []byte(`{"model":"gpt-5","stream":false,"input":"hello"}`)
	result, err := svc.forwardDroidOpenAI(context.Background(), c, newDroidAccountForTest(), body, droidEndpointOpenAI, time.Now())

	require.NoError(t, err)
	require.Equal(t, "https://api.factory.ai/api/llm/o/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "azure_openai", upstream.lastReq.Header.Get("X-Api-Provider"))
	require.Equal(t, "gpt-5-2025-08-07", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, droidSystemPrompt, gjson.GetBytes(upstream.lastBody, "instructions").String())
	require.Equal(t, 17, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, 9, result.Usage.CacheReadInputTokens)
}

func TestBuildDroidUpstreamURL_CustomBaseURL(t *testing.T) {
	svc := &GatewayService{}
	account := newDroidAccountForTest()
	account.Credentials["base_url"] = "https://factory.example/base/"

	url, err := svc.buildDroidUpstreamURL(account, droidEndpointComm)

	require.NoError(t, err)
	require.Equal(t, "https://factory.example/base/o/v1/chat/completions", url)
}

func TestPrepareDroidAnthropicBody_PrependsPromptAndStripsMetadata(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-20250514","stream":"true","metadata":{"user_id":"u"},"temperature":0.2,"top_p":0.9,"thinking":{"type":"enabled"},"system":[{"type":"text","text":"existing"}],"messages":[{"role":"user","content":"hi"}]}`)
	svc := &GatewayService{}

	out := svc.prepareDroidRequestBody(body, droidEndpointAnthropic, "claude-sonnet-4-20250514")

	require.Equal(t, droidSystemPrompt, gjson.GetBytes(out, "system.0.text").String())
	require.Equal(t, "existing", gjson.GetBytes(out, "system.1.text").String())
	require.False(t, gjson.GetBytes(out, "metadata").Exists())
	require.False(t, gjson.GetBytes(out, "top_p").Exists())
	require.True(t, gjson.GetBytes(out, "stream").Bool())

	req, err := svc.buildDroidUpstreamRequest(context.Background(), nil, "https://api.factory.ai/api/llm/a/v1/messages", "token", out)
	require.NoError(t, err)
	require.Equal(t, "interleaved-thinking-2025-05-14", req.Header.Get("Anthropic-Beta"))
}

func TestPrepareDroidCommBody_PrependsSystemMessageAndProvider(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"hi"}]}`)
	svc := &GatewayService{}

	out := svc.prepareDroidRequestBody(body, droidEndpointComm, "claude-sonnet-4-20250514")
	require.Equal(t, "system", gjson.GetBytes(out, "messages.0.role").String())
	require.Equal(t, droidSystemPrompt, gjson.GetBytes(out, "messages.0.content").String())
	require.Equal(t, "user", gjson.GetBytes(out, "messages.1.role").String())

	req, err := svc.buildDroidUpstreamRequest(context.Background(), nil, "https://api.factory.ai/api/llm/o/v1/chat/completions", "token", out)
	require.NoError(t, err)
	require.Equal(t, "anthropic", req.Header.Get("X-Api-Provider"))
}

func TestMergeOpenAIUsageIntoClaude_DerivesOutputFromTotalTokens(t *testing.T) {
	var usage ClaudeUsage

	mergeOpenAIUsageIntoClaude(&usage, []byte(`{"usage":{"prompt_tokens":30,"total_tokens":42,"prompt_tokens_details":{"cached_tokens":7}}}`))

	require.Equal(t, 30, usage.InputTokens)
	require.Equal(t, 12, usage.OutputTokens)
	require.Equal(t, 7, usage.CacheReadInputTokens)
}
