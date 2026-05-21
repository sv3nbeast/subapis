package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepositoryEnsureWebChatKeyReusesExistingHiddenKey(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &apiKeyRepository{sql: db}
	ctx := context.Background()
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	groupID := int64(20)

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "key", "name", "group_id", "status", "source", "is_hidden",
		"quota", "quota_used", "rate_limit_5h", "rate_limit_1d", "rate_limit_7d",
		"created_at", "updated_at",
	}).AddRow(
		int64(10), int64(1), "sk-existing-web-chat", "Web Chat / Claude",
		groupID, service.StatusAPIKeyActive, service.APIKeySourceWebChat, true,
		0.0, 0.0, 0.0, 0.0, 0.0, now, now,
	)

	mock.ExpectQuery("SELECT id, user_id, key, name, group_id, status, source, is_hidden").
		WithArgs(int64(1), groupID, service.APIKeySourceWebChat).
		WillReturnRows(rows)

	got, created, err := repo.EnsureWebChatKey(ctx, 1, groupID, "Claude", "sk-new-web-chat")
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, int64(10), got.ID)
	require.Equal(t, "sk-existing-web-chat", got.Key)
	require.Equal(t, service.APIKeySourceWebChat, got.Source)
	require.True(t, got.IsHidden)
	require.NotNil(t, got.GroupID)
	require.Equal(t, groupID, *got.GroupID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAPIKeyRepositoryEnsureWebChatKeyCreatesHiddenKeyWhenMissing(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &apiKeyRepository{sql: db}
	ctx := context.Background()
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	groupID := int64(30)

	mock.ExpectQuery("SELECT id, user_id, key, name, group_id, status, source, is_hidden").
		WithArgs(int64(2), groupID, service.APIKeySourceWebChat).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "key", "name", "group_id", "status", "source", "is_hidden",
			"quota", "quota_used", "rate_limit_5h", "rate_limit_1d", "rate_limit_7d",
			"created_at", "updated_at",
		}))

	insertRows := sqlmock.NewRows([]string{
		"id", "user_id", "key", "name", "group_id", "status", "source", "is_hidden",
		"quota", "quota_used", "rate_limit_5h", "rate_limit_1d", "rate_limit_7d",
		"created_at", "updated_at",
	}).AddRow(
		int64(11), int64(2), "sk-new-web-chat", "Web Chat / Group 30",
		groupID, service.StatusAPIKeyActive, service.APIKeySourceWebChat, true,
		0.0, 0.0, 0.0, 0.0, 0.0, now, now,
	)

	mock.ExpectQuery("INSERT INTO api_keys").
		WithArgs(
			int64(2),
			"sk-new-web-chat",
			"Web Chat / Group 30",
			groupID,
			service.StatusAPIKeyActive,
			service.APIKeySourceWebChat,
			service.StatusAPIKeyDisabled,
		).
		WillReturnRows(insertRows)

	got, created, err := repo.EnsureWebChatKey(ctx, 2, groupID, "", "sk-new-web-chat")
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, int64(11), got.ID)
	require.Equal(t, "sk-new-web-chat", got.Key)
	require.Equal(t, "Web Chat / Group 30", got.Name)
	require.Equal(t, service.APIKeySourceWebChat, got.Source)
	require.True(t, got.IsHidden)
	require.NoError(t, mock.ExpectationsWereMet())
}
