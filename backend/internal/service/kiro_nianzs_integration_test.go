package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"
	"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"
	nianzscooldown "github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown_nianzs"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

type nianzsCacheCommitProbeUpstream struct {
	base         *httpUpstreamRecorder
	beforeSecond func()
}

func (u *nianzsCacheCommitProbeUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	if u != nil && u.base != nil && len(u.base.requests) == 1 && u.beforeSecond != nil {
		u.beforeSecond()
	}
	return u.base.Do(req, proxyURL, accountID, accountConcurrency)
}

func (u *nianzsCacheCommitProbeUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
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

func TestNianzsOpenAIBridgeZeroFrameExclusionSelectsPeer(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	groupID := int64(33)
	failedSticky := bridgeTestAccount(2570, PlatformKiro, 1, groupID)
	peer := bridgeTestAccount(2571, PlatformKiro, 2, groupID)
	repo := openAIKiroBridgeAccountRepo{schedulerTestOpenAIAccountRepo{accounts: []Account{failedSticky, peer}}}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:zero_frame_session": failedSticky.ID}}
	bridge := &GatewayService{
		accountRepo:             repo,
		nianzsKiroCooldownStore: &trackingNianzsCooldownStore{states: map[string]*nianzscooldown.State{}},
		cfg: &config.Config{Gateway: config.GatewayConfig{
			KiroEngine: config.KiroEngineNianzs,
		}},
	}
	svc := &OpenAIGatewayService{
		accountRepo: repo,
		cache:       cache,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{
			failedSticky.ID: true,
			peer.ID:         true,
		}}),
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
		context.Background(), &groupID, "zero_frame_session",
		OpenAIKiroBridgeModel, OpenAIKiroBridgeModel,
		map[int64]struct{}{failedSticky.ID: {}},
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, peer.ID, selection.Account.ID, "the request-scoped exclusion produced by a zero-frame EOF must bypass the sticky account")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
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

func nianzsKiroContextUsageResponse(t *testing.T, percentage float64, outputTokens int) *http.Response {
	t.Helper()
	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "context usage ok"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "contextUsageEvent", map[string]any{
		"contextUsageEvent": map[string]any{"contextUsagePercentage": percentage},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{"outputTokens": outputTokens, "totalTokens": outputTokens},
		},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}, "x-request-id": []string{"rid_kiro_context_usage"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
}

func TestNianzsMessagesRouteReturns85PercentContextUsageForClaudeCompaction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			streamValue := "false"
			if stream {
				streamValue = "true"
			}
			body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"messages":[{"role":"user","content":"context threshold"}],"stream":` + streamValue + `}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
			require.NoError(t, err)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
			svc, _, account := newNianzsKiroRouteTestRuntime(t, nianzsKiroContextUsageResponse(t, 85.0, 10))

			result, err := svc.Forward(context.Background(), c, account, parsed)

			require.NoError(t, err)
			require.NotNil(t, result)
			billingTotal := result.Usage.InputTokens + result.Usage.CacheReadInputTokens + result.Usage.CacheCreationInputTokens + result.Usage.OutputTokens
			require.Less(t, billingTotal, 850_000, "provider context occupancy must not become billable input")
			require.Equal(t, 10, result.Usage.OutputTokens)
			if stream {
				require.Contains(t, recorder.Body.String(), `"input_tokens":849990`)
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
				require.NotContains(t, recorder.Body.String(), "_sub2api_kiro_usage_final")
				require.NotContains(t, recorder.Body.String(), "_sub2api_billing_usage")
			} else {
				require.Equal(t, int64(849_990), gjson.Get(recorder.Body.String(), "usage.input_tokens").Int())
			}
		})
	}
}

func TestNianzsResponsesRouteReturns85PercentContextUsageWithoutBillingIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"role":"user","content":"context threshold"}],"stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	svc, _, account := newNianzsKiroRouteTestRuntime(t, nianzsKiroContextUsageResponse(t, 85.0, 10))
	groupID := int64(33)
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "gpt-5.6-sol", GroupID: &groupID}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	billingTotal := result.Usage.InputTokens + result.Usage.CacheReadInputTokens + result.Usage.CacheCreationInputTokens + result.Usage.OutputTokens
	require.Less(t, billingTotal, 850_000, "the 85% context projection is client-only and must not be persisted as billing usage")
	wire := recorder.Body.String()
	require.Contains(t, wire, `"input_tokens":849990`)
	require.Equal(t, 1, strings.Count(wire, "event: response.completed"))
	require.NotContains(t, wire, "_sub2api_billing_usage")
	require.NotContains(t, wire, "_sub2api_kiro_usage_final")
}

func TestNianzsResponsesZeroFrameEOFFailsBeforeClientCommitForAccountSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"role":"user","content":"continue"}],"stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	empty := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, empty)
	groupID := int64(33)
	group := &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "gpt-5.6-sol", Stream: true, GroupID: &groupID, Group: group}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)
	require.False(t, failoverErr.RetryableOnSameAccount, "a deterministic zero-frame EOF must switch Kiro accounts directly")
	require.True(t, failoverErr.SuppressTempUnschedule, "request-scoped empty EOF must not globally punish a credential")
	require.False(t, failoverErr.FailoverProhibited, "no semantic output was exposed, so peer-account replay is safe")
	require.Empty(t, recorder.Body.String(), "the bridge must not commit response.created or an empty HTTP 200 before failover")
	require.Len(t, upstream.requests, 1)
}

