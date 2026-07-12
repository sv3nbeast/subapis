package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type webChatDocumentRepository struct{ db *sql.DB }

func NewWebChatDocumentRepository(db *sql.DB) service.WebChatDocumentRepository {
	return &webChatDocumentRepository{db: db}
}

const webChatDocumentColumns = `id,user_id,project_id,session_id,original_name,content_type,extension,size_bytes,sha256,object_key,status,enabled,error_message,extracted_chars,chunk_count,attempt_count,COALESCE(lease_owner,''),created_at,updated_at,deleted_at`

func scanWebChatDocument(row interface{ Scan(...any) error }, d *service.WebChatDocument) error {
	return row.Scan(&d.ID, &d.UserID, &d.ProjectID, &d.SessionID, &d.OriginalName, &d.ContentType, &d.Extension, &d.SizeBytes, &d.SHA256, &d.ObjectKey, &d.Status, &d.Enabled, &d.ErrorMessage, &d.ExtractedChars, &d.ChunkCount, &d.AttemptCount, &d.LeaseOwner, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt)
}

func (r *webChatDocumentRepository) CreateDocument(ctx context.Context, d *service.WebChatDocument, limits service.WebChatDocumentLimits) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lockKey := int64(-730000000000000000) + d.UserID
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, lockKey); err != nil {
		return err
	}
	var projectCount int
	var userBytes int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE $2::bigint IS NOT NULL AND project_id=$2),COALESCE(sum(size_bytes),0) FROM web_chat_documents WHERE user_id=$1 AND deleted_at IS NULL AND status<>'deleting'`, d.UserID, d.ProjectID).Scan(&projectCount, &userBytes); err != nil {
		return err
	}
	if (d.ProjectID != nil && projectCount >= limits.MaxFilesPerProject) || userBytes+d.SizeBytes > limits.MaxBytesPerUser {
		return service.ErrWebChatDocumentQuota
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO web_chat_documents(user_id,project_id,session_id,original_name,content_type,extension,size_bytes,sha256,object_key,status,enabled) SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11 WHERE ($2::bigint IS NULL OR EXISTS(SELECT 1 FROM web_chat_projects WHERE id=$2 AND user_id=$1 AND deleted_at IS NULL)) AND ($3::bigint IS NULL OR EXISTS(SELECT 1 FROM web_chat_sessions WHERE id=$3 AND user_id=$1 AND deleted_at IS NULL)) RETURNING id,created_at,updated_at`, d.UserID, d.ProjectID, d.SessionID, d.OriginalName, d.ContentType, d.Extension, d.SizeBytes, d.SHA256, d.ObjectKey, d.Status, d.Enabled).Scan(&d.ID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *webChatDocumentRepository) ListProjectDocuments(ctx context.Context, userID, projectID int64) (out []service.WebChatDocument, err error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+webChatDocumentColumns+` FROM web_chat_documents WHERE user_id=$1 AND project_id=$2 AND deleted_at IS NULL ORDER BY created_at DESC,id DESC`, userID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d service.WebChatDocument
		if err = scanWebChatDocument(rows, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *webChatDocumentRepository) GetDocument(ctx context.Context, userID, id int64) (*service.WebChatDocument, error) {
	var d service.WebChatDocument
	err := scanWebChatDocument(r.db.QueryRowContext(ctx, `SELECT `+webChatDocumentColumns+` FROM web_chat_documents WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID), &d)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrWebChatDocumentNotFound
	}
	return &d, err
}

func (r *webChatDocumentRepository) SetDocumentEnabled(ctx context.Context, userID, id int64, enabled bool) (*service.WebChatDocument, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE web_chat_documents SET enabled=$3,updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID, enabled)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, service.ErrWebChatDocumentNotFound
	}
	return r.GetDocument(ctx, userID, id)
}
func (r *webChatDocumentRepository) RetryDocument(ctx context.Context, userID, id int64) (*service.WebChatDocument, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE web_chat_documents SET status='uploaded',attempt_count=0,error_message='',lease_owner=NULL,lease_expires_at=NULL,next_attempt_at=now(),updated_at=now() WHERE id=$1 AND user_id=$2 AND status='failed' AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, service.ErrWebChatDocumentNotFound
	}
	return r.GetDocument(ctx, userID, id)
}

func (r *webChatDocumentRepository) DocumentUsage(ctx context.Context, userID int64, projectID *int64) (int, int64, error) {
	var count int
	var total int64
	err := r.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE $2::bigint IS NOT NULL AND project_id=$2),COALESCE(sum(size_bytes),0) FROM web_chat_documents WHERE user_id=$1 AND deleted_at IS NULL AND status<>'deleting'`, userID, projectID).Scan(&count, &total)
	return count, total, err
}

func (r *webChatDocumentRepository) MarkDocumentDeleting(ctx context.Context, userID, id int64) error {
	res, err := r.db.ExecContext(ctx, `UPDATE web_chat_documents SET status='deleting',attempt_count=0,lease_owner=NULL,lease_expires_at=NULL,next_attempt_at=now(),updated_at=now() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return service.ErrWebChatDocumentNotFound
	}
	return nil
}

func (r *webChatDocumentRepository) MarkProjectDocumentsDeleting(ctx context.Context, userID, projectID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE web_chat_documents SET status='deleting',attempt_count=0,lease_owner=NULL,lease_expires_at=NULL,next_attempt_at=now(),updated_at=now() WHERE user_id=$1 AND project_id=$2 AND deleted_at IS NULL`, userID, projectID)
	return err
}
func (r *webChatDocumentRepository) MarkSessionDocumentsDeleting(ctx context.Context, userID, sessionID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE web_chat_documents SET status='deleting',attempt_count=0,lease_owner=NULL,lease_expires_at=NULL,next_attempt_at=now(),updated_at=now() WHERE user_id=$1 AND session_id=$2 AND deleted_at IS NULL`, userID, sessionID)
	return err
}

func (r *webChatDocumentRepository) ClaimDocumentJob(ctx context.Context, owner string, lease time.Duration) (*service.WebChatDocument, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM web_chat_documents WHERE deleted_at IS NULL AND status IN ('uploaded','processing','deleting') AND next_attempt_at<=now() AND (lease_expires_at IS NULL OR lease_expires_at<now()) AND ((status='deleting' AND attempt_count<10) OR (status<>'deleting' AND attempt_count<3)) ORDER BY CASE status WHEN 'deleting' THEN 0 ELSE 1 END,next_attempt_at,id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE web_chat_documents SET status=CASE WHEN status='deleting' THEN 'deleting' ELSE 'processing' END,attempt_count=attempt_count+1,lease_owner=$2,lease_expires_at=now()+$3::interval,updated_at=now() WHERE id=$1`, id, owner, fmt.Sprintf("%f seconds", lease.Seconds()))
	if err != nil {
		return nil, err
	}
	var d service.WebChatDocument
	if err = scanWebChatDocument(tx.QueryRowContext(ctx, `SELECT `+webChatDocumentColumns+` FROM web_chat_documents WHERE id=$1`, id), &d); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *webChatDocumentRepository) CompleteDocument(ctx context.Context, id int64, owner string, chunks []service.WebChatDocumentChunk, chars int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var locked int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM web_chat_documents WHERE id=$1 AND lease_owner=$2 FOR UPDATE`, id, owner).Scan(&locked); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM web_chat_document_chunks WHERE document_id=$1`, id); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO web_chat_document_chunks(document_id,chunk_index,page_number,location_label,content) VALUES($1,$2,$3,$4,$5)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range chunks {
		if _, err = stmt.ExecContext(ctx, id, c.ChunkIndex, c.PageNumber, c.LocationLabel, c.Content); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE web_chat_documents SET status='ready',error_message='',extracted_chars=$2,chunk_count=$3,lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1`, id, chars, len(chunks))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *webChatDocumentRepository) FailDocument(ctx context.Context, id int64, owner, message string, next time.Time) error {
	var attempts int
	err := r.db.QueryRowContext(ctx, `UPDATE web_chat_documents SET status=CASE WHEN status='deleting' THEN 'deleting' WHEN attempt_count>=3 THEN 'failed' ELSE 'uploaded' END,error_message=left($3,1000),lease_owner=NULL,lease_expires_at=NULL,next_attempt_at=$4,updated_at=now() WHERE id=$1 AND lease_owner=$2 RETURNING attempt_count`, id, owner, message, next).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func (r *webChatDocumentRepository) FinishDocumentDelete(ctx context.Context, id int64, owner string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE web_chat_documents SET deleted_at=now(),lease_owner=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1 AND lease_owner=$2`, id, owner)
	return err
}

