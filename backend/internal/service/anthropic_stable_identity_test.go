package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type stableIdentityDirectoryRepo struct {
	AccountRepository
	accounts []Account
	err      error
	calls    int
}

func (r *stableIdentityDirectoryRepo) FindByExtraField(_ context.Context, key string, value any) ([]Account, error) {
	r.calls++
	requireStableIdentityDirectoryQuery(key, value)
	if r.err != nil {
		return nil, r.err
	}
	return append([]Account(nil), r.accounts...), nil
}

func requireStableIdentityDirectoryQuery(key string, value any) {
	if key != AnthropicStableIdentityEnabledExtraKey || value != true {
		panic("unexpected stable identity directory query")
	}
}

func newStableIdentityAccountForTest(accountID int64, groupIDs, keyIDs []int64, keyGroups map[int64]int64) *Account {
	device := strings.Repeat("a", 64)
	return &Account{
		ID: accountID, Name: "stable-identity-test", Platform: PlatformAnthropic,
		Type: AccountTypeOAuth, Status: StatusActive, Schedulable: false, Concurrency: 1,
		GroupIDs: append([]int64(nil), groupIDs...),
		Credentials: map[string]any{
			"access_token":  "sk-ant-oat-stable-identity-token",
			"refresh_token": "stable-identity-refresh-token",
		},
		Extra: map[string]any{
			AnthropicStableIdentityEnabledExtraKey:             true,
			AnthropicStableIdentityStateExtraKey:               AnthropicStableIdentityStateActive,
			AnthropicStableIdentityDeviceIDExtraKey:            device,
			AnthropicStableIdentityPreviousSchedulableExtraKey: true,
			AnthropicStableIdentityPreviousConcurrencyExtraKey: 4,
			AnthropicStableIdentityPreviousGroupIDsExtraKey:    append([]int64(nil), groupIDs...),
			AnthropicStableIdentityProfileExtraKey:             AnthropicStableIngressProfileCLI211222V1,
			AnthropicStableIdentityGroupIDsExtraKey:            append([]int64(nil), groupIDs...),
			AnthropicStableIdentityAPIKeyIDsExtraKey:           append([]int64(nil), keyIDs...),
			AnthropicStableIdentityAPIKeyGroupIDsExtraKey:      stableIdentityKeyGroupJSON(keyGroups),
			AnthropicStableIdentityGenerationExtraKey:          int64(1),
			AnthropicStableIdentityCreatedAtExtraKey:           "2026-08-14T00:00:00Z",
			AnthropicStableIdentityUpdatedAtExtraKey:           "2026-08-14T00:00:00Z",
			AnthropicStableIdentityBlockedExtraKey:             false,
			AnthropicStableIdentityBlockedReasonExtraKey:       "",
		},
	}
}

func stableIdentityTestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.JWT.Secret = strings.Repeat("s", 48)
	return cfg
}

func TestAnthropicStableIdentityAccountValidationAndGenericFence(t *testing.T) {
	account := newStableIdentityAccountForTest(41, []int64{11}, []int64{101}, map[int64]int64{101: 11})
	require.True(t, account.IsAnthropicStableIdentityEnabled())
	require.NoError(t, ValidateAnthropicStableIdentityEnrolledAccount(account))
	require.False(t, account.IsSchedulable(), "managed credentials must never enter the generic scheduler")

	provider := &ClaudeTokenProvider{}
	_, err := provider.GetAccessToken(context.Background(), account)
	require.ErrorIs(t, err, ErrAnthropicStableIdentityOutboundBlocked)

	account.Extra["anthropic_oauth_passthrough"] = true
	require.Error(t, ValidateAnthropicStableIdentityAccount(account))
	delete(account.Extra, "anthropic_oauth_passthrough")

	setupToken := *account
	setupToken.Type = AccountTypeSetupToken
	require.True(t, setupToken.IsAnthropicStableIdentityEnabled())
	require.NoError(t, ValidateAnthropicStableIdentityAccount(&setupToken))

	require.False(t, AnthropicStableIdentityMutationAuthorized(context.Background(), account.ID))
	authorized := withAnthropicStableIdentityMutationAuthorization(context.Background(), account.ID)
	require.True(t, AnthropicStableIdentityMutationAuthorized(authorized, account.ID))
	require.False(t, AnthropicStableIdentityMutationAuthorized(authorized, account.ID+1),
		"the lifecycle marker must never authorize a different managed account")
}

