package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stableCanaryGroupRepoStub struct{ group *Group }

func (r *stableCanaryGroupRepoStub) GetByID(context.Context, int64) (*Group, error) {
	return r.group, nil
}
func (r *stableCanaryGroupRepoStub) GetByIDLite(context.Context, int64) (*Group, error) {
	return r.group, nil
}
func (r *stableCanaryGroupRepoStub) Create(context.Context, *Group) error { return nil }
func (r *stableCanaryGroupRepoStub) Update(context.Context, *Group) error { return nil }
func (r *stableCanaryGroupRepoStub) Delete(context.Context, int64) error  { return nil }
func (r *stableCanaryGroupRepoStub) DeleteCascade(context.Context, int64) ([]int64, error) {
	return nil, nil
}
func (r *stableCanaryGroupRepoStub) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *stableCanaryGroupRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *stableCanaryGroupRepoStub) ListActive(context.Context) ([]Group, error) { return nil, nil }
func (r *stableCanaryGroupRepoStub) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	return nil, nil
}
func (r *stableCanaryGroupRepoStub) ExistsByName(context.Context, string) (bool, error) {
	return false, nil
}
func (r *stableCanaryGroupRepoStub) GetAccountCount(context.Context, int64) (int64, int64, error) {
	return 0, 0, nil
}
func (r *stableCanaryGroupRepoStub) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	return 0, nil
}
func (r *stableCanaryGroupRepoStub) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	return nil, nil
}
func (r *stableCanaryGroupRepoStub) BindAccountsToGroup(context.Context, int64, []int64) error {
	return nil
}
func (r *stableCanaryGroupRepoStub) UpdateSortOrders(context.Context, []GroupSortOrderUpdate) error {
	return nil
}

type stableCanaryChannelRepoStub struct {
	ChannelRepository
	channel        Channel
	groupPlatforms map[int64]string
}

func (r *stableCanaryChannelRepoStub) ListAll(context.Context) ([]Channel, error) {
	return []Channel{r.channel}, nil
}

func (r *stableCanaryChannelRepoStub) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.groupPlatforms, nil
}

type stableCanaryRoundTripFunc func(*http.Request) (*http.Response, error)

func (f stableCanaryRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type stableCanaryTestFixture struct {
	service *GatewayService
	account *Account
	ctx     *gin.Context
	rec     *httptest.ResponseRecorder
	body    []byte
}

type stableCanaryRefreshRepoStub struct {
	AccountRepository
	account           *Account
	updates           int
	blocked           int
	sessionOwner      int64
	sessionHash       string
	keyFingerprint    string
	policyFingerprint string
	sessionErr        error
}

func (r *stableCanaryRefreshRepoStub) AcquireAnthropicStableCanaryLease(context.Context, int64) (func() error, error) {
	return func() error { return nil }, nil
}

func (r *stableCanaryRefreshRepoStub) ClaimAnthropicStableCanarySession(
	_ context.Context,
	_, _, _, ownerUserID int64,
	sessionHash, keyFingerprint, policyFingerprint string,
) error {
	r.sessionOwner = ownerUserID
	r.sessionHash = sessionHash
	r.keyFingerprint = keyFingerprint
	r.policyFingerprint = policyFingerprint
	return r.sessionErr
}

type stableCanaryReloadRepoStub struct {
	AccountRepository
	stale *Account
	fresh *Account
	loads int
}

func (r *stableCanaryReloadRepoStub) AcquireAnthropicStableCanaryLease(context.Context, int64) (func() error, error) {
	return func() error { return nil }, nil
}

type stableCanaryDurableStateRepoStub struct {
	AccountRepository
	accountID  int64
	reason     string
	calls      int
	contextErr error
	deadline   time.Time
	err        error
}

func (r *stableCanaryDurableStateRepoStub) BlockAnthropicStableCanary(ctx context.Context, accountID int64, reason string) error {
	r.calls++
	r.accountID = accountID
	r.reason = reason
	r.contextErr = ctx.Err()
	r.deadline, _ = ctx.Deadline()
	return r.err
}

func (r *stableCanaryReloadRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	r.loads++
	if r.fresh == nil || r.fresh.ID != id {
		return nil, ErrAccountNotFound
	}
	return r.fresh, nil
}

func (r *stableCanaryReloadRepoStub) ListByGroup(_ context.Context, groupID int64) ([]Account, error) {
	if r.fresh == nil || !accountHasOnlyGroup(r.fresh, groupID) {
		return nil, nil
	}
	return []Account{*r.fresh}, nil
}

func (r *stableCanaryRefreshRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	return r.account, nil
}

func (r *stableCanaryRefreshRepoStub) ListByGroup(_ context.Context, groupID int64) ([]Account, error) {
	if r.account == nil || !accountHasOnlyGroup(r.account, groupID) {
		return nil, nil
	}
	return []Account{*r.account}, nil
}

func (r *stableCanaryRefreshRepoStub) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	if r.account == nil || r.account.ID != id {
		return ErrAccountNotFound
	}
	r.account.Credentials = shallowCopyMap(credentials)
	r.updates++
	return nil
}

