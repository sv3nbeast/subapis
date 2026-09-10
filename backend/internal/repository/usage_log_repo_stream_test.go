package repository

import (
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestResolveUsageLogStreamOrder(t *testing.T) {
	cases := []struct {
		name  string
		opts  usagestats.UsageLogStreamOptions
		order usageLogStreamOrder
	}{
		{"defaults to created_at desc", usagestats.UsageLogStreamOptions{}, usageLogStreamOrder{byCreatedAt: true, desc: true}},
		{"created_at asc", usagestats.UsageLogStreamOptions{SortBy: "created_at", SortOrder: "asc"}, usageLogStreamOrder{byCreatedAt: true, desc: false}},
		{"id asc case-insensitive", usagestats.UsageLogStreamOptions{SortBy: " ID ", SortOrder: "ASC"}, usageLogStreamOrder{byCreatedAt: false, desc: false}},
		{"id desc", usagestats.UsageLogStreamOptions{SortBy: "id", SortOrder: "desc"}, usageLogStreamOrder{byCreatedAt: false, desc: true}},
		{"unsupported model sort falls back to created_at", usagestats.UsageLogStreamOptions{SortBy: "model", SortOrder: "asc"}, usageLogStreamOrder{byCreatedAt: true, desc: false}},
		{"garbage direction falls back to desc", usagestats.UsageLogStreamOptions{SortOrder: "sideways"}, usageLogStreamOrder{byCreatedAt: true, desc: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.order, resolveUsageLogStreamOrder(tc.opts))
		})
	}
}

func TestNormalizeUsageLogStreamBatchSize(t *testing.T) {
	require.Equal(t, usageLogStreamDefaultBatchSize, normalizeUsageLogStreamBatchSize(0))
	require.Equal(t, usageLogStreamDefaultBatchSize, normalizeUsageLogStreamBatchSize(-5))
	require.Equal(t, usageLogStreamMinBatchSize, normalizeUsageLogStreamBatchSize(10))
	require.Equal(t, usageLogStreamMaxBatchSize, normalizeUsageLogStreamBatchSize(99999))
	require.Equal(t, 1234, normalizeUsageLogStreamBatchSize(1234))
}

func TestBuildUsageLogStreamQuery(t *testing.T) {
	cursorTime := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC)

	t.Run("first page without filters", func(t *testing.T) {
		query, args := buildUsageLogStreamQuery("", nil, usageLogStreamOrder{byCreatedAt: true, desc: true}, nil, 500)
		require.Equal(t, "SELECT "+usageLogSelectColumns+" FROM usage_logs  ORDER BY created_at DESC, id DESC LIMIT $1", query)
		require.Equal(t, []any{500}, args)
	})

	t.Run("keyset page on created_at desc appends row-value predicate", func(t *testing.T) {
		where := "WHERE user_id = $1 AND created_at >= $2"
		baseArgs := []any{int64(42), cursorTime.Add(-time.Hour)}
		query, args := buildUsageLogStreamQuery(where, baseArgs, usageLogStreamOrder{byCreatedAt: true, desc: true}, &usageLogStreamCursor{createdAt: cursorTime, id: 77}, 500)
		require.Equal(t, "SELECT "+usageLogSelectColumns+" FROM usage_logs WHERE user_id = $1 AND created_at >= $2 AND (created_at, id) < ($3::timestamptz, $4::bigint) ORDER BY created_at DESC, id DESC LIMIT $5", query)
		require.Equal(t, []any{int64(42), cursorTime.Add(-time.Hour), cursorTime, int64(77), 500}, args)
		require.Equal(t, []any{int64(42), cursorTime.Add(-time.Hour)}, baseArgs, "caller args must not be mutated")
	})

	t.Run("keyset page on created_at asc flips the comparison", func(t *testing.T) {
		query, args := buildUsageLogStreamQuery("", nil, usageLogStreamOrder{byCreatedAt: true, desc: false}, &usageLogStreamCursor{createdAt: cursorTime, id: 77}, 100)
		require.Equal(t, "SELECT "+usageLogSelectColumns+" FROM usage_logs WHERE (created_at, id) > ($1::timestamptz, $2::bigint) ORDER BY created_at ASC, id ASC LIMIT $3", query)
		require.Equal(t, []any{cursorTime, int64(77), 100}, args)
	})

	t.Run("keyset page on id only needs the id cursor", func(t *testing.T) {
		query, args := buildUsageLogStreamQuery("WHERE user_id = $1", []any{int64(42)}, usageLogStreamOrder{byCreatedAt: false, desc: false}, &usageLogStreamCursor{createdAt: cursorTime, id: 77}, 100)
		require.Equal(t, "SELECT "+usageLogSelectColumns+" FROM usage_logs WHERE user_id = $1 AND id > $2::bigint ORDER BY id ASC LIMIT $3", query)
		require.Equal(t, []any{int64(42), int64(77), 100}, args)
	})
}

func TestBuildUsageLogFilterConditions_SharedByListAndStream(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 7)
	requestType := int16(2)
	billingType := int8(1)
	compaction := true
	filters := UsageLogFilters{
		UserID:             42,
		APIKeyID:           7,
		GroupID:            3,
		Model:              "gpt-5.4",
		ModelFilterSource:  usagestats.ModelSourceRequested,
		RequestType:        &requestType,
		NativeCompactionV2: &compaction,
		BillingType:        &billingType,
		BillingMode:        "token",
		StartTime:          &start,
		EndTime:            &end,
	}

	conditions, args := buildUsageLogFilterConditions(filters)
	require.Len(t, args, len(conditions), "every condition binds exactly one positional argument")
	require.Equal(t, "user_id = $1", conditions[0])
	require.Equal(t, "api_key_id = $2", conditions[1])
	require.Equal(t, "group_id = $3", conditions[2])
	require.Equal(t, "created_at >= $"+strconv.Itoa(len(args)-1), conditions[len(conditions)-2])
	require.Equal(t, "created_at < $"+strconv.Itoa(len(args)), conditions[len(conditions)-1])
	require.Equal(t, start, args[len(args)-2])
	require.Equal(t, end, args[len(args)-1])
	require.Equal(t, int64(42), args[0])
}
