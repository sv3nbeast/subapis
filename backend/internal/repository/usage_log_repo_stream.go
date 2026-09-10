package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	// usageLogStreamDefaultBatchSize balances per-query cost against round trips
	// for CSV exports; each batch also triggers one association hydration pass.
	usageLogStreamDefaultBatchSize = 2000
	usageLogStreamMinBatchSize     = 100
	usageLogStreamMaxBatchSize     = 5000
)

// usageLogStreamOrder is the whitelisted keyset traversal order.
type usageLogStreamOrder struct {
	byCreatedAt bool
	desc        bool
}

// usageLogStreamCursor points at the last delivered row; the next batch starts
// strictly after it in traversal order so rows are never skipped or repeated.
type usageLogStreamCursor struct {
	createdAt time.Time
	id        int64
}

// StreamWithFilters delivers every usage log matching filters via keyset pagination
// ordered by (created_at, id) or id. Each query is bounded by the batch size and runs
// outside any long-lived transaction, so slow consumers never pin a connection or a
// snapshot; ctx cancellation (client disconnect) stops the iteration between batches.
//
// When opts.MaxRows > 0 the filtered row count is checked up front with a bounded
// COUNT and usagestats.ErrUsageLogStreamTooLarge is returned before any batch is
// delivered. Rows appearing concurrently after that check are hard-capped as well.
func (r *usageLogRepository) StreamWithFilters(ctx context.Context, filters UsageLogFilters, opts usagestats.UsageLogStreamOptions, fn func(batch []service.UsageLog) error) error {
	if fn == nil {
		return errors.New("usage log stream: batch handler is required")
	}

	conditions, args := buildUsageLogFilterConditions(filters)
	whereClause := buildWhere(conditions)
	order := resolveUsageLogStreamOrder(opts)
	batchSize := normalizeUsageLogStreamBatchSize(opts.BatchSize)

	if opts.MaxRows > 0 {
		count, err := r.countUsageLogsBounded(ctx, whereClause, args, opts.MaxRows+1)
		if err != nil {
			return err
		}
		if count > opts.MaxRows {
			return usagestats.ErrUsageLogStreamTooLarge
		}
	}

	var (
		cursor    *usageLogStreamCursor
		delivered int64
	)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		query, queryArgs := buildUsageLogStreamQuery(whereClause, args, order, cursor, batchSize)
		logs, err := r.queryUsageLogs(ctx, query, queryArgs...)
		if err != nil {
			return err
		}
		if len(logs) == 0 {
			return nil
		}

		lastPage := len(logs) < batchSize
		if opts.MaxRows > 0 && delivered+int64(len(logs)) >= opts.MaxRows {
			// Only reachable when rows were inserted between the bounded count and
			// this batch; cap delivery so the export honors the advertised limit.
			logs = logs[:opts.MaxRows-delivered]
			lastPage = true
		}
		if len(logs) > 0 {
			if err := r.hydrateUsageLogAssociations(ctx, logs); err != nil {
				return err
			}
			if err := fn(logs); err != nil {
				return err
			}
			delivered += int64(len(logs))
		}
		if lastPage {
			return nil
		}

		last := logs[len(logs)-1]
		cursor = &usageLogStreamCursor{createdAt: last.CreatedAt, id: last.ID}
	}
}

// countUsageLogsBounded counts matching rows but never scans more than limit of them,
// keeping the pre-flight size check cheap on very large tables.
func (r *usageLogRepository) countUsageLogsBounded(ctx context.Context, whereClause string, args []any, limit int64) (int64, error) {
	countArgs := append(append(make([]any, 0, len(args)+1), args...), limit)
	query := fmt.Sprintf("SELECT COUNT(*) FROM (SELECT 1 FROM usage_logs %s LIMIT $%d) AS bounded", whereClause, len(countArgs))
	var count int64
	if err := scanSingleRow(ctx, r.sql, query, countArgs, &count); err != nil {
		return 0, err
	}
	return count, nil
}

func resolveUsageLogStreamOrder(opts usagestats.UsageLogStreamOptions) usageLogStreamOrder {
	sortBy := strings.ToLower(strings.TrimSpace(opts.SortBy))
	return usageLogStreamOrder{
		byCreatedAt: sortBy != "id",
		desc:        strings.ToLower(strings.TrimSpace(opts.SortOrder)) != "asc",
	}
}

func normalizeUsageLogStreamBatchSize(size int) int {
	switch {
	case size <= 0:
		return usageLogStreamDefaultBatchSize
	case size < usageLogStreamMinBatchSize:
		return usageLogStreamMinBatchSize
	case size > usageLogStreamMaxBatchSize:
		return usageLogStreamMaxBatchSize
	default:
		return size
	}
}

func (o usageLogStreamOrder) direction() string {
	if o.desc {
		return "DESC"
	}
	return "ASC"
}

func (o usageLogStreamOrder) orderByClause() string {
	dir := o.direction()
	if o.byCreatedAt {
		return fmt.Sprintf("created_at %s, id %s", dir, dir)
	}
	return "id " + dir
}

// cursorCondition returns the keyset predicate for the next batch. Row-value
// comparison lets PostgreSQL walk the (user_id, created_at) index directly.
func (o usageLogStreamOrder) cursorCondition(firstArg int) string {
	op := ">"
	if o.desc {
		op = "<"
	}
	if o.byCreatedAt {
		return fmt.Sprintf("(created_at, id) %s ($%d::timestamptz, $%d::bigint)", op, firstArg, firstArg+1)
	}
	return fmt.Sprintf("id %s $%d::bigint", op, firstArg)
}

func buildUsageLogStreamQuery(whereClause string, args []any, order usageLogStreamOrder, cursor *usageLogStreamCursor, limit int) (string, []any) {
	queryArgs := append(make([]any, 0, len(args)+3), args...)
	where := whereClause
	if cursor != nil {
		condition := order.cursorCondition(len(queryArgs) + 1)
		if order.byCreatedAt {
			queryArgs = append(queryArgs, cursor.createdAt, cursor.id)
		} else {
			queryArgs = append(queryArgs, cursor.id)
		}
		if where == "" {
			where = "WHERE " + condition
		} else {
			where += " AND " + condition
		}
	}
	queryArgs = append(queryArgs, limit)
	query := fmt.Sprintf("SELECT %s FROM usage_logs %s ORDER BY %s LIMIT $%d", usageLogSelectColumns, where, order.orderByClause(), len(queryArgs))
	return query, queryArgs
}