func (r *stableCanaryRefreshRepoStub) BlockAnthropicStableCanary(_ context.Context, accountID int64, reason string) error {
	if r.account == nil || r.account.ID != accountID {
		return ErrAccountNotFound
	}
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	r.account.Extra[AnthropicStableCanaryBlockedExtraKey] = true
	r.account.Extra[AnthropicStableCanaryBlockedReasonExtraKey] = NormalizeAnthropicStableCanaryBlockReason(reason)
	r.blocked++
	return nil
}

func strictStableCanaryProfileHeader(c *gin.Context, profileID string) {
	profile := anthropicStableIngressProfiles[profileID]
	c.Request.URL.RawQuery = AnthropicStableIngressQueryV1
	c.Request.Header.Set("User-Agent", profile.userAgent)
	c.Request.Header.Set("anthropic-beta", profile.beta)
	c.Request.Header.Set("anthropic-version", AnthropicStableIngressAPIVersionV1)
	c.Request.Header.Set("x-app", AnthropicStableIngressXAppV1)
}

func newStableCanaryTestFixture(t *testing.T, rt http.RoundTripper) *stableCanaryTestFixture {
	t.Helper()
	const groupID = int64(71)
	const accountID = int64(811)
	accountDevice := strings.Repeat("a", 64)
	clientDevice := accountDevice
	group := &Group{
		ID:                              groupID,
		Status:                          StatusActive,
		Platform:                        PlatformAnthropic,
		IsExclusive:                     true,
		ClaudeCodeOnly:                  true,
		RequireOAuthOnly:                true,
		FallbackGroupID:                 nil,
		FallbackGroupIDOnInvalidRequest: nil,
		AccountCount:                    1,
	}
	account := &Account{
		ID:            accountID,
		Name:          "stable-canary-test",
		Platform:      PlatformAnthropic,
		Type:          AccountTypeOAuth,
		Status:        StatusActive,
		Schedulable:   false,
		Concurrency:   1,
		GroupIDs:      []int64{groupID},
		AccountGroups: []AccountGroup{{AccountID: accountID, GroupID: groupID}},
		Credentials:   map[string]any{"access_token": "sk-ant-oat-upstream-token", "refresh_token": "refresh-token"},
		Extra: map[string]any{
			AnthropicStableCanaryEnabledExtraKey:             true,
			AnthropicStableCanaryReservedExtraKey:            true,
			AnthropicStableCanaryPreviousSchedulableExtraKey: true,
			AnthropicStableCanaryDeviceIDExtraKey:            accountDevice,
			AnthropicStableCanaryProfileExtraKey:             AnthropicStableIngressProfileCLI211222V1,
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.AnthropicStableCanary = config.GatewayAnthropicStableCanaryConfig{
		Enabled: true, GroupID: groupID, AccountID: accountID, OwnerUserID: 1, APIKeyID: 91, MaxBodyBytes: 64 << 20,
	}
	service := &GatewayService{
		cfg:                   cfg,
		groupRepo:             &stableCanaryGroupRepoStub{group: group},
		anthropicStableCanary: newAnthropicStableCanaryRuntime(),
	}
	service.accountRepo = &stableCanaryRefreshRepoStub{account: account}
	service.anthropicStableCanary.clients["811"] = &http.Client{Transport: rt}

	body := []byte(`{"model":"claude-opus-5","max_tokens":4096,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"thinking":{"type":"enabled","budget_tokens":1024},"metadata":{"user_id":"{\"device_id\":\"` + clientDevice + `\",\"account_uuid\":\"\",\"session_id\":\"11111111-1111-4111-8111-111111111111\"}"},"tools":[{"name":"tool_a","input_schema":{"type":"object"}}],"stream":true}`)
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/messages?beta=true", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Content-Encoding", "identity")
	c.Request.Header.Set("User-Agent", "claude-cli/2.1.222 (external, cli)")
	c.Request.Header.Set("x-app", "cli")
	c.Request.Header.Set("X-Claude-Code-Session-Id", "11111111-1111-4111-8111-111111111111")
	c.Request.Header.Set("Authorization", "Bearer inbound")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("Cookie", "session=should-not-forward")
	c.Request.Header.Set("anthropic-version", "2023-06-01")
	c.Request.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
	return &stableCanaryTestFixture{service: service, account: account, ctx: c, rec: rec, body: body}
}

func TestAnthropicStableCanaryRawEntryPreservesWireAndStatus(t *testing.T) {
	const rawSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_raw\",\"usage\":{\"input_tokens\":5}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	var fixture *stableCanaryTestFixture
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, fixture.body, body)
		return &http.Response{
			StatusCode:    http.StatusCreated,
			Header:        http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"req-raw"}},
			Body:          io.NopCloser(strings.NewReader(rawSSE)),
			ContentLength: int64(len(rawSSE)),
			Request:       req,
		}, nil
	}))
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)
	result, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusCreated, fixture.rec.Code)
	require.Equal(t, rawSSE, fixture.rec.Body.String())
	require.Equal(t, "msg_raw", result.RequestID, "SSE message id is the side-channel request id when present")
	require.NotNil(t, result.FirstTokenMs, "the first semantic text delta must define client-visible TTFT")
}

