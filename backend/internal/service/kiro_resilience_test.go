package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type recordingKiroResilienceCooldownStore struct {
	markSuccessCalls         int
	mark429Calls             int
	markUnresponsiveCalls    int
	observeUnresponsiveCalls int
	lastRetryAfter           time.Duration
	lastObservationScope     string
	observationResult        *kirocooldown.UnresponsiveObservationResult
	states                   map[string]*kirocooldown.State
}

func (s *recordingKiroResilienceCooldownStore) ReserveRequest(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *recordingKiroResilienceCooldownStore) MarkSuccess(context.Context, string) error {
	s.markSuccessCalls++
	return nil
}

func (s *recordingKiroResilienceCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	s.mark429Calls++
	return time.Minute, nil
}

func (s *recordingKiroResilienceCooldownStore) Mark429WithRetryAfter(_ context.Context, _ string, retryAfter time.Duration) (time.Duration, error) {
	s.mark429Calls++
	s.lastRetryAfter = retryAfter
	if retryAfter <= 0 {
		return time.Minute, nil
	}
	return retryAfter, nil
}

func (s *recordingKiroResilienceCooldownStore) MarkUnresponsive(context.Context, string, time.Duration, time.Duration) (time.Duration, error) {
	s.markUnresponsiveCalls++
	return 30 * time.Second, nil
}

func (s *recordingKiroResilienceCooldownStore) ObserveUnresponsive(
	_ context.Context,
	_ string,
	observationScope string,
	_ time.Duration,
	_ time.Duration,
	_ int,
	_ time.Duration,
) (*kirocooldown.UnresponsiveObservationResult, error) {
	s.observeUnresponsiveCalls++
	s.lastObservationScope = observationScope
	if s.observationResult != nil {
		return s.observationResult, nil
	}
	return &kirocooldown.UnresponsiveObservationResult{FailureCount: 1}, nil
}

func (s *recordingKiroResilienceCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *recordingKiroResilienceCooldownStore) GetState(_ context.Context, tokenKey string) (*kirocooldown.State, error) {
	return s.states[tokenKey], nil
}

func (s *recordingKiroResilienceCooldownStore) GetStates(_ context.Context, tokenKeys []string) (map[string]*kirocooldown.State, error) {
	result := make(map[string]*kirocooldown.State)
	for _, tokenKey := range tokenKeys {
		if state := s.states[tokenKey]; state != nil && state.Active {
			result[tokenKey] = state
		}
	}
	return result, nil
}

func (s *recordingKiroResilienceCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	return false, nil
}

type writeObservedFailureKiroUpstream struct {
	requests int
}

type blockedKiroMCPHeaderUpstream struct {
	calls   atomic.Int32
	release chan struct{}
	exited  chan struct{}
}

type canceledKiroHeaderUpstream struct {
	calls atomic.Int32
}

type kiroTestErrorReadCloser struct {
	reader io.Reader
	err    error
}

type kiroCloseTrackingBody struct {
	closed atomic.Bool
}

func (b *kiroCloseTrackingBody) Read([]byte) (int, error) { return 0, io.EOF }

func (b *kiroCloseTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}

func (r *kiroTestErrorReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		return n, nil
	}
	if errors.Is(err, io.EOF) && r.err != nil {
		terminalErr := r.err
		r.err = nil
		return 0, terminalErr
	}
	return n, err
}

func (r *kiroTestErrorReadCloser) Close() error { return nil }

func (u *writeObservedFailureKiroUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, errors.New("unexpected Do call")
}

func (u *writeObservedFailureKiroUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.requests++
	if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.WroteRequest != nil {
		trace.WroteRequest(httptrace.WroteRequestInfo{})
	}
	return nil, io.ErrUnexpectedEOF
}

func (u *blockedKiroMCPHeaderUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, errors.New("unexpected Do call")
}

func (u *blockedKiroMCPHeaderUpstream) DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls.Add(1)
	<-u.release
	close(u.exited)
	return nil, context.Canceled
}

func (u *canceledKiroHeaderUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	return nil, errors.New("unexpected Do call")
}

func (u *canceledKiroHeaderUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls.Add(1)
	if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.WroteRequest != nil {
		trace.WroteRequest(httptrace.WroteRequestInfo{})
	}
	<-req.Context().Done()
	return nil, context.Cause(req.Context())
}

func enforcedKiroResilienceTestService() *GatewayService {
	return &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		KiroResilience: config.GatewayKiroResilienceConfig{
			Mode:                         config.KiroResilienceModeEnforce,
			ResponseHeaderTimeoutSeconds: 30,
			FirstSemanticTimeoutSeconds:  60,
			FailoverBudgetSeconds:        105,
			PreSemanticBufferBytes:       256 * 1024,
			CleanupGraceSeconds:          3,
			UnresponsiveCooldownSeconds:  30,
			UnresponsiveCooldownMaxSecs:  120,
		},
	}}}
}

func observedKiroResilienceTestService() *GatewayService {
	svc := enforcedKiroResilienceTestService()
	svc.cfg.Gateway.KiroResilience.Mode = config.KiroResilienceModeObserve
	return svc
}

func incompleteAnthropicSSEForKiroTest() io.ReadCloser {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_incomplete","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":0}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}`,
		``,
	}, "\n")
	return &kiroTestErrorReadCloser{
		reader: strings.NewReader(stream),
		err:    errors.New("incomplete upstream stream"),
	}
}

func TestKiroResilienceModeHonorsGroupAllowlist(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	svc.cfg.Gateway.KiroResilience.GroupIDs = []int64{7, 9}
	allowed := int64(7)
	blocked := int64(8)

	require.True(t, svc.kiroResilienceEnforced(&allowed))
	require.False(t, svc.kiroResilienceEnforced(&blocked))
	require.False(t, svc.kiroResilienceEnforced(nil))
}

func TestStartKiroResilienceTrackingObservePreservesParentCancellation(t *testing.T) {
	svc := observedKiroResilienceTestService()
	parent, cancel := context.WithCancelCause(context.Background())
	groupID := int64(17)
	account := &Account{ID: 171, Platform: PlatformKiro}

	tracked, err := svc.StartKiroResilienceTracking(parent, &groupID, account)
	require.NoError(t, err)
	_, hasDeadline := tracked.Deadline()
	require.False(t, hasDeadline, "observe mode must not add a request deadline")
	require.NotNil(t, kiroObservationFromContext(tracked))

	parentErr := errors.New("client disconnected")
	cancel(parentErr)
	select {
	case <-tracked.Done():
		require.ErrorIs(t, context.Cause(tracked), parentErr)
	case <-time.After(time.Second):
		t.Fatal("observe context did not preserve parent cancellation")
	}
}

