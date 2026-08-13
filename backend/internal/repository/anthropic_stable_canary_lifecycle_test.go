package repository

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const (
	stableLifecycleGroupID   = int64(101)
	stableLifecycleAccountID = int64(202)
	stableLifecycleOwnerID   = int64(303)
	stableLifecycleAPIKeyID  = int64(404)
)

func stableLifecycleConfig() config.GatewayAnthropicStableCanaryConfig {
	return config.GatewayAnthropicStableCanaryConfig{
		GroupID: stableLifecycleGroupID, AccountID: stableLifecycleAccountID,
		OwnerUserID: stableLifecycleOwnerID, APIKeyID: stableLifecycleAPIKeyID,
		MaxBodyBytes: 64 << 20,
	}
}

func stableLifecycleDeviceID() string { return strings.Repeat("a", 64) }

func expectStableLifecycleLockedState(
	mock sqlmock.Sqlmock,
	schedulable bool,
	extra string,
	lastUsedAt any,
	keyState ...string,
) {
	keyStatus, userStatus := service.StatusAPIKeyActive, service.StatusActive
	if len(keyState) > 0 {
		keyStatus = keyState[0]
	}
	if len(keyState) > 1 {
		userStatus = keyState[1]
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).
		WithArgs(anthropicStableCanaryAdvisoryLockID(stableLifecycleAccountID)).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_xact_lock"}).AddRow(""))
	mock.ExpectQuery("SELECT platform, status, is_exclusive").
		WithArgs(stableLifecycleGroupID).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform", "status", "is_exclusive", "claude_code_only", "require_oauth_only",
			"fallback_group_id", "fallback_group_id_on_invalid_request", "model_routing_enabled",
		}).AddRow(service.PlatformAnthropic, service.StatusActive, true, true, true, nil, nil, false))
	mock.ExpectQuery("SELECT id, platform, type, status, schedulable, concurrency").
		WithArgs(stableLifecycleAccountID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "platform", "type", "status", "schedulable", "concurrency",
			"proxy_id", "proxy_fallback_origin_id", "parent_account_id", "auto_pause_on_expired",
			"expires_at", "rate_limit_reset_at", "overload_until", "temp_unschedulable_until",
			"credentials", "extra",
		}).AddRow(
			stableLifecycleAccountID, service.PlatformAnthropic, service.AccountTypeOAuth, service.StatusActive,
			schedulable, 1, nil, nil, nil, true, nil, nil, nil, nil,
			[]byte(`{"access_token":"sk-ant-oat-test","refresh_token":"refresh-test"}`), []byte(extra),
		))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT account_id FROM account_groups WHERE group_id = $1 ORDER BY account_id FOR UPDATE")).
		WithArgs(stableLifecycleGroupID).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(stableLifecycleAccountID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT group_id FROM account_groups WHERE account_id = $1 ORDER BY group_id FOR UPDATE")).
		WithArgs(stableLifecycleAccountID).
		WillReturnRows(sqlmock.NewRows([]string{"group_id"}).AddRow(stableLifecycleGroupID))
	mock.ExpectQuery("SELECT ak.id, ak.user_id, ak.group_id").
		WithArgs(stableLifecycleGroupID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "group_id", "status", "expires_at", "last_used_at", "user_status",
		}).AddRow(
			stableLifecycleAPIKeyID, stableLifecycleOwnerID, stableLifecycleGroupID,
			keyStatus, nil, lastUsedAt, userStatus,
		))
}