func TestAnthropicStableCanarySharedModePreservesIdentityAndClaimsOwner(t *testing.T) {
	const rawSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_shared\",\"usage\":{\"input_tokens\":1}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	var fixture *stableCanaryTestFixture
	var upstreamBody []byte
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var err error
		upstreamBody, err = io.ReadAll(req.Body)
		require.NoError(t, err)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(rawSSE)), Request: req}, nil
	}))
	clientDevice := strings.Repeat("b", 64)
	fixture.body = []byte(strings.Replace(string(fixture.body), strings.Repeat("a", 64), clientDevice, 1))
	fixture.body = []byte(strings.Replace(string(fixture.body), `account_uuid\":\"\"`, `account_uuid\":\"22222222-2222-4222-8222-222222222222\"`, 1))
	fixture.ctx.Request.Body = io.NopCloser(bytes.NewReader(fixture.body))
	fixture.service.cfg.Gateway.AnthropicStableCanary.OwnerUserID = 0
	fixture.service.cfg.Gateway.AnthropicStableCanary.APIKeyID = 0
	fixture.service.cfg.Gateway.AnthropicStableCanary.SharedUsers = true
	fixture.service.cfg.Gateway.AnthropicStableCanary.SharedAPIKeyIDs = []int64{91, 92}
	fixture.service.cfg.Gateway.AnthropicStableCanary.SessionGeneration = 1
	fixture.service.cfg.Gateway.AnthropicStableCanary.SessionHMACKey = "0123456789abcdef0123456789abcdef"
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	result, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, 1002, time.Now())

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, fixture.body, upstreamBody)
	require.Len(t, upstreamBody, len(fixture.body))
	repo := fixture.service.accountRepo.(*stableCanaryRefreshRepoStub)
	require.Equal(t, int64(1002), repo.sessionOwner)
	require.Len(t, repo.sessionHash, 64)
	require.Len(t, repo.keyFingerprint, 64)
	require.Len(t, repo.policyFingerprint, 64)
	mode, _ := fixture.ctx.Get("anthropic_passthrough_mode")
	require.Equal(t, anthropicStableCanarySharedModeName, mode)
	generation, _ := fixture.ctx.Get("anthropic_stable_session_generation")
	require.Equal(t, int64(1), generation)
}

func TestAnthropicStableCanarySharedModeRejectsOwnerConflictBeforeEgress(t *testing.T) {
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected upstream request")
	}))
	fixture.service.cfg.Gateway.AnthropicStableCanary.OwnerUserID = 0
	fixture.service.cfg.Gateway.AnthropicStableCanary.APIKeyID = 0
	fixture.service.cfg.Gateway.AnthropicStableCanary.SharedUsers = true
	fixture.service.cfg.Gateway.AnthropicStableCanary.SharedAPIKeyIDs = []int64{91, 92}
	fixture.service.cfg.Gateway.AnthropicStableCanary.SessionGeneration = 1
	fixture.service.cfg.Gateway.AnthropicStableCanary.SessionHMACKey = "0123456789abcdef0123456789abcdef"
	repo := fixture.service.accountRepo.(*stableCanaryRefreshRepoStub)
	repo.sessionErr = ErrAnthropicStableCanarySessionOwnerConflict
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, 1002, time.Now())

	require.ErrorIs(t, err, ErrAnthropicStableCanarySessionOwnerConflict)
	require.Zero(t, requests)
	require.Equal(t, http.StatusConflict, fixture.rec.Code)
}

func TestAnthropicStableCanaryDoesNotCountProtocolEnvelopeAsFirstToken(t *testing.T) {
	const rawSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_envelope\",\"usage\":{\"input_tokens\":5}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"signature_delta\",\"signature\":\"opaque\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(rawSSE)),
			Request:    req,
		}, nil
	}))
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	result, err := fixture.service.ForwardAnthropicStableCanaryRaw(
		context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now(),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.FirstTokenMs, "message_start/signature/message_stop are protocol metadata, not visible model output")
	require.Equal(t, rawSSE, fixture.rec.Body.String())
}

