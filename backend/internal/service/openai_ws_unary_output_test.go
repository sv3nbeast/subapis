package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise the real Forward entry, including OAuth stream=true normalization
// and the WS pool. A successful usage row alone must not pass this regression.
func TestOpenAIWSUnaryOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		for _, tc := range []struct {
			name   string
			events []string
			output string
			want   string
		}{
			{"text deltas", []string{
				`{"type":"response.output_text.delta","output_index":0,"delta":"5"}`,
				`{"type":"response.output_text.delta","output_index":0,"delta":"9"}`,
			}, `[]`, "59"},
			{"authoritative done preserves extensions", []string{
				`{"type":"response.output_text.delta","delta":"partial"}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_answer","role":"assistant","status":"completed","vendor_extension":{"ok":true},"content":[{"type":"output_text","text":"59"}]}}`,
			}, `[]`, "59"},
			{"populated terminal wins", []string{
				`{"type":"response.output_text.delta","delta":"wrong fallback"}`,
			}, `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"59"}]}]`, "59"},
			{"no output is not fabricated", nil, `[]`, ""},
			{"bounded aggregation", nil, `[]`, ""},
			{"interleaved opaque reasoning tool and text", []string{
				`{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"你好.txt\"}"}}`,
				`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"opaque","summary":[]}}`,
				`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_answer","role":"assistant","content":[{"type":"output_text","text":"59"}]}}`,
			}, `[]`, "59"},
		} {
			t.Run(accountType+"/"+tc.name, func(t *testing.T) {
				for _, streaming := range []bool{false, true} {
					t.Run(fmt.Sprint("stream=", streaming), func(t *testing.T) {
						cfg := &config.Config{}
						if tc.name == "bounded aggregation" {
							cfg.Gateway.UpstreamResponseReadMaxBytes = 8
						}
						cfg.Security.URLAllowlist.Enabled = false
						cfg.Security.URLAllowlist.AllowInsecureHTTP = true
						cfg.Gateway.OpenAIWS.Enabled = true
						cfg.Gateway.OpenAIWS.OAuthEnabled = true
						cfg.Gateway.OpenAIWS.APIKeyEnabled = true
						cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
						cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
						conn := &openAIWSCaptureConn{}
						for _, event := range tc.events {
							conn.events = append(conn.events, []byte(event))
						}
						terminal := fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_unary","model":"gpt-5.1","status":"completed","output":%s,"usage":{"input_tokens":76,"output_tokens":5,"input_tokens_details":{"cached_tokens":32}}}}`, tc.output)
						conn.events = append(conn.events, []byte(terminal))
						pool := newOpenAIWSConnPool(cfg)
						t.Cleanup(pool.Close)
						pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: conn})
						upstream := &httpUpstreamRecorder{}
						svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, cache: &stubGatewayCache{}, openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), openaiWSPool: pool}
						account := &Account{ID: 29, Platform: PlatformOpenAI, Type: accountType, Status: StatusActive, Schedulable: true, Concurrency: 1,
							Credentials: map[string]any{"access_token": "test", "api_key": "test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
						body := []byte(fmt.Sprintf(`{"model":"gpt-5.1","stream":%t,"instructions":"Answer briefly","input":"20+39"}`, streaming))
						result, err := svc.Forward(context.Background(), c, account, body)
						if tc.name == "bounded aggregation" && !streaming {
							require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
							require.Equal(t, http.StatusBadGateway, rec.Code)
							require.Empty(t, upstream.lastBody, "oversize must not replay upstream generation")
							return
						}
						require.NoError(t, err)
						require.True(t, result.OpenAIWSMode)
						require.Empty(t, upstream.lastBody, "must not replay over HTTP")
						require.Equal(t, 76, result.Usage.InputTokens)
						require.Equal(t, 5, result.Usage.OutputTokens)
						require.Equal(t, 32, result.Usage.CacheReadInputTokens)
						if streaming {
							require.Contains(t, rec.Body.String(), "data: "+terminal, "streaming terminal remains unchanged")
							for _, event := range tc.events {
								require.Contains(t, rec.Body.String(), "data: "+event)
							}
							return
						}
						require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
						require.Equal(t, tc.want, extractOpenAIResponsesText(rec.Body.Bytes()))
						require.Equal(t, "completed", gjson.GetBytes(rec.Body.Bytes(), "status").String())
						if tc.name == "authoritative done preserves extensions" {
							require.True(t, gjson.GetBytes(rec.Body.Bytes(), "output.0.vendor_extension.ok").Bool())
							require.Equal(t, "msg_answer", gjson.GetBytes(rec.Body.Bytes(), "output.0.id").String())
						}
						if tc.name == "interleaved opaque reasoning tool and text" {
							require.Equal(t, "opaque", gjson.GetBytes(rec.Body.Bytes(), "output.0.encrypted_content").String())
							require.Equal(t, "call_1", gjson.GetBytes(rec.Body.Bytes(), "output.2.call_id").String())
						}
					})
				}
			})
		}
	}
}
