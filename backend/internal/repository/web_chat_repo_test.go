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

func TestWebChatRepositoryCreateTurnRejectsActiveGeneration(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newWebChatRepositoryWithSQL(db)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT active_leaf_message_id FROM web_chat_sessions").WithArgs(int64(88), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"active_leaf_message_id"}).AddRow(nil))
	mock.ExpectExec("UPDATE web_chat_messages SET status='partial'").WithArgs(int64(88), int64(7)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT EXISTS").WithArgs(int64(88), int64(7)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()
	_, _, err := repo.CreateTurn(context.Background(), 7, 88, "hello", "hello", nil)
	require.ErrorIs(t, err, service.ErrWebChatSessionBusy)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebChatRepositoryListProjectsScopesByOwner(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newWebChatRepositoryWithSQL(db)
	now := time.Now()
	mock.ExpectQuery("FROM web_chat_projects p WHERE p.user_id=\\$1").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "name", "description", "color", "sort_order", "default_group_id", "default_model", "default_template_id", "session_count", "created_at", "updated_at"}).AddRow(1, 7, "Work", "", "#14b8a6", 0, nil, "", nil, 2, now, now))
	items, err := repo.ListProjects(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(7), items[0].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestWebChatRepositoryListTemplatesOnlyReturnsOwnedPersonalOrSystem(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newWebChatRepositoryWithSQL(db)
	now := time.Now()
	mock.ExpectQuery("scope='personal' AND t.user_id=\\$1").WithArgs(int64(9), false).WillReturnRows(sqlmock.NewRows([]string{"id", "scope", "user_id", "source_template_id", "name", "category", "description", "body", "variables", "language", "enabled", "sort_order", "created_at", "updated_at"}).AddRow(2, "personal", 9, nil, "Memo", "office", "", "{{content}}", []byte(`[]`), "en", true, 0, now, now))
	items, err := repo.ListTemplates(context.Background(), 9, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(9), *items[0].UserID)
	require.NoError(t, mock.ExpectationsWereMet())
}