func TestAnthropicStableIdentityAdminGuardKeepsCapturedGroupRollbackExact(t *testing.T) {
	account := newStableIdentityAccountForTest(42, []int64{11, 12}, []int64{101}, map[int64]int64{101: 11})
	sameGroups := []int64{12, 11}
	require.NoError(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{GroupIDs: &sameGroups}))

	addedGroup := []int64{11, 12, 13}
	require.ErrorIs(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{GroupIDs: &addedGroup}), ErrAnthropicStableIdentityManaged)
	removedGroup := []int64{11}
	require.ErrorIs(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{GroupIDs: &removedGroup}), ErrAnthropicStableIdentityManaged)
	clearProxy := int64(0)
	require.NoError(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{ProxyID: &clearProxy}),
		"the edit form's proxy_id=0 representation is a semantic no-op for a proxy-free managed account")

	// The ordinary edit form always echoes a redacted credential object. A
	// semantic no-op must not make name/notes edits impossible while managed.
	require.NoError(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{Credentials: map[string]any{
		"expires_at": account.Credentials["expires_at"],
	}}))
	require.ErrorIs(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{Credentials: map[string]any{
		"access_token": "sk-ant-oat-rotated-outside-lifecycle",
	}}), ErrAnthropicStableIdentityManaged)
	require.ErrorIs(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{Credentials: map[string]any{
		"model_mapping": map[string]any{"claude-opus-5": "claude-sonnet-4-6"},
	}}), ErrAnthropicStableIdentityManaged)

	require.Equal(t, []int64{11, 12, 13}, unionAnthropicStableIdentityGroups([]int64{11, 12}, []int64{12, 13}),
		"lifecycle configuration must add selected existing groups without replacing captured original membership")
	require.Equal(t, []int64{11, 13}, unionAnthropicStableIdentityGroups([]int64{11}, []int64{13}),
		"reconfiguration uses the captured original groups, so a previously managed group can be deselected")
}

func TestAnthropicStableIdentityRollbackGroupsPreserveOrderAndCapturedEmptyState(t *testing.T) {
	account := newStableIdentityAccountForTest(45, []int64{11, 12}, []int64{101}, map[int64]int64{101: 11})
	account.Extra[AnthropicStableIdentityPreviousGroupIDsExtraKey] = []any{float64(12), float64(11)}
	groups, captured := account.AnthropicStableIdentityPreviousGroupIDs()
	require.True(t, captured)
	require.Equal(t, []int64{12, 11}, groups, "rollback must retain account-group priority order")

	account.Extra[AnthropicStableIdentityPreviousGroupIDsExtraKey] = []any{}
	groups, captured = account.AnthropicStableIdentityPreviousGroupIDs()
	require.True(t, captured, "an originally ungrouped account must not be mistaken for missing rollback metadata")
	require.Empty(t, groups)

	delete(account.Extra, AnthropicStableIdentityPreviousGroupIDsExtraKey)
	_, captured = account.AnthropicStableIdentityPreviousGroupIDs()
	require.False(t, captured)
	require.ErrorContains(t, ValidateAnthropicStableIdentityEnrolledAccount(account), "rollback state")
}

func TestAnthropicStableIdentityRouteOwnershipRejectsASecondAccountOnTheSameExistingKey(t *testing.T) {
	owner := newStableIdentityAccountForTest(43, []int64{11}, []int64{101}, map[int64]int64{101: 11})

	err := validateAnthropicStableIdentityRouteOwnership(44, map[int64]int64{101: 11}, []Account{*owner})
	require.ErrorIs(t, err, ErrAnthropicStableIdentityConflict)
	require.NoError(t, validateAnthropicStableIdentityRouteOwnership(43, map[int64]int64{101: 11}, []Account{*owner}),
		"reconfiguring the current account must retain its own exact route")
	require.NoError(t, validateAnthropicStableIdentityRouteOwnership(44, map[int64]int64{102: 11}, []Account{*owner}),
		"different API keys in the same existing group remain independently assignable")
}