func TestKiroSemanticObserverWriterPassesSplitSSEThroughUnchanged(t *testing.T) {
	svc := observedKiroResilienceTestService()
	groupID := int64(18)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tracked, err := svc.StartKiroResilienceTracking(ctx, &groupID, &Account{ID: 181, Platform: PlatformKiro})
	require.NoError(t, err)
	observer := svc.startKiroFirstSemanticObservation(tracked, &groupID, &Account{ID: 181, Platform: PlatformKiro}, time.Now(), 0)
	require.NotNil(t, observer)
	t.Cleanup(observer.stop)

	input := strings.Join([]string{
		"event: sub2api_internal_kiro_ping\r\ndata: {}\r\n\r\n",
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_observe\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
	}, "")
	var output bytes.Buffer
	writer := &kiroSemanticObserverWriter{dst: &output, observer: observer}
	for _, fragment := range []string{input[:13], input[13:61], input[61:129], input[129:]} {
		n, writeErr := writer.Write([]byte(fragment))
		require.NoError(t, writeErr)
		require.Equal(t, len(fragment), n)
		if output.Len() < len(input) {
			continue
		}
	}

	require.Equal(t, input, output.String(), "observe mode must preserve every downstream byte")
	require.True(t, observer.stopped.Load(), "non-empty text delta must end first-semantic observation")
	require.False(t, kiroObservationFromContext(tracked).preSemantic.Load())
}

func TestKiroSemanticObserverIgnoresPingAndMessageStart(t *testing.T) {
	svc := observedKiroResilienceTestService()
	groupID := int64(19)
	ctx, cancel := context.WithCancel(context.Background())
	tracked, err := svc.StartKiroResilienceTracking(ctx, &groupID, &Account{ID: 191, Platform: PlatformKiro})
	require.NoError(t, err)
	observer := svc.startKiroFirstSemanticObservation(tracked, &groupID, &Account{ID: 191, Platform: PlatformKiro}, time.Now(), 0)
	require.NotNil(t, observer)

	var output bytes.Buffer
	writer := &kiroSemanticObserverWriter{dst: &output, observer: observer}
	metadata := "event: sub2api_internal_kiro_ping\ndata: {}\n\nevent: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_metadata\"}}\n\n"
	_, err = writer.Write([]byte(metadata))
	require.NoError(t, err)
	require.False(t, observer.stopped.Load(), "ping and message_start are not semantic output")
	require.True(t, kiroObservationFromContext(tracked).preSemantic.Load())

	cancel()
	select {
	case <-observer.done:
	case <-time.After(time.Second):
		t.Fatal("observer timer was not stopped after request cancellation")
	}
	require.Equal(t, metadata, output.String())
}

func TestKiroResponseHeaderObservationDoesNotCancelRequest(t *testing.T) {
	svc := observedKiroResilienceTestService()
	svc.cfg.Gateway.KiroResilience.ResponseHeaderTimeoutSeconds = 1
	groupID := int64(20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := SnapshotKiroResilienceMetrics().ResponseHeaderTimeoutObservedTotal

	stop := svc.startKiroResponseHeaderObservation(ctx, &groupID, &Account{ID: 201, Platform: PlatformKiro}, "q", 1, 1, time.Second)
	defer stop()
	require.Eventually(t, func() bool {
		return SnapshotKiroResilienceMetrics().ResponseHeaderTimeoutObservedTotal == before+1
	}, 2*time.Second, 10*time.Millisecond)
	require.NoError(t, ctx.Err(), "observe timer must not cancel the upstream request")
}

func TestKiroAPIKeyAccountsShareCooldownIdentityByCredential(t *testing.T) {
	accountA := &Account{ID: 1, Platform: PlatformKiro, Type: AccountTypeAPIKey, Credentials: map[string]any{"kiro_api_key": "shared-secret"}}
	accountB := &Account{ID: 2, Platform: PlatformKiro, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "shared-secret"}}
	accountC := &Account{ID: 3, Platform: PlatformKiro, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "different-secret"}}

	require.NotEqual(t, buildKiroAccountKey(accountA), buildKiroAccountKey(accountB), "request fingerprints remain account-specific")
	require.Equal(t, buildKiroCooldownKey(accountA), buildKiroCooldownKey(accountB))
	require.NotEqual(t, buildKiroCooldownKey(accountA), buildKiroCooldownKey(accountC))
	require.NotContains(t, buildKiroCooldownKey(accountA), "shared-secret")
}

func TestKiroCooldownExhaustedRequiresEligibleAccountIDs(t *testing.T) {
	ctx := context.WithValue(context.Background(), kiroCooldownPrefetchContextKey{}, &kiroCooldownPrefetch{
		enforced: true,
		states: map[int64]*kirocooldown.State{
			301: {
				Active:    true,
				Reason:    kirocooldown.CooldownReason429,
				Remaining: time.Minute,
			},
		},
	})

	require.NoError(t, kiroCooldownExhaustedErrorFromContext(ctx, nil), "ineligible cooling accounts must not determine the response")
	var cooldownErr *KiroCooldownExhaustedError
	require.ErrorAs(t, kiroCooldownExhaustedErrorFromContext(ctx, map[int64]struct{}{301: {}}), &cooldownErr)
	require.Equal(t, http.StatusTooManyRequests, cooldownErr.StatusCode)
}

func TestKiroUnresponsiveCooldownDoesNotBlockFutureRouting(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	groupID := int64(29)
	account := Account{
		ID: 1945, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "shared-kiro-account"},
	}
	key := buildKiroCooldownKey(&account)
	svc.kiroCooldownStore = &recordingKiroResilienceCooldownStore{states: map[string]*kirocooldown.State{
		key: {
			Active:        true,
			Reason:        kirocooldown.CooldownReasonUnresponsive,
			CooldownUntil: time.Now().Add(time.Minute),
			Remaining:     time.Minute,
		},
	}}

	ctx := svc.withKiroCooldownPrefetch(context.Background(), []Account{account}, &groupID)

	require.Nil(t, kiroCooldownStateFromContext(ctx, &account))
	require.NoError(t, kiroCooldownExhaustedErrorFromContext(ctx, map[int64]struct{}{account.ID: {}}))
	require.NoError(t, svc.checkAndWaitKiroCooldownWithMode(context.Background(), key, true))
}

