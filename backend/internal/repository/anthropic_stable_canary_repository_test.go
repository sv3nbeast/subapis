package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAcquireAnthropicStableCanaryLeaseUsesOneSessionAndReleasesOnce(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const accountID = int64(202)
	lockID := anthropicStableCanaryAdvisoryLockID(accountID)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_lock($1)")).
		WithArgs(lockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_lock"}).AddRow(""))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(lockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_unlock"}).AddRow(true))

	repo := &accountRepository{sql: db}
	release, err := repo.AcquireAnthropicStableCanaryLease(context.Background(), accountID)
	require.NoError(t, err)
	require.NotNil(t, release)
	require.NoError(t, release())
	require.NoError(t, release(), "release must be idempotent and must not unlock a reused connection twice")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAcquireAnthropicStableCanaryLeaseDiscardsSessionWhenUnlockIsAmbiguous(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	const accountID = int64(203)
	lockID := anthropicStableCanaryAdvisoryLockID(accountID)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_lock($1)")).
		WithArgs(lockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_lock"}).AddRow(""))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_unlock($1)")).
		WithArgs(lockID).
		WillReturnError(errors.New("unlock connection lost"))
	mock.ExpectClose()

	repo := &accountRepository{sql: db}
	release, err := repo.AcquireAnthropicStableCanaryLease(context.Background(), accountID)
	require.NoError(t, err)
	require.NotNil(t, release)
	require.ErrorContains(t, release(), "release stable canary advisory lock")
	require.NoError(t, mock.ExpectationsWereMet(), "an ambiguous lock session must be physically discarded instead of returning to the pool")
}

type anthropicStableRepositoryFixture struct {
	apiKeys  *apiKeyRepository
	accounts *accountRepository
	groups   *groupRepository
	groupID  int64
	account  int64
	key      *service.APIKey
}

func newAnthropicStableRepositoryFixture(t *testing.T) *anthropicStableRepositoryFixture {
	t.Helper()
	ctx := context.Background()
	apiKeys, client := newAPIKeyRepoSQLite(t)
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "stable-reservation@test.local")

	group, err := client.Group.Create().
		SetName("stable-reserved-group").
		SetPlatform(service.PlatformAnthropic).
		SetStatus(service.StatusActive).
		SetIsExclusive(true).
		SetClaudeCodeOnly(true).
		SetRequireOauthOnly(true).
		Save(ctx)
	require.NoError(t, err)

	account, err := client.Account.Create().
		SetName("stable-reserved-account").
		SetPlatform(service.PlatformAnthropic).
		SetType(service.AccountTypeOAuth).
		SetStatus(service.StatusActive).
		SetSchedulable(false).
		SetConcurrency(1).
		SetCredentials(map[string]any{
			"access_token":  "sk-ant-oat-reserved-test",
			"refresh_token": "reserved-refresh-test",
		}).
		SetExtra(map[string]any{
			service.AnthropicStableCanaryEnabledExtraKey:             true,
			service.AnthropicStableCanaryReservedExtraKey:            true,
			service.AnthropicStableCanaryPreviousSchedulableExtraKey: true,
			service.AnthropicStableCanaryDeviceIDExtraKey:            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			service.AnthropicStableCanaryProfileExtraKey:             service.AnthropicStableIngressProfileCLI211222V1,
		}).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AccountGroup.Create().
		SetAccountID(account.ID).
		SetGroupID(group.ID).
		SetPriority(50).
		Save(ctx)
	require.NoError(t, err)

	keyEntity, err := client.APIKey.Create().
		SetUserID(user.ID).
		SetKey("sk-stable-reserved-owner").
		SetName("stable-reserved-owner").
		SetGroupID(group.ID).
		SetStatus(service.StatusAPIKeyActive).
		Save(ctx)
	require.NoError(t, err)

	return &anthropicStableRepositoryFixture{
		apiKeys:  apiKeys,
		accounts: newAccountRepositoryWithSQL(client, apiKeys.sql, nil),
		groups:   newGroupRepositoryWithSQL(client, apiKeys.sql),
		groupID:  group.ID,
		account:  account.ID,
		key:      apiKeyEntityToService(keyEntity),
	}
}

func TestAnthropicStableReservationKeepsAPIKeyPolicyAndUsageWritable(t *testing.T) {
	fixture := newAnthropicStableRepositoryFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	key := *fixture.key
	key.Status = service.StatusAPIKeyDisabled
	key.Quota = 25
	key.QuotaUsed = 3.5
	key.RateLimit5h = 7
	key.RateLimit1d = 12
	key.RateLimit7d = 20
	key.IPWhitelist = []string{"127.0.0.1"}
	require.NoError(t, fixture.apiKeys.Update(ctx, &key, service.APIKeyUpdateFields{
		Status: true, Quota: true, QuotaUsed: true, RateLimits: true, IPRules: true,
	}))
	require.NoError(t, fixture.apiKeys.UpdateLastUsed(ctx, key.ID, now))
	require.NoError(t, fixture.accounts.UpdateLastUsed(ctx, fixture.account))

	got, err := fixture.apiKeys.GetByID(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, service.StatusAPIKeyDisabled, got.Status)
	require.Equal(t, 25.0, got.Quota)
	require.Equal(t, 3.5, got.QuotaUsed)
	require.Equal(t, 7.0, got.RateLimit5h)
	require.Equal(t, []string{"127.0.0.1"}, got.IPWhitelist)
	require.NotNil(t, got.LastUsedAt)
	require.WithinDuration(t, now, *got.LastUsedAt, time.Second)
}