func TestAnthropicStableIdentityTransportChangesWithIdentityGeneration(t *testing.T) {
	account := newStableIdentityAccountForTest(46, []int64{11}, []int64{101}, map[int64]int64{101: 11})
	svc := &GatewayService{anthropicStableCanary: newAnthropicStableCanaryRuntime()}

	first, err := svc.anthropicStableCanaryHTTPClient(account)
	require.NoError(t, err)
	require.NotNil(t, first)

	account.Extra[AnthropicStableIdentityGenerationExtraKey] = int64(2)
	account.Extra[AnthropicStableIdentityDeviceIDExtraKey] = strings.Repeat("b", 64)
	second, err := svc.anthropicStableCanaryHTTPClient(account)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.NotSame(t, first, second, "a new fixed identity must not inherit the previous TCP/TLS pool")
	require.Len(t, svc.anthropicStableCanary.clients, 1, "obsolete identity transports must not accumulate")
}

func TestAnthropicStableIdentityRouteDirectoryUsesExactExistingGroupAndAPIKeyPairs(t *testing.T) {
	account := newStableIdentityAccountForTest(51, []int64{11, 12}, []int64{101, 102}, map[int64]int64{101: 11, 102: 12})
	repo := &stableIdentityDirectoryRepo{accounts: []Account{*account}}
	svc := &GatewayService{
		cfg:                           stableIdentityTestConfig(),
		accountRepo:                   repo,
		anthropicStableIdentityRoutes: newAnthropicStableIdentityRouteDirectory(),
	}

	route, found, err := svc.LookupAnthropicStableIdentityRoute(context.Background(), 11, 101)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(51), route.AccountID)
	require.Equal(t, int64(11), route.GroupID)

	route, found, err = svc.LookupAnthropicStableIdentityRoute(context.Background(), 12, 102)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, int64(12), route.GroupID)

	// A selected key cannot be cross-producted into another selected group and
	// cannot fall through to generic routing when the tuple is inconsistent.
	route, found, err = svc.LookupAnthropicStableIdentityRoute(context.Background(), 11, 102)
	require.Nil(t, route)
	require.True(t, found)
	require.ErrorIs(t, err, errAnthropicStableIdentityRouteUnavailable)

	route, found, err = svc.LookupAnthropicStableIdentityRoute(context.Background(), 11, 999)
	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, route)
	require.Equal(t, 1, repo.calls)
}

func TestAnthropicStableIdentityRouteDirectoryFailsAmbiguousExistingGroupRouteClosed(t *testing.T) {
	first := newStableIdentityAccountForTest(53, []int64{11}, []int64{101}, map[int64]int64{101: 11})
	second := newStableIdentityAccountForTest(54, []int64{11}, []int64{101}, map[int64]int64{101: 11})
	repo := &stableIdentityDirectoryRepo{accounts: []Account{*first, *second}}
	svc := &GatewayService{
		cfg:                           stableIdentityTestConfig(),
		accountRepo:                   repo,
		anthropicStableIdentityRoutes: newAnthropicStableIdentityRouteDirectory(),
	}

	route, found, err := svc.LookupAnthropicStableIdentityRoute(context.Background(), 11, 101)
	require.Nil(t, route)
	require.True(t, found)
	require.ErrorContains(t, err, "ambiguous")
}

