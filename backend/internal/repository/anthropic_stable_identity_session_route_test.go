package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func stableIdentityRouteBindingForRepositoryTest() service.AnthropicStableIdentitySessionRouteBinding {
	return service.AnthropicStableIdentitySessionRouteBinding{
		GroupID:             71,
		OwnerUserID:         91,
		SessionHash:         strings.Repeat("a", 64),
		AccountID:           811,
		AccountGeneration:   3,
		IdentityFingerprint: strings.Repeat("b", 64),
		CandidateDeviceID:   strings.Repeat("d", 64),
		CandidateProfileID:  service.AnthropicStableIngressProfileCLI211222V1,
	}
}

func expectStableIdentityRouteResolve(
	mock sqlmock.Sqlmock,
	candidate service.AnthropicStableIdentitySessionRouteBinding,
	bound service.AnthropicStableIdentitySessionRouteBinding,
) {
	mock.ExpectQuery(`(?s)WITH existing AS .*INTERVAL '5 minutes'.*JOIN account_groups AS ag.*anthropic_stable_identity_device_id.*INSERT INTO anthropic_stable_identity_session_routes.*ON CONFLICT \(group_id, session_hash\).*RETURNING group_id`).
		WithArgs(candidate.GroupID, candidate.OwnerUserID, candidate.SessionHash, candidate.AccountID,
			candidate.AccountGeneration, candidate.IdentityFingerprint, candidate.CandidateDeviceID,
			candidate.CandidateProfileID, service.PlatformAnthropic, service.AccountTypeOAuth,
			service.AccountTypeSetupToken, service.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{
			"group_id", "owner_user_id", "session_hash", "account_id", "account_generation", "identity_fingerprint",
		}).AddRow(bound.GroupID, bound.OwnerUserID, bound.SessionHash, bound.AccountID,
			bound.AccountGeneration, bound.IdentityFingerprint))
}

func TestResolveAnthropicStableIdentitySessionRouteCreatesFirstBinding(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	candidate := stableIdentityRouteBindingForRepositoryTest()
	expectStableIdentityRouteResolve(mock, candidate, candidate)

	repo := &accountRepository{sql: db}
	bound, err := repo.ResolveAnthropicStableIdentitySessionRoute(context.Background(), candidate)
	require.NoError(t, err)
	require.Equal(t, candidate.GroupID, bound.GroupID)
	require.Equal(t, candidate.OwnerUserID, bound.OwnerUserID)
	require.Equal(t, candidate.SessionHash, bound.SessionHash)
	require.Equal(t, candidate.AccountID, bound.AccountID)
	require.Equal(t, candidate.AccountGeneration, bound.AccountGeneration)
	require.Equal(t, candidate.IdentityFingerprint, bound.IdentityFingerprint)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveAnthropicStableIdentitySessionRouteNeverReplacesExistingAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	candidate := stableIdentityRouteBindingForRepositoryTest()
	bound := candidate
	bound.AccountID = 812
	bound.AccountGeneration = 7
	bound.IdentityFingerprint = strings.Repeat("c", 64)
	expectStableIdentityRouteResolve(mock, candidate, bound)

	repo := &accountRepository{sql: db}
	got, err := repo.ResolveAnthropicStableIdentitySessionRoute(context.Background(), candidate)
	require.NoError(t, err)
	require.Equal(t, bound.AccountID, got.AccountID, "pool edits must not rewrite a conversation onto the new candidate")
	require.Equal(t, bound.AccountGeneration, got.AccountGeneration)
	require.Equal(t, bound.IdentityFingerprint, got.IdentityFingerprint)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveAnthropicStableIdentitySessionRouteRejectsStaleCandidateWithoutCreatingTombstone(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	candidate := stableIdentityRouteBindingForRepositoryTest()
	mock.ExpectQuery(`(?s)WITH existing AS .*INTERVAL '5 minutes'.*JOIN account_groups AS ag.*anthropic_stable_identity_device_id.*INSERT INTO anthropic_stable_identity_session_routes`).
		WithArgs(candidate.GroupID, candidate.OwnerUserID, candidate.SessionHash, candidate.AccountID,
			candidate.AccountGeneration, candidate.IdentityFingerprint, candidate.CandidateDeviceID,
			candidate.CandidateProfileID, service.PlatformAnthropic, service.AccountTypeOAuth,
			service.AccountTypeSetupToken, service.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{
			"group_id", "owner_user_id", "session_hash", "account_id", "account_generation", "identity_fingerprint",
		}))

	repo := &accountRepository{sql: db}
	got, err := repo.ResolveAnthropicStableIdentitySessionRoute(context.Background(), candidate)
	require.Nil(t, got)
	require.ErrorIs(t, err, service.ErrAnthropicStableIdentitySessionRouteUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveAnthropicStableIdentitySessionRouteRejectsOwnerMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	candidate := stableIdentityRouteBindingForRepositoryTest()
	bound := candidate
	bound.OwnerUserID++
	expectStableIdentityRouteResolve(mock, candidate, bound)

	repo := &accountRepository{sql: db}
	got, err := repo.ResolveAnthropicStableIdentitySessionRoute(context.Background(), candidate)
	require.Nil(t, got)
	require.ErrorIs(t, err, service.ErrAnthropicStableIdentitySessionRouteUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResolveAnthropicStableIdentitySessionRouteRejectsMalformedIdentityBeforeSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	candidate := stableIdentityRouteBindingForRepositoryTest()
	candidate.SessionHash = "raw-session-id-must-never-be-stored"

	repo := &accountRepository{sql: db}
	got, err := repo.ResolveAnthropicStableIdentitySessionRoute(context.Background(), candidate)
	require.Nil(t, got)
	require.ErrorIs(t, err, service.ErrAnthropicStableIdentitySessionRouteUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}