func TestKiroPostSemanticFailureProhibitsReplay(t *testing.T) {
	original := &UpstreamFailoverError{
		StatusCode:  http.StatusBadGateway,
		FailureKind: UpstreamFailureIncompleteStream,
		Cause:       io.ErrUnexpectedEOF,
	}
	err := kiroPostSemanticFailure(original)

	require.Same(t, original, err)
	require.True(t, original.FailoverProhibited)
	require.Equal(t, http.StatusServiceUnavailable, original.StatusCode)
	require.Equal(t, defaultKiroTransportRetryAfter, original.RetryAfter)

	rawErr := kiroPostSemanticFailure(errors.New("translator stopped"))
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, rawErr, &failoverErr)
	require.True(t, failoverErr.FailoverProhibited)
	require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)

	canceled := context.Canceled
	require.Same(t, canceled, kiroPostSemanticFailure(canceled))
}

func TestKiroOpenAIConvertersEmitOneTerminalErrorAfterSemanticOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := enforcedKiroResilienceTestService()

	t.Run("chat completions", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		resp := &http.Response{Header: make(http.Header), Body: incompleteAnthropicSSEForKiroTest()}

		_, err := svc.handleCCStreamingFromAnthropic(resp, c, "gpt-5.6", "gpt-5.6", nil, time.Now(), false, true)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)
		require.True(t, HasGatewaySSEErrorWritten(c))
		require.Equal(t, 1, strings.Count(recorder.Body.String(), "Upstream stream ended before a complete response"))
		require.NotContains(t, recorder.Body.String(), "data: [DONE]")
	})

	t.Run("responses", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		resp := &http.Response{Header: make(http.Header), Body: incompleteAnthropicSSEForKiroTest()}

		_, err := svc.handleResponsesStreamingResponse(resp, c, "gpt-5.6", "gpt-5.6", nil, time.Now(), true)
		var failoverErr *UpstreamFailoverError
		require.ErrorAs(t, err, &failoverErr)
		require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)
		require.True(t, HasGatewaySSEErrorWritten(c))
		require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.failed"))
	})
}

func TestKiroOpenAIBufferedConverterOffModeKeepsLegacyTolerance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	resp := &http.Response{Header: make(http.Header), Body: incompleteAnthropicSSEForKiroTest()}

	result, err := (&GatewayService{}).handleCCBufferedFromAnthropic(resp, c, "gpt-5.6", "gpt-5.6", nil, time.Now(), false)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, HasGatewaySSEErrorWritten(c))
	require.Contains(t, recorder.Body.String(), "partial")
}

func TestDoKiroWithResponseHeaderTimeoutReturnsBeforeBlockedTransportExits(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/stream", nil)
	require.NoError(t, err)
	releaseTransport := make(chan struct{})

	started := time.Now()
	resp, elapsed, done, err := doKiroWithResponseHeaderTimeout(req, 20*time.Millisecond, func(*http.Request) (*http.Response, error) {
		<-releaseTransport
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("late"))}, nil
	})

	require.Nil(t, resp)
	require.ErrorIs(t, err, errKiroResponseHeaderTimeout)
	require.Less(t, elapsed, 200*time.Millisecond)
	require.Less(t, time.Since(started), 200*time.Millisecond)
	require.NotNil(t, done)
	select {
	case <-done:
		t.Fatal("physical transport exited before the test released it")
	default:
	}

	close(releaseTransport)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("physical transport did not report completion")
	}
}

func TestDoKiroWithResponseHeaderTimeoutClosesResponseReturnedWithError(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example.test/stream", nil)
	require.NoError(t, err)
	body := &kiroCloseTrackingBody{}

	resp, _, done, err := doKiroWithResponseHeaderTimeout(req, time.Second, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: body}, errors.New("transport returned response and error")
	})

	require.Error(t, err)
	require.Nil(t, resp)
	require.True(t, body.closed.Load())
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("transport completion was not reported")
	}
}

func TestKiroResponseHeaderTimeoutScalesForLargePayloads(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	groupID := int64(29)

	require.Equal(t, 30*time.Second, svc.kiroResponseHeaderTimeoutForInput(&groupID, 200_000))
	require.Equal(t, 60*time.Second, svc.kiroResponseHeaderTimeoutForInput(&groupID, 300_000))
	require.Equal(t, 85*time.Second, svc.kiroResponseHeaderTimeoutForInput(&groupID, 356_583))
	require.Equal(t, 85*time.Second, svc.kiroResponseHeaderTimeoutForInput(&groupID, 400_000))
	require.Equal(t, 95*time.Second, svc.kiroResponseHeaderTimeoutForInput(&groupID, 600_000))
	require.Equal(t, 95*time.Second, svc.kiroResponseHeaderTimeoutForInput(&groupID, 800_000))
	require.Equal(t, 95*time.Second, svc.kiroResponseHeaderTimeoutForInput(&groupID, 1_200_000))
}

func TestKiroLargePayloadTimeoutsPreserveBudgetHeadroom(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	groupID := int64(29)
	payloadInputTokens := 356_583

	headerTimeout := svc.kiroResponseHeaderTimeoutForInput(&groupID, payloadInputTokens)
	semanticTimeout := svc.kiroFirstSemanticTimeoutForInput(&groupID, payloadInputTokens)
	failoverBudget := time.Duration(svc.cfg.Gateway.KiroResilience.FailoverBudgetSeconds) * time.Second

	require.Equal(t, 85*time.Second, headerTimeout)
	require.Equal(t, 90*time.Second, semanticTimeout)
	require.Equal(t, 105*time.Second, failoverBudget)
	require.Less(t, headerTimeout, semanticTimeout)
	require.Less(t, semanticTimeout, failoverBudget)
}

func TestKiroFirstSemanticTimeoutScalesForLargeInputs(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	groupID := int64(13)

	require.Equal(t, 60*time.Second, svc.kiroFirstSemanticTimeoutForInput(&groupID, 200_000))
	require.Equal(t, 75*time.Second, svc.kiroFirstSemanticTimeoutForInput(&groupID, 300_000))
	require.Equal(t, 90*time.Second, svc.kiroFirstSemanticTimeoutForInput(&groupID, 400_000))
	require.Equal(t, 100*time.Second, svc.kiroFirstSemanticTimeoutForInput(&groupID, 600_000))
	require.Equal(t, 100*time.Second, svc.kiroFirstSemanticTimeoutForInput(&groupID, 1_000_000))
}