func TestRunAnthropicStableCanaryLifecycleEnableDryRunRollsBack(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectBegin()
	expectStableLifecycleLockedState(mock, true, `{}`, nil)
	mock.ExpectRollback()

	result, err := RunAnthropicStableCanaryLifecycle(context.Background(), db, AnthropicStableCanaryLifecycleInput{
		Action: AnthropicStableCanaryLifecycleEnable, Config: stableLifecycleConfig(),
		DeviceID: stableLifecycleDeviceID(), Profile: service.AnthropicStableIngressProfileCLI211222V1,
	})

	require.NoError(t, err)
	require.True(t, result.Validated)
	require.False(t, result.Executed)
	require.False(t, result.EnrolledBefore)
	require.True(t, result.EnrolledAfter)
	require.True(t, result.PreviousSchedulable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunAnthropicStableCanaryLifecycleEnableExecuteCommits(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectBegin()
	expectStableLifecycleLockedState(mock, true, `{}`, nil)
	mock.ExpectExec("UPDATE accounts SET extra").
		WithArgs(sqlmock.AnyArg(), stableLifecycleAccountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountChanged, stableLifecycleAccountID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	result, err := RunAnthropicStableCanaryLifecycle(context.Background(), db, AnthropicStableCanaryLifecycleInput{
		Action: AnthropicStableCanaryLifecycleEnable, Config: stableLifecycleConfig(),
		DeviceID: stableLifecycleDeviceID(), Profile: service.AnthropicStableIngressProfileCLI211222V1,
		Execute: true,
	})

	require.NoError(t, err)
	require.True(t, result.Validated)
	require.True(t, result.Executed)
	require.True(t, result.EnrolledAfter)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunAnthropicStableCanaryLifecycleEnableRejectsPreviouslyUsedKey(t *testing.T) {
	db, mock := newSQLMock(t)
	mock.ExpectBegin()
	expectStableLifecycleLockedState(mock, true, `{}`, time.Now())
	mock.ExpectRollback()

	_, err := RunAnthropicStableCanaryLifecycle(context.Background(), db, AnthropicStableCanaryLifecycleInput{
		Action: AnthropicStableCanaryLifecycleEnable, Config: stableLifecycleConfig(),
		DeviceID: stableLifecycleDeviceID(), Profile: service.AnthropicStableIngressProfileCLI211222V1,
		Execute: true,
	})

	require.ErrorContains(t, err, "never been used")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunAnthropicStableCanaryLifecycleDisableBlockedDoesNotRestoreScheduling(t *testing.T) {
	db, mock := newSQLMock(t)
	extra := `{
		"anthropic_stable_canary":true,
		"anthropic_stable_canary_reserved":true,
		"anthropic_stable_canary_previous_schedulable":true,
		"anthropic_stable_canary_device_id":"` + stableLifecycleDeviceID() + `",
		"anthropic_stable_canary_profile":"` + service.AnthropicStableIngressProfileCLI211222V1 + `",
		"anthropic_stable_canary_blocked":true,
		"anthropic_stable_canary_blocked_reason":"credential_rejected"
	}`
	mock.ExpectBegin()
	expectStableLifecycleLockedState(mock, false, extra, time.Now())
	mock.ExpectExec("UPDATE accounts SET extra").
		WithArgs(false, stableLifecycleAccountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountChanged, stableLifecycleAccountID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	result, err := RunAnthropicStableCanaryLifecycle(context.Background(), db, AnthropicStableCanaryLifecycleInput{
		Action: AnthropicStableCanaryLifecycleDisable, Config: stableLifecycleConfig(), Execute: true,
	})

	require.NoError(t, err)
	require.True(t, result.BlockedBefore)
	require.False(t, result.RestoredSchedulable)
	require.True(t, result.RequiresManualRecovery)
	require.False(t, result.EnrolledAfter)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunAnthropicStableCanaryLifecycleDisableDriftedGroupStillRetiresWithoutRestoring(t *testing.T) {
	db, mock := newSQLMock(t)
	extra := `{
		"anthropic_stable_canary":true,
		"anthropic_stable_canary_reserved":true,
		"anthropic_stable_canary_previous_schedulable":true,
		"anthropic_stable_canary_device_id":"` + stableLifecycleDeviceID() + `",
		"anthropic_stable_canary_profile":"` + service.AnthropicStableIngressProfileCLI211222V1 + `"
	}`
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_advisory_xact_lock($1)")).
		WithArgs(anthropicStableCanaryAdvisoryLockID(stableLifecycleAccountID)).
		WillReturnRows(sqlmock.NewRows([]string{"pg_advisory_xact_lock"}).AddRow(""))
	mock.ExpectQuery("SELECT platform, status, is_exclusive").
		WithArgs(stableLifecycleGroupID).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform", "status", "is_exclusive", "claude_code_only", "require_oauth_only",
			"fallback_group_id", "fallback_group_id_on_invalid_request", "model_routing_enabled",
		}).AddRow(service.PlatformAnthropic, service.StatusDisabled, true, true, true, nil, nil, false))
	mock.ExpectQuery("SELECT id, platform, type, status, schedulable, concurrency").
		WithArgs(stableLifecycleAccountID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "platform", "type", "status", "schedulable", "concurrency",
			"proxy_id", "proxy_fallback_origin_id", "parent_account_id", "auto_pause_on_expired",
			"expires_at", "rate_limit_reset_at", "overload_until", "temp_unschedulable_until",
			"credentials", "extra",
		}).AddRow(
			stableLifecycleAccountID, service.PlatformAnthropic, service.AccountTypeOAuth, service.StatusActive,
			false, 1, nil, nil, nil, true, nil, nil, nil, nil,
			[]byte(`{"access_token":"sk-ant-oat-test","refresh_token":"refresh-test"}`), []byte(extra),
		))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT account_id FROM account_groups WHERE group_id = $1 ORDER BY account_id FOR UPDATE")).
		WithArgs(stableLifecycleGroupID).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(stableLifecycleAccountID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT group_id FROM account_groups WHERE account_id = $1 ORDER BY group_id FOR UPDATE")).
		WithArgs(stableLifecycleAccountID).
		WillReturnRows(sqlmock.NewRows([]string{"group_id"}).AddRow(stableLifecycleGroupID))
	mock.ExpectQuery("SELECT ak.id, ak.user_id, ak.group_id").
		WithArgs(stableLifecycleGroupID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "group_id", "status", "expires_at", "last_used_at", "user_status",
		}).AddRow(
			stableLifecycleAPIKeyID, stableLifecycleOwnerID, stableLifecycleGroupID,
			service.StatusAPIKeyActive, nil, time.Now(), service.StatusActive,
		))
	mock.ExpectExec("UPDATE accounts SET extra").
		WithArgs(false, stableLifecycleAccountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO scheduler_outbox").
		WithArgs(service.SchedulerOutboxEventAccountChanged, stableLifecycleAccountID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	result, err := RunAnthropicStableCanaryLifecycle(context.Background(), db, AnthropicStableCanaryLifecycleInput{
		Action: AnthropicStableCanaryLifecycleDisable, Config: stableLifecycleConfig(), Execute: true,
	})

	require.NoError(t, err)
	require.True(t, result.Executed)
	require.False(t, result.RestoredSchedulable)
	require.True(t, result.RequiresManualRecovery)
	require.False(t, result.EnrolledAfter)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRunAnthropicStableCanaryLifecycleInspectAllowsDisabledOwnerKey(t *testing.T) {
	db, mock := newSQLMock(t)
	extra := `{
		"anthropic_stable_canary":true,
		"anthropic_stable_canary_reserved":true,
		"anthropic_stable_canary_previous_schedulable":true,
		"anthropic_stable_canary_device_id":"` + stableLifecycleDeviceID() + `",
		"anthropic_stable_canary_profile":"` + service.AnthropicStableIngressProfileCLI211222V1 + `"
	}`
	mock.ExpectBegin()
	expectStableLifecycleLockedState(
		mock, false, extra, time.Now(), service.StatusAPIKeyDisabled, service.StatusDisabled,
	)
	mock.ExpectRollback()

	result, err := RunAnthropicStableCanaryLifecycle(context.Background(), db, AnthropicStableCanaryLifecycleInput{
		Action: AnthropicStableCanaryLifecycleInspect, Config: stableLifecycleConfig(),
	})

	require.NoError(t, err)
	require.True(t, result.Validated)
	require.True(t, result.EnrolledBefore)
	require.False(t, result.Executed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateAnthropicStableLifecycleAPIKeyRequiresOneExactActiveKey(t *testing.T) {
	cfg := stableLifecycleConfig()
	valid := anthropicStableLifecycleAPIKey{
		ID: cfg.APIKeyID, UserID: cfg.OwnerUserID,
		GroupID: sql.NullInt64{Int64: cfg.GroupID, Valid: true},
		Status:  service.StatusAPIKeyActive, UserStatus: service.StatusActive,
	}
	require.NoError(t, validateAnthropicStableLifecycleAPIKey([]anthropicStableLifecycleAPIKey{valid}, cfg, true, true))

	other := valid
	other.ID++
	require.Error(t, validateAnthropicStableLifecycleAPIKey([]anthropicStableLifecycleAPIKey{valid, other}, cfg, true, false))
}

func TestValidateAnthropicStableLifecycleAPIKeyAllowsInactiveConfiguredKeyForDisable(t *testing.T) {
	cfg := stableLifecycleConfig()
	key := anthropicStableLifecycleAPIKey{
		ID: cfg.APIKeyID, UserID: cfg.OwnerUserID,
		GroupID: sql.NullInt64{Int64: cfg.GroupID, Valid: true},
		Status:  service.StatusAPIKeyDisabled, UserStatus: service.StatusDisabled,
	}
	require.NoError(t, validateAnthropicStableLifecycleAPIKey([]anthropicStableLifecycleAPIKey{key}, cfg, false, false))
}
