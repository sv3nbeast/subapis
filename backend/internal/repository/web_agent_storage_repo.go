package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

var _ service.WebAgentStorageRepository = (*webChatRepository)(nil)

// Per-owner advisory-lock admission and the cleanup visibility recheck need a
// fresh snapshot after each lock wait, regardless of the server's default.
func (r *webChatRepository) beginAgentTransaction(ctx context.Context) (*sql.Tx, error) {
	if r.db == nil {
		return nil, service.ErrWebAgentUnavailable
	}
	return r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func (r *webChatRepository) RegisterArtifactStore(ctx context.Context, id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return service.ErrWebAgentInvalid
	}
	_, err = r.sql.ExecContext(ctx, `INSERT INTO web_agent_storage_identity(id,storage_id) SELECT 1,$1::uuid
 WHERE NOT EXISTS(SELECT 1 FROM web_agent_blob_stages WHERE lease_token='legacy-published') ON CONFLICT(id) DO NOTHING`, id)
	if err != nil {
		return err
	}
	var expected string
	err = r.db.QueryRowContext(ctx, `SELECT storage_id::text FROM web_agent_storage_identity WHERE id=1`).Scan(&expected)
	if errors.Is(err, sql.ErrNoRows) || err == nil && expected != id {
		return service.ErrWebAgentStorageIdentity
	}
	return err
}

const agentStageColumns = `b.id,b.task_id,b.user_id,b.lease_token,b.blob_key,b.preview_key,b.state,b.reserved_bytes`

