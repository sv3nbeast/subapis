package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroAnthropicFallbackUpstream struct {
	service.HTTPUpstream
	calls      int
	accountIDs []int64
}

func (u *kiroAnthropicFallbackUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	return u.DoWithTLS(req, "", accountID, 0, nil)
}

func (u *kiroAnthropicFallbackUpstream) DoWithTLS(_ *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls++
	u.accountIDs = append(u.accountIDs, accountID)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"X-Request-Id": []string{"req_fallback_claude"},
		},
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`event: message_start`,
			`data: {"type":"message_start","message":{"id":"msg_fallback","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"usage":{"input_tokens":12,"output_tokens":0}}}`,
			``,
			`event: content_block_start`,
			`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
			``,
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
			``,
			`event: message_delta`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
			``,
			`event: message_stop`,
			`data: {"type":"message_stop"}`,
			``,
		}, "\n"))),
	}, nil
}

func TestGatewayHandlerMessages_KiroSelectionFailureFallsBackToClaudeWithoutClientError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9210)
	accountID := int64(9211)
	group := &service.Group{
		ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic,
		Status: service.StatusActive, SubscriptionType: service.SubscriptionTypeSubscription,
		KiroAnthropicFallbackEnabled: true,
	}
	account := &service.Account{
		ID: accountID, Name: "fallback-claude", Platform: service.PlatformAnthropic,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Concurrency: 1, Credentials: map[string]any{"api_key": "sk-ant-test"},
		Extra:         map[string]any{"anthropic_passthrough": true},
		AccountGroups: []service.AccountGroup{{AccountID: accountID, GroupID: groupID}},
	}
	upstream := &kiroAnthropicFallbackUpstream{}
	h, cleanup := newTestGatewayHandler(t, group, []*service.Account{account}, upstream)
	defer cleanup()
	h.cfg = &config.Config{RunMode: config.RunModeSimple}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Session-ID", "kiro-fallback-entry-test")
	req = req.WithContext(context.WithValue(req.Context(), ctxkey.Group, group))
	c.Request = req
	apiKey := &service.APIKey{
		ID: 9212, UserID: 9213, GroupID: &groupID, Group: group, Status: service.StatusActive,
		User: &service.User{ID: 9213, Concurrency: 10, Balance: 100},
	}
	c.Set(string(middleware.ContextKeyAPIKey), apiKey)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	h.Messages(c)

	require.Equal(t, 1, upstream.calls)
	require.Equal(t, []int64{accountID}, upstream.accountIDs)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_start"))
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
	require.Contains(t, recorder.Body.String(), `"text":"ok"`)
	require.NotContains(t, recorder.Body.String(), "No available accounts")
	require.NotContains(t, recorder.Body.String(), `"type":"error"`)
}
