package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	nianzscooldown "github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown_nianzs"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type trackingNianzsCooldownStore struct {
	states    map[string]*nianzscooldown.State
	stateKeys []string
}

type nianzsErrorAfterReader struct {
	reader *bytes.Reader
	err    error
}

func (r *nianzsErrorAfterReader) Read(p []byte) (int, error) {
	if r.reader != nil && r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	return 0, r.err
}

func (s *trackingNianzsCooldownStore) CheckCooldown(_ context.Context, tokenKey string) error {
	if state := s.states[tokenKey]; state != nil && state.Active {
		return nianzscooldown.NewError(state.Remaining, state.Reason)
	}
	return nil
}

func (s *trackingNianzsCooldownStore) MarkSuccess(context.Context, string) error { return nil }

func (s *trackingNianzsCooldownStore) Mark429(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *trackingNianzsCooldownStore) MarkSuspended(context.Context, string) (time.Duration, error) {
	return 0, nil
}

func (s *trackingNianzsCooldownStore) GetState(_ context.Context, tokenKey string) (*nianzscooldown.State, error) {
	s.stateKeys = append(s.stateKeys, tokenKey)
	return s.states[tokenKey], nil
}

func (s *trackingNianzsCooldownStore) ClearEarliestTransientCooldown(context.Context, []string) (bool, error) {
	return false, nil
}

func TestKiroOAuthEngineServiceRoutesAuthorizationToNianzs(t *testing.T) {
	legacy := NewKiroOAuthService(nil)
	nianzs := NianzsNewKiroOAuthService(nil)
	selector := NewKiroOAuthEngineService(legacy, nianzs, &config.Config{
		Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs},
	})

	result, err := selector.GenerateAuthURL(context.Background(), &KiroGenerateAuthURLInput{})
	require.NoError(t, err)
	require.NotEmpty(t, result.SessionID)

	_, inNianzs := nianzs.sessionStore.Get(result.SessionID)
	_, inLegacy := legacy.sessionStore.Get(result.SessionID)
	require.True(t, inNianzs)
	require.False(t, inLegacy)
	require.Equal(t, KiroEngineNianzs, selector.Engine())
}

func TestKiroOAuthEngineServiceRoutesAuthorizationToLegacyForRollback(t *testing.T) {
	legacy := NewKiroOAuthService(nil)
	nianzs := NianzsNewKiroOAuthService(nil)
	selector := NewKiroOAuthEngineService(legacy, nianzs, &config.Config{
		Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineLegacy},
	})

	result, err := selector.GenerateAuthURL(context.Background(), &KiroGenerateAuthURLInput{})
	require.NoError(t, err)
	require.NotEmpty(t, result.SessionID)

	_, inNianzs := nianzs.sessionStore.Get(result.SessionID)
	_, inLegacy := legacy.sessionStore.Get(result.SessionID)
	require.False(t, inNianzs)
	require.True(t, inLegacy)
	require.Equal(t, KiroEngineLegacy, selector.Engine())
}

func TestKiroOAuthEngineServiceUsesNianzsModelCatalog(t *testing.T) {
	selector := NewKiroOAuthEngineService(
		NewKiroOAuthService(nil),
		NianzsNewKiroOAuthService(nil),
		&config.Config{Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs}},
	)

	models := selector.DefaultModels()
	foundSonnetThinking := false
	for _, model := range models {
		if model.ID == "claude-sonnet-5-thinking" {
			foundSonnetThinking = true
			break
		}
	}
	require.True(t, foundSonnetThinking)
}

func TestDualKiroCooldownStoreKeepsRollbackStateIndependent(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := newDualKiroCooldownStore(client)
	ctx := context.Background()

	_, err := store.nianzs.Mark429(ctx, "nianzs-token")
	require.NoError(t, err)
	require.Error(t, store.nianzs.CheckCooldown(ctx, "nianzs-token"))
	legacyState, err := store.legacy.GetState(ctx, "nianzs-token")
	require.NoError(t, err)
	require.Nil(t, legacyState)

	_, err = store.legacy.Mark429(ctx, "legacy-token")
	require.NoError(t, err)
	legacyState, err = store.legacy.GetState(ctx, "legacy-token")
	require.NoError(t, err)
	require.NotNil(t, legacyState)
	require.NoError(t, store.nianzs.CheckCooldown(ctx, "legacy-token"))
}

