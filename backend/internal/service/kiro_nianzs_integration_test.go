package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
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

func nianzsSSEPayloadsByType(wire, eventType string) []gjson.Result {
	results := make([]gjson.Result, 0)
	for _, line := range strings.Split(wire, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload == "" || payload == "[DONE]" || gjson.Get(payload, "type").String() != eventType {
			continue
		}
		results = append(results, gjson.Parse(payload))
	}
	return results
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

func TestNianzsMessagesTerminalEvidenceGatesCacheCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		for _, terminal := range []bool{false, true} {
			terminal := terminal
			name := fmt.Sprintf("stream_%t/terminal_%t", stream, terminal)
			t.Run(name, func(t *testing.T) {
				resetNianzsKiroCacheTracker()
				group := nianzsTestKiroCacheGroup(1)
				body := nianzsTestKiroCacheRequestBody("terminal gate "+name, false)
				if stream {
					body = bytes.Replace(body, []byte(`"messages"`), []byte(`"stream":true,"messages"`), 1)
				}
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
				require.NoError(t, err)
				parsed.Group = group
				parsed.GroupID = &group.ID

				upstream := bytes.NewBuffer(nil)
				_, _ = upstream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
					"assistantResponseEvent": map[string]any{"content": "candidate response"},
				}))
				if terminal {
					_, _ = upstream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
						"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
					}))
				}
				upstreamResponse := &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
					Body:       io.NopCloser(bytes.NewReader(upstream.Bytes())),
				}
				svc, _, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
				inputTokens := nianzsEstimateKiroInputTokens(context.Background(), body)
				expectedPlan := svc.prepareKiroCacheEmulationUsageNianzs(
					context.Background(), adaptKiroAccountForNianzs(account), group, body, "claude-sonnet-4-6", inputTokens,
				)
				require.NotNil(t, expectedPlan)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

				result, forwardErr := svc.Forward(context.Background(), c, account, parsed)
				if terminal {
					require.NoError(t, forwardErr)
					require.NotNil(t, result)
				} else {
					require.ErrorContains(t, forwardErr, "missing completion evidence")
					require.Nil(t, result)
					require.NotContains(t, recorder.Body.String(), "event: message_stop")
				}

				nianzsGlobalKiroCacheTracker.mu.Lock()
				committedEntries := len(nianzsGlobalKiroCacheTracker.entries[expectedPlan.cacheKey])
				nianzsGlobalKiroCacheTracker.mu.Unlock()
				if terminal {
					require.Greater(t, committedEntries, 0, "verified terminal response should commit its prefix")
				} else {
					require.Equal(t, 0, committedEntries, "bare EOF must not commit cache state")
				}

				probe := svc.prepareKiroCacheEmulationUsageNianzs(
					context.Background(), adaptKiroAccountForNianzs(account), group, body, "claude-sonnet-4-6", inputTokens,
				)
				require.NotNil(t, probe)
				require.NotNil(t, probe.result())
				if terminal {
					require.Equal(t, expectedPlan.cacheKey, probe.cacheKey)
				} else {
					require.Equal(t, 0, probe.result().CacheReadInputTokens, "bare EOF must not create a phantom cache hit")
					require.Greater(t, probe.result().CacheCreationInputTokens, 0)
				}
			})
		}
	}
}

