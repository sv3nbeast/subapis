package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const webChatStaleGenerationInterval = "10 minutes"

type webChatRepository struct {
	sql sqlExecutor
	db  *sql.DB
}

func NewWebChatRepository(db *sql.DB) service.WebChatRepository {
	return &webChatRepository{sql: db, db: db}
}
func newWebChatRepositoryWithSQL(q sqlExecutor) *webChatRepository {
	r := &webChatRepository{sql: q}
	if db, ok := q.(*sql.DB); ok {
		r.db = db
	}
	return r
}

const webChatSessionColumns = `s.id,s.user_id,s.group_id,COALESCE(g.name,''),COALESCE(g.platform,''),s.model,s.title,s.pinned_at,s.system_prompt,s.temperature,s.max_output_tokens,s.project_id,COALESCE(p.name,''),s.default_template_id,s.active_leaf_message_id,s.created_at,s.updated_at,s.deleted_at`

func scanWebChatSession(row interface{ Scan(...any) error }, s *service.WebChatSession) error {
	return row.Scan(&s.ID, &s.UserID, &s.GroupID, &s.GroupName, &s.Platform, &s.Model, &s.Title, &s.PinnedAt, &s.SystemPrompt, &s.Temperature, &s.MaxOutputTokens, &s.ProjectID, &s.ProjectName, &s.DefaultTemplateID, &s.ActiveLeafMessageID, &s.CreatedAt, &s.UpdatedAt, &s.DeletedAt)
}