func TestRecordKiroEnginePropagatesToOpsEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	svc := &GatewayService{cfg: &config.Config{
		Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs},
	}}
	groupID := int64(29)

	require.Equal(t, KiroEngineNianzs, svc.recordKiroEngine(c, &groupID, &Account{ID: 42}))
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Kind: "http_error"})

	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := raw.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, string(KiroEngineNianzs), events[0].KiroEngine)
}

func TestSelectAccountForModelUsesNianzsSchedulerAndCooldown(t *testing.T) {
	groupID := int64(29)
	group := &Group{ID: groupID, Platform: PlatformKiro, Status: StatusActive, Hydrated: true}
	first := Account{
		ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Priority: 0,
		Credentials: map[string]any{"access_token": "first-token"},
	}
	second := Account{
		ID: 2, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Priority: 1,
		Credentials: map[string]any{"access_token": "second-token"},
	}
	store := &trackingNianzsCooldownStore{states: map[string]*nianzscooldown.State{
		nianzsBuildKiroAccountKey(&first): {
			Active: true, Reason: nianzscooldown.CooldownReason429, Remaining: time.Minute,
		},
	}}
	svc := &GatewayService{
		accountRepo:             stubOpenAIAccountRepo{accounts: []Account{first, second}},
		nianzsKiroCooldownStore: store,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineNianzs,
		}},
	}
	ctx := svc.withGroupContext(context.Background(), group)

	selected, err := svc.SelectAccountForModel(ctx, &groupID, "", "")
	require.NoError(t, err)
	require.Equal(t, second.ID, selected.ID)
	require.Contains(t, store.stateKeys, nianzsBuildKiroAccountKey(&first))
	// This exact path is used by /v1/messages/count_tokens, so count-token
	// probes cannot bypass the selected engine's scheduler state.
}

func TestSelectAccountForModelRollbackDoesNotReadNianzsCooldown(t *testing.T) {
	groupID := int64(29)
	group := &Group{ID: groupID, Platform: PlatformKiro, Status: StatusActive, Hydrated: true}
	account := Account{
		ID: 1, Platform: PlatformKiro, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"access_token": "legacy-token"},
	}
	store := &trackingNianzsCooldownStore{states: map[string]*nianzscooldown.State{}}
	svc := &GatewayService{
		accountRepo:             stubOpenAIAccountRepo{accounts: []Account{account}},
		nianzsKiroCooldownStore: store,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineLegacy,
		}},
	}
	ctx := svc.withGroupContext(context.Background(), group)

	selected, err := svc.SelectAccountForModel(ctx, &groupID, "", "")
	require.NoError(t, err)
	require.Equal(t, account.ID, selected.ID)
	require.Empty(t, store.stateKeys)
}

func TestNianzsOpenAIBridgePrefetchIgnoresLegacyCooldown(t *testing.T) {
	groupID := int64(31)
	account := bridgeTestAccount(701, PlatformKiro, 1, groupID)
	account.Credentials["refresh_token"] = "dual-engine-prefetch"
	legacyStore := &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{
		buildKiroCooldownKey(&account): {
			Active: true, Reason: kirocooldown.CooldownReason429, Remaining: time.Minute,
		},
	}}
	nianzsStore := &trackingNianzsCooldownStore{states: map[string]*nianzscooldown.State{}}
	svc := &GatewayService{
		kiroCooldownStore:       legacyStore,
		nianzsKiroCooldownStore: nianzsStore,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineNianzs,
		}},
	}

	ctx := svc.withKiroCooldownPrefetch(context.Background(), []Account{account}, &groupID)

	require.Nil(t, kiroCooldownStateFromContext(ctx, &account))
	require.Equal(t, []string{nianzsBuildKiroAccountKey(&account)}, nianzsStore.stateKeys)
}

