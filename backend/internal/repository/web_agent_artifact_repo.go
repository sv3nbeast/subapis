package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

var _ service.WebAgentArtifactRepository = (*webChatRepository)(nil)

const artifactColumns = `a.id,a.task_id,a.user_id,a.session_id,a.lineage_id::text,a.version,a.parent_id,
 a.kind,a.title,a.filename,a.mime,a.blob_key,a.preview_key,a.size_bytes,a.preview_bytes,a.sha256,a.spec,a.created_at`
const artifactVisible = `a.deleted_at IS NULL AND EXISTS(SELECT 1 FROM web_agent_tasks t WHERE t.id=a.task_id AND t.user_id=a.user_id AND t.status='succeeded')
 AND EXISTS(SELECT 1 FROM web_chat_sessions s WHERE s.id=a.session_id AND s.user_id=a.user_id AND s.deleted_at IS NULL)`

func scanAgentArtifact(row interface{ Scan(...any) error }) (*service.WebAgentArtifact, error) {
	var a service.WebAgentArtifact
	var spec []byte
	err := row.Scan(&a.ID, &a.TaskID, &a.UserID, &a.SessionID, &a.LineageID, &a.Version, &a.ParentID, &a.Kind, &a.Title, &a.Filename, &a.MIME, &a.BlobKey, &a.PreviewKey, &a.SizeBytes, &a.PreviewBytes, &a.SHA256, &spec, &a.CreatedAt)
	a.Spec = spec
	return &a, err
}
func (r *webChatRepository) PublishArtifact(ctx context.Context, task *service.WebAgentTask, input *service.WebAgentArtifact) (*service.WebAgentArtifact, error) {
	if task == nil || input == nil || input.Kind != task.Kind {
		return nil, service.ErrWebAgentInvalid
	}
	if err := input.Validate(); err != nil {
		return nil, err
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// One short publication lock per owner protects both version numbering and
	// the shared artifact quota. No renderer or file I/O occurs in this transaction.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('web-agent-artifact:' || $1::text,0))`, task.UserID); err != nil {
		return nil, err
	}
	var status string
	var source *int64
	err = tx.QueryRowContext(ctx, `SELECT status,source_artifact_id FROM web_agent_tasks t WHERE id=$1 AND user_id=$2
	 AND session_id=$3 AND kind=$4 AND lease_token=$5 AND lease_expires_at>now() AND status IN ('running','cancel_requested') AND `+webAgentVisible+` FOR UPDATE`,
		task.ID, task.UserID, task.SessionID, task.Kind, task.LeaseToken).Scan(&status, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrWebAgentLeaseLost
	}
	if err != nil {
		return nil, err
	}
	if status == service.WebAgentCancelRequested {
		if _, err = tx.ExecContext(ctx, `UPDATE web_agent_tasks SET status='cancelled',result=NULL,error_code='',lease_token=NULL,lease_expires_at=NULL,finished_at=now(),updated_at=now() WHERE id=$1`, task.ID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO web_agent_task_events(task_id,type)VALUES($1,'task.cancelled')`, task.ID); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	lineage, version := uuid.NewString(), 1
	if source != nil {
		var kind string
		err = tx.QueryRowContext(ctx, `SELECT a.lineage_id::text,a.kind FROM web_agent_artifacts a WHERE a.id=$1 AND a.user_id=$2 AND a.session_id=$3 AND `+artifactVisible, *source, task.UserID, task.SessionID).Scan(&lineage, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrWebAgentArtifactNotFound
		}
		if err != nil {
			return nil, err
		}
		if kind != task.Kind {
			return nil, service.ErrWebAgentInvalid
		}
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(version),0)+1 FROM web_agent_artifacts WHERE user_id=$1 AND lineage_id=$2::uuid`, task.UserID, lineage).Scan(&version); err != nil {
			return nil, err
		}
	}
	var used int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(size_bytes+preview_bytes+octet_length(spec::text)),0)+octet_length($2::jsonb::text) FROM web_agent_artifacts WHERE user_id=$1 AND deleted_at IS NULL`, task.UserID, string(input.Spec)).Scan(&used); err != nil {
		return nil, err
	}
	if used+input.SizeBytes+input.PreviewBytes > 500<<20 {
		return nil, service.ErrWebAgentStorageLimit
	}
	artifact, err := scanAgentArtifact(tx.QueryRowContext(ctx, `INSERT INTO web_agent_artifacts AS a
	 (task_id,user_id,session_id,lineage_id,version,parent_id,kind,title,filename,mime,blob_key,preview_key,size_bytes,preview_bytes,sha256,spec)
	 VALUES($1,$2,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb) RETURNING `+artifactColumns,
		task.ID, task.UserID, task.SessionID, lineage, version, source, task.Kind, input.Title, input.Filename, input.MIME, input.BlobKey, input.PreviewKey, input.SizeBytes, input.PreviewBytes, input.SHA256, string(input.Spec)))
	if err != nil {
		return nil, err
	}
	result, _ := json.Marshal(map[string]any{"artifact_id": artifact.ID, "kind": artifact.Kind, "title": artifact.Title, "version": artifact.Version})
	if _, err = tx.ExecContext(ctx, `UPDATE web_agent_tasks SET status='succeeded',result=$2::jsonb,error_code='',lease_token=NULL,lease_expires_at=NULL,finished_at=now(),updated_at=now() WHERE id=$1`, task.ID, string(result)); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO web_agent_task_events(task_id,type,data)VALUES($1,'task.succeeded',$2::jsonb)`, task.ID, string(result)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return artifact, nil
}
func (r *webChatRepository) GetArtifact(ctx context.Context, userID, id int64) (*service.WebAgentArtifact, error) {
	a, err := scanAgentArtifact(r.db.QueryRowContext(ctx, `SELECT `+artifactColumns+` FROM web_agent_artifacts a WHERE a.id=$1 AND a.user_id=$2 AND `+artifactVisible, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrWebAgentArtifactNotFound
	}
	return a, err
}
func (r *webChatRepository) ListArtifacts(ctx context.Context, userID, sessionID, before int64) (out []service.WebAgentArtifact, err error) {
	return r.listAgentArtifacts(ctx, `a.user_id=$1 AND ($2::bigint=0 OR a.session_id=$2) AND ($3::bigint=0 OR a.id<$3)`, userID, sessionID, before)
}
func (r *webChatRepository) ArtifactVersions(ctx context.Context, userID, id, before int64) ([]service.WebAgentArtifact, error) {
	a, err := r.GetArtifact(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return r.listAgentArtifacts(ctx, `a.user_id=$1 AND a.lineage_id=$2::uuid AND ($3::bigint=0 OR a.id<$3)`, userID, a.LineageID, before)
}
func (r *webChatRepository) listAgentArtifacts(ctx context.Context, where string, args ...any) (out []service.WebAgentArtifact, err error) {
	// Lists need metadata only; avoid loading up to 50 MiB of private revision
	// specifications just to discard them during JSON serialization.
	columns := strings.Replace(artifactColumns, "a.spec", "'{}'::jsonb", 1)
	rows, err := r.sql.QueryContext(ctx, `SELECT `+columns+` FROM web_agent_artifacts a WHERE `+where+` AND `+artifactVisible+` ORDER BY a.id DESC LIMIT 50`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	out = []service.WebAgentArtifact{}
	for rows.Next() {
		a, e := scanAgentArtifact(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}