func TestKiroResponseHeaderTimeoutScalingDisabledOutsideEnforce(t *testing.T) {
	svc := observedKiroResilienceTestService()
	groupID := int64(29)
	require.Zero(t, svc.kiroResponseHeaderTimeoutForInput(&groupID, 800_000))
}

func TestEnsureKiro429CooldownIsIdempotentAcrossStreamGate(t *testing.T) {
	store := &recordingKiroResilienceCooldownStore{}
	svc := enforcedKiroResilienceTestService()
	svc.kiroCooldownStore = store
	groupID := int64(21)
	account := &Account{ID: 211, Platform: PlatformKiro, Credentials: map[string]any{"refresh_token": "single-cooldown-write"}}

	alreadyCommitted := &UpstreamFailoverError{
		StatusCode:            http.StatusTooManyRequests,
		KiroRateLimited:       true,
		KiroCooldownCommitted: true,
		RetryAfter:            15 * time.Second,
	}
	svc.ensureKiro429Cooldown(context.Background(), account, &groupID, alreadyCommitted)
	require.Zero(t, store.mark429Calls)
	require.Equal(t, 15*time.Second, alreadyCommitted.RetryAfter)

	uncommitted := &UpstreamFailoverError{StatusCode: http.StatusTooManyRequests, KiroRateLimited: true}
	svc.ensureKiro429Cooldown(context.Background(), account, &groupID, uncommitted)
	require.Equal(t, 1, store.mark429Calls)
	require.True(t, uncommitted.KiroCooldownCommitted)
	require.Equal(t, time.Minute, uncommitted.RetryAfter)
	svc.ensureKiro429Cooldown(context.Background(), account, &groupID, uncommitted)
	require.Equal(t, 1, store.mark429Calls)
}

func TestRunKiroFirstSemanticGateBuffersMetadataAndDropsInternalPing(t *testing.T) {
	input := strings.Join([]string{
		"event: sub2api_internal_kiro_ping\ndata: {}\n\n",
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
	reader, writer := io.Pipe()
	ready := make(chan error, 1)
	go runKiroFirstSemanticGate(context.Background(), io.NopCloser(strings.NewReader(input)), writer, 256*1024, 1024*1024, ready)

	require.NoError(t, <-ready)
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(output), "message_start")
	require.Contains(t, string(output), "hello")
	require.Contains(t, string(output), "message_stop")
	require.NotContains(t, string(output), "sub2api_internal_kiro_ping")
}

func TestRunKiroFirstSemanticGateAllowsLargeLinesAfterRelease(t *testing.T) {
	largeText := strings.Repeat("x", 300*1024)
	input := strings.Join([]string{
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ready\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"" + largeText + "\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}, "")
	reader, writer := io.Pipe()
	ready := make(chan error, 1)
	go runKiroFirstSemanticGate(context.Background(), io.NopCloser(strings.NewReader(input)), writer, 256*1024, 1024*1024, ready)

	require.NoError(t, <-ready)
	output, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(output), largeText)
}

func TestRunKiroFirstSemanticGateRejectsMessageStartThenEOF(t *testing.T) {
	input := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\n"
	reader, writer := io.Pipe()
	ready := make(chan error, 1)
	go runKiroFirstSemanticGate(context.Background(), io.NopCloser(strings.NewReader(input)), writer, 256*1024, 1024*1024, ready)

	gateErr := <-ready
	require.Error(t, gateErr)
	require.Contains(t, gateErr.Error(), "before semantic output")
	output, readErr := io.ReadAll(reader)
	require.Error(t, readErr)
	require.Empty(t, output, "message_start must remain hidden when no semantic event follows")
}

func TestRunKiroFirstSemanticGateKeepsMetadataHiddenUntilTimeout(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	sourceReader, sourceWriter := io.Pipe()
	clientReader, clientWriter := io.Pipe()
	ready := make(chan error, 1)
	go runKiroFirstSemanticGate(ctx, sourceReader, clientWriter, 256*1024, 1024*1024, ready)

	_, err := io.WriteString(sourceWriter, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\"}}\n\n")
	require.NoError(t, err)
	cancel(errKiroFirstSemanticTimeout)
	require.NoError(t, sourceWriter.CloseWithError(errKiroFirstSemanticTimeout))
	select {
	case gateErr := <-ready:
		require.ErrorIs(t, gateErr, errKiroFirstSemanticTimeout)
	case <-ctx.Done():
		require.ErrorIs(t, context.Cause(ctx), errKiroFirstSemanticTimeout)
	case <-time.After(time.Second):
		t.Fatal("semantic gate did not observe cancellation")
	}

	output, readErr := io.ReadAll(clientReader)
	require.ErrorIs(t, readErr, errKiroFirstSemanticTimeout)
	require.Empty(t, output, "metadata must not leak before failover chooses an account")
}

func TestRunKiroFirstSemanticGateAlwaysSignalsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(errKiroFirstSemanticTimeout)
	reader, writer := io.Pipe()
	ready := make(chan error, 1)
	go runKiroFirstSemanticGate(ctx, io.NopCloser(strings.NewReader("")), writer, 256*1024, 1024*1024, ready)

	select {
	case gateErr := <-ready:
		require.ErrorIs(t, gateErr, errKiroFirstSemanticTimeout)
	case <-time.After(time.Second):
		t.Fatal("semantic gate dropped its cancellation notification")
	}
	_, readErr := io.ReadAll(reader)
	require.ErrorIs(t, readErr, errKiroFirstSemanticTimeout)
}

func TestIsKiroClientSemanticEventRecognizesSupportedOutputKinds(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      string
		want      bool
	}{
		{name: "message start is metadata", eventType: "message_start", data: `{"type":"message_start","message":{"id":"msg"}}`},
		{name: "text", eventType: "content_block_delta", data: `{"type":"content_block_delta","delta":{"text":"hello"}}`, want: true},
		{name: "thinking", eventType: "content_block_delta", data: `{"type":"content_block_delta","delta":{"thinking":"reason"}}`, want: true},
		{name: "json delta", eventType: "content_block_delta", data: `{"type":"content_block_delta","delta":{"partial_json":"{\"x\":"}}`, want: true},
		{name: "tool use", eventType: "content_block_start", data: `{"type":"content_block_start","content_block":{"type":"tool_use","name":"search"}}`, want: true},
		{name: "empty valid terminal", eventType: "message_stop", data: `{"type":"message_stop"}`, want: true},
		{name: "stop reason", eventType: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isKiroClientSemanticEvent(tt.eventType, tt.data))
		})
	}
}