func TestNianzsResponsesPostSemanticEOFReturnsTerminalFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"role":"user","content":"continue"}],"stream":true}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	partial := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body: io.NopCloser(bytes.NewReader(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": "partial answer"},
		}))),
	}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, partial)
	groupID := int64(33)
	group := &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS}
	parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "gpt-5.6-sol", Stream: true, GroupID: &groupID, Group: group}

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, parsed)

	require.Error(t, err)
	require.NotNil(t, result)
	wire := recorder.Body.String()
	require.Contains(t, wire, "partial answer")
	require.Equal(t, 1, strings.Count(wire, "event: response.failed"), "a committed partial stream needs one terminal failure")
	require.NotContains(t, wire, "event: response.completed")
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
			c.Header("X-Request-ID", "gateway-request-id")
			upstreamResponse := kiroEventStreamResponse(t, "nianzs messages ok", 9, 4)
			upstreamResponse.Header.Set("X-Request-ID", "kiro-upstream-request-id")
			upstreamResponse.Header.Set("Request-ID", "kiro-upstream-request-id")
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)

			result, err := svc.Forward(context.Background(), c, account, parsed)

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.lastReq.URL.String())
			require.Equal(t, "nianzs", mustGinString(t, c, OpsKiroEngineKey))
			responseRequestID := recorder.Header().Get("X-Request-ID")
			require.NoError(t, uuid.Validate(responseRequestID))
			require.Empty(t, recorder.Header().Get("Request-ID"))
			if stream {
				require.Contains(t, recorder.Body.String(), `"text":"nianzs messages ok"`)
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
				messageStarts := nianzsSSEPayloadsByType(recorder.Body.String(), "message_start")
				require.Len(t, messageStarts, 1)
				require.Regexp(t, `^msg_01[0-9A-Za-z]{22}$`, messageStarts[0].Get("message.id").String())
			} else {
				require.Equal(t, "nianzs messages ok", gjson.Get(recorder.Body.String(), "content.0.text").String())
				require.Equal(t, "end_turn", gjson.Get(recorder.Body.String(), "stop_reason").String())
				require.Regexp(t, `^msg_01[0-9A-Za-z]{22}$`, gjson.Get(recorder.Body.String(), "id").String())
			}
		})
	}
}

func TestNianzsKRSMessagesFinalizesSemanticContextUsageTailWithoutRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"claude-opus-5","max_tokens":8192,"stream":%t,"messages":[{"role":"user","content":"summarize the long conversation"}]}`, stream))
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
			require.NoError(t, err)
			groupID := int64(29)
			parsed.GroupID = &groupID
			parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS}

			upstreamBody := bytes.NewBuffer(nil)
			_, _ = upstreamBody.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
				"assistantResponseEvent": map[string]any{"content": "semantic tail answer"},
			}))
			_, _ = upstreamBody.Write(kiroEventStreamFrame(t, "metadataEvent", map[string]any{
				"metadataEvent": map[string]any{"requestId": "provider-request"},
			}))
			_, _ = upstreamBody.Write(kiroEventStreamFrame(t, "contextUsageEvent", map[string]any{
				"contextUsageEvent": map[string]any{"contextUsagePercentage": 81.2},
			}))
			upstreamResponse := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
				Body:       io.NopCloser(bytes.NewReader(upstreamBody.Bytes())),
			}
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

			result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

			require.NoError(t, forwardErr)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1, "completed semantic tail must never retry the account")
			require.Equal(t, nianzsKiroKRSEndpointURL, upstream.lastReq.URL.String())
			if stream {
				var visible strings.Builder
				for _, delta := range nianzsSSEPayloadsByType(recorder.Body.String(), "content_block_delta") {
					if delta.Get("delta.type").String() == "text_delta" {
						visible.WriteString(delta.Get("delta.text").String())
					}
				}
				require.Equal(t, "semantic tail answer", visible.String())
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
				require.NotContains(t, recorder.Body.String(), "event: error")
			} else {
				require.Equal(t, "semantic tail answer", gjson.Get(recorder.Body.String(), "content.0.text").String())
				require.Equal(t, "end_turn", gjson.Get(recorder.Body.String(), "stop_reason").String())
			}
		})
	}
}

func TestNianzsKRSMessagesSemanticEOFWithoutContextUsageStaysIncomplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"claude-opus-5","max_tokens":8192,"stream":%t,"messages":[{"role":"user","content":"continue"}]}`, stream))
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
			require.NoError(t, err)
			groupID := int64(29)
			parsed.GroupID = &groupID
			parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS}

			upstreamBody := bytes.NewBuffer(nil)
			_, _ = upstreamBody.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
				"assistantResponseEvent": map[string]any{"content": "truncated answer"},
			}))
			upstreamResponse := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
				Body:       io.NopCloser(bytes.NewReader(upstreamBody.Bytes())),
			}
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

			result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

			require.Nil(t, result)
			require.Len(t, upstream.requests, 1)
			require.NotContains(t, recorder.Body.String(), "event: message_stop")
			if stream {
				require.ErrorContains(t, forwardErr, "missing completion evidence")
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(forwardErr, &failoverErr), "client-visible partial output must not be replayed")
			} else {
				var failoverErr *UpstreamFailoverError
				require.ErrorAs(t, forwardErr, &failoverErr)
				require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)
				require.ErrorContains(t, failoverErr.Cause, "missing completion evidence")
				require.Empty(t, recorder.Body.String())
			}
		})
	}
}