func TestNianzsMessagesWebSearchStreamReachesExactlyOneTerminalOutcome(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"Perform a web search for the query: golang concurrency"}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	mcpEndpoint := nianzskiro.BuildMcpEndpoint("us-east-1")
	nianzsKiroWebSearchDescCache.Store(mcpEndpoint, "Search the web for current information.")
	t.Cleanup(func() { nianzsKiroWebSearchDescCache.Delete(mcpEndpoint) })

	mcpResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"jsonrpc":"2.0","id":"web_search_test","result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Go\",\"url\":\"https://go.dev\",\"snippet\":\"official docs\"}]}"}]}}`,
		)),
	}
	upstreamResponse := kiroEventStreamResponse(t, "Use goroutines and channels.", 12, 5)
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	upstream.responses = []*http.Response{mcpResponse, upstreamResponse}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, mcpEndpoint, upstream.requests[0].URL.String())
	require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.requests[1].URL.String())
	wire := recorder.Body.String()
	require.Equal(t, 1, strings.Count(wire, "event: message_start"))
	require.Equal(t, 1, strings.Count(wire, "event: message_delta"))
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
	require.Contains(t, wire, `"type":"server_tool_use"`)
	require.Contains(t, wire, `"type":"web_search_tool_result"`)
	require.Contains(t, wire, `"text":"Use goroutines and channels."`)
	require.Contains(t, wire, `"type":"citations_delta"`)
	require.Contains(t, wire, `"type":"web_search_result_location"`)
	var serverToolUseID, resultToolUseID string
	for _, event := range nianzsSSEPayloadsByType(wire, "content_block_start") {
		switch event.Get("content_block.type").String() {
		case "server_tool_use":
			serverToolUseID = event.Get("content_block.id").String()
		case "web_search_tool_result":
			resultToolUseID = event.Get("content_block.tool_use_id").String()
		}
	}
	require.NotEmpty(t, serverToolUseID)
	require.Equal(t, serverToolUseID, resultToolUseID)
	messageDeltas := nianzsSSEPayloadsByType(wire, "message_delta")
	require.Len(t, messageDeltas, 1)
	require.Equal(t, int64(1), messageDeltas[0].Get("usage.server_tool_use.web_search_requests").Int())
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

func TestNianzsMessagesWebSearchNonStreamingPairsResultAndReportsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":false,"messages":[{"role":"user","content":"Perform a web search for the query: golang concurrency"}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	mcpEndpoint := nianzskiro.BuildMcpEndpoint("us-east-1")
	nianzsKiroWebSearchDescCache.Store(mcpEndpoint, "Search the web for current information.")
	t.Cleanup(func() { nianzsKiroWebSearchDescCache.Delete(mcpEndpoint) })

	mcpResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"jsonrpc":"2.0","id":"web_search_test","result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Go\",\"url\":\"https://go.dev\",\"snippet\":\"official docs\"}]}"}]}}`,
		)),
	}
	upstreamResponse := kiroEventStreamResponse(t, "Use goroutines and channels.", 12, 5)
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	upstream.responses = []*http.Response{mcpResponse, upstreamResponse}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Len(t, upstream.requests, 2)
	response := recorder.Body.String()
	require.Equal(t, "server_tool_use", gjson.Get(response, "content.0.type").String())
	require.Equal(t, "web_search_tool_result", gjson.Get(response, "content.1.type").String())
	require.Equal(t, gjson.Get(response, "content.0.id").String(), gjson.Get(response, "content.1.tool_use_id").String())
	require.Equal(t, int64(1), gjson.Get(response, "usage.server_tool_use.web_search_requests").Int())
	require.Equal(t, "Use goroutines and channels.", gjson.Get(response, "content.2.text").String())
	require.Equal(t, "web_search_result_location", gjson.Get(response, "content.2.citations.0.type").String())
	require.Equal(t, "https://go.dev", gjson.Get(response, "content.2.citations.0.url").String())
	require.NotEmpty(t, gjson.Get(response, "content.2.citations.0.encrypted_index").String())
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
}