func TestAnthropicStableCanaryRejectsNonStreamingBeforeUpstream(t *testing.T) {
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected upstream request")
	}))
	fixture.body = []byte(strings.Replace(string(fixture.body), `"stream":true`, `"stream":false`, 1))
	fixture.ctx.Request.Body = io.NopCloser(bytes.NewReader(fixture.body))
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())

	require.ErrorIs(t, err, ErrAnthropicStableIngressMalformed)
	require.Zero(t, requests)
	require.Equal(t, http.StatusBadRequest, fixture.rec.Code)
}

func TestAnthropicStableCanaryRejectsChannelPolicyThatWouldChangeOrDenyRawModel(t *testing.T) {
	tests := []struct {
		name    string
		channel Channel
		wantErr error
	}{
		{
			name: "restricted model",
			channel: Channel{
				ID: 901, Status: StatusActive, GroupIDs: []int64{71}, RestrictModels: true,
				ModelPricing: []ChannelModelPricing{{Platform: PlatformAnthropic, Models: []string{"claude-sonnet-4"}}},
			},
			wantErr: errAnthropicStableCanaryModelRestricted,
		},
		{
			name: "channel mapping",
			channel: Channel{
				ID: 902, Status: StatusActive, GroupIDs: []int64{71},
				ModelMapping: map[string]map[string]string{PlatformAnthropic: {"claude-opus-5": "claude-opus-5-20260801"}},
			},
			wantErr: errAnthropicStableCanaryAccountInvalid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return nil, errors.New("unexpected upstream request")
			}))
			fixture.service.channelService = NewChannelService(&stableCanaryChannelRepoStub{
				channel: tt.channel, groupPlatforms: map[int64]string{71: PlatformAnthropic},
			}, nil, nil, nil)
			strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

			_, err := fixture.service.ForwardAnthropicStableCanaryRaw(
				context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now(),
			)

			require.ErrorIs(t, err, tt.wantErr)
			require.Zero(t, requests, "channel policy must be enforced before stable egress")
		})
	}
}

func TestAnthropicStableCanaryDownstreamWriteFailureIsClientDisconnectOnly(t *testing.T) {
	const rawSSE = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"req-client-gone"}},
			Body:       io.NopCloser(strings.NewReader(rawSSE)),
			Request:    req,
		}, nil
	}))
	fixture.ctx.Writer = &failWriteResponseWriter{ResponseWriter: fixture.ctx.Writer}
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	result, err := fixture.service.ForwardAnthropicStableCanaryRaw(
		context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now(),
	)

	require.Error(t, err)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 1, requests)
	_, recordedAsUpstreamFailure := fixture.ctx.Get(OpsUpstreamErrorsKey)
	require.False(t, recordedAsUpstreamFailure, "a downstream socket failure must not pollute upstream SLA evidence")
}

func TestAnthropicStableCanaryMarksClientCancellationWithoutRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	const rawSSE = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(&stableCancelAfterChunkReader{chunk: []byte(rawSSE), cancel: cancel}),
			Request:    req,
		}, nil
	}))
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	result, err := fixture.service.ForwardAnthropicStableCanaryRaw(ctx, fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())

	require.ErrorIs(t, err, context.Canceled)
	require.NotNil(t, result)
	require.True(t, result.ClientDisconnect)
	require.Equal(t, 1, requests, "client cancellation must never start a replay")
	require.Equal(t, rawSSE, fixture.rec.Body.String(), "already received bytes are relayed exactly once")
}