func (r *webChatDocumentRepository) SearchDocumentChunks(ctx context.Context, userID, projectID int64, ids []int64, query string, limit int) (out []service.WebChatDocumentChunk, err error) {
	if limit <= 0 {
		limit = 8
	}
	query = truncateDocumentSearchQuery(strings.TrimSpace(query), 512)
	if query == "" {
		return []service.WebChatDocumentChunk{}, nil
	}
	var hasTrigram bool
	if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_trgm')`).Scan(&hasTrigram); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, webChatDocumentSearchQuery(hasTrigram), userID, projectID, pq.Array(ids), query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c service.WebChatDocumentChunk
		if err = rows.Scan(&c.ID, &c.DocumentID, &c.ChunkIndex, &c.PageNumber, &c.LocationLabel, &c.Content, &c.DocumentName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func truncateDocumentSearchQuery(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return strings.TrimSpace(string(runes))
}

func webChatDocumentSearchQuery(hasTrigram bool) string {
	const baseFilter = `d.user_id=$1 AND d.deleted_at IS NULL AND d.status='ready' AND d.enabled AND (($2>0 AND d.project_id=$2) OR d.id=ANY($3))`
	if !hasTrigram {
		return `WITH candidates AS (
			SELECT c.id,c.document_id,c.chunk_index,c.page_number,c.location_label,c.content,d.original_name,
				ts_rank_cd(c.search_vector,plainto_tsquery('simple',$4)) score
			FROM web_chat_document_chunks c JOIN web_chat_documents d ON d.id=c.document_id
			WHERE ` + baseFilter + ` AND c.search_vector @@ plainto_tsquery('simple',$4)
		), ranked AS (
			SELECT *,row_number() OVER(PARTITION BY document_id ORDER BY score DESC,chunk_index) per_doc FROM candidates
		)
		SELECT id,document_id,chunk_index,page_number,location_label,content,original_name
		FROM ranked WHERE per_doc<=3 ORDER BY score DESC,id LIMIT $5`
	}
	return `WITH candidates AS (
		SELECT c.id,c.document_id,c.chunk_index,c.page_number,c.location_label,c.content,d.original_name,
			(CASE WHEN c.search_vector @@ plainto_tsquery('simple',$4) THEN ts_rank_cd(c.search_vector,plainto_tsquery('simple',$4)) ELSE 0 END)+word_similarity($4,c.content) score
		FROM web_chat_document_chunks c JOIN web_chat_documents d ON d.id=c.document_id
		WHERE ` + baseFilter + ` AND (c.search_vector @@ plainto_tsquery('simple',$4) OR word_similarity($4,c.content)>=0.1)
	), ranked AS (
		SELECT *,row_number() OVER(PARTITION BY document_id ORDER BY score DESC,chunk_index) per_doc FROM candidates
	)
	SELECT id,document_id,chunk_index,page_number,location_label,content,original_name
	FROM ranked WHERE per_doc<=3 AND score>0 ORDER BY score DESC,id LIMIT $5`
}

func (r *webChatDocumentRepository) LinkMessageDocuments(ctx context.Context, userID, messageID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO web_chat_message_documents(message_id,document_id) SELECT $1,d.id FROM web_chat_documents d JOIN web_chat_messages m ON m.id=$1 AND m.user_id=$2 WHERE d.id=ANY($3) AND d.user_id=$2 AND d.session_id=m.session_id AND d.deleted_at IS NULL AND d.status='ready' ON CONFLICT DO NOTHING`, messageID, userID, pq.Array(ids))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != int64(len(ids)) {
		return service.ErrWebChatDocumentNotReady
	}
	return nil
}
func (r *webChatDocumentRepository) MessageDocumentIDs(ctx context.Context, userID, messageID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT document_id FROM (SELECT md.document_id FROM web_chat_message_documents md JOIN web_chat_messages m ON m.id=md.message_id WHERE m.id=$1 AND m.user_id=$2 UNION ALL SELECT (source->>'document_id')::bigint FROM web_chat_messages m CROSS JOIN LATERAL jsonb_array_elements(m.sources) source WHERE m.id=$1 AND m.user_id=$2 AND source ? 'document_id') ids`, messageID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *webChatDocumentRepository) UpdateMessageSources(ctx context.Context, userID, messageID int64, sources []service.WebChatSource) error {
	raw, err := json.Marshal(sources)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, `UPDATE web_chat_messages SET sources=$3 WHERE id=$1 AND user_id=$2 AND role='assistant' AND deleted_at IS NULL`, messageID, userID, raw)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return service.ErrWebChatMessageNotFound
	}
	return nil
}