func TestAnthropicStableReservationRejectsIdentityAndMembershipMutation(t *testing.T) {
	fixture := newAnthropicStableRepositoryFixture(t)
	ctx := context.Background()

	otherGroup := &service.Group{
		Name: "stable-other-group", Platform: service.PlatformAnthropic,
		Status: service.StatusActive, RateMultiplier: 1,
	}
	require.NoError(t, fixture.groups.Create(ctx, otherGroup))

	key := *fixture.key
	key.GroupID = &otherGroup.ID
	require.ErrorIs(t, fixture.apiKeys.Update(ctx, &key, service.APIKeyUpdateFields{GroupID: true}), service.ErrAnthropicStableCanaryReserved)
	require.ErrorIs(t, fixture.apiKeys.Delete(ctx, key.ID), service.ErrAnthropicStableCanaryReserved)
	require.ErrorIs(t, fixture.apiKeys.Create(ctx, &service.APIKey{
		UserID: key.UserID, Key: "sk-stable-second-key", Name: "second", GroupID: &fixture.groupID,
		Status: service.StatusAPIKeyActive,
	}), service.ErrAnthropicStableCanaryReserved)

	account, err := fixture.accounts.GetByID(ctx, fixture.account)
	require.NoError(t, err)
	account.Name = "must-not-change"
	require.ErrorIs(t, fixture.accounts.Update(ctx, account), service.ErrAnthropicStableCanaryReserved)
	require.ErrorIs(t, fixture.accounts.Delete(ctx, fixture.account), service.ErrAnthropicStableCanaryReserved)

	reservedGroup, err := fixture.groups.GetByID(ctx, fixture.groupID)
	require.NoError(t, err)
	reservedGroup.Name = "must-not-change"
	require.ErrorIs(t, fixture.groups.Update(ctx, reservedGroup), service.ErrAnthropicStableCanaryReserved)
	require.ErrorIs(t, fixture.groups.Delete(ctx, fixture.groupID), service.ErrAnthropicStableCanaryReserved)
}

func TestAnthropicStableIdentityReservationRejectsGenericMembershipAndDelete(t *testing.T) {
	ctx := context.Background()
	apiKeys, client := newAPIKeyRepoSQLite(t)
	group, err := client.Group.Create().
		SetName("existing-anthropic-group").
		SetPlatform(service.PlatformAnthropic).
		SetStatus(service.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	account, err := client.Account.Create().
		SetName("stable-identity-managed-account").
		SetPlatform(service.PlatformAnthropic).
		SetType(service.AccountTypeOAuth).
		SetStatus(service.StatusActive).
		SetSchedulable(false).
		SetConcurrency(1).
		SetCredentials(map[string]any{
			"access_token": "sk-ant-oat-stable-identity",
		}).
		SetExtra(map[string]any{
			service.AnthropicStableIdentityEnabledExtraKey: true,
		}).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.AccountGroup.Create().SetAccountID(account.ID).SetGroupID(group.ID).SetPriority(1).Save(ctx)
	require.NoError(t, err)

	repo := newAccountRepositoryWithSQL(client, apiKeys.sql, nil)
	groups := newGroupRepositoryWithSQL(client, apiKeys.sql)
	require.ErrorIs(t, repo.BindGroups(ctx, account.ID, []int64{group.ID}), service.ErrAnthropicStableIdentityManaged)
	require.ErrorIs(t, repo.Delete(ctx, account.ID), service.ErrAnthropicStableIdentityManaged)
	_, err = groups.DeleteAccountGroupsByGroupID(ctx, group.ID)
	require.ErrorIs(t, err, service.ErrAnthropicStableIdentityManaged)
	_, err = groups.DeleteCascade(ctx, group.ID)
	require.ErrorIs(t, err, service.ErrAnthropicStableIdentityManaged)
}

func TestAnthropicStableReservationStateGuardWorksOnSQLite(t *testing.T) {
	fixture := newAnthropicStableRepositoryFixture(t)
	ctx := context.Background()

	require.ErrorIs(t, fixture.accounts.SetError(ctx, fixture.account, "must not mutate"), service.ErrAnthropicStableCanaryReserved)
	require.ErrorIs(t, fixture.accounts.ClearError(ctx, fixture.account), service.ErrAnthropicStableCanaryReserved)
	require.ErrorIs(t, fixture.accounts.SetSchedulable(ctx, fixture.account, true), service.ErrAnthropicStableCanaryReserved)

	ordinary, err := fixture.accounts.client.Account.Create().
		SetName("ordinary-sqlite-state-guard").
		SetPlatform(service.PlatformAnthropic).
		SetType(service.AccountTypeOAuth).
		SetStatus(service.StatusActive).
		SetSchedulable(true).
		SetConcurrency(1).
		SetCredentials(map[string]any{"access_token": "ordinary"}).
		SetExtra(map[string]any{}).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, fixture.accounts.SetError(ctx, ordinary.ID, "ordinary error"))
	require.NoError(t, fixture.accounts.ClearError(ctx, ordinary.ID))
	require.NoError(t, fixture.accounts.SetSchedulable(ctx, ordinary.ID, false))
	require.NoError(t, fixture.accounts.SetSchedulable(ctx, ordinary.ID, true))

	nullMarker, err := fixture.accounts.client.Account.Create().
		SetName("null-marker-sqlite-state-guard").
		SetPlatform(service.PlatformAnthropic).
		SetType(service.AccountTypeOAuth).
		SetStatus(service.StatusActive).
		SetSchedulable(true).
		SetConcurrency(1).
		SetCredentials(map[string]any{"access_token": "null-marker"}).
		SetExtra(map[string]any{service.AnthropicStableCanaryEnabledExtraKey: nil}).
		Save(ctx)
	require.NoError(t, err)
	require.ErrorIs(t, fixture.accounts.SetError(ctx, nullMarker.ID, "must fail closed"), service.ErrAnthropicStableCanaryReserved)
}
