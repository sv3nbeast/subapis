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
	"github.com/tidwall/gjson"
)

type kiroStreamFailoverCooldownStore struct {
	mark429Calls int
}

func TestKiroContextLimitErrorReturnsClaudeCodeCompactionSignal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	svc := &GatewayService{}
	err := &kiropkg.ContextLimitError{Reason: "CONTENT_LENGTH_EXCEEDS_THRESHOLD"}

	require.True(t, svc.handleKiroContextLimitError(c, &Account{ID: 9, Platform: PlatformKiro, Name: "kiro"}, err))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "error", gjson.Get(rec.Body.String(), "type").String())
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "prompt is too long", gjson.Get(rec.Body.String(), "error.message").String())
	require.True(t, HasOpsClientBusinessLimitedReason(c, OpsClientBusinessLimitedReasonContextLimit))
	require.True(t, IsResponseCommitted(c))
}

func TestKiroContextLimitErrorUsesSSEAfterResponseStarted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	_, _ = c.Writer.WriteString(": keepalive\n\n")
	svc := &GatewayService{}
	err := &kiropkg.ContextLimitError{Reason: "CONTENT_LENGTH_EXCEEDS_THRESHOLD"}

	require.True(t, svc.handleKiroContextLimitError(c, &Account{ID: 9, Platform: PlatformKiro}, err))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "event: error")
	require.Contains(t, rec.Body.String(), `"type":"invalid_request_error"`)
	require.Contains(t, rec.Body.String(), `"message":"prompt is too long"`)
	require.True(t, HasGatewaySSEErrorWritten(c))
}

func TestKiroContextLimitErrorIsEligibleForOneConversationRetry(t *testing.T) {
	err := &kiropkg.ContextLimitError{Reason: "CONTENT_LENGTH_EXCEEDS_THRESHOLD"}
	require.Equal(t, "prompt is too long", kiroEmptyEventStreamMessage(err))
	require.True(t, isKiroContextLimitError(&kiroStreamBodyRetriesExhaustedError{Attempts: 2, Cause: err}))
}

func TestHandleKiroHTTPContextLimitNormalizesClientResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	svc := &GatewayService{}
	account := &Account{ID: 9, Platform: PlatformKiro, Name: "kiro"}
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD","message":"Content length exceeds threshold"}`)),
	}

	err := svc.handleKiroHTTPError(context.Background(), resp, c, account, "claude-opus-4.8", nil)
	var contextErr *kiropkg.ContextLimitError
	require.ErrorAs(t, err, &contextErr)
	var failoverErr *UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "prompt is too long", gjson.Get(rec.Body.String(), "error.message").String())
	require.True(t, HasOpsClientBusinessLimitedReason(c, OpsClientBusinessLimitedReasonContextLimit))
}

func TestForwardKiroMessagesStreamContextLimitRetriesConversationThenSignalsCompaction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	oldSleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	defer func() { kiroRetrySleep = oldSleep }()

	contextLimitBody := func() []byte {
		body := bytes.NewBuffer(nil)
		_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
			":event-type": "invalidStateEvent",
		}, []byte(`{"reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD","message":"Content length exceeds threshold"}`)))
		return body.Bytes()
	}
	metadataOnlyBody := func() []byte {
		body := bytes.NewBuffer(nil)
		_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
			":event-type": "messageMetadataEvent",
		}, []byte(`{"conversationId":"retry-still-failed"}`)))
		return body.Bytes()
	}
	successBody := func() []byte {
		body := bytes.NewBuffer(nil)
		_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
			":event-type": "assistantResponseEvent",
		}, []byte(`{"content":"must not reach a third attempt"}`)))
		return body.Bytes()
	}
	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, contextLimitBody()),
			newKiroEventStreamResponse(http.StatusOK, metadataOnlyBody()),
			newKiroEventStreamResponse(http.StatusOK, successBody()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID: 88, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/CONTEXT",
		},
	}
	body := []byte(`{"model":"claude-opus-4-8","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.Nil(t, result)
	var contextErr *kiropkg.ContextLimitError
	require.ErrorAs(t, err, &contextErr)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request_error", gjson.Get(rec.Body.String(), "error.type").String())
	require.Equal(t, "prompt is too long", gjson.Get(rec.Body.String(), "error.message").String())
	require.NotContains(t, rec.Body.String(), "stream_read_error")
	require.Len(t, upstream.requests, 2, "context invalid-state must get exactly one fresh-conversation retry")

	firstPayload, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	secondPayload, readErr := io.ReadAll(upstream.requests[1].Body)
	require.NoError(t, readErr)
	require.NotEqual(t,
		gjson.GetBytes(firstPayload, "conversationState.conversationId").String(),
		gjson.GetBytes(secondPayload, "conversationState.conversationId").String(),
	)
}

func TestForwardKiroMessagesMidStreamContextLimitDoesNotRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	body := bytes.NewBuffer(nil)
	_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"content":"this partial response is already visible to the client"}`)))
	_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "invalidStateEvent",
	}, []byte(`{"reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD","message":"Content length exceeds threshold"}`)))
	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{newKiroEventStreamResponse(http.StatusOK, body.Bytes())},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID: 89, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/CONTEXT-MIDSTREAM",
		},
	}
	requestBody := []byte(`{"model":"claude-opus-4-8","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.Nil(t, result)
	var contextErr *kiropkg.ContextLimitError
	require.ErrorAs(t, err, &contextErr)
	require.True(t, contextErr.ResponseStarted)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"text":"this partial response`)
	require.Contains(t, rec.Body.String(), `"type":"invalid_request_error"`)
	require.Contains(t, rec.Body.String(), `"message":"prompt is too long"`)
	require.Len(t, upstream.requests, 1, "a visible partial response must never be replayed into the same SSE stream")
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