func TestNianzsOpenAIBridgePrefetchUsesNianzsCooldown(t *testing.T) {
	groupID := int64(32)
	account := bridgeTestAccount(702, PlatformKiro, 1, groupID)
	state := &nianzscooldown.State{
		Active: true, Reason: nianzscooldown.CooldownReason429,
		CooldownUntil: time.Now().Add(time.Minute), Remaining: time.Minute, FailCount: 2,
	}
	nianzsStore := &trackingNianzsCooldownStore{states: map[string]*nianzscooldown.State{
		nianzsBuildKiroAccountKey(&account): state,
	}}
	svc := &GatewayService{
		kiroCooldownStore:       &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{}},
		nianzsKiroCooldownStore: nianzsStore,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineNianzs,
		}},
	}

	ctx := svc.withKiroCooldownPrefetch(context.Background(), []Account{account}, &groupID)
	observed := kiroCooldownStateFromContext(ctx, &account)

	require.NotNil(t, observed)
	require.Equal(t, state.Reason, observed.Reason)
	require.Equal(t, state.FailCount, observed.FailCount)
}

func TestLegacyOpenAIBridgePrefetchIgnoresNianzsCooldown(t *testing.T) {
	groupID := int64(33)
	account := bridgeTestAccount(703, PlatformKiro, 1, groupID)
	nianzsStore := &trackingNianzsCooldownStore{states: map[string]*nianzscooldown.State{
		nianzsBuildKiroAccountKey(&account): {
			Active: true, Reason: nianzscooldown.CooldownReason429, Remaining: time.Minute,
		},
	}}
	svc := &GatewayService{
		kiroCooldownStore:       &mappedKiroCooldownStore{states: map[string]*kirocooldown.State{}},
		nianzsKiroCooldownStore: nianzsStore,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineLegacy,
			KiroResilience: config.GatewayKiroResilienceConfig{
				Mode: config.KiroResilienceModeEnforce,
			},
		}},
	}

	ctx := svc.withKiroCooldownPrefetch(context.Background(), []Account{account}, &groupID)

	require.Nil(t, kiroCooldownStateFromContext(ctx, &account))
	require.Empty(t, nianzsStore.stateKeys)
}

func TestNianzsOpenAIBridgeSchedulerRecoversEarliestTransientCooldown(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(34)
	account := bridgeTestAccount(704, PlatformKiro, 1, groupID)
	account.Credentials["refresh_token"] = "nianzs-openai-bridge-recovery"
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{account}}}
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	dualStore := newDualKiroCooldownStore(redisClient)
	_, err := dualStore.nianzs.Mark429(context.Background(), nianzsBuildKiroAccountKey(&account))
	require.NoError(t, err)

	bridge := &GatewayService{
		accountRepo:             repo,
		kiroCooldownStore:       dualStore,
		nianzsKiroCooldownStore: dualStore.nianzs,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineNianzs,
		}},
	}
	svc := &OpenAIGatewayService{
		accountRepo: repo,
		cfg: &config.Config{Gateway: config.GatewayConfig{
			OpenAIKiroBridgeEnabled: true,
			OpenAIWS: config.GatewayOpenAIWSConfig{
				LBTopK:                1,
				SchedulerScoreWeights: config.GatewayOpenAIWSSchedulerScoreWeights{Priority: 1},
			},
		}},
	}
	svc.SetKiroBridgeService(bridge)

	selection, _, err := svc.SelectAccountWithSchedulerForKiroBridge(
		context.Background(), &groupID, "", OpenAIKiroBridgeModel, OpenAIKiroBridgeModel, nil,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, account.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	state, err := dualStore.nianzs.GetState(context.Background(), nianzsBuildKiroAccountKey(&account))
	require.NoError(t, err)
	require.Nil(t, state)
}

func newNianzsKiroRouteTestRuntime(t *testing.T, response *http.Response) (*GatewayService, *httpUpstreamRecorder, *Account) {
	t.Helper()
	upstream := &httpUpstreamRecorder{resp: response}
	svc := &GatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineNianzs,
		}},
		httpUpstream:            upstream,
		nianzsKiroCooldownStore: &trackingNianzsCooldownStore{states: map[string]*nianzscooldown.State{}},
		tlsFPProfileService:     &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          1801,
		Name:        "nianzs-route-test",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "nianzs-oauth-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO",
		},
	}
	return svc, upstream, account
}

func TestNianzsMessagesRouteStreamingAndNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"messages":[{"role":"user","content":"hello messages"}],"stream":` + map[bool]string{false: "false", true: "true"}[stream] + `}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, kiroEventStreamResponse(t, "nianzs messages ok", 9, 4))

			result, err := svc.Forward(context.Background(), c, account, parsed)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
			require.Equal(t, "nianzs", mustGinString(t, c, OpsKiroEngineKey))
			if stream {
				require.Contains(t, recorder.Body.String(), `"text":"nianzs messages ok"`)
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
			} else {
				require.Equal(t, "nianzs messages ok", gjson.Get(recorder.Body.String(), "content.0.text").String())
				require.Equal(t, "end_turn", gjson.Get(recorder.Body.String(), "stop_reason").String())
			}
		})
	}
}

func TestNianzsMessagesRoutePreservesAccountCachePolicyInMixedAnthropicGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			resetNianzsKiroCacheTracker()
			streamValue := "false"
			if stream {
				streamValue = "true"
			}
			body := []byte(strings.Replace(
				string(nianzsTestKiroCacheRequestBody("mixed-route", false)),
				`{"model"`, `{"stream":`+streamValue+`,"model"`, 1,
			))
			groupID := int64(9)
			group := &Group{ID: groupID, Platform: PlatformAnthropic, Status: StatusActive}
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
			account.Extra = map[string]any{
				"kiro_cache_emulation_enabled": true,
				"kiro_cache_emulation_ratio":   0.91,
			}
			upstream.responses = []*http.Response{
				kiroEventStreamResponse(t, "mixed cache create", 2000, 4),
				kiroEventStreamResponse(t, "mixed cache read", 2000, 4),
			}

			forward := func() *ForwardResult {
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
				require.NoError(t, err)
				parsed.GroupID = &groupID
				parsed.Group = group
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
				result, err := svc.Forward(context.Background(), c, account, parsed)
				require.NoError(t, err)
				require.NotNil(t, result)
				return result
			}

			first := forward()
			require.Greater(t, first.Usage.CacheCreationInputTokens, 0)
			require.Zero(t, first.Usage.CacheReadInputTokens)
			second := forward()
			require.Zero(t, second.Usage.CacheCreationInputTokens)
			require.Greater(t, second.Usage.CacheReadInputTokens, 0)
			require.Len(t, upstream.requests, 2)
		})
	}
}

func TestNianzsMessagesStreamFlushesBeforeUpstreamTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"messages":[{"role":"user","content":"flush early"}],"stream":true}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	upstreamBody, upstreamWriter := io.Pipe()
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       upstreamBody,
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	firstWrite := make(chan struct{})
	releaseWrite := make(chan struct{})
	c.Writer = &grokFirstWriteGate{
		ResponseWriter: c.Writer,
		firstWrite:     firstWrite,
		releaseWrite:   releaseWrite,
	}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)

	allowTerminal := make(chan struct{})
	upstreamDone := make(chan struct{})
	go func() {
		defer close(upstreamDone)
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": "visible before terminal"},
		}))
		<-allowTerminal
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
			"messageMetadataEvent": map[string]any{
				"tokenUsage": map[string]any{"uncachedInputTokens": 9, "outputTokens": 4},
			},
		}))
		_, _ = upstreamWriter.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
			"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
		}))
		_ = upstreamWriter.Close()
	}()

	type forwardOutcome struct {
		result *ForwardResult
		err    error
	}
	outcome := make(chan forwardOutcome, 1)
	go func() {
		result, forwardErr := svc.Forward(context.Background(), c, account, parsed)
		outcome <- forwardOutcome{result: result, err: forwardErr}
	}()

	select {
	case <-firstWrite:
		// The Kiro terminal frame is still gated, proving the nianzs translator
		// and downstream adapter do not aggregate the full response before TTFT.
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the first downstream Kiro write")
	}
	close(releaseWrite)
	close(allowTerminal)

	select {
	case got := <-outcome:
		require.NoError(t, got.err)
		require.NotNil(t, got.result)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Kiro stream completion")
	}
	<-upstreamDone
	require.Contains(t, recorder.Body.String(), "visible before terminal")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
}