func TestNianzsKRSSemanticTailCommitsCacheForStreamingAndNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			resetNianzsKiroCacheTracker()
			group := nianzsTestKiroCacheGroup(1)
			group.ID = 29
			group.KiroEndpointMode = KiroEndpointModeKRS
			body := nianzsTestKiroCacheRequestBody("semantic tail cache", false)
			if stream {
				body = bytes.Replace(body, []byte(`"messages"`), []byte(`"stream":true,"messages"`), 1)
			}
			semanticTailResponse := func() *http.Response {
				upstreamBody := bytes.NewBuffer(nil)
				_, _ = upstreamBody.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
					"assistantResponseEvent": map[string]any{"content": "cached semantic tail"},
				}))
				_, _ = upstreamBody.Write(kiroEventStreamFrame(t, "contextUsageEvent", map[string]any{
					"contextUsageEvent": map[string]any{"contextUsagePercentage": 64.2},
				}))
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
					Body:       io.NopCloser(bytes.NewReader(upstreamBody.Bytes())),
				}
			}
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
			upstream.responses = []*http.Response{semanticTailResponse(), semanticTailResponse()}
			forward := func() *ForwardResult {
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
				require.NoError(t, err)
				parsed.GroupID = &group.ID
				parsed.Group = group
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
				result, forwardErr := svc.Forward(context.Background(), c, account, parsed)
				require.NoError(t, forwardErr)
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

func TestNianzsMessagesThinkingWithoutProviderSignatureKeepsVisibleAnswer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-opus-5","max_tokens":128,"stream":true,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"continue"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "provider omitted its opaque signature"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "visible answer"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))

	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 1)
	wire := recorder.Body.String()
	var visible strings.Builder
	for _, delta := range nianzsSSEPayloadsByType(wire, "content_block_delta") {
		if delta.Get("delta.type").String() == "text_delta" {
			visible.WriteString(delta.Get("delta.text").String())
		}
	}
	require.Equal(t, "visible answer", visible.String())
	require.NotContains(t, wire, "provider omitted its opaque signature")
	require.NotContains(t, wire, "api_error")
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"), "wire=%q", wire)
}

func TestNianzsMessagesThinkingWithoutProviderSignatureKeepsVisibleAnswerNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-opus-5","max_tokens":128,"stream":false,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"continue"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	stream := bytes.NewBuffer(nil)
	_, _ = stream.Write(kiroEventStreamFrame(t, "reasoningContentEvent", map[string]any{
		"reasoningContentEvent": map[string]any{"text": "provider omitted its opaque signature"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "visible answer"},
	}))
	_, _ = stream.Write(kiroEventStreamFrame(t, "messageStopEvent", map[string]any{
		"messageStopEvent": map[string]any{"stop_reason": "end_turn"},
	}))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(stream.Bytes())),
	})

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 1)
	wire := recorder.Body.String()
	require.Equal(t, "visible answer", gjson.Get(wire, "content.0.text").String())
	require.NotContains(t, wire, "provider omitted its opaque signature")
	require.NotContains(t, wire, "api_error")
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
					require.Nil(t, result)
					require.NotContains(t, recorder.Body.String(), "event: message_stop")
					if stream {
						require.ErrorContains(t, forwardErr, "missing completion evidence")
						var failoverErr *UpstreamFailoverError
						require.False(t, errors.As(forwardErr, &failoverErr), "partial downstream output must never be replayed")
					} else {
						var failoverErr *UpstreamFailoverError
						require.ErrorAs(t, forwardErr, &failoverErr)
						require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)
						require.True(t, failoverErr.RetryableOnSameAccount)
						require.True(t, failoverErr.SuppressTempUnschedule)
						require.True(t, nianzskiro.IsIncompleteStream(failoverErr.Cause))
						require.Empty(t, recorder.Body.String(), "non-stream parse failure must remain uncommitted for handler failover")
					}
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

func TestNianzsMessagesStreamingBareEOFFailsOverBeforeOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-sonnet-4-6","max_tokens":128,"stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	groupID := int64(29)
	parsed.GroupID = &groupID
	parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeKRS}

	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, forwardErr, &failoverErr)
	require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)
	require.True(t, nianzskiro.IsIncompleteStream(failoverErr.Cause))
	require.Empty(t, recorder.Body.String(), "bare EOF before semantics must remain replayable")
}

func TestNianzsMessagesContextLimitExceptionSignalsCompactionWithoutAccountFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"claude-opus-5","max_tokens":8192,"stream":%t,"messages":[{"role":"user","content":"long context"}]}`, stream))
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
			require.NoError(t, err)

			upstreamBody := buildKiroEventStreamFrameWithHeaders(t, map[string]string{
				":message-type":   "exception",
				":exception-type": "ContentLengthExceededException",
			}, []byte(`{"message":"Input length and max_tokens exceed context limit"}`))
			upstreamResponse := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
				Body:       io.NopCloser(bytes.NewReader(upstreamBody)),
			}
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

			result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

			require.Nil(t, result)
			var contextErr *nianzskiro.ContextLimitError
			require.ErrorAs(t, forwardErr, &contextErr)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, "invalid_request_error", gjson.Get(recorder.Body.String(), "error.type").String())
			require.Equal(t, "prompt is too long", gjson.Get(recorder.Body.String(), "error.message").String())
			require.Len(t, upstream.requests, 1, "request-content failure must not probe another account or body")
			require.True(t, HasOpsClientBusinessLimitedReason(c, OpsClientBusinessLimitedReasonContextLimit))
		})
	}
}

func TestNianzsMessagesContextLimitAfterOutputTerminatesInBandExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-opus-5","max_tokens":8192,"stream":true,"messages":[{"role":"user","content":"long context"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(kiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "partial answer"},
	}))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrameWithHeaders(t, map[string]string{
		":message-type":   "exception",
		":exception-type": "ContentLengthExceededException",
	}, []byte(`{"message":"Input length and max_tokens exceed context limit"}`)))
	upstreamResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(bytes.NewReader(upstreamBody.Bytes())),
	}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

	require.Nil(t, result)
	var contextErr *nianzskiro.ContextLimitError
	require.ErrorAs(t, forwardErr, &contextErr)
	require.True(t, contextErr.ResponseStarted)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"text":"part"`)
	require.Contains(t, recorder.Body.String(), "prompt is too long")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
	require.NotContains(t, recorder.Body.String(), "event: message_stop")
	require.Len(t, upstream.requests, 1, "client-visible output must never be replayed")
	require.True(t, HasGatewaySSEErrorWritten(c))
}

