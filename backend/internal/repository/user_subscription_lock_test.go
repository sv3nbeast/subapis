package repository

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/ent/usersubscription"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestUserSubscriptionGetByIDForUpdateLocksRow(t *testing.T) {
	var capturedSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &capturedSQL}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	entDriver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(entDriver))
	t.Cleanup(func() { _ = client.Close() })
	repo := NewUserSubscriptionRepository(client)
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)

	// Build the mock row against the generated column names. The subscription
	// schema gained quota-cycle fields; pinning an old positional row silently
	// turns this lock regression test into a panic instead of exercising FOR UPDATE.
	values := make([]driver.Value, len(usersubscription.Columns))
	for i, column := range usersubscription.Columns {
		switch column {
		case usersubscription.FieldID:
			values[i] = int64(7)
		case usersubscription.FieldCreatedAt, usersubscription.FieldUpdatedAt, usersubscription.FieldStartsAt, usersubscription.FieldExpiresAt, usersubscription.FieldAssignedAt:
			values[i] = now
		case usersubscription.FieldUserID:
			values[i] = int64(11)
		case usersubscription.FieldGroupID:
			values[i] = int64(13)
		case usersubscription.FieldStatus:
			values[i] = "active"
		case usersubscription.FieldNotes:
			values[i] = "renewal"
		case usersubscription.FieldDailyUsageUsd, usersubscription.FieldWeeklyUsageUsd, usersubscription.FieldMonthlyUsageUsd:
			values[i] = 0.0
		default:
			values[i] = nil
		}
	}
	mock.ExpectQuery("locked subscription").WillReturnRows(sqlmock.NewRows(usersubscription.Columns).AddRow(values...))

	sub, err := repo.GetByIDForUpdate(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, int64(7), sub.ID)
	require.NoError(t, mock.ExpectationsWereMet())
	require.Contains(t, strings.ToUpper(normalizeSQLWhitespace(capturedSQL)), "FOR UPDATE")
}