func TestNianzsChatCompletionsRouteStreamingAndNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hello chat"}],"stream":` + map[bool]string{false: "false", true: "true"}[stream] + `}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, kiroEventStreamResponse(t, "nianzs chat ok", 10, 5))

			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
				Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6",
			})

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
			require.Equal(t, "nianzs", mustGinString(t, c, OpsKiroEngineKey))
			if stream {
				require.Contains(t, recorder.Body.String(), "nianzs chat ok")
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "data: [DONE]"))
			} else {
				require.Equal(t, "nianzs chat ok", gjson.Get(recorder.Body.String(), "choices.0.message.content").String())
			}
		})
	}
}

func TestNianzsResponsesRouteStreamingAndNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			body := []byte(`{"model":"claude-sonnet-4-6","input":[{"type":"input_text","text":"hello responses"}],"stream":` + map[bool]string{false: "false", true: "true"}[stream] + `}`)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, kiroEventStreamResponse(t, "nianzs responses ok", 11, 6))

			result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
				Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6",
			})

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
			require.Equal(t, "nianzs", mustGinString(t, c, OpsKiroEngineKey))
			if stream {
				require.Contains(t, recorder.Body.String(), "nianzs responses ok")
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: response.completed"))
			} else {
				require.Equal(t, "nianzs responses ok", gjson.Get(recorder.Body.String(), "output.0.content.0.text").String())
			}
		})
	}
}

func TestNianzsRuntimeAdaptsExistingOAuthAccountWithCLIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hybrid cli"}],"stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, kiroEventStreamResponse(t, "hybrid cli ok", 8, 4))
	account.Credentials["kiro_api_key"] = "ksk_existing_cli_key"
	account.Credentials["base_url"] = "https://stale-relay.invalid"

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
		Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer ksk_existing_cli_key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, []string{"API_KEY"}, upstream.lastReq.Header["TokenType"])
	require.Empty(t, upstream.lastReq.Header.Get("x-amzn-kiro-profile-arn"))
	// The adapter is request-local and never rewrites the persisted account.
	require.Equal(t, AccountTypeOAuth, account.Type)
	require.Equal(t, "nianzs-oauth-token", account.GetCredential("access_token"))
	require.Equal(t, "https://stale-relay.invalid", account.GetCredential("base_url"))
}

func TestNianzsAccountTestAdaptsExistingOAuthAccountWithCLIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := Account{
		ID: 1802, Name: "hybrid-account-test", Platform: PlatformKiro,
		Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "oauth-token-not-for-generation",
			"kiroApiKey":   "ksk_existing_account_test_key",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/OAUTH",
		},
	}
	upstream := &httpUpstreamRecorder{resp: kiroEventStreamResponse(t, "account test ok", 7, 3)}
	svc := &AccountTestService{
		accountRepo:         stubOpenAIAccountRepo{accounts: []Account{account}},
		httpUpstream:        upstream,
		cfg:                 &config.Config{Gateway: config.GatewayConfig{KiroEngine: config.KiroEngineNianzs}},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/1802/test", nil)

	err := svc.TestAccountConnection(c, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault)

	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "Bearer ksk_existing_account_test_key", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, []string{"API_KEY"}, upstream.lastReq.Header["TokenType"])
	require.Empty(t, upstream.lastReq.Header.Get("x-amzn-kiro-profile-arn"))
	require.Contains(t, recorder.Body.String(), `"success":true`)
}