func scanAgentStage(row interface{ Scan(...any) error }) (*service.WebAgentBlobStage, error) {
	var b service.WebAgentBlobStage
	err := row.Scan(&b.ID, &b.TaskID, &b.UserID, &b.LeaseToken, &b.BlobKey, &b.PreviewKey, &b.State, &b.ReservedBytes)
	return &b, err
}
func (r *webChatRepository) ReserveArtifactStorage(ctx context.Context, t *service.WebAgentTask) (*service.WebAgentBlobStage, error) {
	if t == nil || t.ID <= 0 || t.UserID <= 0 {
		return nil, service.ErrWebAgentInvalid
	}
	ext := map[string]string{"slides": "pptx", "document": "docx", "spreadsheet": "xlsx"}[t.Kind]
	if ext == "" {
		return nil, service.ErrWebAgentInvalid
	}
	tx, err := r.beginAgentTransaction(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('web-agent-artifact:' || $1::text,0))`, t.UserID); err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT t.id FROM web_agent_tasks t WHERE t.id=$1 AND t.user_id=$2 AND t.lease_token=$3 AND t.kind=$4 AND t.status='running' AND t.lease_expires_at>clock_timestamp() AND t.deadline_at>clock_timestamp() AND `+webAgentVisible+` FOR UPDATE`, t.ID, t.UserID, t.LeaseToken, t.Kind).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrWebAgentLeaseLost
	}
	if err != nil {
		return nil, err
	}
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(reserved_bytes),0) FROM web_agent_blob_stages WHERE user_id=$1`, t.UserID).Scan(&used); err != nil {
		return nil, err
	}
	if used+service.WebAgentStorageReservationBytes > service.WebAgentUserStorageBytes {
		return nil, service.ErrWebAgentStorageLimit
	}
	b, err := scanAgentStage(tx.QueryRowContext(ctx, `INSERT INTO web_agent_blob_stages AS b(task_id,user_id,lease_token,blob_key,preview_key,state,reserved_bytes)
 VALUES($1,$2,$3,$4,$5,'allocated',$6) ON CONFLICT(task_id) DO NOTHING RETURNING `+agentStageColumns,
		t.ID, t.UserID, t.LeaseToken, uuid.NewString()+"."+ext, uuid.NewString()+".pdf", service.WebAgentStorageReservationBytes))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrWebAgentLeaseLost
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return b, nil
}
func (r *webChatRepository) CheckArtifactStorage(ctx context.Context, t *service.WebAgentTask, b *service.WebAgentBlobStage) error {
	if t == nil || b == nil {
		return service.ErrWebAgentInvalid
	}
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM web_agent_blob_stages b JOIN web_agent_tasks t ON t.id=b.task_id
 WHERE b.id=$1 AND b.task_id=$2 AND b.user_id=$3 AND b.lease_token=$4 AND b.state='allocated' AND b.blob_key=$5 AND b.preview_key=$6
 AND t.lease_token=b.lease_token AND t.status='running' AND t.lease_expires_at>clock_timestamp() AND t.deadline_at>clock_timestamp() AND `+webAgentVisible+`)`, b.ID, t.ID, t.UserID, t.LeaseToken, b.BlobKey, b.PreviewKey).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return service.ErrWebAgentLeaseLost
	}
	return nil
}
func (r *webChatRepository) ReadyArtifactStorage(ctx context.Context, t *service.WebAgentTask, b *service.WebAgentBlobStage, a *service.WebAgentArtifact) error {
	if t == nil || b == nil {
		return service.ErrWebAgentInvalid
	}
	if err := a.Validate(); err != nil {
		return err
	}
	if a.BlobKey != b.BlobKey || a.PreviewKey != b.PreviewKey {
		return service.ErrWebAgentInvalid
	}
	result, err := r.sql.ExecContext(ctx, `UPDATE web_agent_blob_stages b SET state='ready',reserved_bytes=$5,updated_at=now()
 WHERE b.id=$1 AND b.task_id=$2 AND b.user_id=$3 AND b.lease_token=$4 AND b.state='allocated'
 AND EXISTS(SELECT 1 FROM web_agent_tasks t WHERE t.id=b.task_id AND t.lease_token=b.lease_token AND t.status='running'
 AND t.lease_expires_at>clock_timestamp() AND t.deadline_at>clock_timestamp() AND `+webAgentVisible+`)`, b.ID, t.ID, t.UserID, t.LeaseToken, a.SizeBytes+a.PreviewBytes+int64(len(a.Spec)))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrWebAgentLeaseLost
	}
	return nil
}
func (r *webChatRepository) AbandonArtifactStorage(ctx context.Context, id, taskID int64, lease string) error {
	_, err := r.sql.ExecContext(ctx, `UPDATE web_agent_blob_stages b SET state='abandoned',updated_at=now()
 WHERE b.id=$1 AND b.task_id=$2 AND b.lease_token=$3 AND b.state IN('allocated','ready')
 AND NOT EXISTS(SELECT 1 FROM web_agent_artifacts a WHERE a.blob_key=b.blob_key OR a.preview_key=b.preview_key)`, id, taskID, lease)
	return err
}
func (r *webChatRepository) ArtifactStorageUsage(ctx context.Context, userID int64) (int64, error) {
	var used int64
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(sum(reserved_bytes),0) FROM web_agent_blob_stages WHERE user_id=$1`, userID).Scan(&used)
	return used, err
}
func (r *webChatRepository) DeleteArtifact(ctx context.Context, userID, id int64) error {
	// A deleted version remains a tombstone to preserve monotonic version IDs.
	// Contents are purged only after its physical file cleanup succeeds.
	var owned int64
	err := r.db.QueryRowContext(ctx, `UPDATE web_agent_artifacts a SET deleted_at=COALESCE(deleted_at,now()) WHERE a.id=$1 AND a.user_id=$2
 AND EXISTS(SELECT 1 FROM web_chat_sessions s WHERE s.id=a.session_id AND s.user_id=$2 AND s.deleted_at IS NULL) RETURNING a.id`, id, userID).Scan(&owned)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrWebAgentArtifactNotFound
	}
	return err
}

const stageHasLiveTask = `EXISTS(SELECT 1 FROM web_agent_tasks t WHERE t.id=b.task_id AND t.lease_token=b.lease_token
 AND t.status IN('running','cancel_requested') AND t.lease_expires_at>clock_timestamp() AND t.deadline_at>clock_timestamp() AND ` + webAgentVisible + `)`
const stageHasVisibleArtifact = `EXISTS(SELECT 1 FROM web_agent_artifacts a WHERE (a.blob_key=b.blob_key OR a.preview_key=b.preview_key) AND ` + artifactVisible + `)`

func (r *webChatRepository) CollectArtifactStorage(ctx context.Context, remove func(*service.WebAgentBlobStage) error) (int, error) {
	if remove == nil {
		return 0, service.ErrWebAgentInvalid
	}
	count := 0
	for count < 25 {
		did, err := r.collectOneAgentStage(ctx, remove)
		if err != nil {
			return count, err
		}
		if !did {
			return count, nil
		}
		count++
	}
	return count, nil
}
func (r *webChatRepository) collectOneAgentStage(ctx context.Context, remove func(*service.WebAgentBlobStage) error) (bool, error) {
	tx, err := r.beginAgentTransaction(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	b, err := scanAgentStage(tx.QueryRowContext(ctx, `SELECT `+agentStageColumns+` FROM web_agent_blob_stages b
 WHERE NOT (`+stageHasVisibleArtifact+`) AND (b.state='abandoned' OR NOT (`+stageHasLiveTask+`)) ORDER BY b.id FOR UPDATE OF b SKIP LOCKED LIMIT 1`))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A fresh READ COMMITTED statement after acquiring the row lock sees any
	// publication that committed while the candidate query was waiting.
	var visible, live bool
	err = tx.QueryRowContext(ctx, `SELECT `+stageHasVisibleArtifact+`,`+stageHasLiveTask+` FROM web_agent_blob_stages b WHERE b.id=$1`, b.ID).Scan(&visible, &live)
	if err != nil {
		return false, err
	}
	if visible || live && b.State != "abandoned" {
		return false, nil
	}
	if err = remove(b); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE web_agent_artifacts a SET spec='{}',title='Deleted artifact',filename='deleted'
 WHERE (a.blob_key=$1 OR a.preview_key=$2) AND NOT (`+artifactVisible+`)`, b.BlobKey, b.PreviewKey)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM web_agent_blob_stages WHERE id=$1`, b.ID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