func TestNianzsMessagesForwardScopesCompletedToolHistoryByEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []struct {
		name              string
		mode              string
		host              string
		flattenOldHistory bool
	}{
		{name: "amazon_q", mode: KiroEndpointModeQ, host: "q.us-east-1.amazonaws.com", flattenOldHistory: false},
		{name: "krs", mode: KiroEndpointModeKRS, host: "runtime.us-east-1.kiro.dev", flattenOldHistory: true},
	} {
		endpoint := endpoint
		for _, stream := range []bool{false, true} {
			stream := stream
			t.Run(fmt.Sprintf("%s/stream_%t", endpoint.name, stream), func(t *testing.T) {
				body := []byte(fmt.Sprintf(`{
					"model":"claude-opus-5",
					"max_tokens":128,
					"stream":%t,
					"tools":[{"name":"exec_command","description":"run","input_schema":{"type":"object"}}],
					"messages":[
						{"role":"user","content":"run build"},
						{"role":"assistant","content":[{"type":"tool_use","id":"old-tool","name":"exec_command","input":{"cmd":"make"}}]},
						{"role":"user","content":[{"type":"tool_result","tool_use_id":"old-tool","content":"build ok"}]},
						{"role":"assistant","content":"build completed"},
						{"role":"user","content":"run tests"},
						{"role":"assistant","content":[{"type":"tool_use","id":"active-tool","name":"exec_command","input":{"cmd":"go test ./..."}}]},
						{"role":"user","content":[{"type":"tool_result","tool_use_id":"active-tool","content":"tests pass"}]}
					]
				}`, stream))
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
				require.NoError(t, err)
				groupID := int64(29)
				parsed.GroupID = &groupID
				parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: endpoint.mode}
				upstreamResponse := kiroEventStreamResponse(t, "continue after tool result", 64, 5)
				svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

				result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

				require.NoError(t, forwardErr)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1)
				require.Equal(t, endpoint.host, upstream.requests[0].URL.Host)
				payload := upstream.lastBody
				if endpoint.flattenOldHistory {
					require.NotContains(t, string(payload), `"toolUseId":"old-tool"`)
					require.Contains(t, string(payload), "Tool calls:")
					require.Contains(t, string(payload), `[exec_command] {\"cmd\":\"make\"}`)
					require.Contains(t, string(payload), "Tool results:")
					require.Contains(t, string(payload), "[exec_command] build ok")
				} else {
					require.Contains(t, string(payload), `"toolUseId":"old-tool"`)
					require.NotContains(t, string(payload), "Tool results:")
					foundOldResult := false
					for _, message := range gjson.GetBytes(payload, "conversationState.history").Array() {
						if message.Get("userInputMessage.userInputMessageContext.toolResults.0.toolUseId").String() == "old-tool" {
							foundOldResult = true
						}
					}
					require.True(t, foundOldResult)
				}
				history := gjson.GetBytes(payload, "conversationState.history").Array()
				require.NotEmpty(t, history)
				last := history[len(history)-1]
				require.Equal(t, "active-tool", last.Get("assistantResponseMessage.toolUses.0.toolUseId").String())
				require.Equal(t, "active-tool", gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults.0.toolUseId").String())
				if stream {
					require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
				} else {
					require.Equal(t, "continue after tool result", gjson.GetBytes(recorder.Body.Bytes(), "content.0.text").String())
				}
			})
		}
	}
}

