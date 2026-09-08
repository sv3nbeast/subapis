package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.WebAgentRepository = (*webChatRepository)(nil)

const webAgentColumns = `t.id,t.user_id,t.session_id,t.group_id,t.model,t.kind,t.prompt,t.document_ids,
 t.session_snapshot,t.idempotency_key,t.request_hash,t.status,t.result,t.error_code,t.step_count,
 COALESCE(t.lease_token,''),t.deadline_at,t.created_at,t.updated_at,t.finished_at`
const webAgentVisible = `EXISTS (SELECT 1 FROM web_chat_sessions s WHERE s.id=t.session_id AND s.user_id=t.user_id AND s.deleted_at IS NULL)`

func scanWebAgentTask(row interface{ Scan(...any) error }) (*service.WebAgentTask, error) {
	var t service.WebAgentTask
	var docs, snapshot, result []byte
	err := row.Scan(&t.ID, &t.UserID, &t.SessionID, &t.GroupID, &t.Model, &t.Kind, &t.Prompt, &docs, &snapshot,
		&t.IdempotencyKey, &t.RequestHash, &t.Status, &result, &t.ErrorCode, &t.StepCount, &t.LeaseToken,
		&t.DeadlineAt, &t.CreatedAt, &t.UpdatedAt, &t.FinishedAt)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(docs, &t.DocumentIDs); err != nil {
		return nil, err
	}
	t.SessionSnapshot = snapshot
	t.Result = result
	return &t, nil
}
func (r *webChatRepository) CreateTask(ctx context.Context, t *service.WebAgentTask) (*service.WebAgentTask, error) {
	if t == nil {
		return nil, service.ErrWebAgentInvalid
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize admission across sessions belonging to one user, not globally.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('web-agent:' || $1::text,0))`, t.UserID); err != nil {
		return nil, err
	}
	var sessionID int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM web_chat_sessions WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL FOR SHARE`, t.SessionID, t.UserID).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrWebChatSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	existing, err := scanWebAgentTask(tx.QueryRowContext(ctx, `SELECT `+webAgentColumns+` FROM web_agent_tasks t WHERE t.user_id=$1 AND t.idempotency_key=$2`, t.UserID, t.IdempotencyKey))
	if err == nil {
		if existing.RequestHash != t.RequestHash {
			return nil, service.ErrWebAgentConflict
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var count int
	err = tx.QueryRowContext(ctx, `SELECT count(*) FROM web_agent_tasks t WHERE t.user_id=$1 AND t.status IN ('queued','running','cancel_requested') AND `+webAgentVisible, t.UserID).Scan(&count)
	if err != nil {
		return nil, err
	}
	if count >= service.WebAgentMaxActiveTasks {
		return nil, service.ErrWebAgentBusy
	}
	if len(t.DocumentIDs) > 0 {
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM web_chat_documents d JOIN web_chat_sessions s ON s.id=$2
		 WHERE d.id=ANY($3::bigint[]) AND d.user_id=$1 AND d.deleted_at IS NULL AND d.enabled AND d.status='ready'
		 AND (d.session_id=s.id OR (d.project_id=s.project_id AND EXISTS(
		  SELECT 1 FROM web_chat_projects p WHERE p.id=d.project_id AND p.user_id=$1 AND p.deleted_at IS NULL)))`, t.UserID, t.SessionID, pq.Array(t.DocumentIDs)).Scan(&count)
		if err != nil {
			return nil, err
		}
		if count != len(t.DocumentIDs) {
			return nil, service.ErrWebChatDocumentNotFound
		}
	}
	docs, err := json.Marshal(t.DocumentIDs)
	if err != nil {
		return nil, err
	}
	task, err := scanWebAgentTask(tx.QueryRowContext(ctx, `INSERT INTO web_agent_tasks AS t
	 (user_id,session_id,group_id,model,kind,prompt,document_ids,session_snapshot,idempotency_key,request_hash,deadline_at)
	 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING `+webAgentColumns,
		t.UserID, t.SessionID, t.GroupID, t.Model, t.Kind, t.Prompt, string(docs), string(t.SessionSnapshot), t.IdempotencyKey, t.RequestHash, t.DeadlineAt))
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO web_agent_task_events(task_id,type) VALUES($1,'task.queued')`, task.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}
func (r *webChatRepository) GetTask(ctx context.Context, userID, id int64) (*service.WebAgentTask, error) {
	t, err := scanWebAgentTask(r.db.QueryRowContext(ctx, `SELECT `+webAgentColumns+` FROM web_agent_tasks t WHERE t.id=$1 AND t.user_id=$2 AND `+webAgentVisible, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrWebAgentNotFound
	}
	return t, err
}
func (r *webChatRepository) ListTasks(ctx context.Context, userID, sessionID, before int64) (out []service.WebAgentTask, err error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT `+webAgentColumns+` FROM web_agent_tasks t
	 WHERE t.user_id=$1 AND ($2::bigint=0 OR t.session_id=$2) AND ($3::bigint=0 OR t.id<$3) AND `+webAgentVisible+` ORDER BY t.id DESC LIMIT 50`, userID, sessionID, before)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = []service.WebAgentTask{}
	for rows.Next() {
		t, e := scanWebAgentTask(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}
func (r *webChatRepository) ListTaskEvents(ctx context.Context, userID, taskID, after int64) (out []service.WebAgentTaskEvent, err error) {
	if _, err = r.GetTask(ctx, userID, taskID); err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT id,task_id,type,data,created_at FROM web_agent_task_events WHERE task_id=$1 AND id>$2 ORDER BY id LIMIT 100`, taskID, after)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = []service.WebAgentTaskEvent{}
	for rows.Next() {
		var e service.WebAgentTaskEvent
		var data []byte
		if err = rows.Scan(&e.ID, &e.TaskID, &e.Type, &data, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Data = data
		out = append(out, e)
	}
	return out, rows.Err()
}
func (r *webChatRepository) CancelTask(ctx context.Context, userID, id int64) (*service.WebAgentTask, error) {
	_, err := r.sql.ExecContext(ctx, `WITH changed AS (
	 UPDATE web_agent_tasks t SET
	  status=CASE WHEN status='queued' THEN 'cancelled' ELSE 'cancel_requested' END,
	  finished_at=CASE WHEN status='queued' THEN now() ELSE NULL END,
	  updated_at=now()
	 WHERE t.id=$1 AND t.user_id=$2 AND t.status IN ('queued','running') AND `+webAgentVisible+`
	 RETURNING t.id,t.status
	) INSERT INTO web_agent_task_events(task_id,type) SELECT id,'task.' || status FROM changed`, id, userID)
	if err != nil {
		return nil, err
	}
	return r.GetTask(ctx, userID, id)
}
func (r *webChatRepository) ClaimTask(ctx context.Context, token string, ttl time.Duration) (*service.WebAgentTask, error) {
	if token == "" || ttl < time.Second {
		return nil, service.ErrWebAgentInvalid
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	task, err := scanWebAgentTask(tx.QueryRowContext(ctx, `WITH candidate AS (
	 SELECT t.id FROM web_agent_tasks t WHERE t.status='queued' AND t.deadline_at>now() AND `+webAgentVisible+`
	 ORDER BY t.id FOR UPDATE SKIP LOCKED LIMIT 1
	) UPDATE web_agent_tasks t SET status='running',lease_token=$1,
	  lease_expires_at=LEAST(now()+$2::bigint*interval '1 second',deadline_at),updated_at=now()
	  FROM candidate WHERE t.id=candidate.id RETURNING `+webAgentColumns, token, int64(ttl/time.Second)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO web_agent_task_events(task_id,type) VALUES($1,'task.started')`, task.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return task, nil
}
func (r *webChatRepository) RenewTaskLease(ctx context.Context, id int64, token string, ttl time.Duration) error {
	res, err := r.sql.ExecContext(ctx, `UPDATE web_agent_tasks t SET lease_expires_at=LEAST(now()+$3::bigint*interval '1 second',deadline_at),updated_at=now()
	 WHERE t.id=$1 AND lease_token=$2 AND status='running' AND lease_expires_at>now() AND deadline_at>now() AND `+webAgentVisible, id, token, int64(ttl/time.Second))
	return webChatAffected(err, res, service.ErrWebAgentLeaseLost)
}
func (r *webChatRepository) AppendTaskStep(ctx context.Context, id int64, token, eventType string, data json.RawMessage) error {
	if eventType != "step.started" && eventType != "step.completed" {
		return service.ErrWebAgentInvalid
	}
	if len(data) > 64*1024 || !json.Valid(data) {
		return service.ErrWebAgentInvalid
	}
	res, err := r.sql.ExecContext(ctx, `WITH live AS (
	 UPDATE web_agent_tasks t SET step_count=step_count+CASE WHEN $3='step.started' THEN 1 ELSE 0 END,updated_at=now()
	 WHERE t.id=$1 AND lease_token=$2 AND status='running' AND lease_expires_at>now() AND deadline_at>now()
	 AND ($3<>'step.started' OR step_count<16) AND `+webAgentVisible+`
	 RETURNING t.id
	) INSERT INTO web_agent_task_events(task_id,type,data) SELECT id,$3,$4::jsonb FROM live`, id, token, eventType, string(data))
	return webChatAffected(err, res, service.ErrWebAgentLeaseLost)
}
func (r *webChatRepository) FinishTask(ctx context.Context, id int64, token, status string, result json.RawMessage, errorCode string) error {
	if status != service.WebAgentSucceeded && status != service.WebAgentFailed && status != service.WebAgentInterrupted {
		return service.ErrWebAgentInvalid
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	if len(result) > 512*1024 || !json.Valid(result) || len(errorCode) > 100 {
		return service.ErrWebAgentInvalid
	}
	res, err := r.sql.ExecContext(ctx, `WITH changed AS (
	 UPDATE web_agent_tasks t SET
	  status=CASE WHEN status='cancel_requested' THEN 'cancelled' ELSE $3 END,
	  result=CASE WHEN status='cancel_requested' THEN NULL ELSE $4::jsonb END,
	  error_code=CASE WHEN status='cancel_requested' THEN '' ELSE $5 END,
	  lease_token=NULL,lease_expires_at=NULL,updated_at=now(),finished_at=now()
	 WHERE t.id=$1 AND lease_token=$2 AND status IN ('running','cancel_requested') AND lease_expires_at>now() AND `+webAgentVisible+`
	 RETURNING t.id,t.status,t.error_code
	) INSERT INTO web_agent_task_events(task_id,type,data) SELECT id,'task.' || status,jsonb_build_object('error_code',error_code) FROM changed`, id, token, status, string(result), errorCode)
	return webChatAffected(err, res, service.ErrWebAgentLeaseLost)
}
func (r *webChatRepository) ExpireTasks(ctx context.Context) error {
	_, err := r.sql.ExecContext(ctx, `WITH expired AS (
	 SELECT t.id FROM web_agent_tasks t WHERE t.status IN ('queued','running','cancel_requested') AND
	  (deadline_at<=now() OR lease_expires_at<=now() OR NOT (`+webAgentVisible+`))
	 ORDER BY t.id LIMIT 100 FOR UPDATE SKIP LOCKED
	), changed AS (
	 UPDATE web_agent_tasks t SET status=CASE
	  WHEN status='cancel_requested' OR NOT (`+webAgentVisible+`) THEN 'cancelled'
	  WHEN deadline_at<=now() THEN 'failed' ELSE 'interrupted' END,
	  error_code=CASE
	   WHEN status='cancel_requested' OR NOT (`+webAgentVisible+`) THEN ''
	   WHEN status='queued' THEN 'queue_deadline'
	   WHEN deadline_at<=now() THEN 'execution_deadline' ELSE 'execution_interrupted' END,
	  lease_token=NULL,lease_expires_at=NULL,updated_at=now(),finished_at=now()
	 FROM expired WHERE t.id=expired.id
	 RETURNING t.id,t.status,t.error_code
	) INSERT INTO web_agent_task_events(task_id,type,data) SELECT id,'task.' || status,jsonb_build_object('error_code',error_code) FROM changed`)
	if err != nil {
		return fmt.Errorf("expire agent tasks: %w", err)
	}
	return nil
}