type kiroCancelingReadCloser struct {
	cancel context.CancelFunc
}

func (r *kiroCancelingReadCloser) Read(_ []byte) (int, error) {
	if r.cancel != nil {
		r.cancel()
	}
	return 0, context.Canceled
}

func (r *kiroCancelingReadCloser) Close() error { return nil }

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
		require.True(t, failoverErr.SuppressTempUnschedule)
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
	require.True(t, failoverErr.SuppressTempUnschedule)
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
	require.True(t, failoverErr.SuppressTempUnschedule)
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

func TestForwardKiroMessagesNonStreamingClientCancelDoesNotWrite502(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	requestCtx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(requestCtx)

	account := &Account{
		ID: 43, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TEST",
		},
	}
	upstream := &kiroStreamFailoverQueuedUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       &kiroCancelingReadCloser{cancel: cancel},
	}}}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	parsed := &ParsedRequest{
		Model: "claude-opus-4-8",
		Body:  NewRequestBodyRef([]byte(`{"model":"claude-opus-4-8","max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)),
	}

	result, err := svc.forwardKiroMessages(requestCtx, c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	require.True(t, IsOpenAIClientCanceledError(err), "got %v", err)
	require.False(t, rec.Result().StatusCode == http.StatusBadGateway)
	require.Empty(t, rec.Body.String())
}

func TestForwardKiroMessagesNonStreamingMetadataOnlyRetriesWithFreshConversationAndEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	oldSleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	defer func() { kiroRetrySleep = oldSleep }()

	metadataOnlyBody := bytes.NewBuffer(nil)
	_, _ = metadataOnlyBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "reasoningContentEvent",
	}, []byte(`{"reasoningContentEvent":{"text":"I should think first."}}`)))
	_, _ = metadataOnlyBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stopReason":"end_turn"}}`)))

	successBody := bytes.NewBuffer(nil)
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"non-stream recovered after retry"}}`)))
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageMetadataEvent",
	}, []byte(`{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":11,"outputTokens":5,"totalTokens":16}}}`)))
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stopReason":"end_turn"}}`)))

	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, metadataOnlyBody.Bytes()),
			newKiroEventStreamResponse(http.StatusOK, successBody.Bytes()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          88,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/METAONLY",
		},
	}
	body := []byte(`{"model":"claude-opus-4-7","max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Contains(t, rec.Body.String(), "non-stream recovered after retry")
	require.Len(t, upstream.requests, 2)
	require.NotEqual(t, upstream.requests[0].URL.String(), upstream.requests[1].URL.String(), "non-stream body retry should rotate Kiro endpoint")

	firstPayload, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	secondPayload, err := io.ReadAll(upstream.requests[1].Body)
	require.NoError(t, err)
	firstConversationID := gjson.GetBytes(firstPayload, "conversationState.conversationId").String()
	secondConversationID := gjson.GetBytes(secondPayload, "conversationState.conversationId").String()
	require.NotEmpty(t, firstConversationID)
	require.NotEmpty(t, secondConversationID)
	require.NotEqual(t, firstConversationID, secondConversationID, "non-stream body retry must avoid replaying the same stable conversation id")
}

func TestForwardKiroMessagesNonStreamingMetadataOnlyExhaustionDoesNotAmplifySameAccountRetry(t *testing.T) {
	t.Setenv(kiroStreamBodyRetryEnvVariable, "1")
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	oldSleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	defer func() { kiroRetrySleep = oldSleep }()

	metadataOnly := func() []byte {
		body := bytes.NewBuffer(nil)
		_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
			":event-type": "reasoningContentEvent",
		}, []byte(`{"reasoningContentEvent":{"text":"I should think first."}}`)))
		_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
			":event-type": "messageStopEvent",
		}, []byte(`{"messageStopEvent":{"stopReason":"end_turn"}}`)))
		return body.Bytes()
	}

	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, metadataOnly()),
			newKiroEventStreamResponse(http.StatusOK, metadataOnly()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          88,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/METAONLY",
		},
	}
	body := []byte(`{"model":"claude-opus-4-7","max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount, "internal non-stream Kiro body retries are exhausted; handler must not multiply same-account retries")
	require.True(t, failoverErr.SuppressTempUnschedule)
	require.Contains(t, ExtractUpstreamErrorMessage(failoverErr.ResponseBody), "no deliverable assistant output")
	require.Empty(t, rec.Body.String(), "non-stream exhaustion must not write a partial response body")
	require.Len(t, upstream.requests, 2)
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

func TestForwardKiroMessagesStreamThinkingOnlyReturnsCompleteThinkingResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "reasoningContentEvent",
	}, []byte(`{"reasoningContentEvent":{"text":"I should think first."}}`)))
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
		ID:          88,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/THINKONLY",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"thinking":{"type":"enabled","budget_tokens":1024},"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"type":"thinking_delta"`)
	require.Contains(t, rec.Body.String(), `"thinking":"I should think first."`)
	require.Contains(t, rec.Body.String(), `"type":"signature_delta"`)
	require.Contains(t, rec.Body.String(), "event: message_stop")
}

func TestForwardKiroMessagesStreamMetadataOnlyDoesNotWriteSuccessfulEmptyAnswer(t *testing.T) {
	t.Setenv(kiroStreamBodyRetryEnvVariable, "0")
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageMetadataEvent",
	}, []byte(`{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":119824,"outputTokens":5,"totalTokens":119829}}}`)))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stopReason":"end_turn"}}`)))

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
		ID:          88,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/METAONLY",
		},
	}
	body := []byte(`{"model":"claude-opus-4-8-thinking","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, failoverErr.RetryableOnSameAccount)
	require.True(t, failoverErr.SuppressTempUnschedule)
	require.Contains(t, ExtractUpstreamErrorMessage(failoverErr.ResponseBody), "metadata-only assistant output")
	require.Empty(t, rec.Body.String(), "metadata-only empty stream must not write a successful empty response body")
}

