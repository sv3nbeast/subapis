package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type stableIdentityDirectoryRepo struct {
	AccountRepository
	mu       sync.Mutex
	accounts []Account
	err      error
	calls    int
	bindings map[string]AnthropicStableIdentitySessionRouteBinding
}

type stableIdentityAdminAccountRepo struct {
	AccountRepository
	account *Account
	creates int
	binds   int
	updates int
}

func cloneStableIdentityAdminTestAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	copyAccount := *account
	copyAccount.GroupIDs = append([]int64(nil), account.GroupIDs...)
	copyAccount.Extra = shallowCopyMap(account.Extra)
	copyAccount.Credentials = shallowCopyMap(account.Credentials)
	return &copyAccount
}

func (r *stableIdentityAdminAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	return cloneStableIdentityAdminTestAccount(r.account), nil
}

func (r *stableIdentityAdminAccountRepo) Update(ctx context.Context, account *Account) error {
	if account == nil || !AnthropicStableIdentityMutationAuthorized(ctx, account.ID) {
		return ErrAnthropicStableIdentityManaged
	}
	r.account = cloneStableIdentityAdminTestAccount(account)
	r.updates++
	return nil
}

func (r *stableIdentityAdminAccountRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	if r.account == nil || r.account.ID != id || !AnthropicStableIdentityMutationAuthorized(ctx, id) {
		return ErrAnthropicStableIdentityManaged
	}
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	r.updates++
	return nil
}

func (r *stableIdentityAdminAccountRepo) CreateWithAccountGroups(ctx context.Context, account *Account, groups []AccountGroup) error {
	if account == nil || !AnthropicStableIdentityCreateAuthorized(ctx) {
		return ErrAnthropicStableIdentityManaged
	}
	if account.ID <= 0 {
		account.ID = 40
	}
	account.GroupIDs = make([]int64, 0, len(groups))
	account.AccountGroups = make([]AccountGroup, 0, len(groups))
	for _, binding := range groups {
		binding.AccountID = account.ID
		account.GroupIDs = append(account.GroupIDs, binding.GroupID)
		account.AccountGroups = append(account.AccountGroups, binding)
	}
	r.account = cloneStableIdentityAdminTestAccount(account)
	r.account.AccountGroups = append([]AccountGroup(nil), account.AccountGroups...)
	r.creates++
	return nil
}

func (r *stableIdentityAdminAccountRepo) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	if r.account == nil || r.account.ID != accountID || !AnthropicStableIdentityGroupMutationAuthorized(ctx, accountID) {
		return ErrAnthropicStableIdentityManaged
	}
	r.account.GroupIDs = append([]int64(nil), groupIDs...)
	r.binds++
	return nil
}

func (r *stableIdentityAdminAccountRepo) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return nil, nil
}

func (r *stableIdentityAdminAccountRepo) AcquireAnthropicStableCanaryLease(context.Context, int64) (func() error, error) {
	return func() error { return nil }, nil
}

type stableIdentityAdminGroupRepo struct {
	GroupRepository
	groups map[int64]*Group
}

func (r *stableIdentityAdminGroupRepo) GetByID(_ context.Context, id int64) (*Group, error) {
	group := r.groups[id]
	if group == nil {
		return nil, ErrGroupNotFound
	}
	copyGroup := *group
	return &copyGroup, nil
}

func (r *stableIdentityAdminGroupRepo) ListActiveByPlatform(_ context.Context, platform string) ([]Group, error) {
	groups := make([]Group, 0, len(r.groups))
	for _, group := range r.groups {
		if group == nil || !group.IsActive() || group.Platform != platform {
			continue
		}
		groups = append(groups, *group)
	}
	return groups, nil
}

func (r *stableIdentityDirectoryRepo) FindByExtraField(_ context.Context, key string, value any) ([]Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	requireStableIdentityDirectoryQuery(key, value)
	if r.err != nil {
		return nil, r.err
	}
	return append([]Account(nil), r.accounts...), nil
}

func (r *stableIdentityDirectoryRepo) ResolveAnthropicStableIdentitySessionRoute(
	_ context.Context,
	candidate AnthropicStableIdentitySessionRouteBinding,
) (*AnthropicStableIdentitySessionRouteBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bindings == nil {
		r.bindings = make(map[string]AnthropicStableIdentitySessionRouteBinding)
	}
	key := fmt.Sprintf("%d:%s", candidate.GroupID, candidate.SessionHash)
	bound, exists := r.bindings[key]
	if !exists {
		bound = candidate
		r.bindings[key] = bound
	}
	copyBinding := bound
	return &copyBinding, nil
}