func TestNianzsMessagesForwardCompactsLongAmazonQToolHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const longToolUses = 300
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			messages := make([]any, 0, longToolUses*2+1)
			messages = append(messages, map[string]any{"role": "user", "content": "run the checks"})
			for i := 0; i < longToolUses; i++ {
				toolID := fmt.Sprintf("long-tool-%d", i)
				resultText := fmt.Sprintf("result-%d-%s", i, strings.Repeat("x", 8_000))
				var resultContent any = resultText
				if i%2 == 1 {
					resultContent = []any{map[string]any{"type": "text", "text": resultText}}
				}
				messages = append(messages,
					map[string]any{"role": "assistant", "content": []any{map[string]any{
						"type": "tool_use", "id": toolID, "name": "exec_command", "input": map[string]any{"cmd": fmt.Sprintf("check-%d", i)},
					}}},
					map[string]any{"role": "user", "content": []any{map[string]any{
						"type": "tool_result", "tool_use_id": toolID, "content": resultContent,
					}}},
				)
			}
			body, err := json.Marshal(map[string]any{
				"model":      "claude-opus-5",
				"max_tokens": 128,
				"stream":     stream,
				"tools": []any{map[string]any{
					"name": "exec_command", "description": "run", "input_schema": map[string]any{"type": "object"},
				}},
				"messages": messages,
			})
			require.NoError(t, err)

			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
			require.NoError(t, err)
			groupID := int64(29)
			parsed.GroupID = &groupID
			parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}
			upstreamResponse := kiroEventStreamResponse(t, "continued", 64, 5)
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

			result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

			require.NoError(t, forwardErr)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 1)
			require.Equal(t, "q.us-east-1.amazonaws.com", upstream.requests[0].URL.Host)
			payload := upstream.lastBody
			require.Less(t, len(payload), nianzsKiroLongContextPayloadSoftLimitBytes)
			require.NotContains(t, string(payload), `"toolUseId":"long-tool-0"`)
			require.Contains(t, string(payload), `[exec_command] {\"cmd\":\"check-0\"}`)
			require.Contains(t, string(payload), "[exec_command] result-0")
			require.Contains(t, string(payload), "[exec_command] result-1")
			require.Contains(t, string(payload), "Older completed tool result compacted")
			lastToolID := fmt.Sprintf("long-tool-%d", longToolUses-1)
			require.Equal(t, lastToolID, gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.toolResults.0.toolUseId").String())
			history := gjson.GetBytes(payload, "conversationState.history").Array()
			require.NotEmpty(t, history)
			require.Equal(t, lastToolID, history[len(history)-1].Get("assistantResponseMessage.toolUses.0.toolUseId").String())
			if stream {
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: message_stop"))
			} else {
				require.Equal(t, "continued", gjson.GetBytes(recorder.Body.Bytes(), "content.0.text").String())
			}
		})
	}
}

func TestNianzsMessagesForwardKeepsSmallAmazonQToolHistoryNativePastRecentWindow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	toolUses := nianzsKiroLongContextKeepRecentToolUses + 1
	messages := make([]any, 0, toolUses*2+1)
	messages = append(messages, map[string]any{"role": "user", "content": "run the checks"})
	for i := 0; i < toolUses; i++ {
		toolID := fmt.Sprintf("small-tool-%d", i)
		messages = append(messages,
			map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type": "tool_use", "id": toolID, "name": "exec_command", "input": map[string]any{"cmd": fmt.Sprintf("check-%d", i)},
			}}},
			map[string]any{"role": "user", "content": []any{map[string]any{
				"type": "tool_result", "tool_use_id": toolID, "content": fmt.Sprintf("result-%d", i),
			}}},
		)
	}
	body, err := json.Marshal(map[string]any{
		"model": "claude-opus-5", "max_tokens": 128, "stream": false,
		"tools": []any{map[string]any{
			"name": "exec_command", "description": "run", "input_schema": map[string]any{"type": "object"},
		}},
		"messages": messages,
	})
	require.NoError(t, err)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	groupID := int64(29)
	parsed.GroupID = &groupID
	parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeQ}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, kiroEventStreamResponse(t, "continued", 64, 5))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 1)
	require.Less(t, len(upstream.lastBody), nianzsKiroLongContextPayloadSoftLimitBytes)
	require.Contains(t, string(upstream.lastBody), `"toolUseId":"small-tool-0"`)
	require.NotContains(t, string(upstream.lastBody), "Older completed tool result compacted")
}

func TestNianzsShouldCompactOldCompletedToolHistory(t *testing.T) {
	buildToolHistory := func(count int) []byte {
		messages := make([]any, 0, count)
		for i := 0; i < count; i++ {
			messages = append(messages, map[string]any{
				"role": "assistant",
				"content": []any{map[string]any{
					"type": "tool_use",
					"id":   fmt.Sprintf("tool-%d", i),
					"name": "exec_command",
				}},
			})
		}
		body, err := json.Marshal(map[string]any{"messages": messages})
		require.NoError(t, err)
		return body
	}
	shortToolHistory := buildToolHistory(nianzsKiroLongContextKeepRecentToolUses)
	longToolHistory := buildToolHistory(nianzsKiroLongContextKeepRecentToolUses + 1)
	textOnly, err := json.Marshal(map[string]any{"messages": []any{map[string]any{
		"role": "user", "content": strings.Repeat(`"tool_use"`, nianzsKiroLongContextKeepRecentToolUses+1),
	}}})
	require.NoError(t, err)
	belowSoftLimit := bytes.Repeat([]byte{'x'}, nianzsKiroLongContextPayloadSoftLimitBytes-1)
	atSoftLimit := bytes.Repeat([]byte{'x'}, nianzsKiroLongContextPayloadSoftLimitBytes)
	require.False(t, nianzsShouldCompactOldCompletedToolHistory(belowSoftLimit, longToolHistory))
	require.False(t, nianzsShouldCompactOldCompletedToolHistory(atSoftLimit, shortToolHistory))
	require.False(t, nianzsShouldCompactOldCompletedToolHistory(atSoftLimit, textOnly))
	require.True(t, nianzsShouldCompactOldCompletedToolHistory(atSoftLimit, longToolHistory))
}