func TestNianzsMessagesWebSearchNonStreamingRejectsBareEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":false,"messages":[{"role":"user","content":"Perform a web search for the query: golang concurrency"}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	mcpEndpoint := nianzskiro.BuildMcpEndpoint("us-east-1")
	nianzsKiroWebSearchDescCache.Store(mcpEndpoint, "Search the web for current information.")
	t.Cleanup(func() { nianzsKiroWebSearchDescCache.Delete(mcpEndpoint) })

	mcpResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"jsonrpc":"2.0","id":"web_search_test","result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Go\",\"url\":\"https://go.dev\"}]}"}]}}`,
		)),
	}
	partial := bytes.NewBuffer(nil)
	_, _ = partial.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "partial answer"},
	}))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	upstream.responses = []*http.Response{mcpResponse, {
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(partial.Bytes())),
	}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)
	require.ErrorContains(t, forwardErr, "missing completion evidence")
	require.Nil(t, result)
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "partial answer")
}

func TestNianzsKiroWebSearchErrorCodeMapsAnthropicValues(t *testing.T) {
	require.Equal(t, nianzskiro.WebSearchErrorTooManyRequests, nianzsKiroWebSearchErrorCode(&nianzsKiroWebSearchMCPError{
		StatusCode: http.StatusTooManyRequests,
	}))
	require.Equal(t, nianzskiro.WebSearchErrorQueryTooLong, nianzsKiroWebSearchErrorCode(&nianzsKiroWebSearchMCPError{
		StatusCode: http.StatusBadRequest,
		Message:    "query is too long",
	}))
	require.Equal(t, nianzskiro.WebSearchErrorUnavailable, nianzsKiroWebSearchErrorCode(errors.New("transport reset")))
}

func TestNianzsMessagesWebSearchMultipleIterationsKeepOneTerminalAndPairedBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"Research Go concurrency and refine the query if needed"}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":2}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	mcpEndpoint := nianzskiro.BuildMcpEndpoint("us-east-1")
	nianzsKiroWebSearchDescCache.Store(mcpEndpoint, "Search the web for current information.")
	t.Cleanup(func() { nianzsKiroWebSearchDescCache.Delete(mcpEndpoint) })

	mcpResponse := func(id, title, url string) *http.Response {
		payload := fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"result":{"content":[{"type":"text","text":%q}]}}`, id,
			fmt.Sprintf(`{"results":[{"title":%q,"url":%q,"snippet":"official docs"}]}`, title, url))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(payload)),
		}
	}
	intermediate := bytes.NewBuffer(nil)
	_, _ = intermediate.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "srvtoolu_refined",
			"name":      "web_search",
			"input":     `{"query":"Go memory model channels 2026"}`,
			"stop":      true,
		},
	}))
	_, _ = intermediate.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 10, "outputTokens": 2}},
	}))
	_, _ = intermediate.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))
	intermediateResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(intermediate.Bytes())),
	}

	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	upstream.responses = []*http.Response{
		mcpResponse("search_one", "Go Concurrency", "https://go.dev/doc/effective_go#concurrency"),
		intermediateResponse,
		mcpResponse("search_two", "Go Memory Model", "https://go.dev/ref/mem"),
		kiroEventStreamResponse(t, "Use channels for communication and synchronize shared memory explicitly.", 18, 7),
	}

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 4)
	firstTurnTools := gjson.GetBytes(upstream.bodies[1], "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools")
	finalTurnTools := gjson.GetBytes(upstream.bodies[3], "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools")
	require.True(t, firstTurnTools.IsArray())
	require.Equal(t, int64(0), finalTurnTools.Get("#").Int(),
		"the final allowed search turn must not expose an active or placeholder web-search tool")
	wire := recorder.Body.String()
	require.Equal(t, 1, strings.Count(wire, "event: message_start"))
	require.Equal(t, 1, strings.Count(wire, "event: message_delta"))
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))

	serverToolIDs := make(map[string]bool)
	resultToolIDs := make(map[string]bool)
	indices := make(map[int64]bool)
	for _, event := range nianzsSSEPayloadsByType(wire, "content_block_start") {
		indices[event.Get("index").Int()] = true
		switch event.Get("content_block.type").String() {
		case "server_tool_use":
			serverToolIDs[event.Get("content_block.id").String()] = true
		case "web_search_tool_result":
			resultToolIDs[event.Get("content_block.tool_use_id").String()] = true
		}
	}
	require.Len(t, serverToolIDs, 2)
	require.Equal(t, serverToolIDs, resultToolIDs)
	require.Contains(t, wire, `"name":"remote_web_search"`)
	for id := range serverToolIDs {
		require.True(t, strings.HasPrefix(id, "srvtoolu_"), "server tool ID must use Anthropic namespace: %s", id)
	}
	// The model's refinement tool_use remains visible between the first search
	// result and the second native server-tool turn.  This fixture has no
	// narrative text before the refinement call, so the wire uses indices 0..5.
	require.Len(t, indices, 6, "two search pairs, refinement tool, and final text must use distinct indices")
	messageDeltas := nianzsSSEPayloadsByType(wire, "message_delta")
	require.Len(t, messageDeltas, 1)
	require.Equal(t, int64(2), messageDeltas[0].Get("usage.server_tool_use.web_search_requests").Int())
	require.Contains(t, wire, `"text":"Use channels for communication and synchronize shared memory explicitly."`)
}

