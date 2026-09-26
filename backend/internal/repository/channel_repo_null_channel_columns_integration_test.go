//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestChannelRepositoryToleratesNullChannelColumns covers the same outage
// shape as the NULL tier_label regression, on the channels table itself:
// description, billing_model_source and restrict_models are nullable columns
// scanned into a string, a string and a bool. One NULL written by a migration
// or a manual edit would otherwise fail ListAll for every channel.
func TestChannelRepositoryToleratesNullChannelColumns(t *testing.T) {
	ctx := context.Background()
	repo := NewChannelRepository(integrationDB)

	var channelID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`INSERT INTO channels (name, description, status, billing_model_source, restrict_models)
		 VALUES ('null-channel-columns', NULL, 'active', NULL, NULL) RETURNING id`,
	).Scan(&channelID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM channels WHERE id = $1`, channelID)
	})

	assertDefaults := func(t *testing.T, ch *service.Channel) {
		t.Helper()
		require.NotNil(t, ch)
		require.Equal(t, "", ch.Description)
		require.Equal(t, "", ch.BillingModelSource, "the service layer normalizes empty to channel_mapped")
		require.False(t, ch.RestrictModels)
	}
	find := func(channels []service.Channel) *service.Channel {
		for i := range channels {
			if channels[i].ID == channelID {
				return &channels[i]
			}
		}
		return nil
	}

	all, err := repo.ListAll(ctx)
	require.NoError(t, err, "NULL channel columns must not fail the channel cache load")
	assertDefaults(t, find(all))

	page, _, err := repo.List(ctx, pagination.PaginationParams{Page: 1, PageSize: 200}, "", "null-channel-columns")
	require.NoError(t, err, "NULL channel columns must not fail the admin channel list")
	assertDefaults(t, find(page))

	ch, err := repo.GetByID(ctx, channelID)
	require.NoError(t, err)
	assertDefaults(t, ch)
}