func TestNianzsMessagesAutoRebuildsToolHistoryForEachEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"claude-opus-5",
		"max_tokens":128,
		"stream":false,
		"tools":[{"name":"exec_command","description":"run","input_schema":{"type":"object"}}],
		"messages":[
			{"role":"user","content":"run build"},
			{"role":"assistant","content":[{"type":"tool_use","id":"old-tool","name":"exec_command","input":{"cmd":"make"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"old-tool","content":"build ok"}]},
			{"role":"assistant","content":"build completed"},
			{"role":"user","content":"summarize"}
		]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformKiro)
	require.NoError(t, err)
	groupID := int64(29)
	parsed.GroupID = &groupID
	parsed.Group = &Group{ID: groupID, Platform: PlatformKiro, KiroEndpointMode: KiroEndpointModeAuto}

	serviceUnavailable := func() *http.Response {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"temporary"}`)),
		}
	}
	svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
	upstream.responses = []*http.Response{
		serviceUnavailable(),
		serviceUnavailable(),
		serviceUnavailable(),
		kiroEventStreamResponse(t, "recovered on krs", 64, 5),
	}
	originalRetrySleep := nianzsKiroRetrySleep
	nianzsKiroRetrySleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { nianzsKiroRetrySleep = originalRetrySleep })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))

	result, forwardErr := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, forwardErr)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 4)
	require.Len(t, upstream.bodies, 4)
	for i := 0; i < 3; i++ {
		require.Equal(t, "q.us-east-1.amazonaws.com", upstream.requests[i].URL.Host)
		require.Contains(t, string(upstream.bodies[i]), `"toolUseId":"old-tool"`)
		require.NotContains(t, string(upstream.bodies[i]), "Tool results:")
	}
	require.Equal(t, "runtime.us-east-1.kiro.dev", upstream.requests[3].URL.Host)
	require.NotContains(t, string(upstream.bodies[3]), `"toolUseId":"old-tool"`)
	require.Contains(t, string(upstream.bodies[3]), "[exec_command] build ok")
	require.Equal(t, "recovered on krs", gjson.GetBytes(recorder.Body.Bytes(), "content.0.text").String())
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
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219,context-management-2025-06-27")
	result, err := svc.Forward(context.Background(), c, account, parsed)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, mcpEndpoint, upstream.requests[0].URL.String())
	require.Equal(t, "https://q.us-east-1.amazonaws.com/generateAssistantResponse", upstream.requests[1].URL.String())
	wire := recorder.Body.String()
	require.Equal(t, 1, strings.Count(wire, "event: message_start"))
	require.Equal(t, 1, strings.Count(wire, "event: ping"))
	require.Equal(t, 1, strings.Count(wire, "event: message_delta"))
	require.Equal(t, 1, strings.Count(wire, "event: message_stop"))
	firstBlockStart := strings.Index(wire, "event: content_block_start")
	ping := strings.Index(wire, "event: ping")
	firstBlockDelta := strings.Index(wire, "event: content_block_delta")
	require.Greater(t, firstBlockStart, -1)
	require.Greater(t, ping, firstBlockStart)
	require.Greater(t, firstBlockDelta, ping)
	require.Contains(t, wire, `"type":"server_tool_use"`)
	require.Contains(t, wire, `"type":"web_search_tool_result"`)
	require.Contains(t, wire, `"caller":{"type":"direct"}`)
	require.Contains(t, wire, `"text":"Use goroutines and channels."`)
	require.Contains(t, wire, `"type":"citations_delta"`)
	require.Contains(t, wire, `"citations":[]`)
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
	require.True(t, messageDeltas[0].Get("context_management.applied_edits").IsArray())
	require.Equal(t, int64(0), messageDeltas[0].Get("context_management.applied_edits.#").Int())
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
	require.Equal(t, "direct", gjson.Get(response, "content.1.caller.type").String())
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
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, forwardErr, &failoverErr)
	require.Equal(t, UpstreamFailureIncompleteStream, failoverErr.FailureKind)
	require.True(t, failoverErr.SuppressTempUnschedule)
	require.True(t, nianzskiro.IsIncompleteStream(failoverErr.Cause))
	require.Empty(t, recorder.Body.String(), "handler must be able to fail over before committing an error")
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
			"name":      "remote_web_search",
			"input":     `{"query":"Go memory model channels 2026","blocked_domains":[]}`,
			"stop":      true,
		},
	}))
	// Kiro may propose multiple search calls in one assistant turn. The adapter
	// selects one query for the next MCP iteration, but every private tool block
	// must be consumed so none leaks into the Anthropic server-tool stream.
	_, _ = intermediate.Write(kiroEventStreamFrame(t, "toolUseEvent", map[string]any{
		"toolUseEvent": map[string]any{
			"toolUseId": "srvtoolu_parallel_private",
			"name":      "remote_web_search",
			"input":     `{"query":"Go scheduler news 2026"}`,
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
	require.NotContains(t, wire, `"name":"remote_web_search"`)
	require.NotContains(t, wire, "srvtoolu_parallel_private")
	for id := range serverToolIDs {
		require.True(t, strings.HasPrefix(id, "srvtoolu_"), "server tool ID must use Anthropic namespace: %s", id)
	}
	// The private Kiro refinement tool_use is consumed by the adapter. The
	// client sees two server-tool pairs and final text at contiguous 0..4.
	require.Len(t, indices, 5, "two search pairs and final text must use distinct indices")
	for index := int64(0); index < 5; index++ {
		require.True(t, indices[index], "client-visible content indices must be contiguous; missing %d", index)
	}
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
		require.Contains(t, resp.Header.Values("Vary"), "Accept-Encoding")
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

func TestNianzsMessagesRouteMovingExplicitBreakpointReusesPreviousTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			resetNianzsKiroCacheTracker()
			firstBody, secondBody := nianzsTestKiroMovingBreakpointWindowBodies(3, false, "5m")
			streamValue := map[bool]string{false: "false", true: "true"}[stream]
			firstBody = bytes.Replace(firstBody, []byte(`"model"`), []byte(`"stream":`+streamValue+`,"model"`), 1)
			secondBody = bytes.Replace(secondBody, []byte(`"model"`), []byte(`"stream":`+streamValue+`,"model"`), 1)

			groupID := int64(29)
			group := &Group{
				ID:                        groupID,
				Platform:                  PlatformKiro,
				Status:                    StatusActive,
				KiroCacheEmulationEnabled: true,
				KiroCacheEmulationRatio:   1,
			}
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, nil)
			account.Extra = map[string]any{
				"kiro_cache_emulation_enabled": true,
				"kiro_cache_emulation_ratio":   1.0,
			}
			firstInput := nianzsEstimateKiroInputTokens(context.Background(), firstBody)
			secondInput := nianzsEstimateKiroInputTokens(context.Background(), secondBody)
			upstream.responses = []*http.Response{
				kiroEventStreamResponse(t, "first answer", firstInput, 4),
				kiroEventStreamResponse(t, "second answer", secondInput, 4),
			}

			forward := func(body []byte) (*ForwardResult, string) {
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
				return result, recorder.Body.String()
			}

			first, firstWire := forward(firstBody)
			require.Greater(t, first.Usage.CacheCreationInputTokens, 0)
			require.Zero(t, first.Usage.CacheReadInputTokens)
			second, secondWire := forward(secondBody)
			require.Equal(t, first.Usage.CacheCreationInputTokens, second.Usage.CacheReadInputTokens)
			require.Greater(t, second.Usage.CacheCreationInputTokens, 0)
			require.Less(t, second.Usage.CacheCreationInputTokens, second.Usage.CacheReadInputTokens)
			for _, wire := range []string{firstWire, secondWire} {
				require.NotContains(t, wire, "api_error")
				if stream {
					require.Equal(t, 1, strings.Count(wire, "event: message_stop"), "wire=%q", wire)
				}
			}
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

func TestNianzsResponsesRecoversCodexNamespacedWaitTool(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		stream := stream
		t.Run(map[bool]string{false: "non_stream", true: "stream"}[stream], func(t *testing.T) {
			resetKiroResponsesHistoryStoreForTest()
			body := []byte(`{
				"model":"gpt-5.6-sol",
				"input":[{"role":"user","content":"run the command and wait"}],
				"tools":[{"type":"namespace","name":"functions","tools":[
					{"type":"custom","name":"exec","description":"Run JavaScript orchestration"},
					{"type":"function","name":"wait","description":"Wait for a running cell","parameters":{"type":"object","properties":{"cell_id":{"type":"string"},"yield_time_ms":{"type":"integer"}},"required":["cell_id"]}}
				]}],
				"stream":` + map[bool]string{false: "false", true: "true"}[stream] + `
			}`)
			leaked := "load active. Need wait.\n\n=functions.wait to=functions.wait (commentary) ... for cell 133.\n" +
				`{"cell_id":"133","yield_time_ms":30000}`
			upstreamResponse := kiroEventStreamResponse(t, leaked, 11, 5)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			svc, upstream, account := newNianzsKiroRouteTestRuntime(t, upstreamResponse)
			account.Credentials["model_mapping"] = map[string]any{"gpt-5.6-sol": "gpt-5.6-sol"}

			result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
				Body: NewRequestBodyRef(body), Model: "gpt-5.6-sol",
			})

			require.NoError(t, err)
			require.NotNil(t, result)
			tools := gjson.GetBytes(upstream.lastBody, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array()
			require.Len(t, tools, 2)
			require.Equal(t, "functionsExec", tools[0].Get("toolSpecification.name").String())
			require.Equal(t, "functionsWait", tools[1].Get("toolSpecification.name").String())

			wire := recorder.Body.String()
			require.Contains(t, wire, `"type":"function_call"`)
			require.Contains(t, wire, `"name":"wait"`)
			require.Contains(t, wire, `"namespace":"functions"`)
			require.Contains(t, wire, `\"cell_id\":\"133\"`)
			require.NotContains(t, wire, "=functions.wait")
			require.NotContains(t, wire, "assistant to=functions.wait")
			require.NotContains(t, wire, `"name":"functions__wait"`)
			require.NotContains(t, wire, `"name":"functionsWait"`)
			if stream {
				require.Equal(t, 1, strings.Count(wire, "event: response.completed"))
			} else {
				require.Equal(t, "completed", gjson.Get(wire, "status").String())
			}
		})
	}
}

