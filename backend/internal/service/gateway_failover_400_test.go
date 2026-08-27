package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type failover400SettingRepo struct{ SettingRepository }

func (r *failover400SettingRepo) GetValue(context.Context, string) (string, error) {
	return "", ErrSettingNotFound
}

func (r *failover400SettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{}, nil
}

func TestShouldFailoverOn400AllowsOnlyExplicitBetaCompatibilityErrors(t *testing.T) {
	svc := &GatewayService{}
	for _, message := range []string{
		"anthropic-beta header is required",
		"request requires beta header",
		"beta feature is not enabled for this account",
	} {
		t.Run(message, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"error":{"message":%q}}`, message))
			require.True(t, svc.shouldFailoverOn400(body))
		})
	}
}

func TestShouldFailoverOn400RejectsDeterministicHistoryAndToolErrors(t *testing.T) {
	svc := &GatewayService{}
	for _, message := range []string{
		"Invalid signature in thinking block",
		"thinking or redacted_thinking blocks in the latest assistant message cannot be modified",
		"missing thought_signature",
		"tool_use block requires a matching tool_result",
		"tools must have unique names",
		"the model is thinking about a beta feature",
	} {
		t.Run(message, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"error":{"message":%q}}`, message))
			require.False(t, svc.shouldFailoverOn400(body))
		})
	}
}

func TestShouldFailoverOn400RejectsUnknownOrEmptyError(t *testing.T) {
	svc := &GatewayService{}
	require.False(t, svc.shouldFailoverOn400([]byte(`{"error":{"message":"bad request"}}`)))
	require.False(t, svc.shouldFailoverOn400(nil))
}

func TestGatewayServiceForwardFailoverOn400HonorsClassifierBeforeClientWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, stream := range []bool{false, true} {
		stream := stream
		for _, tc := range []struct {
			name           string
			message        string
			expectFailover bool
		}{
			{
				name:           "explicit beta compatibility error",
				message:        "anthropic-beta header is required",
				expectFailover: true,
			},
			{
				name:           "deterministic tool schema error",
				message:        "tools must have unique names",
				expectFailover: false,
			},
		} {
			t.Run(fmt.Sprintf("stream=%t/%s", stream, tc.name), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

				body := []byte(fmt.Sprintf(`{"model":"claude-opus-5","stream":%t,"messages":[{"role":"user","content":"hello"}]}`, stream))
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
				require.NoError(t, err)

				upstreamBody := fmt.Sprintf(`{"type":"error","error":{"type":"invalid_request_error","message":%q}}`, tc.message)
				upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(upstreamBody)),
				}}
				cfg := &config.Config{Gateway: config.GatewayConfig{
					FailoverOn400: true,
					MaxLineSize:   defaultMaxLineSize,
				}}
				svc := &GatewayService{
					cfg:                  cfg,
					responseHeaderFilter: compileResponseHeaderFilter(cfg),
					httpUpstream:         upstream,
					rateLimitService:     &RateLimitService{},
					deferredService:      &DeferredService{},
					settingService:       &SettingService{settingRepo: &failover400SettingRepo{}},
				}
				account := &Account{
					ID:          4001,
					Name:        "failover-400-entrypoint",
					Platform:    PlatformAnthropic,
					Type:        AccountTypeAPIKey,
					Concurrency: 1,
					Credentials: map[string]any{"api_key": "sk-test"},
					Status:      StatusActive,
					Schedulable: true,
				}

				result, err := svc.Forward(context.Background(), c, account, parsed)
				require.Nil(t, result)
				require.Error(t, err)
				var failoverErr *UpstreamFailoverError
				if tc.expectFailover {
					require.True(t, errors.As(err, &failoverErr))
					require.Empty(t, recorder.Body.String(), "failover must happen before client-visible output")
					return
				}

				require.False(t, errors.As(err, &failoverErr))
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Contains(t, recorder.Body.String(), tc.message)
			})
		}
	}
}
