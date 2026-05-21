package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type webChatRepository struct {
	sql sqlExecutor
}

func NewWebChatRepository(sqlDB *sql.DB) service.WebChatRepository {
	return newWebChatRepositoryWithSQL(sqlDB)
}

func newWebChatRepositoryWithSQL(sqlq sqlExecutor) *webChatRepository {
	return &webChatRepository{sql: sqlq}
}

func (r *webChatRepository) CreateSession(ctx context.Context, session *service.WebChatSession) error {
	if session == nil {
		return fmt.Errorf("web chat session is nil")
	}
	if err := scanSingleRow(ctx, r.sql, `
		INSERT INTO web_chat_sessions (user_id, group_id, model, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())
		RETURNING id, created_at, updated_at
	`, []any{session.UserID, session.GroupID, session.Model, session.Title},
		&session.ID, &session.CreatedAt, &session.UpdatedAt,
	); err != nil {
		return err
	}
	return nil
}

func (r *webChatRepository) ListSessions(ctx context.Context, userID int64) ([]service.WebChatSession, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT s.id, s.user_id, s.group_id, COALESCE(g.name, ''), COALESCE(g.platform, ''),
		       s.model, s.title, s.created_at, s.updated_at, s.deleted_at
		FROM web_chat_sessions s
		LEFT JOIN groups g ON g.id = s.group_id
		WHERE s.user_id = $1 AND s.deleted_at IS NULL
		ORDER BY s.updated_at DESC, s.id DESC
		LIMIT 100
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	out := make([]service.WebChatSession, 0)
	for rows.Next() {
		var item service.WebChatSession
		if err := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.GroupID,
			&item.GroupName,
			&item.Platform,
			&item.Model,
			&item.Title,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.DeletedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *webChatRepository) GetSession(ctx context.Context, userID, sessionID int64) (*service.WebChatSession, error) {
	var item service.WebChatSession
	if err := scanSingleRow(ctx, r.sql, `
		SELECT s.id, s.user_id, s.group_id, COALESCE(g.name, ''), COALESCE(g.platform, ''),
		       s.model, s.title, s.created_at, s.updated_at, s.deleted_at
		FROM web_chat_sessions s
		LEFT JOIN groups g ON g.id = s.group_id
		WHERE s.id = $1 AND s.user_id = $2 AND s.deleted_at IS NULL
	`, []any{sessionID, userID},
		&item.ID,
		&item.UserID,
		&item.GroupID,
		&item.GroupName,
		&item.Platform,
		&item.Model,
		&item.Title,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.DeletedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrWebChatSessionNotFound
		}
		return nil, err
	}
	return &item, nil
}

func (r *webChatRepository) DeleteSession(ctx context.Context, userID, sessionID int64) error {
	res, err := r.sql.ExecContext(ctx, `
		UPDATE web_chat_sessions
		SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
	`, sessionID, userID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return service.ErrWebChatSessionNotFound
	}
	return nil
}

func (r *webChatRepository) CreateMessage(ctx context.Context, message *service.WebChatMessage) error {
	if message == nil {
		return fmt.Errorf("web chat message is nil")
	}
	if err := scanSingleRow(ctx, r.sql, `
		INSERT INTO web_chat_messages (session_id, user_id, role, content, status, error_message, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now(), now())
		RETURNING id, created_at, updated_at
	`, []any{
		message.SessionID,
		message.UserID,
		message.Role,
		message.Content,
		message.Status,
		webChatNullString(message.ErrorMessage),
	}, &message.ID, &message.CreatedAt, &message.UpdatedAt); err != nil {
		return err
	}
	return nil
}

func (r *webChatRepository) UpdateMessageStatus(ctx context.Context, messageID int64, content, status, errorMessage string) error {
	res, err := r.sql.ExecContext(ctx, `
		UPDATE web_chat_messages
		SET content = $2, status = $3, error_message = $4, updated_at = now()
		WHERE id = $1
	`, messageID, content, status, webChatNullString(errorMessage))
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return service.ErrWebChatMessageNotFound
	}
	return nil
}

func (r *webChatRepository) TouchSession(ctx context.Context, sessionID int64, title string) error {
	if strings.TrimSpace(title) == "" {
		_, err := r.sql.ExecContext(ctx, `
			UPDATE web_chat_sessions
			SET updated_at = now()
			WHERE id = $1 AND deleted_at IS NULL
		`, sessionID)
		return err
	}
	_, err := r.sql.ExecContext(ctx, `
		UPDATE web_chat_sessions
		SET title = CASE WHEN title = '' THEN $2 ELSE title END,
		    updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL
	`, sessionID, title)
	return err
}

func (r *webChatRepository) ListMessages(ctx context.Context, userID, sessionID int64) ([]service.WebChatMessage, error) {
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, session_id, user_id, role, content, status, COALESCE(error_message, ''), created_at, updated_at
		FROM web_chat_messages
		WHERE user_id = $1 AND session_id = $2
		ORDER BY created_at ASC, id ASC
	`, userID, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	out := make([]service.WebChatMessage, 0)
	for rows.Next() {
		var item service.WebChatMessage
		if err := rows.Scan(
			&item.ID,
			&item.SessionID,
			&item.UserID,
			&item.Role,
			&item.Content,
			&item.Status,
			&item.ErrorMessage,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *webChatRepository) RecentMessages(ctx context.Context, userID, sessionID int64, limit int) ([]service.WebChatMessage, error) {
	if limit <= 0 {
		limit = 40
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, session_id, user_id, role, content, status, COALESCE(error_message, ''), created_at, updated_at
		FROM (
			SELECT id, session_id, user_id, role, content, status, error_message, created_at, updated_at
			FROM web_chat_messages
			WHERE user_id = $1
			  AND session_id = $2
			  AND status IN ('completed', 'partial')
			  AND role IN ('user', 'assistant')
			ORDER BY created_at DESC, id DESC
			LIMIT $3
		) recent
		ORDER BY created_at ASC, id ASC
	`, userID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	out := make([]service.WebChatMessage, 0, limit)
	for rows.Next() {
		var item service.WebChatMessage
		if err := rows.Scan(
			&item.ID,
			&item.SessionID,
			&item.UserID,
			&item.Role,
			&item.Content,
			&item.Status,
			&item.ErrorMessage,
			&item.CreatedAt,
			&item.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func webChatNullString(value string) sql.NullString {
	if strings.TrimSpace(value) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}