func TestNianzsMessagesWebSearchCommitsCacheOnlyAfterCompleteFirstTurn(t *testing.T) {
	body := []byte(`{"model":"claude-opus-5","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"Perform a web search for the query: golang"}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	mcpEndpoint := nianzskiro.BuildMcpEndpoint("us-east-1")
	nianzsKiroWebSearchDescCache.Store(mcpEndpoint, "Search the web for current information.")
	t.Cleanup(func() { nianzsKiroWebSearchDescCache.Delete(mcpEndpoint) })

	mcpResponse := func() *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"jsonrpc":"2.0","id":"search","result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Go\",\"url\":\"https://go.dev\",\"snippet\":\"docs\"}]}"}]}}`,
			)),
		}
	}

	t.Run("truncated turn does not commit", func(t *testing.T) {
		truncated := bytes.NewBuffer(nil)
		_, _ = truncated.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": "PARTIAL_ONLY"},
		}))
		svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
		upstream.responses = []*http.Response{mcpResponse(), {
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
			Body:       io.NopCloser(bytes.NewReader(truncated.Bytes())),
		}}
		cacheKey := uint64(time.Now().UnixNano())
		plan := nianzsCodeExecutionCachePlanForTest(cacheKey)
		defer nianzsDeleteCodeExecutionCachePlanForTest(cacheKey)

		resp, _, openErr := svc.openKiroAnthropicStreamResponseNianzs(
			context.Background(), account, parsed, body, "claude-opus-5", "claude-opus-5", nil, nil, plan,
		)
		require.NoError(t, openErr)
		require.NotNil(t, resp)
		wire, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Error(t, readErr)
		require.Contains(t, string(wire), `"type":"server_tool_use"`)
		require.NotContains(t, string(wire), "event: message_stop")
		require.False(t, nianzsCodeExecutionCachePlanCommittedForTest(cacheKey))
	})

	t.Run("complete turn commits", func(t *testing.T) {
		svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
		upstream.responses = []*http.Response{mcpResponse(), kiroEventStreamResponse(t, "done", 9, 3)}
		cacheKey := uint64(time.Now().UnixNano())
		plan := nianzsCodeExecutionCachePlanForTest(cacheKey)
		defer nianzsDeleteCodeExecutionCachePlanForTest(cacheKey)

		resp, _, openErr := svc.openKiroAnthropicStreamResponseNianzs(
			context.Background(), account, parsed, body, "claude-opus-5", "claude-opus-5", nil, nil, plan,
		)
		require.NoError(t, openErr)
		require.NotNil(t, resp)
		wire, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.NoError(t, readErr)
		require.Equal(t, 1, strings.Count(string(wire), "event: message_stop"))
		require.True(t, nianzsCodeExecutionCachePlanCommittedForTest(cacheKey))
	})
}

func TestNianzsMessagesWebSearchTruncatedTurnSurfacesOneInBandError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-opus-5","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"Perform a web search for the query: golang"}],"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	mcpEndpoint := nianzskiro.BuildMcpEndpoint("us-east-1")
	nianzsKiroWebSearchDescCache.Store(mcpEndpoint, "Search the web for current information.")
	t.Cleanup(func() { nianzsKiroWebSearchDescCache.Delete(mcpEndpoint) })

	truncated := bytes.NewBuffer(nil)
	_, _ = truncated.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "PARTIAL_ONLY"},
	}))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	upstream.responses = []*http.Response{
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"jsonrpc":"2.0","id":"search","result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Go\",\"url\":\"https://go.dev\",\"snippet\":\"docs\"}]}"}]}}`,
			)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
			Body:       io.NopCloser(bytes.NewReader(truncated.Bytes())),
		},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, forwardErr)
	require.Nil(t, result)
	wire := recorder.Body.String()
	require.Contains(t, wire, `"type":"server_tool_use"`)
	require.Contains(t, wire, `"type":"stream_read_error"`)
	require.Equal(t, 1, strings.Count(wire, "event: error"))
	require.Equal(t, 0, strings.Count(wire, "event: message_stop"))
}

func TestNianzsMessagesWebSearchContinuationNormalizesSyntheticHistoryBeforeKiro(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"claude-opus-5","max_tokens":128,"stream":false,
		"messages":[
			{"role":"user","content":"search go"},
			{"role":"assistant","content":[
				{"type":"server_tool_use","id":"srvtoolu_01history","name":"web_search","input":{"query":"go"}},
				{"type":"web_search_tool_result","tool_use_id":"srvtoolu_01history","content":[{"type":"web_search_result","title":"Go","url":"https://go.dev","encrypted_content":"expired-token","page_age":null}]},
				{"type":"text","text":"Go docs","citations":[{"type":"web_search_result_location","url":"https://go.dev","title":"Go","encrypted_index":"expired-index","cited_text":"docs"}]}
			]},
			{"role":"user","content":"Perform a web search for the query: Go release news"}
		],
		"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1}]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	mcpEndpoint := nianzskiro.BuildMcpEndpoint("us-east-1")
	nianzsKiroWebSearchDescCache.Store(mcpEndpoint, "Search the web for current information.")
	t.Cleanup(func() { nianzsKiroWebSearchDescCache.Delete(mcpEndpoint) })

	mcpResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"jsonrpc":"2.0","id":"search","result":{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Go News\",\"url\":\"https://go.dev/blog\",\"snippet\":\"news\"}]}"}]}}`,
		)),
	}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	upstream.responses = []*http.Response{mcpResponse, kiroEventStreamResponse(t, "latest news", 15, 4)}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2)
	kiroPayload := string(upstream.bodies[1])
	require.NotContains(t, kiroPayload, "server_tool_use")
	require.NotContains(t, kiroPayload, "web_search_tool_result")
	require.NotContains(t, kiroPayload, "encrypted_index")
	require.Contains(t, kiroPayload, "web_search_history")
	require.Contains(t, kiroPayload, "https://go.dev")
}