func TestForwardKiroMessagesStreamMetadataOnlyRetriesWithFreshConversationAndEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	oldSleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	defer func() { kiroRetrySleep = oldSleep }()

	metadataOnlyBody := bytes.NewBuffer(nil)
	_, _ = metadataOnlyBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageMetadataEvent",
	}, []byte(`{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":119824,"outputTokens":5,"totalTokens":119829}}}`)))
	_, _ = metadataOnlyBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stopReason":"end_turn"}}`)))

	successBody := bytes.NewBuffer(nil)
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"recovered after retry"}}`)))
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageMetadataEvent",
	}, []byte(`{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":11,"outputTokens":4,"totalTokens":15}}}`)))
	_, _ = successBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stopReason":"end_turn"}}`)))

	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, metadataOnlyBody.Bytes()),
			newKiroEventStreamResponse(http.StatusOK, successBody.Bytes()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          88,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/METAONLY",
		},
	}
	body := []byte(`{"model":"claude-sonnet-5","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, rec.Body.String(), "recovered after retry")
	require.Len(t, upstream.requests, 2)
	require.NotEqual(t, upstream.requests[0].URL.String(), upstream.requests[1].URL.String(), "body-level retry should rotate Kiro endpoint before falling back to handler failover")

	firstPayload, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	secondPayload, err := io.ReadAll(upstream.requests[1].Body)
	require.NoError(t, err)
	firstConversationID := gjson.GetBytes(firstPayload, "conversationState.conversationId").String()
	secondConversationID := gjson.GetBytes(secondPayload, "conversationState.conversationId").String()
	require.NotEmpty(t, firstConversationID)
	require.NotEmpty(t, secondConversationID)
	require.NotEqual(t, firstConversationID, secondConversationID, "metadata-only body retry must avoid replaying the same stable conversation id")
}

func TestForwardKiroMessagesStreamMetadataOnlyExhaustionDoesNotAmplifySameAccountRetry(t *testing.T) {
	t.Setenv(kiroStreamBodyRetryEnvVariable, "1")
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	oldSleep := kiroRetrySleep
	kiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	defer func() { kiroRetrySleep = oldSleep }()

	metadataOnly := func() []byte {
		body := bytes.NewBuffer(nil)
		_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
			":event-type": "messageMetadataEvent",
		}, []byte(`{"messageMetadataEvent":{"tokenUsage":{"uncachedInputTokens":119824,"outputTokens":5,"totalTokens":119829}}}`)))
		_, _ = body.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
			":event-type": "messageStopEvent",
		}, []byte(`{"messageStopEvent":{"stopReason":"end_turn"}}`)))
		return body.Bytes()
	}

	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{
			newKiroEventStreamResponse(http.StatusOK, metadataOnly()),
			newKiroEventStreamResponse(http.StatusOK, metadataOnly()),
		},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &kiroStreamFailoverCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          88,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/METAONLY",
		},
	}
	body := []byte(`{"model":"claude-sonnet-5","stream":true,"max_tokens":128,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.False(t, failoverErr.RetryableOnSameAccount, "internal Kiro body retries are already exhausted; handler must not multiply same-account retries")
	require.True(t, failoverErr.SuppressTempUnschedule, "metadata-only empty stream must not mark a healthy Kiro account unschedulable")
	require.Contains(t, ExtractUpstreamErrorMessage(failoverErr.ResponseBody), "metadata-only assistant output")
	require.Empty(t, rec.Body.String(), "metadata-only exhaustion must not write a successful empty response body")
	require.Len(t, upstream.requests, 2)
}

func TestForwardKiroMessagesStreamFinalMeteringAllowsMissingTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"complete answer from kiro"}}`)))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "meteringEvent",
	}, []byte(`{"meteringEvent":{"unit":"credit","usage":0.17}}`)))
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

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.NotNil(t, result.FirstTokenMs)
	require.Contains(t, rec.Body.String(), "complete answer")
	require.Contains(t, rec.Body.String(), "from kiro")
	require.Contains(t, rec.Body.String(), "event: message_stop")
	require.NotContains(t, rec.Body.String(), "event: error")
	require.NotContains(t, rec.Body.String(), "sub2api_internal_kiro_ping")
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

func TestForwardKiroMessagesStreamMissingTerminalAfterPartialOutputReturnsSSEError(t *testing.T) {
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
	require.False(t, errors.As(err, &failoverErr), "partial output cannot be retried into the same client stream")
	require.Contains(t, err.Error(), "missing terminal event")
	require.Contains(t, rec.Body.String(), `"text":"part"`)
	require.Contains(t, rec.Body.String(), `"text":"ial answer"`)
	require.NotContains(t, rec.Body.String(), "sub2api_internal_kiro_ping")
	require.NotContains(t, rec.Body.String(), "event: message_stop")
	require.Contains(t, rec.Body.String(), "event: error")
	require.Contains(t, rec.Body.String(), `"type":"stream_read_error"`)
	require.True(t, HasGatewaySSEErrorWritten(c))
	rawOpsEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	opsEvents, ok := rawOpsEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.NotEmpty(t, opsEvents)
	require.Equal(t, "stream_partial_error", opsEvents[len(opsEvents)-1].Kind)
	require.Equal(t, account.ID, opsEvents[len(opsEvents)-1].AccountID)
}

func TestForwardKiroMessagesAllowsAssistantTextPrefill(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":" continuation"}}`)))
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
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"stream":true,
		"max_tokens":128,
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":"prefill"}
		]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
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

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Contains(t, rec.Body.String(), " continuation")
	require.Len(t, upstream.requests, 1)
	payload, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)
	require.True(t, kiroPayloadHistoryContainsAssistantContent(payload, "prefill"))
	require.Equal(t, "Continue", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String())
}

func TestForwardKiroMessagesRejectsFinalAssistantToolUseBeforeUpstream(t *testing.T) {
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
	body := []byte(`{
		"model":"claude-sonnet-4-6",
		"stream":true,
		"max_tokens":128,
		"messages":[
			{"role":"user","content":"find weather"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"get_weather","input":{}}]}
		]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
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
	require.Contains(t, rec.Body.String(), "assistant final tool_use is not supported")
	require.Empty(t, upstream.requests, "invalid Kiro final tool_use shape must not hit upstream")
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

func kiroPayloadHistoryContainsAssistantContent(payload []byte, content string) bool {
	for _, item := range gjson.GetBytes(payload, "conversationState.history").Array() {
		if item.Get("assistantResponseMessage.content").String() == content {
			return true
		}
	}
	return false
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
