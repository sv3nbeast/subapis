package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestWebChatDocumentSearchQueryFallsBackWithoutTrigram(t *testing.T) {
	query := webChatDocumentSearchQuery(false)
	require.Contains(t, query, "search_vector @@ plainto_tsquery")
	require.NotContains(t, query, "similarity(")
	require.Contains(t, query, "per_doc<=3")
}

func TestWebChatDocumentSearchQueryFiltersZeroScoreWithTrigram(t *testing.T) {
	query := webChatDocumentSearchQuery(true)
	require.Contains(t, query, "similarity(")
	require.Contains(t, query, "score>0")
	require.Contains(t, query, "per_doc<=3")
}

func TestMergeExplicitDocumentChunksKeepsAttachmentHeaders(t *testing.T) {
	explicit := []service.WebChatDocumentChunk{
		{ID: 10, DocumentID: 1, ChunkIndex: 0, Content: "header 1"},
		{ID: 11, DocumentID: 1, ChunkIndex: 1, Content: "row 1"},
		{ID: 20, DocumentID: 2, ChunkIndex: 0, Content: "header 2"},
	}
	ranked := []service.WebChatDocumentChunk{
		{ID: 11, DocumentID: 1, ChunkIndex: 1, Content: "row 1"},
		{ID: 21, DocumentID: 2, ChunkIndex: 1, Content: "row 2"},
	}

	got := mergeExplicitDocumentChunks(explicit, ranked, 4)

	require.Equal(t, []int64{10, 20, 11, 21}, []int64{got[0].ID, got[1].ID, got[2].ID, got[3].ID})
}

func TestTruncateDocumentSearchQuery(t *testing.T) {
	value := strings.Repeat("知识", 400)
	truncated := truncateDocumentSearchQuery(value, 512)
	require.Len(t, []rune(truncated), 512)
	require.Equal(t, "", truncateDocumentSearchQuery(value, 0))
}

func TestDeleteWebChatSessionQueuesDocumentCleanupAtomically(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := NewWebChatRepository(db)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE web_chat_sessions SET deleted_at").WithArgs(int64(9), int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE web_chat_documents SET status='deleting'").WithArgs(int64(3), int64(9)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()
	require.NoError(t, repo.DeleteSession(context.Background(), 3, 9))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteWebChatProjectQueuesCleanupAndDetachesSessionsAtomically(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := NewWebChatRepository(db)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE web_chat_projects SET deleted_at").WithArgs(int64(12), int64(4)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE web_chat_documents SET status='deleting'").WithArgs(int64(4), int64(12)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE web_chat_sessions SET project_id=NULL").WithArgs(int64(12), int64(4)).WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	require.NoError(t, repo.DeleteProject(context.Background(), 4, 12))
	require.NoError(t, mock.ExpectationsWereMet())
}