func TestAnthropicStableIdentityRouteDirectoryKeepsManagedKeysFailClosedOnRefreshFailure(t *testing.T) {
	account := newStableIdentityAccountForTest(52, []int64{11}, []int64{101}, map[int64]int64{101: 11})
	repo := &stableIdentityDirectoryRepo{accounts: []Account{*account}}
	svc := &GatewayService{
		cfg:                           stableIdentityTestConfig(),
		accountRepo:                   repo,
		anthropicStableIdentityRoutes: newAnthropicStableIdentityRouteDirectory(),
	}

	_, found, err := svc.LookupAnthropicStableIdentityRoute(context.Background(), 11, 101)
	require.NoError(t, err)
	require.True(t, found)

	repo.err = errors.New("database unavailable")
	svc.anthropicStableIdentityRoutes.mu.Lock()
	svc.anthropicStableIdentityRoutes.loadedAt = time.Now().Add(-2 * anthropicStableIdentityRouteRefreshInterval)
	svc.anthropicStableIdentityRoutes.mu.Unlock()
	_, found, err = svc.LookupAnthropicStableIdentityRoute(context.Background(), 11, 101)
	require.True(t, found)
	require.Error(t, err)

	_, found, err = svc.LookupAnthropicStableIdentityRoute(context.Background(), 11, 999)
	require.NoError(t, err, "ordinary keys must not suffer a global outage from the optional directory")
	require.False(t, found)
}

func TestAnthropicStableIdentityRouteDirectoryFailsExactTrafficClosedWithoutAnySnapshot(t *testing.T) {
	repo := &stableIdentityDirectoryRepo{err: errors.New("database unavailable")}
	svc := &GatewayService{
		cfg:                           stableIdentityTestConfig(),
		accountRepo:                   repo,
		anthropicStableIdentityRoutes: newAnthropicStableIdentityRouteDirectory(),
	}

	route, found, err := svc.LookupAnthropicStableIdentityRoute(context.Background(), 11, 101)
	require.Nil(t, route)
	require.True(t, found, "a cold process must not fail open an enrolled key through the generic scheduler")
	require.Error(t, err)
}

func convertStableCanaryFixtureToIdentity(t *testing.T, fixture *stableCanaryTestFixture, apiKeyID int64) *AnthropicStableIdentityRoute {
	t.Helper()
	groupID := fixture.account.GroupIDs[0]
	deviceID := fixture.account.AnthropicStableCanaryDeviceID()
	fixture.account.Extra = map[string]any{
		AnthropicStableIdentityEnabledExtraKey:             true,
		AnthropicStableIdentityStateExtraKey:               AnthropicStableIdentityStateActive,
		AnthropicStableIdentityDeviceIDExtraKey:            deviceID,
		AnthropicStableIdentityPreviousSchedulableExtraKey: true,
		AnthropicStableIdentityPreviousConcurrencyExtraKey: 1,
		AnthropicStableIdentityPreviousGroupIDsExtraKey:    []int64{groupID},
		AnthropicStableIdentityProfileExtraKey:             AnthropicStableIngressProfileCLI211222V1,
		AnthropicStableIdentityGroupIDsExtraKey:            []int64{groupID},
		AnthropicStableIdentityAPIKeyIDsExtraKey:           []int64{apiKeyID},
		AnthropicStableIdentityAPIKeyGroupIDsExtraKey:      stableIdentityKeyGroupJSON(map[int64]int64{apiKeyID: groupID}),
		AnthropicStableIdentityGenerationExtraKey:          int64(1),
		AnthropicStableIdentityBlockedExtraKey:             false,
		AnthropicStableIdentityBlockedReasonExtraKey:       "",
	}
	fixture.service.cfg.JWT.Secret = strings.Repeat("j", 48)
	route, err := stableIdentityRouteFromAccount(fixture.service.cfg, *fixture.account)
	require.NoError(t, err)
	require.NoError(t, bindAnthropicStableIdentityRouteGroup(route, groupID))
	// The shared fixture installs its fake transport under the static-canary
	// cache key before this helper replaces the account metadata. Move it to the
	// generation-bound identity key so tests can never reach the real network.
	legacyKey := fmt.Sprintf("%d", fixture.account.ID)
	identityKey := fmt.Sprintf("identity:%d:%d:%s", fixture.account.ID, route.Generation, route.DeviceID[:12])
	fixture.service.anthropicStableCanary.mu.Lock()
	client := fixture.service.anthropicStableCanary.clients[legacyKey]
	if client == nil {
		fixture.service.anthropicStableCanary.mu.Unlock()
		require.FailNow(t, "stable canary fixture transport is missing")
	}
	delete(fixture.service.anthropicStableCanary.clients, legacyKey)
	fixture.service.anthropicStableCanary.clients[identityKey] = client
	fixture.service.anthropicStableCanary.mu.Unlock()
	return route
}

