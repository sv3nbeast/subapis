package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type kiroStreamFailoverCooldownStore struct {
	mark429Calls int
}

func (s *kiroStreamFailoverCooldownStore) ReserveRequest(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *kiroStreamFailoverCooldownStore) MarkSuccess(context.Context, string) error {
	return nil
}

func (s *kiroStreamFailoverCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	s.mark429Calls++
	return time.Minute, nil
}

func (s *kiroStreamFailoverCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *kiroStreamFailoverCooldownStore) GetState(context.Context, string) (*kirocooldown.State, error) {
	return nil, nil
}

func (s *kiroStreamFailoverCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	return false, nil
}

type kiroStreamFailoverQueuedUpstream struct {
	responses []*http.Response
	requests  []*http.Request
	errs      []error
}

func (u *kiroStreamFailoverQueuedUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, fmt.Errorf("unexpected Do call")
}

func (u *kiroStreamFailoverQueuedUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests = append(u.requests, req)
	if len(u.errs) > 0 {
		err := u.errs[0]
		u.errs = u.errs[1:]
		return nil, err
	}
	if len(u.responses) == 0 {
		return nil, fmt.Errorf("no mocked response")
	}
	resp := u.responses[0]
	u.responses = u.responses[1:]
	return resp, nil
}

func TestGatewayServiceKiroStreamExceptionReturnsKiro429FailoverWithoutCooldown(t *testing.T) {
	store := &kiroStreamFailoverCooldownStore{}
	svc := &GatewayService{kiroCooldownStore: store}
	account := &Account{
		ID:       1459,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
	}
	err := &UpstreamFailoverError{
		StatusCode: http.StatusBadGateway,
		Cause: &kiropkg.UpstreamExceptionError{
			ExceptionType: "ThrottlingException",
			Message:       "Too many requests, please wait before trying again.",
		},
	}

	failoverErr := svc.kiroStreamErrorToFailover(context.Background(), account, err)

	require.NotNil(t, failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.KiroRateLimited)
	require.Equal(t, 0, store.mark429Calls)
}

func TestGatewayServiceKiroEmptyStreamIsRetryableFailover(t *testing.T) {
	svc := &GatewayService{kiroCooldownStore: &kiroStreamFailoverCooldownStore{}}
	account := &Account{ID: 1459, Platform: PlatformKiro, Type: AccountTypeOAuth}
	for _, errText := range []string{
		"stream read error: empty kiro event stream: no assistant output",
		"stream read error: empty kiro event stream: no deliverable assistant output",
	} {
		failoverErr := svc.kiroStreamErrorToFailover(context.Background(), account, errors.New(errText))

		require.NotNil(t, failoverErr)
		require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
		require.True(t, failoverErr.RetryableOnSameAccount)
	}
}

func TestGatewayServiceKiroIncompleteStreamIsRetryableFailover(t *testing.T) {
	svc := &GatewayService{kiroCooldownStore: &kiroStreamFailoverCooldownStore{}}
	account := &Account{ID: 1459, Platform: PlatformKiro, Type: AccountTypeOAuth}
	err := fmt.Errorf("stream read error: %w", &kiropkg.IncompleteStreamError{Message: "incomplete kiro event stream: missing terminal event"})

	failoverErr := svc.kiroStreamErrorToFailover(context.Background(), account, err)

	require.NotNil(t, failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, ExtractUpstreamErrorMessage(failoverErr.ResponseBody), "incomplete kiro event stream")
}

func TestGatewayServiceKiroEmptyStreamWrappedFailoverKeepsKiroClassification(t *testing.T) {
	svc := &GatewayService{kiroCooldownStore: &kiroStreamFailoverCooldownStore{}}
	account := &Account{ID: 1459, Platform: PlatformKiro, Type: AccountTypeOAuth}
	err := &UpstreamFailoverError{
		StatusCode:             http.StatusBadGateway,
		ResponseBody:           []byte(`{"type":"error","error":{"type":"upstream_disconnected","message":"upstream stream disconnected: upstream connection error"}}`),
		RetryableOnSameAccount: true,
		Cause:                  errors.New("empty kiro event stream: no deliverable assistant output"),
	}

	failoverErr := svc.kiroStreamErrorToFailover(context.Background(), account, err)

	require.NotNil(t, failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, ExtractUpstreamErrorMessage(failoverErr.ResponseBody), "empty kiro event stream")
}

func TestForwardKiroMessagesNonStreamingExceptionReturnsKiro429Failover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		},
	}
	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, buildKiroExceptionFrame(t, "ThrottlingException", map[string]any{
				"message": "Too many requests, please wait before trying again.",
			})),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	parsed := &ParsedRequest{
		Model: "claude-sonnet-4-6",
		Body:  NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)),
	}

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.True(t, failoverErr.KiroRateLimited)
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, http.StatusOK, rec.Code, "parse failover should not write a plain 502 before handler can retry")
}

func TestForwardKiroMessagesStreamCapturesMeteringCredits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"hello"}}`)))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageMetadataEvent",
	}, []byte(`{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":7,"outputTokens":3}}}`)))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "meteringEvent",
	}, []byte(`{"meteringEvent":{"usage":0.17}}`)))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stop_reason":"end_turn"}}`)))

	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, upstreamBody.Bytes()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          21,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/STREAMCREDITS",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.InDelta(t, 0.17, result.Usage.KiroCredits, 0.000001)
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_credits")
}

