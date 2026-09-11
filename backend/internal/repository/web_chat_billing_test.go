package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestWebChatBillingUsesMessageRouteAndOwnerNotCurrentSelection(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := newWebChatRepositoryWithSQL(db)
	items := []service.WebChatMessage{{ID: 1, Role: "user"}, {ID: 2, Role: "assistant"}, {ID: 3, Role: "assistant"}}
	mock.ExpectQuery(`u.user_id=m.user_id AND u.group_id=m.group_id.*m.user_id=\$1 AND m.id=ANY`).WithArgs(int64(7), "{2,3}").WillReturnRows(sqlmock.NewRows([]string{"id", "model", "platform", "cost"}).AddRow(2, "claude-opus-5", "anthropic", 0.12345678).AddRow(3, "", "", nil))
	require.NoError(t, repo.enrichWebChatBilling(context.Background(), 7, items))
	require.Equal(t, "claude-opus-5", items[1].Model)
	require.Equal(t, 0.12345678, *items[1].ActualCost)
	require.Empty(t, items[2].Model)
	require.Nil(t, items[2].ActualCost, "unknown historical/pending usage must not be estimated")
	require.NoError(t, mock.ExpectationsWereMet())
}