func TestAnthropicStableCanaryReloadsCredentialsAfterAccountQueue(t *testing.T) {
	var authorization string
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		authorization = req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"permission_error","message":"fixture"}}`)),
			Request:    req,
		}, nil
	}))
	stale := *fixture.account
	stale.Credentials = shallowCopyMap(fixture.account.Credentials)
	stale.Credentials["access_token"] = "sk-ant-oat-stale-token"
	fresh := *fixture.account
	fresh.Credentials = shallowCopyMap(fixture.account.Credentials)
	fresh.Credentials["access_token"] = "sk-ant-oat-fresh-token"
	repo := &stableCanaryReloadRepoStub{stale: &stale, fresh: &fresh}
	fixture.service.accountRepo = repo
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, &stale, fixture.body, int64(9101), time.Now())

	require.Error(t, err)
	require.Equal(t, "Bearer sk-ant-oat-fresh-token", authorization)
	require.Equal(t, 1, repo.loads, "the executor reloads exactly once after acquiring the account slot")
}

func TestHashAnthropicStableCanarySessionIDDoesNotExposeRawUUID(t *testing.T) {
	const sessionID = "11111111-1111-4111-8111-111111111111"
	hash := HashAnthropicStableCanarySessionID(sessionID)
	require.Len(t, hash, 64)
	require.NotContains(t, hash, "11111111")
	require.Equal(t, hash, HashAnthropicStableCanarySessionID(sessionID))
	require.NotEqual(t, hash, HashAnthropicStableCanarySessionID("22222222-2222-4222-8222-222222222222"))
}

func TestAnthropicStableCanaryBlockPersistsFiniteReasonAndKeepsMemoryFence(t *testing.T) {
	repo := &stableCanaryDurableStateRepoStub{}
	runtime := newAnthropicStableCanaryRuntime()
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected upstream request")
	}))
	fixture.service.accountRepo = repo
	fixture.service.anthropicStableCanary = runtime

	fixture.service.blockAnthropicStableCanary(context.Background(), fixture.account.ID, "upstream leaked token text")

	require.Equal(t, 1, repo.calls)
	require.Equal(t, fixture.account.ID, repo.accountID)
	require.Equal(t, anthropicStableCanaryBlockReasonCredentialRejected, repo.reason)
	require.Equal(t, anthropicStableCanaryBlockReasonCredentialRejected, runtime.blockReason(fixture.account.ID))
	require.Equal(t, "refresh_failed", NormalizeAnthropicStableCanaryBlockReason(" refresh_failed "))
}

func TestAnthropicStableCanaryBlockPersistsAfterCallerCancellation(t *testing.T) {
	repo := &stableCanaryDurableStateRepoStub{}
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected upstream request")
	}))
	fixture.service.accountRepo = repo
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fixture.service.blockAnthropicStableCanary(ctx, fixture.account.ID, anthropicStableCanaryBlockReasonCredentialRejected)

	require.Equal(t, 1, repo.calls)
	require.NoError(t, repo.contextErr, "definitive rejection persistence must not inherit caller cancellation")
	require.False(t, repo.deadline.IsZero(), "detached persistence must remain bounded")
	require.LessOrEqual(t, time.Until(repo.deadline), anthropicStableCanaryDurableBlockTimeout)
}

func TestAnthropicStableCanaryRoundTripNeverEntersRedirectStateMachine(t *testing.T) {
	const rawRedirect = `{"type":"error","error":{"type":"redirect","message":"do not follow"}}`
	requests := 0
	getBodyCalls := 0
	client := &http.Client{Transport: stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"Location":     []string{"https://redirect.invalid/v1/messages"},
			},
			Body:    io.NopCloser(strings.NewReader(rawRedirect)),
			Request: req,
		}, nil
	})}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, AnthropicStableMessagesOriginV1+AnthropicStableMessagesPath, bytes.NewReader([]byte(`{"stream":true}`)))
	require.NoError(t, err)
	originalGetBody := request.GetBody
	request.GetBody = func() (io.ReadCloser, error) {
		getBodyCalls++
		return originalGetBody()
	}

	response, err := roundTripAnthropicStableCanary(client, request)

	require.NoError(t, err)
	require.Equal(t, http.StatusTemporaryRedirect, response.StatusCode)
	require.Equal(t, 1, requests)
	require.Zero(t, getBodyCalls, "redirect handling must not construct a replay request")
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, rawRedirect, string(body))
}

func TestAnthropicStableCanaryReturnsRedirectWithoutFollowOrReplay(t *testing.T) {
	const rawRedirect = `{"type":"error","error":{"type":"redirect","message":"do not follow"}}`
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"Location":     []string{"https://redirect.invalid/v1/messages"},
			},
			Body:    io.NopCloser(strings.NewReader(rawRedirect)),
			Request: req,
		}, nil
	}))
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())

	require.Error(t, err)
	require.Equal(t, 1, requests)
	require.Equal(t, http.StatusTemporaryRedirect, fixture.rec.Code)
	require.Equal(t, rawRedirect, fixture.rec.Body.String())
	require.Empty(t, fixture.service.anthropicStableCanary.blockReason(fixture.account.ID))
}

func TestAnthropicStableCanaryValidationHonorsDurableBlock(t *testing.T) {
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, errors.New("unexpected upstream request")
	}))
	fixture.account.Extra[AnthropicStableCanaryBlockedExtraKey] = true
	fixture.account.Extra[AnthropicStableCanaryBlockedReasonExtraKey] = anthropicStableCanaryBlockReasonCredentialRejected
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.GetAnthropicStableCanaryAccount(context.Background(), fixture.service.cfg.Gateway.AnthropicStableCanary.GroupID)

	require.Error(t, err)
	require.Equal(t, http.StatusOK, fixture.rec.Code, "account validation happens before writing a response")
}

func TestAnthropicStableCanaryDoesNotRetrySecondUnauthorized(t *testing.T) {
	var fixture *stableCanaryTestFixture
	requests := 0
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() == AnthropicStableRefreshURL {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"access_token":"sk-ant-oat-refreshed-token","token_type":"Bearer","expires_in":3600}`)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"authentication_error","message":"rejected"}}`)),
			Request:    req,
		}, nil
	}))
	fixture.service.accountRepo = &stableCanaryRefreshRepoStub{account: fixture.account}
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)
	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())
	require.Error(t, err)
	require.Equal(t, 3, requests, "one message, one refresh and one replay; a second 401 must not start another request")
	require.Equal(t, http.StatusUnauthorized, fixture.rec.Code)

	// The rejected credential epoch is paused in-process. A later request must
	// fail locally instead of starting another message/refresh cycle.
	nextRecorder := httptest.NewRecorder()
	nextContext, _ := gin.CreateTestContext(nextRecorder)
	nextContext.Request = fixture.ctx.Request.Clone(context.Background())
	nextContext.Request.Body = io.NopCloser(bytes.NewReader(fixture.body))
	strictStableCanaryProfileHeader(nextContext, AnthropicStableIngressProfileCLI211222V1)
	_, nextErr := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), nextContext, fixture.account, fixture.body, int64(9101), time.Now())
	require.Error(t, nextErr)
	require.Equal(t, 3, requests, "paused credential must not produce another upstream request")
	require.Equal(t, http.StatusServiceUnavailable, nextRecorder.Code)
}

func TestAnthropicStableCanaryDoesNotRetryNonUnauthorizedStatus(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{
					StatusCode: status,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"upstream_error","message":"rejected"}}`)),
					Request:    req,
				}, nil
			}))
			strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)
			_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())
			require.Error(t, err)
			require.Equal(t, 1, requests, "only an explicit 401 may start the refresh/replay sequence")
			require.Equal(t, status, fixture.rec.Code)
			require.Empty(t, fixture.service.anthropicStableCanary.blockReason(fixture.account.ID), "non-401 errors must not mutate canary credential state")
		})
	}
}