func requireStableIdentityDirectoryQuery(key string, value any) {
	if key != AnthropicStableIdentityEnabledExtraKey || value != true {
		panic("unexpected stable identity directory query")
	}
}

func newStableIdentityAccountForTest(accountID int64, groupIDs []int64) *Account {
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
			AnthropicStableIdentityProfileExtraKey:             AnthropicStableIngressProfileCLI211222V1,
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

func newStableIdentityAdminFixture() (*adminServiceImpl, *stableIdentityAdminAccountRepo) {
	accountRepo := &stableIdentityAdminAccountRepo{account: &Account{
		ID: 40, Name: "one-switch-stable-account", Platform: PlatformAnthropic,
		Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 4,
		GroupIDs: []int64{12, 11},
		Credentials: map[string]any{
			"access_token":  "sk-ant-oat-one-switch-test",
			"refresh_token": "one-switch-refresh-token",
		},
		Extra: map[string]any{"operator_note": "preserve-me"},
	}}
	groupRepo := &stableIdentityAdminGroupRepo{groups: map[int64]*Group{
		11: {ID: 11, Name: "Claude latest", Platform: PlatformAnthropic, Status: StatusActive},
		12: {ID: 12, Name: "Claude history", Platform: PlatformAnthropic, Status: StatusActive},
	}}
	return &adminServiceImpl{accountRepo: accountRepo, groupRepo: groupRepo}, accountRepo
}

func TestAnthropicStableIdentityAccountIsCreatedAtomicallyAlreadyFenced(t *testing.T) {
	repo := &stableIdentityAdminAccountRepo{}
	groupRepo := &stableIdentityAdminGroupRepo{groups: map[int64]*Group{
		11: {ID: 11, Name: "Claude latest", Platform: PlatformAnthropic, Status: StatusActive},
	}}
	admin := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo, groupRepo: groupRepo}

	created, err := admin.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "created-stable", Platform: PlatformAnthropic, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "sk-ant-oat-atomic-create",
			"refresh_token": "atomic-create-refresh",
		},
		Extra:                   map[string]any{"operator_note": "preserve-me"},
		Concurrency:             6,
		GroupIDs:                []int64{11},
		AnthropicStableIdentity: true,
		SkipMixedChannelCheck:   true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.creates)
	require.Equal(t, created.ID, repo.account.ID)
	require.True(t, created.IsAnthropicStableIdentityEnabled())
	require.False(t, created.Schedulable)
	require.Equal(t, 1, created.Concurrency)
	require.Equal(t, []int64{11}, created.GroupIDs)
	require.Equal(t, "preserve-me", created.Extra["operator_note"])
	previousSchedulable, ok := created.AnthropicStableIdentityPreviousSchedulable()
	require.True(t, ok)
	require.True(t, previousSchedulable)
	previousConcurrency, ok := created.AnthropicStableIdentityPreviousConcurrency()
	require.True(t, ok)
	require.Equal(t, 6, previousConcurrency)
	require.NoError(t, ValidateAnthropicStableIdentityEnrolledAccount(created))
}

func TestAnthropicStableIdentityCreateUsesPlatformDefaultGroupWithoutExtraStableRoutingConfig(t *testing.T) {
	repo := &stableIdentityAdminAccountRepo{}
	groupRepo := &stableIdentityAdminGroupRepo{groups: map[int64]*Group{
		11: {ID: 11, Name: PlatformAnthropic + "-default", Platform: PlatformAnthropic, Status: StatusActive},
	}}
	admin := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo, groupRepo: groupRepo}

	created, err := admin.CreateAccount(context.Background(), &CreateAccountInput{
		Name: "default-group-stable", Platform: PlatformAnthropic, Type: AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "sk-ant-oat-default-group-create",
			"refresh_token": "default-group-refresh",
		},
		Concurrency:             3,
		AnthropicStableIdentity: true,
		SkipMixedChannelCheck:   true,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{11}, created.GroupIDs)
	require.True(t, created.IsAnthropicStableIdentityEnabled())
	require.False(t, created.Schedulable)
	require.Equal(t, 1, created.Concurrency)
}

