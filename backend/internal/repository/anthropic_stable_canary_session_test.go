package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestClaimAnthropicStableCanarySessionCreatesOrReusesSameOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sessionHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keyFingerprint := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	policyFingerprint := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	mock.ExpectQuery(regexp.QuoteMeta("WITH generation_gate AS (")+".*").
		WithArgs(int64(7), int64(8), int64(1), int64(9), sessionHash, keyFingerprint, policyFingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "owner_user_id", "key_fingerprint"}).
			AddRow(int64(8), int64(9), keyFingerprint))

	repo := &accountRepository{sql: db}
	err = repo.ClaimAnthropicStableCanarySession(context.Background(), 7, 8, 1, 9, sessionHash, keyFingerprint, policyFingerprint)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimAnthropicStableCanarySessionRejectsDifferentOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sessionHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keyFingerprint := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	policyFingerprint := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	mock.ExpectQuery(regexp.QuoteMeta("WITH generation_gate AS (")+".*").
		WithArgs(int64(7), int64(8), int64(1), int64(10), sessionHash, keyFingerprint, policyFingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "owner_user_id", "key_fingerprint"}).
			AddRow(int64(8), int64(9), keyFingerprint))

	repo := &accountRepository{sql: db}
	err = repo.ClaimAnthropicStableCanarySession(context.Background(), 7, 8, 1, 10, sessionHash, keyFingerprint, policyFingerprint)
	require.ErrorIs(t, err, service.ErrAnthropicStableCanarySessionOwnerConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimAnthropicStableCanarySessionFailsClosedOnKeyMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sessionHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keyFingerprint := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	policyFingerprint := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	mock.ExpectQuery(regexp.QuoteMeta("WITH generation_gate AS (")+".*").
		WithArgs(int64(7), int64(8), int64(1), int64(9), sessionHash, keyFingerprint, policyFingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "owner_user_id", "key_fingerprint"}))

	repo := &accountRepository{sql: db}
	err = repo.ClaimAnthropicStableCanarySession(context.Background(), 7, 8, 1, 9, sessionHash, keyFingerprint, policyFingerprint)
	require.ErrorIs(t, err, service.ErrAnthropicStableCanarySessionBindingUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimAnthropicStableCanarySessionSQLGuardsGenerationRollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	sessionHash := strings.Repeat("a", 64)
	keyFingerprint := strings.Repeat("b", 64)
	policyFingerprint := strings.Repeat("c", 64)
	mock.ExpectQuery(regexp.QuoteMeta("WITH generation_gate AS (")+".*").
		WithArgs(int64(7), int64(8), int64(1), int64(9), sessionHash, keyFingerprint, policyFingerprint).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "owner_user_id", "key_fingerprint"}))

	repo := &accountRepository{sql: db}
	err = repo.ClaimAnthropicStableCanarySession(context.Background(), 7, 8, 1, 9, sessionHash, keyFingerprint, policyFingerprint)
	require.ErrorIs(t, err, service.ErrAnthropicStableCanarySessionBindingUnavailable)
	require.NoError(t, mock.ExpectationsWereMet())
}