func TestNianzsResponsesRetriesInternalGPTOrchestrationLeak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroResponsesHistoryStoreForTest()
	resetNianzsKiroCacheTracker()
	body := []byte(fmt.Sprintf(`{
		"model":"gpt-5.6-sol",
		"input":[{"role":"user","content":%q}],
		"tools":[{"type":"namespace","name":"functions","tools":[
			{"type":"custom","name":"exec","description":"Run JavaScript orchestration"}
		]}],
		"prompt_cache_key":"nianzs-orchestration-retry-cache",
		"stream":true
	}`, strings.Repeat("inspect the workspace and preserve this stable prefix ", 700)))
	leak := kiroEventStreamResponse(t, "as codex? Need wait.\nWait cell.\nNeed model selection.\nAlso security: key sent to specific destination cctest.", 11, 5)
	baseUpstream := &httpUpstreamRecorder{responses: []*http.Response{
		leak,
		kiroCustomToolEventStreamResponse(t, "toolu_nianzs_orchestration_retry", "functionsExec", `{"input":"text(\"done\")"}`),
	}}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, nil)
	account.Extra = map[string]any{
		"kiro_cache_emulation_enabled": true,
		"kiro_cache_emulation_ratio":   1.0,
	}
	var beforeRetryUsage *nianzsKiroCacheEmulationUsage
	var runtimeCacheInputTokens int
	var runtimeCacheBody []byte
	svc.httpUpstream = &nianzsCacheCommitProbeUpstream{
		base: baseUpstream,
		beforeSecond: func() {
			// The Responses route prepares its cache plan with the converted
			// Anthropic body's token estimate. Reuse that exact runtime value
			// when probing the tracker so this test exercises cache commit timing,
			// not a different token-scaling profile.
			normalizedBody, _, normalizeErr := normalizeKiroCodexResponsesTools(body)
			require.NoError(t, normalizeErr)
			runtimeCacheBody = normalizedBody
			var responsesReq apicompat.ResponsesRequest
			convertErr := json.Unmarshal(normalizedBody, &responsesReq)
			require.NoError(t, convertErr)
			convertedBody, convertErr := apicompat.ResponsesToAnthropicRequest(&responsesReq)
			require.NoError(t, convertErr)
			anthropicBody, marshalErr := json.Marshal(convertedBody)
			require.NoError(t, marshalErr)
			runtimeCacheInputTokens = nianzsEstimateKiroInputTokens(context.Background(), anthropicBody)
			beforeRetryUsage = svc.prepareKiroResponsesCacheEmulationUsageNianzs(
				context.Background(), account, nil, runtimeCacheBody, "gpt-5.6-sol", runtimeCacheInputTokens,
			).result()
		},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	result, err := svc.ForwardAsResponses(context.Background(), c, account, body, &ParsedRequest{
		Body: NewRequestBodyRef(body), Model: "gpt-5.6-sol",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, baseUpstream.requests, 2)
	require.NotNil(t, beforeRetryUsage)
	require.Zero(t, beforeRetryUsage.CacheReadInputTokens, "discarded first attempt must not commit cache state")
	require.Greater(t, beforeRetryUsage.CacheCreationInputTokens, 0)
	afterSuccessUsage := svc.prepareKiroResponsesCacheEmulationUsageNianzs(
		context.Background(), account, nil, runtimeCacheBody, "gpt-5.6-sol", runtimeCacheInputTokens,
	).result()
	require.NotNil(t, afterSuccessUsage)
	require.Greater(t, afterSuccessUsage.CacheReadInputTokens, 0, "accepted retry must commit cache state")
	wire := recorder.Body.String()
	require.NotContains(t, wire, "Need model selection")
	require.Contains(t, wire, `"type":"custom_tool_call"`)
	require.Contains(t, wire, `"name":"exec"`)
	firstContent := gjson.GetBytes(baseUpstream.bodies[0], "conversationState.currentMessage.userInputMessage.content").String()
	secondContent := gjson.GetBytes(baseUpstream.bodies[1], "conversationState.currentMessage.userInputMessage.content").String()
	require.NotContains(t, firstContent, nianzsKiroNativeToolProgressRetryInstruction)
	require.Contains(t, secondContent, nianzsKiroNativeToolProgressRetryInstruction)
	require.NotEqual(t,
		gjson.GetBytes(baseUpstream.bodies[0], "conversationState.conversationId").String(),
		gjson.GetBytes(baseUpstream.bodies[1], "conversationState.conversationId").String(),
	)
}

func TestNianzsChatRetriesInternalGPTOrchestrationLeak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"messages":[{"role":"user","content":"inspect the workspace"}],
		"tools":[{"type":"function","function":{"name":"exec","description":"Run JavaScript orchestration","parameters":{"type":"object"}}}],
		"stream":true
	}`)
	leak := kiroEventStreamResponse(t, "as codex? Need wait.\nWait cell.\nNeed model selection.\nAlso security: key sent to specific destination cctest.", 11, 5)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		leak,
		kiroCustomToolEventStreamResponse(t, "toolu_nianzs_chat_orchestration_retry", "exec", `{"input":"text(\"done\")"}`),
	}}
	svc, _, account := newNianzsKiroRouteTestRuntime(t, nil)
	svc.httpUpstream = upstream
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, &ParsedRequest{
		Body: NewRequestBodyRef(body), Model: "gpt-5.6-sol",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 2)
	wire := recorder.Body.String()
	require.NotContains(t, wire, "Need model selection")
	require.Contains(t, wire, `"name":"exec"`)
	require.Contains(t, wire, "toolu_nianzs_chat_orchestration_retry")
	firstContent := gjson.GetBytes(upstream.bodies[0], "conversationState.currentMessage.userInputMessage.content").String()
	secondContent := gjson.GetBytes(upstream.bodies[1], "conversationState.currentMessage.userInputMessage.content").String()
	require.NotContains(t, firstContent, nianzsKiroNativeToolProgressRetryInstruction)
	require.Contains(t, secondContent, nianzsKiroNativeToolProgressRetryInstruction)
}

func mustGinString(t *testing.T, c *gin.Context, key string) string {
	t.Helper()
	value, ok := c.Get(key)
	require.True(t, ok)
	text, ok := value.(string)
	require.True(t, ok)
	return text
}
