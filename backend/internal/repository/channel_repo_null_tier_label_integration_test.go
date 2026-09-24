//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestChannelRepositoryToleratesNullTierLabel is the regression for the
// 2026-09-24 outage: migration 246 inserted pricing intervals without a
// tier_label, the column is nullable, and scanning NULL into a string made
// every ListAll fail. That took down the channel cache (channel pricing,
// routing and attribution for all traffic) and the admin channel list.
//
// The rows are written with raw SQL on purpose — the service layer always
// supplies a label, so only a migration or a manual edit can produce this
// shape, and that is exactly the path that broke production.
func TestChannelRepositoryToleratesNullTierLabel(t *testing.T) {
	ctx := context.Background()
	repo := NewChannelRepository(integrationDB)

	var channelID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`INSERT INTO channels (name, description, status) VALUES ('null-tier-label', '', 'active') RETURNING id`,
	).Scan(&channelID))
	t.Cleanup(func() {
		// Pricing, intervals and statistics rules all cascade from the channel.
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM channels WHERE id = $1`, channelID)
	})

	// User-billing pricing with one NULL-labelled and one labelled interval.
	var pricingID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, input_price, output_price)
		 VALUES ($1, 'grok', '["grok-4.7"]', 'token', 0.000002, 0.000006) RETURNING id`, channelID,
	).Scan(&pricingID))
	_, err := integrationDB.ExecContext(ctx,
		`INSERT INTO channel_pricing_intervals (pricing_id, min_tokens, max_tokens, tier_label, input_price, sort_order)
		 VALUES ($1, 0, 199999, NULL, 0.000002, 0), ($1, 199999, NULL, 'long', 0.000004, 1)`, pricingID)
	require.NoError(t, err)

	// Account-statistics pricing with a NULL-labelled interval: the same scan
	// shape lives in a separate loader and must be covered independently.
	var ruleID, statsPricingID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`INSERT INTO channel_account_stats_pricing_rules (channel_id, name) VALUES ($1, 'r') RETURNING id`, channelID,
	).Scan(&ruleID))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`INSERT INTO channel_account_stats_model_pricing (rule_id, platform, models, input_price)
		 VALUES ($1, 'grok', '["grok-4.7"]', 0.000001) RETURNING id`, ruleID,
	).Scan(&statsPricingID))
	_, err = integrationDB.ExecContext(ctx,
		`INSERT INTO channel_account_stats_pricing_intervals (pricing_id, min_tokens, tier_label, input_price)
		 VALUES ($1, 0, NULL, 0.000001)`, statsPricingID)
	require.NoError(t, err)

	// The label read back for each affected interval, in scope order.
	userLabels := func(ch *service.Channel) []string {
		var out []string
		for _, p := range ch.ModelPricing {
			for _, iv := range p.Intervals {
				out = append(out, iv.TierLabel)
			}
		}
		return out
	}
	statsLabels := func(ch *service.Channel) []string {
		var out []string
		for _, rule := range ch.AccountStatsPricingRules {
			for _, p := range rule.Pricing {
				for _, iv := range p.Intervals {
					out = append(out, iv.TierLabel)
				}
			}
		}
		return out
	}
	find := func(channels []service.Channel) *service.Channel {
		for i := range channels {
			if channels[i].ID == channelID {
				return &channels[i]
			}
		}
		return nil
	}

	// ListAll is what the channel cache is built from.
	all, err := repo.ListAll(ctx)
	require.NoError(t, err, "a NULL tier_label must not fail the channel cache load")
	ch := find(all)
	require.NotNil(t, ch)
	require.Equal(t, []string{"", "long"}, userLabels(ch),
		"NULL is read back as the empty label and a real label is preserved")
	require.Equal(t, []string{""}, statsLabels(ch),
		"account-statistics intervals tolerate NULL through their own loader")

	// List backs the admin channel page that surfaced the 500.
	page, _, err := repo.List(ctx, pagination.PaginationParams{Page: 1, PageSize: 200}, "", "null-tier-label")
	require.NoError(t, err, "a NULL tier_label must not fail the admin channel list")
	ch = find(page)
	require.NotNil(t, ch)
	require.Equal(t, []string{"", "long"}, userLabels(ch))
}