func TestAnthropicStableCanaryAcceptsCLIAndSDKCLIWithinCapturedFamily(t *testing.T) {
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_family\",\"usage\":{\"input_tokens\":1}}}\n\n" +
					"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
					"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			)),
			Request: req,
		}, nil
	}))
	fixture.account.Extra[AnthropicStableCanaryProfileExtraKey] = AnthropicStableIngressProfileSDKCLI211222V1
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)
	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, requests)
	require.Equal(t, http.StatusOK, fixture.rec.Code)
}

func TestAnthropicStableCanaryRejectsUnknownPersistedProfileBeforeUpstream(t *testing.T) {
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected upstream request")
	}))
	fixture.account.Extra[AnthropicStableCanaryProfileExtraKey] = "claude_cli_2_1_223_unreviewed"
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)
	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())
	require.Error(t, err)
	require.Zero(t, requests, "an unreviewed persisted identity must fail before egress")
	require.Equal(t, http.StatusServiceUnavailable, fixture.rec.Code)
}

func TestAnthropicStableCanaryPreservesDifferentDeviceBeforeUpstream(t *testing.T) {
	requests := 0
	var fixture *stableCanaryTestFixture
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, fixture.body, body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_identity\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")),
			Request:    req,
		}, nil
	}))
	fixture.body = bytes.Replace(
		fixture.body,
		[]byte(strings.Repeat("a", 64)),
		[]byte(strings.Repeat("b", 64)),
		1,
	)
	fixture.ctx.Request.Body = io.NopCloser(bytes.NewReader(fixture.body))
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(
		context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now(),
	)

	require.NoError(t, err)
	require.Equal(t, 1, requests, "the native client device must be forwarded without a gateway rewrite")
	require.Equal(t, http.StatusOK, fixture.rec.Code)
}

func TestAnthropicStableCanaryRejectsInvalidAccountIsolationBeforeUpstream(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*stableCanaryTestFixture)
	}{
		{name: "schedulable", mutate: func(f *stableCanaryTestFixture) { f.account.Schedulable = true }},
		{name: "concurrency", mutate: func(f *stableCanaryTestFixture) { f.account.Concurrency = 2 }},
		{name: "second account member", mutate: func(f *stableCanaryTestFixture) {
			f.service.groupRepo.(*stableCanaryGroupRepoStub).group.AccountCount = 2
		}},
		{name: "second group binding", mutate: func(f *stableCanaryTestFixture) {
			f.account.GroupIDs = append(f.account.GroupIDs, 72)
		}},
		{name: "missing previous schedulable", mutate: func(f *stableCanaryTestFixture) {
			delete(f.account.Extra, AnthropicStableCanaryPreviousSchedulableExtraKey)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return nil, errors.New("unexpected upstream request")
			}))
			tt.mutate(fixture)
			strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)
			_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())
			require.Error(t, err)
			require.Zero(t, requests)
		})
	}
}