func TestAnthropicStableIdentityOneSwitchUsesLiveGroupsAndDisableDoesNotRollThemBack(t *testing.T) {
	admin, repo := newStableIdentityAdminFixture()
	deviceID := strings.Repeat("d", 64)

	status, err := admin.ConfigureAnthropicStableIdentity(context.Background(), repo.account.ID, &AnthropicStableIdentityConfigureInput{
		DeviceID: deviceID,
	})
	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.Equal(t, []int64{11, 12}, status.GroupIDs)
	require.Equal(t, int64(1), status.Generation)
	require.False(t, status.Schedulable)
	require.Equal(t, 1, status.Concurrency)
	require.Equal(t, "preserve-me", repo.account.Extra["operator_note"])
	require.False(t, hasAnthropicStableIdentityLegacyRoutingMetadata(repo.account.Extra))
	require.Equal(t, []int64{12, 11}, repo.account.GroupIDs, "enabling must not rewrite ordinary group membership")
	require.Equal(t, 1, repo.updates)

	// A retried PUT is idempotent and does not rotate the fixed identity.
	retried, err := admin.ConfigureAnthropicStableIdentity(context.Background(), repo.account.ID, &AnthropicStableIdentityConfigureInput{
		DeviceID: deviceID,
	})
	require.NoError(t, err)
	require.Equal(t, status.Generation, retried.Generation)
	require.Equal(t, 1, repo.updates)

	// Group administration stays live while the identity is reserved.
	repo.account.GroupIDs = []int64{12}
	disabled, err := admin.DisableAnthropicStableIdentity(context.Background(), repo.account.ID)
	require.NoError(t, err)
	require.False(t, disabled.Enabled)
	require.True(t, disabled.Schedulable)
	require.Equal(t, 4, disabled.Concurrency)
	require.Equal(t, []int64{12}, repo.account.GroupIDs, "disabling must keep the current group set")
	require.Equal(t, "preserve-me", repo.account.Extra["operator_note"])
}

func TestAnthropicStableIdentityOneSwitchAtomicallyReplacesOAuthPassthrough(t *testing.T) {
	admin, repo := newStableIdentityAdminFixture()
	repo.account.Extra["anthropic_oauth_passthrough"] = true

	status, err := admin.ConfigureAnthropicStableIdentity(context.Background(), repo.account.ID, &AnthropicStableIdentityConfigureInput{
		DeviceID: strings.Repeat("c", 64),
	})

	require.NoError(t, err)
	require.True(t, status.Enabled)
	require.True(t, repo.account.IsAnthropicStableIdentityEnabled())
	require.False(t, repo.account.IsAnthropicOAuthPassthroughEnabled())
	_, exists := repo.account.Extra["anthropic_oauth_passthrough"]
	require.False(t, exists, "the mutually exclusive legacy mode must be removed in the stable-mode write")
	require.Equal(t, 1, repo.updates)
}

func TestAnthropicStableIdentityDisableBlockedAccountDoesNotRestoreGenericScheduling(t *testing.T) {
	admin, repo := newStableIdentityAdminFixture()
	_, err := admin.ConfigureAnthropicStableIdentity(context.Background(), repo.account.ID, &AnthropicStableIdentityConfigureInput{
		DeviceID: strings.Repeat("b", 64),
	})
	require.NoError(t, err)
	repo.account.Extra[AnthropicStableIdentityBlockedExtraKey] = true
	repo.account.Extra[AnthropicStableIdentityBlockedReasonExtraKey] = anthropicStableCanaryBlockReasonCredentialRejected

	status, err := admin.DisableAnthropicStableIdentity(context.Background(), repo.account.ID)

	require.NoError(t, err)
	require.False(t, status.Enabled)
	require.False(t, status.Schedulable)
	require.False(t, repo.account.Schedulable, "a rejected credential must require manual recovery")
	require.Equal(t, 4, repo.account.Concurrency)
}