func (r *webChatRepository) CreateSession(ctx context.Context, s *service.WebChatSession) error {
	if s == nil {
		return fmt.Errorf("web chat session is nil")
	}
	return scanSingleRow(ctx, r.sql, `INSERT INTO web_chat_sessions(user_id,group_id,model,title,max_output_tokens,project_id,default_template_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,now(),now()) RETURNING id,created_at,updated_at`, []any{s.UserID, s.GroupID, s.Model, s.Title, s.MaxOutputTokens, s.ProjectID, s.DefaultTemplateID}, &s.ID, &s.CreatedAt, &s.UpdatedAt)
}
func (r *webChatRepository) ListSessions(ctx context.Context, userID int64, query string) (out []service.WebChatSession, err error) {
	pattern := "%" + strings.TrimSpace(query) + "%"
	rows, err := r.sql.QueryContext(ctx, `SELECT `+webChatSessionColumns+` FROM web_chat_sessions s LEFT JOIN groups g ON g.id=s.group_id LEFT JOIN web_chat_projects p ON p.id=s.project_id AND p.deleted_at IS NULL WHERE s.user_id=$1 AND s.deleted_at IS NULL AND ($2='%%' OR s.title ILIKE $2 OR s.model ILIKE $2 OR COALESCE(g.name,'') ILIKE $2) ORDER BY (s.pinned_at IS NOT NULL) DESC,s.updated_at DESC,s.id DESC LIMIT 100`, userID, pattern)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]service.WebChatSession, 0)
	for rows.Next() {
		var s service.WebChatSession
		if err = scanWebChatSession(rows, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *webChatRepository) GetSession(ctx context.Context, userID, sessionID int64) (*service.WebChatSession, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+webChatSessionColumns+` FROM web_chat_sessions s LEFT JOIN groups g ON g.id=s.group_id LEFT JOIN web_chat_projects p ON p.id=s.project_id AND p.deleted_at IS NULL WHERE s.id=$1 AND s.user_id=$2 AND s.deleted_at IS NULL`, sessionID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrWebChatSessionNotFound
	}
	var s service.WebChatSession
	if err = scanWebChatSession(rows, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
func (r *webChatRepository) UpdateSession(ctx context.Context, userID, sessionID int64, req service.WebChatPatchSessionRequest) (*service.WebChatSession, error) {
	var temperature any
	if req.Temperature != nil {
		temperature = *req.Temperature
	}
	res, err := r.sql.ExecContext(ctx, `UPDATE web_chat_sessions SET title=CASE WHEN $3 THEN $4 ELSE title END,pinned_at=CASE WHEN $5 THEN CASE WHEN $6 THEN now() ELSE NULL END ELSE pinned_at END,system_prompt=CASE WHEN $7 THEN $8 ELSE system_prompt END,temperature=CASE WHEN $9 THEN $10::double precision ELSE temperature END,max_output_tokens=CASE WHEN $11 THEN $12 ELSE max_output_tokens END,project_id=CASE WHEN $13 THEN $14::bigint ELSE project_id END,default_template_id=CASE WHEN $15 THEN $16::bigint ELSE default_template_id END,updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, sessionID, userID, req.Title != nil, valueOrEmpty(req.Title), req.Pinned != nil, valueOrFalse(req.Pinned), req.SystemPrompt != nil, valueOrEmpty(req.SystemPrompt), req.TemperatureSet, temperature, req.MaxOutputTokens != nil, valueOrInt(req.MaxOutputTokens, 8192), req.ProjectIDSet, nullableInt(req.ProjectID), req.DefaultTemplateIDSet, nullableInt(req.DefaultTemplateID))
	if err = webChatAffected(err, res, service.ErrWebChatSessionNotFound); err != nil {
		return nil, err
	}
	return r.GetSession(ctx, userID, sessionID)
}
func (r *webChatRepository) UpdateSessionTarget(ctx context.Context, userID, sessionID, groupID int64, model string) error {
	res, err := r.sql.ExecContext(ctx, `UPDATE web_chat_sessions SET group_id=$3,model=$4,updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, sessionID, userID, groupID, model)
	return webChatAffected(err, res, service.ErrWebChatSessionNotFound)
}
func (r *webChatRepository) DeleteSession(ctx context.Context, userID, sessionID int64) error {
	res, err := r.sql.ExecContext(ctx, `UPDATE web_chat_sessions SET deleted_at=now(),updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, sessionID, userID)
	return webChatAffected(err, res, service.ErrWebChatSessionNotFound)
}

const webChatMessageColumns = `m.id,m.session_id,m.user_id,m.role,m.content,m.status,COALESCE(m.error_message,''),m.request_id,m.input_tokens,m.output_tokens,m.cache_read_tokens,m.cache_creation_tokens,m.logical_id,m.parent_message_id,m.version_index,(SELECT count(*) FROM web_chat_messages v WHERE v.session_id=m.session_id AND v.logical_id=m.logical_id AND v.deleted_at IS NULL),m.version_reason,m.template_id,m.created_at,m.updated_at`

func scanWebChatMessage(row interface{ Scan(...any) error }, m *service.WebChatMessage) error {
	return row.Scan(&m.ID, &m.SessionID, &m.UserID, &m.Role, &m.Content, &m.Status, &m.ErrorMessage, &m.RequestID, &m.InputTokens, &m.OutputTokens, &m.CacheReadTokens, &m.CacheCreationTokens, &m.LogicalID, &m.ParentMessageID, &m.VersionIndex, &m.VersionCount, &m.VersionReason, &m.TemplateID, &m.CreatedAt, &m.UpdatedAt)
}

func (r *webChatRepository) CreateTurn(ctx context.Context, userID, sessionID int64, content, title string, templateID *int64) (*service.WebChatMessage, *service.WebChatMessage, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	leaf, err := lockWebChatSession(ctx, tx, userID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if err = ensureWebChatGenerationAvailable(ctx, tx, userID, sessionID); err != nil {
		return nil, nil, err
	}
	user, err := insertWebChatMessage(ctx, tx, userID, sessionID, service.WebChatMessageRoleUser, content, service.WebChatMessageStatusCompleted, leaf, nil, 1, "original", templateID)
	if err != nil {
		return nil, nil, err
	}
	assistant, err := insertWebChatMessage(ctx, tx, userID, sessionID, service.WebChatMessageRoleAssistant, "", service.WebChatMessageStatusStreaming, &user.ID, nil, 1, "original", nil)
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE web_chat_sessions SET title=CASE WHEN title='' THEN $2 ELSE title END,active_leaf_message_id=$3,updated_at=now() WHERE id=$1`, sessionID, title, assistant.ID); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return user, assistant, nil
}
func (r *webChatRepository) RegenerateTurn(ctx context.Context, userID, sessionID, messageID int64) (*service.WebChatMessage, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockWebChatSession(ctx, tx, userID, sessionID); err != nil {
		return nil, err
	}
	if err = ensureWebChatGenerationAvailable(ctx, tx, userID, sessionID); err != nil {
		return nil, err
	}
	var logical int64
	var parent *int64
	var version int
	err = tx.QueryRowContext(ctx, `SELECT logical_id,parent_message_id,(SELECT COALESCE(max(version_index),0)+1 FROM web_chat_messages WHERE session_id=$2 AND logical_id=m.logical_id AND deleted_at IS NULL) FROM web_chat_messages m WHERE id=$1 AND session_id=$2 AND user_id=$3 AND role='assistant' AND deleted_at IS NULL`, messageID, sessionID, userID).Scan(&logical, &parent, &version)
	if err == sql.ErrNoRows {
		return nil, service.ErrWebChatMessageNotFound
	}
	if err != nil {
		return nil, err
	}
	assistant, err := insertWebChatMessage(ctx, tx, userID, sessionID, service.WebChatMessageRoleAssistant, "", service.WebChatMessageStatusStreaming, parent, &logical, version, "regenerate", nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE web_chat_sessions SET active_leaf_message_id=$2,updated_at=now() WHERE id=$1`, sessionID, assistant.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return assistant, nil
}
func (r *webChatRepository) ReviseTurn(ctx context.Context, userID, sessionID, messageID int64, content, title string) (*service.WebChatMessage, *service.WebChatMessage, error) {
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if _, err = lockWebChatSession(ctx, tx, userID, sessionID); err != nil {
		return nil, nil, err
	}
	if err = ensureWebChatGenerationAvailable(ctx, tx, userID, sessionID); err != nil {
		return nil, nil, err
	}
	var logical int64
	var parent *int64
	var version int
	err = tx.QueryRowContext(ctx, `SELECT logical_id,parent_message_id,(SELECT COALESCE(max(version_index),0)+1 FROM web_chat_messages WHERE session_id=$2 AND logical_id=m.logical_id AND deleted_at IS NULL) FROM web_chat_messages m WHERE id=$1 AND session_id=$2 AND user_id=$3 AND role='user' AND deleted_at IS NULL`, messageID, sessionID, userID).Scan(&logical, &parent, &version)
	if err == sql.ErrNoRows {
		return nil, nil, service.ErrWebChatMessageNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	user, err := insertWebChatMessage(ctx, tx, userID, sessionID, service.WebChatMessageRoleUser, content, service.WebChatMessageStatusCompleted, parent, &logical, version, "edit", nil)
	if err != nil {
		return nil, nil, err
	}
	assistant, err := insertWebChatMessage(ctx, tx, userID, sessionID, service.WebChatMessageRoleAssistant, "", service.WebChatMessageStatusStreaming, &user.ID, nil, 1, "original", nil)
	if err != nil {
		return nil, nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE web_chat_sessions SET title=CASE WHEN title='' THEN $2 ELSE title END,active_leaf_message_id=$3,updated_at=now() WHERE id=$1`, sessionID, title, assistant.ID); err != nil {
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, err
	}
	return user, assistant, nil
}
func (r *webChatRepository) UpdateMessageResult(ctx context.Context, userID, messageID int64, content, status, errorMessage, requestID string, usage service.WebChatUsage) (*service.WebChatMessage, error) {
	rows, err := r.sql.QueryContext(ctx, `UPDATE web_chat_messages SET content=$3,status=$4,error_message=$5,request_id=$6,input_tokens=$7,output_tokens=$8,cache_read_tokens=$9,cache_creation_tokens=$10,updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL RETURNING id`, messageID, userID, content, status, webChatNullString(errorMessage), requestID, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheCreationTokens)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, service.ErrWebChatMessageNotFound
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return r.getMessage(ctx, userID, messageID)
}
func (r *webChatRepository) getMessage(ctx context.Context, userID, messageID int64) (*service.WebChatMessage, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+webChatMessageColumns+` FROM web_chat_messages m WHERE m.id=$1 AND m.user_id=$2 AND m.deleted_at IS NULL`, messageID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, service.ErrWebChatMessageNotFound
	}
	var m service.WebChatMessage
	if err = scanWebChatMessage(rows, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

const activePathCTE = `WITH RECURSIVE path AS (SELECT m.* FROM web_chat_messages m JOIN web_chat_sessions s ON s.active_leaf_message_id=m.id WHERE s.id=$2 AND s.user_id=$1 AND s.deleted_at IS NULL AND m.deleted_at IS NULL UNION ALL SELECT parent.* FROM web_chat_messages parent JOIN path child ON child.parent_message_id=parent.id WHERE parent.deleted_at IS NULL)`

func (r *webChatRepository) ListMessages(ctx context.Context, userID, sessionID int64) ([]service.WebChatMessage, error) {
	_, _ = r.sql.ExecContext(ctx, `UPDATE web_chat_messages SET status='partial',error_message='generation timed out',updated_at=now() WHERE user_id=$1 AND session_id=$2 AND status='streaming' AND deleted_at IS NULL AND updated_at < now()-interval '`+webChatStaleGenerationInterval+`'`, userID, sessionID)
	return r.listMessages(ctx, activePathCTE+` SELECT `+webChatMessageColumns+` FROM path m ORDER BY m.created_at,m.id`, userID, sessionID)
}
func (r *webChatRepository) RecentMessages(ctx context.Context, userID, sessionID int64, limit int) ([]service.WebChatMessage, error) {
	if limit <= 0 {
		limit = 40
	}
	return r.listMessages(ctx, activePathCTE+` SELECT `+webChatMessageColumns+` FROM (SELECT * FROM path WHERE status IN ('completed','partial') AND role IN ('user','assistant') ORDER BY created_at DESC,id DESC LIMIT $3) m ORDER BY m.created_at,m.id`, userID, sessionID, limit)
}
func (r *webChatRepository) ListMessageVersions(ctx context.Context, userID, sessionID, messageID int64) ([]service.WebChatMessage, error) {
	return r.listMessages(ctx, `SELECT `+webChatMessageColumns+` FROM web_chat_messages m WHERE m.session_id=$2 AND m.user_id=$1 AND m.deleted_at IS NULL AND m.logical_id=(SELECT logical_id FROM web_chat_messages WHERE id=$3 AND session_id=$2 AND user_id=$1 AND deleted_at IS NULL) ORDER BY m.version_index,m.id`, userID, sessionID, messageID)
}
func (r *webChatRepository) ActivateMessageVersion(ctx context.Context, userID, sessionID, messageID int64) error {
	tx, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockWebChatSession(ctx, tx, userID, sessionID); err != nil {
		return err
	}
	if err = ensureWebChatGenerationAvailable(ctx, tx, userID, sessionID); err != nil {
		return err
	}
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM web_chat_messages WHERE id=$1 AND session_id=$2 AND user_id=$3 AND deleted_at IS NULL`, messageID, sessionID, userID).Scan(&id); err == sql.ErrNoRows {
		return service.ErrWebChatMessageNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE web_chat_sessions SET active_leaf_message_id=$2,updated_at=now() WHERE id=$1`, sessionID, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *webChatRepository) listMessages(ctx context.Context, q string, args ...any) (out []service.WebChatMessage, err error) {
	rows, err := r.sql.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = make([]service.WebChatMessage, 0)
	for rows.Next() {
		var m service.WebChatMessage
		if err = scanWebChatMessage(rows, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const projectColumns = `p.id,p.user_id,p.name,p.description,p.color,p.sort_order,p.default_group_id,COALESCE(p.default_model,''),p.default_template_id,(SELECT count(*) FROM web_chat_sessions s WHERE s.project_id=p.id AND s.deleted_at IS NULL),p.created_at,p.updated_at`

func scanProject(row interface{ Scan(...any) error }, p *service.WebChatProject) error {
	return row.Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.Color, &p.SortOrder, &p.DefaultGroupID, &p.DefaultModel, &p.DefaultTemplateID, &p.SessionCount, &p.CreatedAt, &p.UpdatedAt)
}
func (r *webChatRepository) ListProjects(ctx context.Context, userID int64) (out []service.WebChatProject, err error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+projectColumns+` FROM web_chat_projects p WHERE p.user_id=$1 AND p.deleted_at IS NULL ORDER BY p.sort_order,p.updated_at DESC,p.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p service.WebChatProject
		if err = scanProject(rows, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *webChatRepository) CreateProject(ctx context.Context, p *service.WebChatProject) error {
	return scanSingleRow(ctx, r.sql, `INSERT INTO web_chat_projects(user_id,name,description,color,sort_order,default_group_id,default_model,default_template_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id,created_at,updated_at`, []any{p.UserID, p.Name, p.Description, p.Color, p.SortOrder, p.DefaultGroupID, webChatNullableString(p.DefaultModel), p.DefaultTemplateID}, &p.ID, &p.CreatedAt, &p.UpdatedAt)
}
func (r *webChatRepository) UpdateProject(ctx context.Context, userID, projectID int64, in service.WebChatProjectInput) (*service.WebChatProject, error) {
	res, err := r.sql.ExecContext(ctx, `UPDATE web_chat_projects SET name=$3,description=$4,color=$5,sort_order=$6,default_group_id=$7,default_model=$8,default_template_id=$9,updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, projectID, userID, in.Name, in.Description, in.Color, in.SortOrder, in.DefaultGroupID, webChatNullableString(in.DefaultModel), in.DefaultTemplateID)
	if err = webChatAffected(err, res, service.ErrWebChatSessionNotFound); err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT `+projectColumns+` FROM web_chat_projects p WHERE p.id=$1 AND p.user_id=$2`, projectID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, service.ErrWebChatSessionNotFound
	}
	var p service.WebChatProject
	if err = scanProject(rows, &p); err != nil {
		return nil, err
	}
	return &p, nil
}
func (r *webChatRepository) DeleteProject(ctx context.Context, userID, projectID int64) error {
	tx, err := r.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE web_chat_projects SET deleted_at=now(),updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, projectID, userID)
	if err = webChatAffected(err, res, service.ErrWebChatSessionNotFound); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE web_chat_sessions SET project_id=NULL,updated_at=now() WHERE project_id=$1 AND user_id=$2 AND deleted_at IS NULL`, projectID, userID); err != nil {
		return err
	}
	return tx.Commit()
}

const templateColumns = `t.id,t.scope,t.user_id,t.source_template_id,t.name,t.category,t.description,t.body,t.variables,t.language,t.enabled,t.sort_order,t.created_at,t.updated_at`

func scanTemplate(row interface{ Scan(...any) error }, t *service.WebChatTemplate) error {
	var raw []byte
	if err := row.Scan(&t.ID, &t.Scope, &t.UserID, &t.SourceTemplateID, &t.Name, &t.Category, &t.Description, &t.Body, &raw, &t.Language, &t.Enabled, &t.SortOrder, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return err
	}
	t.Variables = json.RawMessage(raw)
	return nil
}
func (r *webChatRepository) ListTemplates(ctx context.Context, userID int64, includeDisabledSystem bool) (out []service.WebChatTemplate, err error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+templateColumns+` FROM web_chat_templates t WHERE t.deleted_at IS NULL AND ((t.scope='personal' AND t.user_id=$1) OR (t.scope='system' AND ($2 OR t.enabled))) ORDER BY t.scope DESC,t.sort_order,t.id`, userID, includeDisabledSystem)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t service.WebChatTemplate
		if err = scanTemplate(rows, &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (r *webChatRepository) GetTemplate(ctx context.Context, userID, templateID int64, allowSystem bool) (*service.WebChatTemplate, error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+templateColumns+` FROM web_chat_templates t WHERE t.id=$2 AND t.deleted_at IS NULL AND ((t.scope='personal' AND t.user_id=$1) OR (t.scope='system' AND ($3 OR t.enabled)))`, userID, templateID, allowSystem)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, service.ErrWebChatMessageNotFound
	}
	var t service.WebChatTemplate
	if err = scanTemplate(rows, &t); err != nil {
		return nil, err
	}
	return &t, nil
}
func (r *webChatRepository) CreateTemplate(ctx context.Context, t *service.WebChatTemplate) error {
	return scanSingleRow(ctx, r.sql, `INSERT INTO web_chat_templates(scope,user_id,source_template_id,name,category,description,body,variables,language,enabled,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id,created_at,updated_at`, []any{t.Scope, t.UserID, t.SourceTemplateID, t.Name, t.Category, t.Description, t.Body, []byte(t.Variables), t.Language, t.Enabled, t.SortOrder}, &t.ID, &t.CreatedAt, &t.UpdatedAt)
}
func (r *webChatRepository) UpdateTemplate(ctx context.Context, userID, templateID int64, in service.WebChatTemplateInput, system bool) (*service.WebChatTemplate, error) {
	scope := "personal"
	var owner any = userID
	if system {
		scope = "system"
		owner = nil
	}
	res, err := r.sql.ExecContext(ctx, `UPDATE web_chat_templates SET name=$4,category=$5,description=$6,body=$7,variables=$8,language=$9,enabled=$10,sort_order=$11,updated_at=now() WHERE id=$1 AND scope=$2 AND user_id IS NOT DISTINCT FROM $3::bigint AND deleted_at IS NULL`, templateID, scope, owner, in.Name, in.Category, in.Description, in.Body, []byte(in.Variables), in.Language, in.Enabled, in.SortOrder)
	if err = webChatAffected(err, res, service.ErrWebChatMessageNotFound); err != nil {
		return nil, err
	}
	return r.GetTemplate(ctx, userID, templateID, true)
}
func (r *webChatRepository) DeleteTemplate(ctx context.Context, userID, templateID int64, system bool) error {
	scope := "personal"
	var owner any = userID
	if system {
		scope = "system"
		owner = nil
	}
	res, err := r.sql.ExecContext(ctx, `UPDATE web_chat_templates SET deleted_at=now(),updated_at=now() WHERE id=$1 AND scope=$2 AND user_id IS NOT DISTINCT FROM $3::bigint AND deleted_at IS NULL`, templateID, scope, owner)
	return webChatAffected(err, res, service.ErrWebChatMessageNotFound)
}
func (r *webChatRepository) CountPersonalTemplates(ctx context.Context, userID int64) (int, error) {
	var n int
	rows, err := r.sql.QueryContext(ctx, `SELECT count(*) FROM web_chat_templates WHERE scope='personal' AND user_id=$1 AND deleted_at IS NULL`, userID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		return 0, sql.ErrNoRows
	}
	return n, rows.Scan(&n)
}

func (r *webChatRepository) begin(ctx context.Context) (*sql.Tx, error) {
	if r.db == nil {
		return nil, fmt.Errorf("web chat transactions unavailable")
	}
	return r.db.BeginTx(ctx, nil)
}
func lockWebChatSession(ctx context.Context, tx *sql.Tx, userID, sessionID int64) (*int64, error) {
	var leaf *int64
	err := tx.QueryRowContext(ctx, `SELECT active_leaf_message_id FROM web_chat_sessions WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR UPDATE`, sessionID, userID).Scan(&leaf)
	if err == sql.ErrNoRows {
		return nil, service.ErrWebChatSessionNotFound
	}
	return leaf, err
}
func ensureWebChatGenerationAvailable(ctx context.Context, tx *sql.Tx, userID, sessionID int64) error {
	if _, err := tx.ExecContext(ctx, `UPDATE web_chat_messages SET status='partial',error_message='generation timed out',updated_at=now() WHERE session_id=$1 AND user_id=$2 AND status='streaming' AND deleted_at IS NULL AND updated_at<now()-interval '`+webChatStaleGenerationInterval+`'`, sessionID, userID); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM web_chat_messages WHERE session_id=$1 AND user_id=$2 AND status='streaming' AND deleted_at IS NULL)`, sessionID, userID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return service.ErrWebChatSessionBusy
	}
	return nil
}
func insertWebChatMessage(ctx context.Context, tx *sql.Tx, userID, sessionID int64, role, content, status string, parent *int64, logical *int64, version int, reason string, templateID *int64) (*service.WebChatMessage, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `WITH next AS (SELECT nextval(pg_get_serial_sequence('web_chat_messages','id')) AS id) INSERT INTO web_chat_messages(id,session_id,user_id,role,content,status,logical_id,parent_message_id,version_index,version_reason,template_id,created_at,updated_at) SELECT id,$1,$2,$3,$4,$5,COALESCE($7,id),$6,$8,$9,$10,now(),now() FROM next RETURNING id`, sessionID, userID, role, content, status, parent, logical, version, reason, templateID).Scan(&id)
	if err != nil {
		return nil, err
	}
	return &service.WebChatMessage{ID: id, SessionID: sessionID, UserID: userID, Role: role, Content: content, Status: status, LogicalID: func() int64 {
		if logical != nil {
			return *logical
		}
		return id
	}(), ParentMessageID: parent, VersionIndex: version, VersionCount: 1, VersionReason: reason, TemplateID: templateID}, nil
}
func webChatAffected(err error, res sql.Result, notFound error) error {
	if err != nil {
		return err
	}
	n, e := res.RowsAffected()
	if e == nil && n == 0 {
		return notFound
	}
	return e
}
func webChatNullString(v string) sql.NullString {
	if strings.TrimSpace(v) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}
func webChatNullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
func nullableInt(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}
func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func valueOrFalse(v *bool) bool { return v != nil && *v }
func valueOrInt(v *int, f int) int {
	if v == nil {
		return f
	}
	return *v
}