func TestAnthropicStableCanary401RefreshUsesDedicatedWireAndReplaysOnce(t *testing.T) {
	const rawSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_refresh\",\"usage\":{\"input_tokens\":2}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	var fixture *stableCanaryTestFixture
	var requests []*http.Request
	var requestBodies [][]byte
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req)
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		requestBodies = append(requestBodies, body)
		switch len(requests) {
		case 1:
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"req-401"}},
				Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"authentication_error","message":"expired"}}`)),
				Request:    req,
			}, nil
		case 2:
			require.Equal(t, AnthropicStableRefreshURL, req.URL.String())
			require.Equal(t, http.MethodPost, req.Method)
			require.Empty(t, req.Header.Get("User-Agent"))
			require.Equal(t, AnthropicStableOAuthBetaV1, req.Header.Get("anthropic-beta"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"access_token":"sk-ant-oat-refreshed-token","token_type":"Bearer","expires_in":3600}`)),
				Request:    req,
			}, nil
		default:
			require.Equal(t, AnthropicStableMessagesOriginV1+AnthropicStableMessagesPath, req.URL.String())
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"req-replayed"}},
				Body:       io.NopCloser(strings.NewReader(rawSSE)),
				Request:    req,
			}, nil
		}
	}))
	fixture.service.accountRepo = &stableCanaryRefreshRepoStub{account: fixture.account}
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)
	result, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, requests, 3, "one message, one refresh and one replay")
	require.Equal(t, fixture.body, requestBodies[0], "the first attempt must use the original body bytes")
	require.Equal(t, requestBodies[0], requestBodies[2], "401 replay must reuse the exact original body")
	require.Equal(t, "Bearer sk-ant-oat-refreshed-token", requests[2].Header.Get("Authorization"))
	require.Equal(t, rawSSE, fixture.rec.Body.String())
	require.Equal(t, "sk-ant-oat-refreshed-token", fixture.account.GetCredential("access_token"))
	require.Equal(t, "refresh-token", fixture.account.GetCredential("refresh_token"), "an omitted refresh_token must preserve the existing rotating credential family")
}

func TestAnthropicStableCanaryCallerCancellationDoesNotPauseCredential(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fixture *stableCanaryTestFixture
	var refreshRepo *stableCanaryRefreshRepoStub
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == AnthropicStableRefreshURL {
			cancel()
			return nil, context.Canceled
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"authentication_error","message":"expired"}}`)),
			Request:    req,
		}, nil
	}))
	refreshRepo = &stableCanaryRefreshRepoStub{account: fixture.account}
	fixture.service.accountRepo = refreshRepo
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(ctx, fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())

	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, refreshRepo.blocked, "caller cancellation is not credential evidence")
	require.False(t, fixture.account.IsAnthropicStableCanaryBlocked())
	require.Empty(t, fixture.service.anthropicStableCanary.blockReason(fixture.account.ID))
}

func TestAnthropicStableCanaryAmbiguousRefreshTimeoutDurablyPausesCredential(t *testing.T) {
	var fixture *stableCanaryTestFixture
	var refreshRepo *stableCanaryRefreshRepoStub
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == AnthropicStableRefreshURL {
			if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.WroteHeaders != nil {
				trace.WroteHeaders()
			}
			return nil, context.DeadlineExceeded
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"authentication_error","message":"expired"}}`)),
			Request:    req,
		}, nil
	}))
	refreshRepo = &stableCanaryRefreshRepoStub{account: fixture.account}
	fixture.service.accountRepo = refreshRepo
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(
		context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now(),
	)

	require.Error(t, err)
	require.Equal(t, 1, refreshRepo.blocked, "a refresh timeout after egress makes the rotating credential family ambiguous")
	require.True(t, fixture.account.IsAnthropicStableCanaryBlocked())
	require.Equal(t, anthropicStableCanaryBlockReasonRefreshFailed, fixture.service.anthropicStableCanary.blockReason(fixture.account.ID))
}

