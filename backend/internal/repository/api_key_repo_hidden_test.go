package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepositoryHiddenWebChatKeyFilteredFromUserLists(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hidden-web-chat-list@test.com")

	visible := &service.APIKey{
		UserID: user.ID,
		Key:    "sk-visible-user-key",
		Name:   "Visible user key",
		Status: service.StatusAPIKeyActive,
	}
	hidden := &service.APIKey{
		UserID:   user.ID,
		Key:      "sk-hidden-web-chat-key",
		Name:     "Web Chat / Claude",
		Status:   service.StatusAPIKeyActive,
		Source:   service.APIKeySourceWebChat,
		IsHidden: true,
	}

	require.NoError(t, repo.Create(ctx, visible))
	require.NoError(t, repo.Create(ctx, hidden))

	keys, result, err := repo.ListByUserID(ctx, user.ID, pagination.PaginationParams{Page: 1, PageSize: 10}, service.APIKeyListFilters{})
	require.NoError(t, err)
	require.Equal(t, int64(1), result.Total)
	require.Len(t, keys, 1)
	require.Equal(t, visible.ID, keys[0].ID)

	count, err := repo.CountByUserID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	search, err := repo.SearchAPIKeys(ctx, user.ID, "Web Chat", 10)
	require.NoError(t, err)
	require.Empty(t, search)
}

func TestAPIKeyRepositoryHiddenWebChatKeyAllowedForAuth(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "hidden-web-chat-auth@test.com")

	hidden := &service.APIKey{
		UserID:   user.ID,
		Key:      "sk-hidden-web-chat-auth-key",
		Name:     "Web Chat / Claude",
		Status:   service.StatusAPIKeyActive,
		Source:   service.APIKeySourceWebChat,
		IsHidden: true,
	}
	require.NoError(t, repo.Create(ctx, hidden))

	got, err := repo.GetByKeyForAuth(ctx, hidden.Key)
	require.NoError(t, err)
	require.Equal(t, hidden.ID, got.ID)
	require.Equal(t, user.ID, got.UserID)
	require.NotNil(t, got.User)
	require.Equal(t, user.ID, got.User.ID)
}