func TestNianzsRuntime429UsesIsolatedCooldownWithoutSameAccountLoop(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { require.NoError(t, redisClient.Close()) })
	dualStore := newDualKiroCooldownStore(redisClient)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"message":"slow down"}`)),
	}}
	svc := &GatewayService{
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineNianzs,
		}},
		httpUpstream:            upstream,
		kiroCooldownStore:       dualStore,
		nianzsKiroCooldownStore: dualStore.nianzs,
		tlsFPProfileService:     &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID: 1803, Name: "nianzs-429", Platform: PlatformKiro,
		Type: AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "oauth-token"},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`)

	resp, _, err := svc.executeKiroUpstreamWithParsedNianzs(
		context.Background(), account, nil, body, "claude-sonnet-4-6", "claude-sonnet-4-6", "oauth-token", nil,
	)

	require.NoError(t, err)
	require.NotNil(t, resp)
	t.Cleanup(func() { _ = resp.Body.Close() })
	require.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	require.Len(t, upstream.requests, 1, "a final Q-endpoint 429 must return to account failover, not loop on the same account")
	key := nianzsBuildKiroAccountKey(account)
	nianzsState, err := dualStore.nianzs.GetState(context.Background(), key)
	require.NoError(t, err)
	require.NotNil(t, nianzsState)
	require.True(t, nianzsState.Active)
	require.Equal(t, nianzscooldown.CooldownReason429, nianzsState.Reason)
	legacyState, err := dualStore.legacy.GetState(context.Background(), key)
	require.NoError(t, err)
	require.Nil(t, legacyState)
}

func TestNianzsMessagesStreamSurfacesPostOutputUpstreamFailureWithoutSuccessTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"messages":[{"role":"user","content":"stream then fail"}],"stream":true}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	frame := kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "visible before failure"},
	})
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body: io.NopCloser(&nianzsErrorAfterReader{
			reader: bytes.NewReader(frame), err: io.ErrUnexpectedEOF,
		}),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

	require.Error(t, forwardErr)
	require.Nil(t, result)
	require.Len(t, upstream.requests, 1)
	wire := recorder.Body.String()
	require.Contains(t, wire, "visible before failure")
	require.Equal(t, 1, strings.Count(wire, "event: error"), "wire=%q", wire)
	require.NotContains(t, wire, "event: message_stop")
}

func TestNianzsMessagesStreamAllowsFailoverOnlyBeforeClientOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"messages":[{"role":"user","content":"fail before output"}],"stream":true}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body: io.NopCloser(&nianzsErrorAfterReader{
			reader: bytes.NewReader(nil), err: io.ErrUnexpectedEOF,
		}),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

	require.Error(t, forwardErr)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(forwardErr, &failoverErr))
	require.Len(t, upstream.requests, 1)
	require.Empty(t, recorder.Body.String())
}

func TestNianzsOpenAICompatibleToolStreamsEmitOneTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("chat_completions", func(t *testing.T) {
		body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"use exec"}],"tools":[{"type":"function","function":{"name":"exec","description":"run","parameters":{"type":"object"}}}],"stream":true}`)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		svc, _, account := newNianzsKiroRouteTestRuntime(t, kiroCustomToolEventStreamResponse(t, "toolu_nianzs_chat", "exec", `{"command":"pwd"}`))

		result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
			Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6",
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		wire := recorder.Body.String()
		require.Contains(t, wire, `"name":"exec"`)
		require.Contains(t, wire, "toolu_nianzs_chat")
		require.Equal(t, 1, strings.Count(wire, "data: [DONE]"))
	})

	t.Run("responses", func(t *testing.T) {
		resetKiroResponsesHistoryStoreForTest()
		body := []byte(`{"model":"claude-sonnet-4-6","input":[{"type":"input_text","text":"use exec"}],"tools":[{"type":"function","name":"exec","description":"run","parameters":{"type":"object"}}],"stream":true}`)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
		svc, _, account := newNianzsKiroRouteTestRuntime(t, kiroCustomToolEventStreamResponse(t, "toolu_nianzs_responses", "exec", `{"command":"pwd"}`))

		result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
			Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6",
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		wire := recorder.Body.String()
		require.Contains(t, wire, `"name":"exec"`)
		require.Contains(t, wire, "toolu_nianzs_responses")
		require.Equal(t, 1, strings.Count(wire, "event: response.completed"))
	})
}

func mustGinString(t *testing.T, c *gin.Context, key string) string {
	t.Helper()
	value, ok := c.Get(key)
	require.True(t, ok)
	text, ok := value.(string)
	require.True(t, ok)
	return text
}