func TestKiroPreSemanticContextUsesSharedBudgetAndStopsAfterReady(t *testing.T) {
	svc := enforcedKiroResilienceTestService()

	t.Run("budget expires", func(t *testing.T) {
		budget := &kiroResilienceBudget{duration: 20 * time.Millisecond}
		ctx := context.WithValue(context.Background(), kiroResilienceBudgetContextKey{}, budget)
		attemptCtx, cancel, stop, err := svc.kiroPreSemanticContext(ctx, nil, 0)
		require.NoError(t, err)
		defer cancel(context.Canceled)
		defer stop()

		select {
		case <-attemptCtx.Done():
			require.ErrorIs(t, context.Cause(attemptCtx), errKiroFailoverBudgetExceeded)
		case <-time.After(time.Second):
			t.Fatal("shared Kiro budget did not cancel the pre-semantic attempt")
		}
	})

	t.Run("semantic ready stops failover timer", func(t *testing.T) {
		budget := &kiroResilienceBudget{duration: 40 * time.Millisecond}
		ctx := context.WithValue(context.Background(), kiroResilienceBudgetContextKey{}, budget)
		attemptCtx, cancel, stop, err := svc.kiroPreSemanticContext(ctx, nil, 0)
		require.NoError(t, err)
		stop()
		defer cancel(context.Canceled)

		select {
		case <-attemptCtx.Done():
			t.Fatalf("stopped failover timer canceled a healthy stream: %v", context.Cause(attemptCtx))
		case <-time.After(80 * time.Millisecond):
		}
	})
}

func TestKiroCleanupPendingErrorCarriesPhysicalCompletion(t *testing.T) {
	done := make(chan struct{})
	err := &kiroUpstreamCleanupPendingError{cause: context.Canceled, done: done}

	require.True(t, errors.Is(err, context.Canceled))
	require.Equal(t, (<-chan struct{})(done), UpstreamDoneFromError(err))
	close(done)
}

func TestWaitKiroUpstreamCleanupRecordsSlowCleanup(t *testing.T) {
	before := SnapshotKiroResilienceMetrics().SlowCleanupTotal
	done := make(chan struct{})
	require.False(t, waitKiroUpstreamCleanup(done, 10*time.Millisecond))
	require.Equal(t, before+1, SnapshotKiroResilienceMetrics().SlowCleanupTotal)
	close(done)
}

func TestKiroStreamCompletionFinalizesOnlyAfterDownstreamSuccess(t *testing.T) {
	store := &recordingKiroResilienceCooldownStore{}
	svc := enforcedKiroResilienceTestService()
	svc.kiroCooldownStore = store
	account := &Account{ID: 91, Platform: PlatformKiro, Credentials: map[string]any{"refresh_token": "completion-token"}}

	failedCompletion := &kiroStreamCompletion{service: svc, account: account}
	failedCompletion.markTerminal(nil)
	failedDone := make(chan struct{})
	close(failedDone)
	failedResp := &http.Response{Body: &kiroTrackedStreamBody{
		ReadCloser: io.NopCloser(strings.NewReader("")),
		done:       failedDone,
		completion: failedCompletion,
	}}
	err := svc.finishKiroStreamResponse(context.Background(), failedResp, nil, context.Canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, store.markSuccessCalls, "a canceled downstream must not finalize Kiro state")

	successCompletion := &kiroStreamCompletion{service: svc, account: account}
	successCompletion.markTerminal(nil)
	successDone := make(chan struct{})
	close(successDone)
	successResp := &http.Response{Body: &kiroTrackedStreamBody{
		ReadCloser: io.NopCloser(strings.NewReader("")),
		done:       successDone,
		completion: successCompletion,
	}}
	require.NoError(t, svc.finishKiroStreamResponse(context.Background(), successResp, nil, nil))
	require.Equal(t, 1, store.markSuccessCalls)
	require.NoError(t, successCompletion.complete(context.Background()))
	require.Equal(t, 1, store.markSuccessCalls, "terminal finalization must be idempotent")
}

func TestFinishKiroStreamResponseWaitsForPhysicalCleanupAfterCancel(t *testing.T) {
	store := &recordingKiroResilienceCooldownStore{}
	svc := enforcedKiroResilienceTestService()
	svc.kiroCooldownStore = store
	done := make(chan struct{})
	canceled := make(chan struct{})
	completion := &kiroStreamCompletion{service: svc, account: &Account{ID: 92, Platform: PlatformKiro}}
	resp := &http.Response{Body: &kiroTrackedStreamBody{
		ReadCloser: io.NopCloser(strings.NewReader("")),
		cancel:     func() { close(canceled) },
		done:       done,
		completion: completion,
	}}
	go func() {
		<-canceled
		time.Sleep(25 * time.Millisecond)
		close(done)
	}()

	started := time.Now()
	err := svc.finishKiroStreamResponse(context.Background(), resp, nil, context.Canceled)
	require.ErrorIs(t, err, context.Canceled)
	require.GreaterOrEqual(t, time.Since(started), 20*time.Millisecond)
	require.Zero(t, store.markSuccessCalls)
}

func TestJoinKiroGatedStreamCleanupWaitsForGateDrain(t *testing.T) {
	producerDone := make(chan struct{})
	gateDone := make(chan struct{})
	released := make(chan struct{})
	close(producerDone)

	pipelineDone := joinKiroGatedStreamCleanup(producerDone, gateDone, func() { close(released) })
	select {
	case <-released:
		t.Fatal("stream context released before semantic gate drained producer EOF")
	case <-time.After(20 * time.Millisecond):
	}

	close(gateDone)
	select {
	case <-pipelineDone:
	case <-time.After(time.Second):
		t.Fatal("gated stream cleanup did not complete")
	}
	select {
	case <-released:
	default:
		t.Fatal("stream context was not released after the gate drained")
	}
}

