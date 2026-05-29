package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *opsRepository) ListRequestDetails(ctx context.Context, filter *service.OpsRequestDetailFilter) ([]*service.OpsRequestDetail, int64, error) {
	if r == nil || r.db == nil {
		return nil, 0, fmt.Errorf("nil ops repository")
	}

	page, pageSize, startTime, endTime := filter.Normalize()
	offset := (page - 1) * pageSize

	conditions := make([]string, 0, 16)
	args := make([]any, 0, 24)

	// Placeholders $1/$2 reserved for time window inside the CTE.
	args = append(args, startTime.UTC(), endTime.UTC())

	addCondition := func(condition string, values ...any) {
		conditions = append(conditions, condition)
		args = append(args, values...)
	}

	if filter != nil {
		if kind := strings.TrimSpace(strings.ToLower(filter.Kind)); kind != "" && kind != "all" {
			if kind != string(service.OpsRequestKindSuccess) && kind != string(service.OpsRequestKindError) {
				return nil, 0, fmt.Errorf("invalid kind")
			}
			addCondition(fmt.Sprintf("c.kind = $%d", len(args)+1), kind)
		}

		if platform := strings.TrimSpace(strings.ToLower(filter.Platform)); platform != "" {
			addCondition(fmt.Sprintf("c.platform = $%d", len(args)+1), platform)
		}
		if filter.GroupID != nil && *filter.GroupID > 0 {
			addCondition(fmt.Sprintf("c.group_id = $%d", len(args)+1), *filter.GroupID)
		}

		if filter.UserID != nil && *filter.UserID > 0 {
			addCondition(fmt.Sprintf("c.user_id = $%d", len(args)+1), *filter.UserID)
		}
		if filter.APIKeyID != nil && *filter.APIKeyID > 0 {
			addCondition(fmt.Sprintf("c.api_key_id = $%d", len(args)+1), *filter.APIKeyID)
		}
		if filter.AccountID != nil && *filter.AccountID > 0 {
			addCondition(fmt.Sprintf("c.account_id = $%d", len(args)+1), *filter.AccountID)
		}

		if model := strings.TrimSpace(filter.Model); model != "" {
			addCondition(fmt.Sprintf("c.model = $%d", len(args)+1), model)
		}
		if requestID := strings.TrimSpace(filter.RequestID); requestID != "" {
			addCondition(fmt.Sprintf("c.request_id = $%d", len(args)+1), requestID)
		}
		if q := strings.TrimSpace(filter.Query); q != "" {
			like := "%" + strings.ToLower(q) + "%"
			startIdx := len(args) + 1
			addCondition(
				fmt.Sprintf("(LOWER(COALESCE(c.request_id,'')) LIKE $%d OR LOWER(COALESCE(c.model,'')) LIKE $%d OR LOWER(COALESCE(c.message,'')) LIKE $%d)",
					startIdx, startIdx+1, startIdx+2,
				),
				like, like, like,
			)
		}

		if filter.MinDurationMs != nil {
			addCondition(fmt.Sprintf("c.duration_ms >= $%d", len(args)+1), *filter.MinDurationMs)
		}
		if filter.MaxDurationMs != nil {
			addCondition(fmt.Sprintf("c.duration_ms <= $%d", len(args)+1), *filter.MaxDurationMs)
		}
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	cte := `
WITH combined AS (
  SELECT
    'success'::TEXT AS kind,
    ul.created_at AS created_at,
    ul.request_id AS request_id,
    COALESCE(NULLIF(g.platform, ''), NULLIF(a.platform, ''), '') AS platform,
    ul.model AS model,
    ul.duration_ms AS duration_ms,
    NULL::INT AS status_code,
    NULL::BIGINT AS error_id,
    NULL::TEXT AS phase,
    NULL::TEXT AS severity,
    NULL::TEXT AS message,
    ul.user_id AS user_id,
    ul.api_key_id AS api_key_id,
    ul.account_id AS account_id,
    ul.group_id AS group_id,
    ul.stream AS stream
  FROM usage_logs ul
  LEFT JOIN groups g ON g.id = ul.group_id
  LEFT JOIN accounts a ON a.id = ul.account_id
  WHERE ul.created_at >= $1 AND ul.created_at < $2

  UNION ALL

  SELECT
    'error'::TEXT AS kind,
    o.created_at AS created_at,
    COALESCE(NULLIF(o.request_id,''), NULLIF(o.client_request_id,''), '') AS request_id,
    COALESCE(NULLIF(o.platform, ''), NULLIF(g.platform, ''), NULLIF(a.platform, ''), '') AS platform,
    o.model AS model,
    o.duration_ms AS duration_ms,
    o.status_code AS status_code,
    o.id AS error_id,
    o.error_phase AS phase,
    o.severity AS severity,
    o.error_message AS message,
    o.user_id AS user_id,
    o.api_key_id AS api_key_id,
    o.account_id AS account_id,
    o.group_id AS group_id,
    o.stream AS stream
  FROM ops_error_logs o
  LEFT JOIN groups g ON g.id = o.group_id
  LEFT JOIN accounts a ON a.id = o.account_id
  WHERE o.created_at >= $1 AND o.created_at < $2
    AND COALESCE(o.status_code, 0) >= 400
)
`

	countQuery := fmt.Sprintf(`%s SELECT COUNT(1) FROM combined c %s`, cte, where)
	var total int64
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		if err == sql.ErrNoRows {
			total = 0
		} else {
			return nil, 0, err
		}
	}

	sort := "ORDER BY c.created_at DESC"
	if filter != nil {
		switch strings.TrimSpace(strings.ToLower(filter.Sort)) {
		case "", "created_at_desc":
			// default
		case "duration_desc":
			sort = "ORDER BY c.duration_ms DESC NULLS LAST, c.created_at DESC"
		default:
			return nil, 0, fmt.Errorf("invalid sort")
		}
	}

	listQuery := fmt.Sprintf(`
%s
SELECT
  c.kind,
  c.created_at,
  c.request_id,
  c.platform,
  c.model,
  c.duration_ms,
  c.status_code,
  c.error_id,
  c.phase,
  c.severity,
  c.message,
  c.user_id,
  COALESCE(u.email, u_api.email, '') AS user_email,
  c.api_key_id,
  COALESCE(k.name, '') AS api_key_name,
  c.account_id,
  COALESCE(a.name, '') AS account_name,
  c.group_id,
  COALESCE(g.name, '') AS group_name,
  c.stream
FROM combined c
LEFT JOIN api_keys k ON c.api_key_id = k.id
LEFT JOIN users u ON c.user_id = u.id
LEFT JOIN users u_api ON k.user_id = u_api.id
LEFT JOIN accounts a ON c.account_id = a.id
LEFT JOIN groups g ON c.group_id = g.id
%s
%s
LIMIT $%d OFFSET $%d
`, cte, where, sort, len(args)+1, len(args)+2)

	listArgs := append(append([]any{}, args...), pageSize, offset)
	rows, err := r.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()

	toIntPtr := func(v sql.NullInt64) *int {
		if !v.Valid {
			return nil
		}
		i := int(v.Int64)
		return &i
	}
	toInt64Ptr := func(v sql.NullInt64) *int64 {
		if !v.Valid {
			return nil
		}
		i := v.Int64
		return &i
	}

	out := make([]*service.OpsRequestDetail, 0, pageSize)
	for rows.Next() {
		var (
			kind      string
			createdAt time.Time
			requestID sql.NullString
			platform  sql.NullString
			model     sql.NullString

			durationMs sql.NullInt64
			statusCode sql.NullInt64
			errorID    sql.NullInt64

			phase    sql.NullString
			severity sql.NullString
			message  sql.NullString

			userID      sql.NullInt64
			userEmail   sql.NullString
			apiKeyID    sql.NullInt64
			apiKeyName  sql.NullString
			accountID   sql.NullInt64
			accountName sql.NullString
			groupID     sql.NullInt64
			groupName   sql.NullString

			stream bool
		)

		if err := rows.Scan(
			&kind,
			&createdAt,
			&requestID,
			&platform,
			&model,
			&durationMs,
			&statusCode,
			&errorID,
			&phase,
			&severity,
			&message,
			&userID,
			&userEmail,
			&apiKeyID,
			&apiKeyName,
			&accountID,
			&accountName,
			&groupID,
			&groupName,
			&stream,
		); err != nil {
			return nil, 0, err
		}

		item := &service.OpsRequestDetail{
			Kind:      service.OpsRequestKind(kind),
			CreatedAt: createdAt,
			RequestID: strings.TrimSpace(requestID.String),
			Platform:  strings.TrimSpace(platform.String),
			Model:     strings.TrimSpace(model.String),

			DurationMs: toIntPtr(durationMs),
			StatusCode: toIntPtr(statusCode),
			ErrorID:    toInt64Ptr(errorID),
			Phase:      phase.String,
			Severity:   severity.String,
			Message:    message.String,

			UserID:      toInt64Ptr(userID),
			UserEmail:   strings.TrimSpace(userEmail.String),
			APIKeyID:    toInt64Ptr(apiKeyID),
			APIKeyName:  strings.TrimSpace(apiKeyName.String),
			AccountID:   toInt64Ptr(accountID),
			AccountName: strings.TrimSpace(accountName.String),
			GroupID:     toInt64Ptr(groupID),
			GroupName:   strings.TrimSpace(groupName.String),

			Stream: stream,
		}

		if item.Platform == "" {
			item.Platform = "unknown"
		}

		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	return out, total, nil
}