func TestAnthropicStableIdentityOrdinaryEditCanChangeOnlyLiveGroupsAndEchoManagedExtra(t *testing.T) {
	admin, repo := newStableIdentityAdminFixture()
	_, err := admin.ConfigureAnthropicStableIdentity(context.Background(), repo.account.ID, &AnthropicStableIdentityConfigureInput{
		DeviceID: strings.Repeat("f", 64),
	})
	require.NoError(t, err)
	updatesBefore := repo.updates

	groups := []int64{11}
	concurrency := 1
	clearProxy := int64(0)
	updated, err := admin.UpdateAccount(context.Background(), repo.account.ID, &UpdateAccountInput{
		Name:                  "renamed-with-live-group",
		Extra:                 shallowCopyMap(repo.account.Extra),
		ProxyID:               &clearProxy,
		Concurrency:           &concurrency,
		Status:                StatusActive,
		GroupIDs:              &groups,
		SkipMixedChannelCheck: true,
	})
	require.NoError(t, err)
	require.Equal(t, "renamed-with-live-group", updated.Name)
	require.Equal(t, []int64{11}, updated.GroupIDs)
	require.Equal(t, 1, repo.binds)
	require.Equal(t, updatesBefore+1, repo.updates)
	require.True(t, updated.IsAnthropicStableIdentityEnabled())
	require.Equal(t, strings.Repeat("f", 64), updated.AnthropicStableIdentityDeviceID())

	tamperedExtra := shallowCopyMap(updated.Extra)
	tamperedExtra[AnthropicStableIdentityDeviceIDExtraKey] = strings.Repeat("0", 64)
	_, err = admin.UpdateAccount(context.Background(), repo.account.ID, &UpdateAccountInput{Extra: tamperedExtra})
	require.ErrorIs(t, err, ErrAnthropicStableIdentityManaged)
}

func TestAnthropicStableIdentityConfigureMigratesLegacySelectorsOnce(t *testing.T) {
	admin, repo := newStableIdentityAdminFixture()
	deviceID := strings.Repeat("e", 64)
	repo.account.Schedulable = false
	repo.account.Concurrency = 1
	repo.account.Extra = map[string]any{
		"operator_note":                                    "preserve-me",
		AnthropicStableIdentityEnabledExtraKey:             true,
		AnthropicStableIdentityStateExtraKey:               AnthropicStableIdentityStateActive,
		AnthropicStableIdentityDeviceIDExtraKey:            deviceID,
		AnthropicStableIdentityPreviousSchedulableExtraKey: true,
		AnthropicStableIdentityPreviousConcurrencyExtraKey: 4,
		AnthropicStableIdentityPreviousGroupIDsExtraKey:    []int64{11},
		AnthropicStableIdentityProfileExtraKey:             AnthropicStableIngressProfileCLI211222V1,
		AnthropicStableIdentityGroupIDsExtraKey:            []int64{11},
		AnthropicStableIdentityAPIKeyIDsExtraKey:           []int64{91},
		AnthropicStableIdentityAPIKeyGroupIDsExtraKey:      map[string]any{"91": int64(11)},
		AnthropicStableIdentityGenerationExtraKey:          int64(1),
		AnthropicStableIdentityCreatedAtExtraKey:           "2026-08-01T00:00:00Z",
		AnthropicStableIdentityUpdatedAtExtraKey:           "2026-08-01T00:00:00Z",
		AnthropicStableIdentityBlockedExtraKey:             false,
		AnthropicStableIdentityBlockedReasonExtraKey:       "",
	}

	status, err := admin.ConfigureAnthropicStableIdentity(context.Background(), repo.account.ID, &AnthropicStableIdentityConfigureInput{
		DeviceID: deviceID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), status.Generation)
	require.False(t, hasAnthropicStableIdentityLegacyRoutingMetadata(repo.account.Extra))
	require.Equal(t, "preserve-me", repo.account.Extra["operator_note"])
	require.Equal(t, 1, repo.updates)

	_, err = admin.ConfigureAnthropicStableIdentity(context.Background(), repo.account.ID, &AnthropicStableIdentityConfigureInput{
		DeviceID: deviceID,
	})
	require.NoError(t, err)
	require.Equal(t, 1, repo.updates, "the migrated representation must become idempotent")
}