func TestNianzsMessagesNamedToolChoiceUsesOnlySelectedToolEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,
		"messages":[{"role":"user","content":"use beta"}],
		"tools":[
			{"name":"alpha","description":"alpha","input_schema":{"type":"object","properties":{}}},
			{"name":"beta","description":"beta","input_schema":{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}},
			{"name":"gamma","description":"gamma","input_schema":{"type":"object","properties":{}}}
		],
		"tool_choice":{"type":"tool","name":"beta"}
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{"toolUseId": "toolu_beta", "name": "beta", "input": `{"value":"ok"}`, "stop": true},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 20, "outputTokens": 4}},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	forwardedTools := gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array()
	require.Len(t, forwardedTools, 1)
	require.Equal(t, "beta", forwardedTools[0].Get("toolSpecification.name").String())
	wire := recorder.Body.String()
	require.Contains(t, wire, `"type":"tool_use"`)
	require.Contains(t, wire, `"name":"beta"`)
	require.Contains(t, wire, `"partial_json":"{\"value\":\"ok\"}"`)
	require.Contains(t, wire, `"stop_reason":"tool_use"`)
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
}

func TestNianzsMessagesStructuredOutputStreamingToolBecomesJSONEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,
		"messages":[{"role":"user","content":"return the structured answer"}],
		"output_config":{"format":{"type":"json_schema","name":"schema_answer","schema":{"type":"object","properties":{"ok":{"type":"boolean"},"count":{"type":"integer"}},"required":["ok","count"],"additionalProperties":false}}}
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{"toolUseId": "toolu_schema", "name": "schema_answer", "input": `{"ok":true,"count":2}`, "stop": true},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 15, "outputTokens": 6}},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	forwardedTools := gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array()
	require.Len(t, forwardedTools, 1)
	require.Equal(t, "schema_answer", forwardedTools[0].Get("toolSpecification.name").String())
	require.False(t, forwardedTools[0].Get("toolSpecification.inputSchema.json.additionalProperties").Bool())
	wire := recorder.Body.String()
	require.NotContains(t, wire, `"type":"tool_use"`)
	require.Contains(t, wire, `"type":"text_delta"`)
	require.Contains(t, wire, `"text":"{\"count\":2,\"ok\":true}"`)
	require.Contains(t, wire, `"stop_reason":"end_turn"`)
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
}

func TestNianzsMessagesStructuredOutputNonStreamingToolBecomesJSONEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"claude-sonnet-4-6","max_tokens":128,"stream":false,
		"messages":[{"role":"user","content":"return the structured answer"}],
		"output_config":{"format":{"type":"json_schema","name":"schema_answer","schema":{"type":"object","properties":{"ok":{"type":"boolean"},"count":{"type":"integer"}},"required":["ok","count"],"additionalProperties":false}}}
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{"toolUseId": "toolu_schema_nonstream", "name": "schema_answer", "input": `{"ok":true,"count":2}`, "stop": true},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 15, "outputTokens": 6}},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "tool_use"},
	}))
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, "text", gjson.Get(recorder.Body.String(), "content.0.type").String())
	require.JSONEq(t, `{"count":2,"ok":true}`, gjson.Get(recorder.Body.String(), "content.0.text").String())
	require.Equal(t, "end_turn", gjson.Get(recorder.Body.String(), "stop_reason").String())
	require.Equal(t, int64(15), gjson.Get(recorder.Body.String(), "usage.input_tokens").Int())
	require.Equal(t, int64(6), gjson.Get(recorder.Body.String(), "usage.output_tokens").Int())
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