func TestExecuteKiroUpstreamEnforceTriesEachEndpointOnceOn429(t *testing.T) {
	store := &recordingKiroResilienceCooldownStore{}
	responses := make([]kiroScriptedUpstreamOutcome, 0, 3)
	for _, retryAfter := range []string{"15", "5", ""} {
		resp := newKiroJSONTestResponse(http.StatusTooManyRequests, `{"message":"slow down"}`)
		if retryAfter != "" {
			resp.Header.Set("Retry-After", retryAfter)
		}
		responses = append(responses, kiroScriptedUpstreamOutcome{resp: resp})
	}
	upstream := &kiroScriptedUpstream{outcomes: responses}
	svc := enforcedKiroResilienceTestService()
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = store
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 93, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_region": "us-east-1", "refresh_token": "rate-limited-token"},
	}
	groupID := int64(7)
	parsed := &ParsedRequest{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}}
	payload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)

	resp, _, err := svc.executeKiroUpstreamWithParsed(ctx, account, parsed, body, "claude-sonnet-4-6", "claude-sonnet-4-6", "token", nil)
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, UpstreamFailureRateLimited, failoverErr.FailureKind)
	require.Len(t, upstream.requests, 3, "every logical endpoint must be attempted once")
	require.Equal(t, 1, store.mark429Calls, "the account cooldown is written only after its endpoint round is exhausted")
	require.Equal(t, 15*time.Second, store.lastRetryAfter)
}

func TestExecuteKiroUpstreamEnforceDoesNotReplayAfterRequestWrite(t *testing.T) {
	upstream := &writeObservedFailureKiroUpstream{}
	svc := enforcedKiroResilienceTestService()
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = &recordingKiroResilienceCooldownStore{}
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 94, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_region": "us-east-1", "refresh_token": "write-observed-token"},
	}
	groupID := int64(8)
	parsed := &ParsedRequest{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}}
	payload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)

	resp, _, err := svc.executeKiroUpstreamWithParsed(ctx, account, parsed, body, "claude-sonnet-4-6", "claude-sonnet-4-6", "token", nil)
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Equal(t, UpstreamFailureTransportError, failoverErr.FailureKind)
	require.True(t, failoverErr.FailoverProhibited, "a possibly delivered request must not be replayed on another account")
	require.Equal(t, 1, upstream.requests, "a possibly delivered request must never be replayed on another endpoint")
}

func TestExecuteKiroUpstreamEnforceAllowsOneUnwrittenRetryAcrossEndpointRound(t *testing.T) {
	upstream := &kiroScriptedUpstream{outcomes: []kiroScriptedUpstreamOutcome{
		{err: io.ErrUnexpectedEOF},
		{err: io.ErrUnexpectedEOF},
		{err: io.ErrUnexpectedEOF},
		{err: io.ErrUnexpectedEOF},
	}}
	svc := enforcedKiroResilienceTestService()
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = &recordingKiroResilienceCooldownStore{}
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 943, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_region": "us-east-1", "refresh_token": "one-connection-retry-token"},
	}
	groupID := int64(83)
	parsed := &ParsedRequest{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}}
	payload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)

	resp, _, err := svc.executeKiroUpstreamWithParsed(ctx, account, parsed, body, "claude-sonnet-4-6", "claude-sonnet-4-6", "token", nil)
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, UpstreamFailureTransportError, failoverErr.FailureKind)
	require.Len(t, upstream.requests, 4, "three endpoints plus one account-wide unwritten connection retry")
}

func TestExecuteKiroUpstreamHeaderTimeoutAfterWriteDoesNotPauseAccount(t *testing.T) {
	upstream := &canceledKiroHeaderUpstream{}
	store := &recordingKiroResilienceCooldownStore{}
	svc := enforcedKiroResilienceTestService()
	svc.cfg.Gateway.KiroResilience.ResponseHeaderTimeoutSeconds = 1
	svc.cfg.Gateway.KiroResilience.CleanupGraceSeconds = 1
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = store
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 944, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_region": "us-east-1", "refresh_token": "header-timeout-after-write"},
	}
	groupID := int64(84)
	parsed := &ParsedRequest{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}}
	payload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)

	resp, _, err := svc.executeKiroUpstreamWithParsed(ctx, account, parsed, body, "claude-sonnet-4-6", "claude-sonnet-4-6", "token", nil)
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, UpstreamFailureResponseHeaderTimeout, failoverErr.FailureKind)
	require.True(t, failoverErr.FailoverProhibited, "a written request must never be replayed")
	require.Equal(t, int32(1), upstream.calls.Load())
	require.Zero(t, store.observeUnresponsiveCalls, "timeouts must not write account-level observations")
	require.Zero(t, store.markUnresponsiveCalls, "timeouts must not open the credential circuit")
	require.Nil(t, account.RateLimitResetAt)
}

func TestExecuteKiroUpstreamEnforceMixed429AndTransportReturns503(t *testing.T) {
	store := &recordingKiroResilienceCooldownStore{}
	upstream := &kiroScriptedUpstream{outcomes: []kiroScriptedUpstreamOutcome{
		{resp: newKiroJSONTestResponse(http.StatusTooManyRequests, `{"message":"slow down"}`)},
		{err: io.ErrUnexpectedEOF},
		{err: io.ErrUnexpectedEOF},
	}}
	svc := enforcedKiroResilienceTestService()
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = store
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 941, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_region": "eu-central-1", "refresh_token": "mixed-failure-token"},
	}
	groupID := int64(81)
	parsed := &ParsedRequest{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}}
	payload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)

	resp, _, err := svc.executeKiroUpstreamWithParsed(ctx, account, parsed, body, "claude-sonnet-4-6", "claude-sonnet-4-6", "token", nil)
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Equal(t, UpstreamFailureTransportError, failoverErr.FailureKind)
	require.False(t, failoverErr.KiroRateLimited)
	require.Zero(t, store.mark429Calls, "a partial Endpoint 429 must not create an all-Endpoint rate-limit cooldown")
	require.Len(t, upstream.requests, 3)
}

func TestExecuteKiroUpstreamEnforceMixed5xxAnd429Returns503(t *testing.T) {
	store := &recordingKiroResilienceCooldownStore{}
	upstream := &kiroScriptedUpstream{outcomes: []kiroScriptedUpstreamOutcome{
		{resp: newKiroJSONTestResponse(http.StatusBadGateway, `{"message":"temporary failure"}`)},
		{resp: newKiroJSONTestResponse(http.StatusTooManyRequests, `{"message":"slow down"}`)},
	}}
	svc := enforcedKiroResilienceTestService()
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = store
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 942, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_region": "eu-central-1", "refresh_token": "mixed-http-failure-token"},
	}
	groupID := int64(82)
	parsed := &ParsedRequest{GroupID: &groupID, Group: &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}}
	payload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)

	resp, _, err := svc.executeKiroUpstreamWithParsed(ctx, account, parsed, body, "claude-sonnet-4-6", "claude-sonnet-4-6", "token", nil)
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Equal(t, UpstreamFailureTransportError, failoverErr.FailureKind)
	require.False(t, failoverErr.KiroRateLimited)
	require.Zero(t, store.mark429Calls)
	require.Len(t, upstream.requests, 2)
}