func TestAnthropicStableCanaryCallerCancellationCannotHideAmbiguousRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fixture *stableCanaryTestFixture
	var refreshRepo *stableCanaryRefreshRepoStub
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() == AnthropicStableRefreshURL {
			if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.WroteHeaders != nil {
				trace.WroteHeaders()
			}
			cancel()
			return nil, context.Canceled
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"authentication_error","message":"expired"}}`)),
			Request:    req,
		}, nil
	}))
	refreshRepo = &stableCanaryRefreshRepoStub{account: fixture.account}
	fixture.service.accountRepo = refreshRepo
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(
		ctx, fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now(),
	)

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, refreshRepo.blocked, "caller cancellation cannot reopen a refresh family whose request already escaped")
	require.True(t, fixture.account.IsAnthropicStableCanaryBlocked())
}

func TestAnthropicStableCanaryTruncatedStreamRecordsBoundedOpsEvidence(t *testing.T) {
	const rawSSE = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"X-Request-Id": []string{"req-truncated"},
			},
			Body:    io.NopCloser(strings.NewReader(rawSSE)),
			Request: req,
		}, nil
	}))
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	result, err := fixture.service.ForwardAnthropicStableCanaryRaw(
		context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now(),
	)

	require.ErrorIs(t, err, ErrAnthropicStableResponseTruncated)
	require.NotNil(t, result)
	require.False(t, result.ClientDisconnect)
	require.Equal(t, rawSSE, fixture.rec.Body.String())
	rawEvents, ok := fixture.ctx.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, events, 1)
	require.Equal(t, "anthropic_stable_canary_stream_error", events[0].Kind)
	require.Equal(t, "truncated_before_terminal", events[0].Reason)
	require.Equal(t, "req-truncated", events[0].UpstreamRequestID)
	require.Empty(t, events[0].UpstreamRequestBody)
	require.Empty(t, events[0].UpstreamResponseBody)
}

func TestAnthropicStableCanaryTransientRefreshFailureDoesNotPauseCredential(t *testing.T) {
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() == AnthropicStableRefreshURL {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"error":"temporarily unavailable"}`)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"authentication_error","message":"expired"}}`)),
			Request:    req,
		}, nil
	}))
	refreshRepo := &stableCanaryRefreshRepoStub{account: fixture.account}
	fixture.service.accountRepo = refreshRepo
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now())

	require.Error(t, err)
	require.Equal(t, http.StatusServiceUnavailable, fixture.rec.Code)
	require.Equal(t, 2, requests, "one message and one refresh request; transient refresh failures are not replayable")
	require.Zero(t, refreshRepo.updates, "a failed refresh must not mutate credentials")
	require.Zero(t, refreshRepo.blocked, "a transient refresh failure must not persist a credential pause")
	require.False(t, fixture.account.IsAnthropicStableCanaryBlocked())
	require.Empty(t, fixture.service.anthropicStableCanary.blockReason(fixture.account.ID))
}

func TestAnthropicStableCanaryUnexpectedRefresh2xxDurablyPausesCredential(t *testing.T) {
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		if req.URL.String() == AnthropicStableRefreshURL {
			return &http.Response{
				StatusCode: http.StatusCreated,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"access_token":"sk-ant-oat-unexpected","token_type":"Bearer"}`)),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"authentication_error","message":"expired"}}`)),
			Request:    req,
		}, nil
	}))
	refreshRepo := &stableCanaryRefreshRepoStub{account: fixture.account}
	fixture.service.accountRepo = refreshRepo
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	_, err := fixture.service.ForwardAnthropicStableCanaryRaw(
		context.Background(), fixture.ctx, fixture.account, fixture.body, int64(9101), time.Now(),
	)

	require.Error(t, err)
	require.Equal(t, 2, requests, "an unexpected refresh 2xx must never replay the messages request")
	require.Zero(t, refreshRepo.updates)
	require.Equal(t, 1, refreshRepo.blocked)
	require.True(t, fixture.account.IsAnthropicStableCanaryBlocked())
	require.Equal(t, anthropicStableCanaryBlockReasonRefreshFailed, fixture.service.anthropicStableCanary.blockReason(fixture.account.ID))
}

func TestValidateAnthropicStableOAuthAccessTokenUsesRawReferencePrefix(t *testing.T) {
	for _, test := range []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "leading_space", token: " upstream"},
		{name: "api_key_prefix", token: "sk-ant-api03-key"},
		{name: "trailing_space", token: "sk-ant-oat-token "},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateAnthropicStableOAuthAccessToken(test.token))
		})
	}
	require.NoError(t, validateAnthropicStableOAuthAccessToken("sk-ant-oat-token"))
}

func TestValidateAnthropicStableOAuthRefreshTokenPreservesOpaqueBytes(t *testing.T) {
	require.NoError(t, validateAnthropicStableOAuthRefreshToken("refresh-token-without-padding"))
	for _, test := range []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "leading_space", token: " refresh-token"},
		{name: "trailing_space", token: "refresh-token "},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateAnthropicStableOAuthRefreshToken(test.token))
		})
	}
}