func TestAnthropicStableIdentitySessionScopeSeparatesAccountsAndReenrollment(t *testing.T) {
	first := newStableIdentityAccountForTest(61, []int64{11}, []int64{101}, map[int64]int64{101: 11})
	second := newStableIdentityAccountForTest(62, []int64{11}, []int64{102}, map[int64]int64{102: 11})
	firstRoute, err := stableIdentityRouteFromAccount(stableIdentityTestConfig(), *first)
	require.NoError(t, err)
	secondRoute, err := stableIdentityRouteFromAccount(stableIdentityTestConfig(), *second)
	require.NoError(t, err)
	require.NoError(t, bindAnthropicStableIdentityRouteGroup(firstRoute, 11))
	require.NoError(t, bindAnthropicStableIdentityRouteGroup(secondRoute, 11))
	require.NotEqual(t, firstRoute.SessionScopeID, secondRoute.SessionScopeID,
		"different stable accounts in one existing group need independent durable session namespaces")
	require.GreaterOrEqual(t, firstRoute.SessionScopeID, int64(1)<<62)

	reenrolled := *first
	reenrolled.Extra = shallowCopyMap(first.Extra)
	reenrolled.Extra[AnthropicStableIdentityDeviceIDExtraKey] = strings.Repeat("c", 64)
	reenrolledRoute, err := stableIdentityRouteFromAccount(stableIdentityTestConfig(), reenrolled)
	require.NoError(t, err)
	require.NoError(t, bindAnthropicStableIdentityRouteGroup(reenrolledRoute, 11))
	require.NotEqual(t, firstRoute.SessionScopeID, reenrolledRoute.SessionScopeID,
		"disable and re-enroll must not reuse old owner bindings even if generation restarts")
}

func TestAnthropicStableIdentityRawForwardPatchesOnlyDeviceAndPreservesSSE(t *testing.T) {
	const apiKeyID = int64(91)
	const ownerUserID = int64(1002)
	const rawSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-stable\",\"usage\":{\"input_tokens\":2}}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	var fixture *stableCanaryTestFixture
	var upstreamBody []byte
	var upstreamHeader http.Header
	var upstreamURL string
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var err error
		upstreamBody, err = io.ReadAll(req.Body)
		require.NoError(t, err)
		upstreamHeader = req.Header.Clone()
		upstreamURL = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"req-stable"}},
			Body:       io.NopCloser(strings.NewReader(rawSSE)), Request: req,
		}, nil
	}))
	route := convertStableCanaryFixtureToIdentity(t, fixture, apiKeyID)
	clientDevice := strings.Repeat("b", 64)
	fixture.body = bytes.Replace(fixture.body, []byte(strings.Repeat("a", 64)), []byte(clientDevice), 1)
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	result, err := fixture.service.ForwardAnthropicStableIdentityRaw(
		context.Background(), fixture.ctx, fixture.account, route, fixture.body, apiKeyID, ownerUserID, time.Now(),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, rawSSE, fixture.rec.Body.String())
	expectedBody := bytes.Replace(fixture.body, []byte(clientDevice), []byte(route.DeviceID), 1)
	require.Equal(t, expectedBody, upstreamBody)
	require.Len(t, upstreamBody, len(fixture.body), "device patch must not re-encode or reorder JSON")
	require.Equal(t, AnthropicStableMessagesOriginV1+AnthropicStableMessagesPath, upstreamURL)
	require.Equal(t, "Bearer "+fixture.account.GetCredential("access_token"), upstreamHeader.Get("Authorization"))
	require.Equal(t, stableCanaryHandlerBetaForServiceTest()+","+AnthropicStableOAuthBetaV1, upstreamHeader.Get("anthropic-beta"))
	require.Equal(t, AnthropicStableIngressAPIVersionV1, upstreamHeader.Get("anthropic-version"))
	require.Empty(t, upstreamHeader.Get("User-Agent"), "the dedicated Go transport owns its User-Agent")
	require.Empty(t, upstreamHeader.Get("x-app"))
	require.Empty(t, upstreamHeader.Get("Cookie"))
	require.Empty(t, upstreamHeader.Get("X-Claude-Code-Session-Id"))
	require.NotNil(t, result.FirstTokenMs)
	repo := fixture.service.accountRepo.(*stableCanaryRefreshRepoStub)
	require.Equal(t, ownerUserID, repo.sessionOwner)
	require.Len(t, repo.sessionHash, 64)
	mode, _ := fixture.ctx.Get("anthropic_passthrough_mode")
	require.Equal(t, "stable_identity", mode)
}