func TestAnthropicStableIdentityAccountValidationAndGenericFence(t *testing.T) {
	account := newStableIdentityAccountForTest(41, []int64{11})
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

func TestAnthropicStableIdentityAdminGuardAllowsLiveGroupMembershipOnly(t *testing.T) {
	account := newStableIdentityAccountForTest(42, []int64{11, 12})
	addedGroups := []int64{11, 12, 13}
	removedGroups := []int64{11}
	require.NoError(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{GroupIDs: &addedGroups}))
	require.NoError(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{GroupIDs: &removedGroups}))

	clearProxy := int64(0)
	require.NoError(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{ProxyID: &clearProxy}))
	require.NoError(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{Credentials: map[string]any{
		"expires_at": account.Credentials["expires_at"],
	}}))
	require.ErrorIs(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{Credentials: map[string]any{
		"access_token": "sk-ant-oat-rotated-outside-lifecycle",
	}}), ErrAnthropicStableIdentityManaged)
	require.ErrorIs(t, validateAnthropicStableIdentityAdminUpdate(account, &UpdateAccountInput{Credentials: map[string]any{
		"model_mapping": map[string]any{"claude-opus-5": "claude-sonnet-4-6"},
	}}), ErrAnthropicStableIdentityManaged)

	groupAuthorized := WithAnthropicStableIdentityGroupMutationAuthorization(context.Background(), account.ID)
	require.True(t, AnthropicStableIdentityGroupMutationAuthorized(groupAuthorized, account.ID))
	require.False(t, AnthropicStableIdentityMutationAuthorized(groupAuthorized, account.ID),
		"group membership authorization must not unlock identity fields")
}

func TestAnthropicStableIdentityTransportChangesWithIdentityGeneration(t *testing.T) {
	account := newStableIdentityAccountForTest(46, []int64{11})
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

func newStableIdentityPoolService(repo *stableIdentityDirectoryRepo) *GatewayService {
	return &GatewayService{
		cfg:                           stableIdentityTestConfig(),
		accountRepo:                   repo,
		anthropicStableIdentityRoutes: newAnthropicStableIdentityRouteDirectory(),
	}
}

func TestAnthropicStableIdentityGroupPoolKeepsSessionSticky(t *testing.T) {
	first := newStableIdentityAccountForTest(51, []int64{11, 12})
	second := newStableIdentityAccountForTest(52, []int64{11})
	second.Extra[AnthropicStableIdentityDeviceIDExtraKey] = strings.Repeat("b", 64)
	repo := &stableIdentityDirectoryRepo{accounts: []Account{*first, *second}}
	svc := newStableIdentityPoolService(repo)

	managed, err := svc.HasAnthropicStableIdentityGroup(context.Background(), 11)
	require.NoError(t, err)
	require.True(t, managed)
	managed, err = svc.HasAnthropicStableIdentityGroup(context.Background(), 12)
	require.NoError(t, err)
	require.True(t, managed)
	managed, err = svc.HasAnthropicStableIdentityGroup(context.Background(), 99)
	require.NoError(t, err)
	require.False(t, managed)

	route, found, err := svc.ResolveAnthropicStableIdentityRoute(context.Background(), 11, 1001, "session-a")
	require.NoError(t, err)
	require.True(t, found)
	require.Contains(t, []int64{51, 52}, route.AccountID)

	again, found, err := svc.ResolveAnthropicStableIdentityRoute(context.Background(), 11, 1001, "session-a")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, route.AccountID, again.AccountID)
	require.Equal(t, route.KeyFingerprint, again.KeyFingerprint)
	require.Equal(t, 1, repo.calls)
}

func TestAnthropicStableIdentitySameClientSessionIsIsolatedByUser(t *testing.T) {
	account := newStableIdentityAccountForTest(53, []int64{11})
	repo := &stableIdentityDirectoryRepo{accounts: []Account{*account}}
	svc := newStableIdentityPoolService(repo)

	_, found, err := svc.ResolveAnthropicStableIdentityRoute(context.Background(), 11, 2001, "same-session")
	require.NoError(t, err)
	require.True(t, found)
	_, found, err = svc.ResolveAnthropicStableIdentityRoute(context.Background(), 11, 2002, "same-session")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, repo.bindings, 2, "different users must never share a durable conversation binding")
}

func TestAnthropicStableIdentityExistingSessionNeverSwitchesAfterPoolChange(t *testing.T) {
	first := newStableIdentityAccountForTest(54, []int64{11})
	second := newStableIdentityAccountForTest(55, []int64{11})
	second.Extra[AnthropicStableIdentityDeviceIDExtraKey] = strings.Repeat("b", 64)
	repo := &stableIdentityDirectoryRepo{accounts: []Account{*first, *second}}
	svc := newStableIdentityPoolService(repo)

	bound, found, err := svc.ResolveAnthropicStableIdentityRoute(context.Background(), 11, 3001, "sticky-session")
	require.NoError(t, err)
	require.True(t, found)

	repo.mu.Lock()
	if bound.AccountID == first.ID {
		repo.accounts = []Account{*second}
	} else {
		repo.accounts = []Account{*first}
	}
	repo.mu.Unlock()
	svc.InvalidateAnthropicStableIdentityRoutes()

	route, found, err := svc.ResolveAnthropicStableIdentityRoute(context.Background(), 11, 3001, "sticky-session")
	require.Nil(t, route)
	require.True(t, found)
	require.ErrorIs(t, err, errAnthropicStableIdentityRouteUnavailable)
}