func TestDoKiroMCPJSONRequestEnforceReturns429WithoutSameAccountRetry(t *testing.T) {
	upstreamResp := newKiroJSONTestResponse(http.StatusTooManyRequests, `{"message":"mcp slow down"}`)
	upstreamResp.Header.Set("Retry-After", "9")
	upstream := &kiroScriptedUpstream{outcomes: []kiroScriptedUpstreamOutcome{{resp: upstreamResp}}}
	store := &recordingKiroResilienceCooldownStore{}
	svc := enforcedKiroResilienceTestService()
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = store
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 95, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"refresh_token": "mcp-rate-limited"},
	}
	groupID := int64(9)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)

	resp, _, err := svc.doKiroMCPJSONRequest(ctx, account, "https://example.test/mcp", []byte(`{"jsonrpc":"2.0"}`), "token", &groupID)
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.KiroRateLimited)
	require.Equal(t, UpstreamFailureRateLimited, failoverErr.FailureKind)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, 1, store.mark429Calls)
	require.Equal(t, 9*time.Second, failoverErr.RetryAfter)
}

func TestDoKiroMCPJSONRequestDoesNotRetryWhileTimedOutTransportIsStillRunning(t *testing.T) {
	upstream := &blockedKiroMCPHeaderUpstream{release: make(chan struct{}), exited: make(chan struct{})}
	svc := enforcedKiroResilienceTestService()
	svc.cfg.Gateway.KiroResilience.ResponseHeaderTimeoutSeconds = 1
	svc.cfg.Gateway.KiroResilience.CleanupGraceSeconds = 1
	svc.httpUpstream = upstream
	store := &recordingKiroResilienceCooldownStore{}
	svc.kiroCooldownStore = store
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 951, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"refresh_token": "mcp-header-timeout"},
	}
	groupID := int64(91)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)

	resp, _, err := svc.doKiroMCPJSONRequest(ctx, account, "https://example.test/mcp", []byte(`{"jsonrpc":"2.0"}`), "token", &groupID)
	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, UpstreamFailureResponseHeaderTimeout, failoverErr.FailureKind)
	require.True(t, failoverErr.FailoverProhibited, "a still-running request must not be replayed on another account")
	require.NotNil(t, failoverErr.UpstreamDone)
	require.Equal(t, int32(1), upstream.calls.Load(), "a still-running transport must block the one allowed connection retry")
	require.Zero(t, store.observeUnresponsiveCalls, "timeouts must not write account-level observations")
	require.Zero(t, store.markUnresponsiveCalls, "timeouts must not open the credential circuit")
	require.Nil(t, account.RateLimitResetAt)

	close(upstream.release)
	select {
	case <-upstream.exited:
	case <-time.After(time.Second):
		t.Fatal("blocked MCP transport did not exit after release")
	}
}

func TestNewKiroTimeoutFailoverProhibitsReplay(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	store := &recordingKiroResilienceCooldownStore{}
	svc.kiroCooldownStore = store
	groupID := int64(92)
	account := &Account{
		ID: 952, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "first-semantic-timeout"},
	}

	failoverErr := svc.newKiroTimeoutFailover(context.Background(), account, &groupID, errKiroFirstSemanticTimeout, "claude-sonnet-4-6", true)

	require.NotNil(t, failoverErr)
	require.Equal(t, UpstreamFailureFirstSemanticTimeout, failoverErr.FailureKind)
	require.True(t, failoverErr.FailoverProhibited, "a request awaiting semantic output has already reached upstream")
	require.Equal(t, defaultKiroTransportRetryAfter, failoverErr.RetryAfter)
	require.Zero(t, store.observeUnresponsiveCalls)
	require.Zero(t, store.markUnresponsiveCalls)
	require.Nil(t, account.RateLimitResetAt, "timeouts must not block future account routing")
}

func TestNewKiroTimeoutFailoverBeforeUpstreamDoesNotPenalizeCredential(t *testing.T) {
	tests := []struct {
		name               string
		cause              error
		failoverProhibited bool
	}{
		{name: "account attempt timeout can switch", cause: errKiroFirstSemanticTimeout},
		{name: "exhausted total budget stops switching", cause: errKiroFailoverBudgetExceeded, failoverProhibited: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := enforcedKiroResilienceTestService()
			store := &recordingKiroResilienceCooldownStore{}
			svc.kiroCooldownStore = store
			groupID := int64(92)
			account := &Account{
				ID: 956, Platform: PlatformKiro, Type: AccountTypeOAuth,
				Credentials: map[string]any{"refresh_token": "timeout-before-upstream"},
			}

			failoverErr := svc.newKiroTimeoutFailover(context.Background(), account, &groupID, tt.cause, "claude-sonnet-4-6", false)

			require.NotNil(t, failoverErr)
			require.Equal(t, tt.failoverProhibited, failoverErr.FailoverProhibited)
			require.Equal(t, defaultKiroTransportRetryAfter, failoverErr.RetryAfter)
			require.Zero(t, store.observeUnresponsiveCalls)
			require.Zero(t, store.markUnresponsiveCalls)
			require.Nil(t, account.RateLimitResetAt)
		})
	}
}

func TestOpenKiroAnthropicStreamResponseExpiredBudgetDoesNotPenalizeSelectedAccount(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	store := &recordingKiroResilienceCooldownStore{}
	svc.kiroCooldownStore = store
	account := &Account{
		ID: 957, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "unused", "refresh_token": "expired-before-upstream"},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	groupID := int64(93)
	parsed.GroupID = &groupID
	parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}
	expiredBudget := &kiroResilienceBudget{duration: -time.Second}
	ctx := context.WithValue(context.Background(), kiroResilienceBudgetContextKey{}, expiredBudget)

	resp, _, err := svc.openKiroAnthropicStreamResponse(ctx, account, parsed, body, parsed.Model, parsed.Model, http.Header{}, parsed.Group)

	require.Nil(t, resp)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.ErrorIs(t, err, errKiroFailoverBudgetExceeded)
	require.True(t, failoverErr.FailoverProhibited, "an exhausted request budget must stop further account switches")
	require.Equal(t, defaultKiroTransportRetryAfter, failoverErr.RetryAfter)
	require.Zero(t, store.observeUnresponsiveCalls)
	require.Zero(t, store.markUnresponsiveCalls)
	require.Nil(t, account.RateLimitResetAt)
}