func TestAnthropicStableIdentity401RefreshReplaysTheSamePatchedBodyOnce(t *testing.T) {
	const apiKeyID = int64(91)
	const rawSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-refresh\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	var fixture *stableCanaryTestFixture
	var requests []*http.Request
	var bodies [][]byte
	fixture = newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		requests = append(requests, req)
		bodies = append(bodies, body)
		switch len(requests) {
		case 1:
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"error"}`)), Request: req}, nil
		case 2:
			require.Equal(t, AnthropicStableRefreshURL, req.URL.String())
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"access_token":"sk-ant-oat-stable-refreshed","token_type":"Bearer","expires_in":3600}`)), Request: req}, nil
		default:
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(rawSSE)), Request: req}, nil
		}
	}))
	route := convertStableCanaryFixtureToIdentity(t, fixture, apiKeyID)
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	result, err := fixture.service.ForwardAnthropicStableIdentityRaw(
		context.Background(), fixture.ctx, fixture.account, route, fixture.body, apiKeyID, 1002, time.Now(),
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, requests, 3, "only first 401 may trigger one refresh and one replay")
	require.Equal(t, bodies[0], bodies[2])
	require.Equal(t, "Bearer sk-ant-oat-stable-refreshed", requests[2].Header.Get("Authorization"))
	require.Equal(t, rawSSE, fixture.rec.Body.String())
	repo := fixture.service.accountRepo.(*stableCanaryRefreshRepoStub)
	require.Equal(t, 1, repo.updates)
}

func TestAnthropicStableIdentityDoesNotRetryNonUnauthorizedResponse(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"type":"error"}`)), Request: req}, nil
			}))
			route := convertStableCanaryFixtureToIdentity(t, fixture, 91)
			strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

			_, err := fixture.service.ForwardAnthropicStableIdentityRaw(
				context.Background(), fixture.ctx, fixture.account, route, fixture.body, 91, 1002, time.Now(),
			)

			require.Error(t, err)
			require.Equal(t, 1, requests)
			require.Equal(t, status, fixture.rec.Code)
		})
	}
}

func TestAnthropicStableIdentityDoesNotReplayATruncatedAcceptedStream(t *testing.T) {
	const rawPartialSSE = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-truncated\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"
	requests := 0
	fixture := newStableCanaryTestFixture(t, stableCanaryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(rawPartialSSE)),
			Request:    req,
		}, nil
	}))
	route := convertStableCanaryFixtureToIdentity(t, fixture, 91)
	strictStableCanaryProfileHeader(fixture.ctx, AnthropicStableIngressProfileCLI211222V1)

	result, err := fixture.service.ForwardAnthropicStableIdentityRaw(
		context.Background(), fixture.ctx, fixture.account, route, fixture.body, 91, 1002, time.Now(),
	)

	require.ErrorIs(t, err, ErrAnthropicStableResponseTruncated)
	require.NotNil(t, result, "accepted partial output must retain usage/latency evidence")
	require.Equal(t, 1, requests, "an accepted upstream stream must never be replayed after downstream bytes exist")
	require.Equal(t, rawPartialSSE, fixture.rec.Body.String(), "the gateway must preserve, not repair, partial upstream bytes")
}

func stableCanaryHandlerBetaForServiceTest() string {
	return anthropicStableIngressProfiles[AnthropicStableIngressProfileCLI211222V1].beta
}