func TestAnthropicStableIdentityReenrollmentFingerprintInvalidatesOldSession(t *testing.T) {
	account := newStableIdentityAccountForTest(56, []int64{11})
	repo := &stableIdentityDirectoryRepo{accounts: []Account{*account}}
	svc := newStableIdentityPoolService(repo)

	_, found, err := svc.ResolveAnthropicStableIdentityRoute(context.Background(), 11, 4001, "old-session")
	require.NoError(t, err)
	require.True(t, found)

	reenrolled := *account
	reenrolled.Extra = shallowCopyMap(account.Extra)
	reenrolled.Extra[AnthropicStableIdentityDeviceIDExtraKey] = strings.Repeat("c", 64)
	repo.mu.Lock()
	repo.accounts = []Account{reenrolled}
	repo.mu.Unlock()
	svc.InvalidateAnthropicStableIdentityRoutes()

	route, found, err := svc.ResolveAnthropicStableIdentityRoute(context.Background(), 11, 4001, "old-session")
	require.Nil(t, route)
	require.True(t, found)
	require.ErrorIs(t, err, errAnthropicStableIdentityRouteUnavailable)
}

func TestAnthropicStableIdentityDirectoryKeepsManagedGroupFailClosedOnRefreshFailure(t *testing.T) {
	account := newStableIdentityAccountForTest(57, []int64{11})
	repo := &stableIdentityDirectoryRepo{accounts: []Account{*account}}
	svc := newStableIdentityPoolService(repo)

	managed, err := svc.HasAnthropicStableIdentityGroup(context.Background(), 11)
	require.NoError(t, err)
	require.True(t, managed)

	repo.mu.Lock()
	repo.err = errors.New("database unavailable")
	repo.mu.Unlock()
	svc.anthropicStableIdentityRoutes.mu.Lock()
	svc.anthropicStableIdentityRoutes.loadedAt = time.Now().Add(-2 * anthropicStableIdentityRouteRefreshInterval)
	svc.anthropicStableIdentityRoutes.mu.Unlock()
	managed, err = svc.HasAnthropicStableIdentityGroup(context.Background(), 11)
	require.True(t, managed)
	require.Error(t, err)

	managed, err = svc.HasAnthropicStableIdentityGroup(context.Background(), 99)
	require.NoError(t, err)
	require.False(t, managed)
}

func TestAnthropicStableIdentityDirectoryFailsClaudeTrafficClosedWithoutSnapshot(t *testing.T) {
	repo := &stableIdentityDirectoryRepo{err: errors.New("database unavailable")}
	svc := newStableIdentityPoolService(repo)
	managed, err := svc.HasAnthropicStableIdentityGroup(context.Background(), 11)
	require.True(t, managed)
	require.Error(t, err)
}

func convertStableCanaryFixtureToIdentity(t *testing.T, fixture *stableCanaryTestFixture, _ int64) *AnthropicStableIdentityRoute {
	t.Helper()
	groupID := fixture.account.GroupIDs[0]
	deviceID := fixture.account.AnthropicStableCanaryDeviceID()
	fixture.account.Extra = map[string]any{
		AnthropicStableIdentityEnabledExtraKey:             true,
		AnthropicStableIdentityStateExtraKey:               AnthropicStableIdentityStateActive,
		AnthropicStableIdentityDeviceIDExtraKey:            deviceID,
		AnthropicStableIdentityPreviousSchedulableExtraKey: true,
		AnthropicStableIdentityPreviousConcurrencyExtraKey: 1,
		AnthropicStableIdentityProfileExtraKey:             AnthropicStableIngressProfileCLI211222V1,
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
	first := newStableIdentityAccountForTest(61, []int64{11})
	second := newStableIdentityAccountForTest(62, []int64{11})
	second.Extra[AnthropicStableIdentityDeviceIDExtraKey] = strings.Repeat("b", 64)
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