func TestObserveKiroAccountUnresponsiveNeverPausesAccount(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	store := &recordingKiroResilienceCooldownStore{observationResult: &kirocooldown.UnresponsiveObservationResult{
		Cooldown:     30 * time.Second,
		FailureCount: 2,
		Escalated:    true,
	}}
	svc.kiroCooldownStore = store
	groupID := int64(92)
	account := &Account{
		ID: 954, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "repeated-timeout"},
	}

	retryAfter := svc.observeKiroAccountUnresponsive(
		context.Background(),
		account,
		&groupID,
		UpstreamFailureResponseHeaderTimeout,
		"response_header|endpoint:q|proxy:1",
	)

	require.Equal(t, defaultKiroTransportRetryAfter, retryAfter)
	require.Zero(t, store.observeUnresponsiveCalls)
	require.Zero(t, store.markUnresponsiveCalls)
	require.Nil(t, account.RateLimitResetAt)
}

func TestConfirmedKiroUnresponsiveFailureNeverPausesAccount(t *testing.T) {
	svc := enforcedKiroResilienceTestService()
	store := &recordingKiroResilienceCooldownStore{}
	svc.kiroCooldownStore = store
	groupID := int64(92)
	account := &Account{
		ID: 955, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "confirmed-timeout"},
	}

	retryAfter := svc.markKiroAccountUnresponsive(context.Background(), account, &groupID, UpstreamFailureResponseHeaderTimeout)

	require.Equal(t, defaultKiroTransportRetryAfter, retryAfter)
	require.Zero(t, store.markUnresponsiveCalls)
	require.Zero(t, store.observeUnresponsiveCalls)
	require.Nil(t, account.RateLimitResetAt)
}

func TestRecordKiroHeaderTimeoutOpsUsesGatewayNetworkSemantics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	headerTimeout := &kiroResponseHeaderTimeoutError{Timeout: 30 * time.Second, Endpoint: "q.us-east-1.amazonaws.com"}
	transportErr := &kiroEndpointTransportError{
		EndpointName: "KiroIDE",
		EndpointURL:  "https://q.us-east-1.amazonaws.com/generateAssistantResponse",
		NetworkType:  "transport",
		Cause:        headerTimeout,
	}
	failoverErr := newKiroEndpointTransportFailover(transportErr)

	recordKiroFailoverOps(c, &Account{ID: 953, Name: "header-timeout", Platform: PlatformKiro}, failoverErr)

	require.Equal(t, "response_header_timeout", GetOpsNetworkErrorType(c))
	_, hasUpstreamStatus := c.Get(OpsUpstreamStatusCodeKey)
	require.False(t, hasUpstreamStatus)
	rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Zero(t, events[0].UpstreamStatusCode)
	require.Equal(t, "network_failover", events[0].Kind)
	require.Equal(t, "network_error_type=response_header_timeout", events[0].Detail)
}

func TestHandleKiroHTTPErrorEnforceNormalizesFinal5xxTo503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	svc := enforcedKiroResilienceTestService()
	groupID := int64(10)
	ctx, err := svc.StartKiroResilienceBudget(context.Background(), &groupID)
	require.NoError(t, err)
	resp := newKiroJSONTestResponse(http.StatusBadGateway, `{"message":"temporary upstream failure"}`)
	account := &Account{ID: 96, Platform: PlatformKiro, Type: AccountTypeOAuth}

	err = svc.handleKiroHTTPError(ctx, resp, c, account, "claude-sonnet-4-6", nil)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusServiceUnavailable, failoverErr.StatusCode)
	require.Equal(t, UpstreamFailureTransportError, failoverErr.FailureKind)
	require.Equal(t, defaultKiroTransportRetryAfter, failoverErr.RetryAfter)
}

func TestOpenKiroAnthropicStreamResponseEnforceCancelsUpstreamWithClient(t *testing.T) {
	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	streamReader, streamWriter := io.Pipe()
	upstream := &kiroStreamFailoverQueuedUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       streamReader,
	}}}
	svc := enforcedKiroResilienceTestService()
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = &recordingKiroResilienceCooldownStore{}
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 97, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "api_region": "us-east-1"},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	groupID := int64(11)
	parsed.GroupID = &groupID
	parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}

	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = streamWriter.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
			":event-type": "assistantResponseEvent",
		}, []byte(`{"assistantResponseEvent":{"content":"first semantic output"}}`)))
	}()

	resp, _, err := svc.openKiroAnthropicStreamResponse(parentCtx, account, parsed, body, parsed.Model, parsed.Model, http.Header{}, nil)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Len(t, upstream.requests, 1)
	<-writeDone

	cancelParent()
	select {
	case <-upstream.requests[0].Context().Done():
	case <-time.After(time.Second):
		t.Fatal("Kiro upstream request remained alive after client cancellation")
	}
	_ = resp.Body.Close()
	_ = streamWriter.Close()
}

func TestOpenKiroAnthropicStreamResponseEnforceDrainsTerminalBeforeInternalCancel(t *testing.T) {
	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "assistantResponseEvent",
	}, []byte(`{"assistantResponseEvent":{"content":"complete"}}`)))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":event-type": "messageStopEvent",
	}, []byte(`{"messageStopEvent":{"stop_reason":"end_turn"}}`)))
	upstream := &kiroStreamFailoverQueuedUpstream{
		responses: []*http.Response{newKiroEventStreamResponse(http.StatusOK, upstreamBody.Bytes())},
	}
	svc := enforcedKiroResilienceTestService()
	svc.httpUpstream = upstream
	svc.kiroCooldownStore = &recordingKiroResilienceCooldownStore{}
	svc.tlsFPProfileService = &TLSFingerprintProfileService{}
	account := &Account{
		ID: 98, Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "api_region": "us-east-1"},
	}
	groupID := int64(29)
	group := &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}
	body := []byte(`{"model":"claude-opus-4-8","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	parsed.GroupID = &groupID
	parsed.Group = group

	resp, _, err := svc.openKiroAnthropicStreamResponse(context.Background(), account, parsed, body, parsed.Model, parsed.Model, http.Header{}, group)
	require.NoError(t, err)

	streamBytes, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.Contains(t, string(streamBytes), "complete")
	require.Contains(t, string(streamBytes), "event: message_stop")
	require.NoError(t, svc.finishKiroStreamResponse(context.Background(), resp, &groupID, nil))
}