func TestForwardKiroMessagesStreamOpenContextCanceledDoesNotWriteFallbackBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstream := &kiroStreamFailoverQueuedUpstream{errs: []error{
		context.Canceled,
		context.Canceled,
		context.Canceled,
	}}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		},
	}
	parsed := &ParsedRequest{
		Model:  "claude-opus-4-8",
		Stream: true,
		Body:   NewRequestBodyRef([]byte(`{"model":"claude-opus-4-8","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)),
	}

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "context canceled")
	require.False(t, c.Writer.Written(), "stream open failures must not pre-write JSON before handler fallback")
	require.Empty(t, rec.Body.String())
}

func TestForwardKiroMessagesStreamOpenNetworkErrorTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstream := &kiroStreamFailoverQueuedUpstream{errs: []error{
		io.ErrUnexpectedEOF,
		io.ErrUnexpectedEOF,
		io.ErrUnexpectedEOF,
	}}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		},
	}
	parsed := &ParsedRequest{
		Model:  "claude-opus-4-8",
		Stream: true,
		Body:   NewRequestBodyRef([]byte(`{"model":"claude-opus-4-8","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)),
	}

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, ExtractUpstreamErrorMessage(failoverErr.ResponseBody), "upstream request disconnected before response")
	require.False(t, c.Writer.Written(), "pre-response failover must not write before handler retry")
	require.Empty(t, rec.Body.String())
}

func TestOpenKiroAnthropicStreamResponseDetachesClientCancellation(t *testing.T) {
	parentCtx, cancelParent := context.WithCancel(context.Background())
	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"hello from kiro"}}`)))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stop_reason":"end_turn"}}`)))
	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, upstreamBody.Bytes()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	resp, _, err := svc.openKiroAnthropicStreamResponse(parentCtx, account, parsed, body, parsed.Model, parsed.Model, http.Header{}, nil)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)

	cancelParent()
	select {
	case <-upstream.requests[0].Context().Done():
		t.Fatal("kiro stream upstream request context must not be canceled by client context")
	default:
	}

	streamBytes, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(streamBytes), "hello from kiro")
}

func TestOpenKiroAnthropicStreamResponseAllowsDirectAPIKey(t *testing.T) {
	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"hello from kiro api key"}}`)))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stop_reason":"end_turn"}}`)))
	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, upstreamBody.Bytes()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          43,
		Platform:    PlatformKiro,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"kiroApiKey": "ksk_test_key",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	resp, _, err := svc.openKiroAnthropicStreamResponse(context.Background(), account, parsed, body, parsed.Model, parsed.Model, http.Header{}, nil)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "Bearer ksk_test_key", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, []string{"API_KEY"}, upstream.requests[0].Header["TokenType"])
	streamBytes, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(streamBytes), "hello from kiro api key")
}

func TestForwardKiroMessagesStreamMissingTerminalEventTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"partial answer"}}`)))
	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, upstreamBody.Bytes()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		},
	}
	parsed := &ParsedRequest{
		Model:  "claude-opus-4-8",
		Stream: true,
		Body:   NewRequestBodyRef([]byte(`{"model":"claude-opus-4-8","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)),
	}

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.Contains(t, ExtractUpstreamErrorMessage(failoverErr.ResponseBody), "incomplete kiro event stream")
	require.Empty(t, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "sub2api_internal_kiro_ping")
	require.NotContains(t, rec.Body.String(), "event: message_stop")
}

func TestForwardKiroMessagesRejectsAssistantPrefillBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstream := &kiroStreamFailoverQueuedUpstream{}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	parsed := &ParsedRequest{
		Model: "claude-sonnet-4-6",
		Body: NewRequestBodyRef([]byte(`{
			"model":"claude-sonnet-4-6",
			"max_tokens":128,
			"messages":[
				{"role":"user","content":"hello"},
				{"role":"assistant","content":"prefill"}
			]
		}`)),
	}
	account := &Account{
		ID:          42,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token": "test-token",
		},
	}

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "assistant-prefill final message is not supported")
	require.Empty(t, upstream.requests, "invalid Kiro request shape must not hit upstream")
}

func TestValidateKiroRequestShapeAllowsToolResultFinalTurn(t *testing.T) {
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"messages":[
			{"role":"user","content":"find weather"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"get_weather","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"sunny"}]}
		]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)

	require.Empty(t, validateKiroRequestShape(parsed))
}

func newKiroEventStreamResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}
}

func buildKiroExceptionFrame(t *testing.T, exceptionType string, payload any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	return buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":message-type":   "exception",
		":exception-type": exceptionType,
	}, payloadBytes)
}

func buildKiroEventStreamFrameWithHeaders(t *testing.T, headerValues map[string]string, payloadBytes []byte) []byte {
	t.Helper()
	headers := bytes.NewBuffer(nil)
	for name, value := range headerValues {
		_ = headers.WriteByte(byte(len(name)))
		_, _ = headers.WriteString(name)
		_ = headers.WriteByte(7)
		require.NoError(t, binary.Write(headers, binary.BigEndian, uint16(len(value))))
		_, _ = headers.WriteString(value)
	}

	totalLength := uint32(12 + headers.Len() + len(payloadBytes) + 4)
	frame := bytes.NewBuffer(nil)
	require.NoError(t, binary.Write(frame, binary.BigEndian, totalLength))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(headers.Len())))
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payloadBytes)
	require.NoError(t, binary.Write(frame, binary.BigEndian, uint32(0)))
	return frame.Bytes()
}
