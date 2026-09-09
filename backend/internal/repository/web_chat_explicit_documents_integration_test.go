//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestWebChatExplicitIntentDoesNotInheritAutomaticSources(t *testing.T) {
	_, db := agentIntegrationRepo(t)
	_, err := db.Exec(`CREATE TABLE web_chat_messages(id bigint PRIMARY KEY,user_id bigint,session_id bigint,role text,parent_message_id bigint,deleted_at timestamptz,sources jsonb DEFAULT '[]');
 CREATE TABLE web_chat_message_documents(message_id bigint,document_id bigint REFERENCES web_chat_documents(id) ON DELETE CASCADE,created_at timestamptz DEFAULT now(),PRIMARY KEY(message_id,document_id));
 INSERT INTO web_chat_projects(id,user_id)VALUES(77,1),(88,1);
 UPDATE web_chat_sessions SET project_id=77 WHERE id=2;
 INSERT INTO web_chat_documents(id,user_id,project_id,status,enabled)VALUES(12,1,77,'ready',true),(13,1,88,'ready',true);
 INSERT INTO web_chat_messages(id,user_id,session_id,role,parent_message_id,sources)VALUES
 (100,1,2,'user',NULL,'[]'),(101,1,2,'assistant',100,'[{"document_id":13}]'),
 (110,1,2,'user',NULL,'[]'),(111,1,2,'assistant',110,'[{"document_id":13}]'),
 (120,1,2,'user',NULL,'[]'),(200,2,3,'user',NULL,'[]');
 INSERT INTO web_chat_message_documents(message_id,document_id)VALUES(110,10);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/237_web_chat_explicit_document_intent.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	repo := NewWebChatDocumentRepository(db)
	ctx := context.Background()
	require.NoError(t, repo.LinkMessageDocuments(ctx, 1, 100, []int64{12, 10}))
	require.NoError(t, repo.LinkMessageDocuments(ctx, 1, 100, []int64{12, 10}), "same attachment linkage is idempotent")
	require.ErrorIs(t, repo.LinkMessageDocuments(ctx, 1, 100, []int64{10, 12}), service.ErrWebChatDocumentNotReady, "a saved turn's attachment order is immutable")
	for _, id := range []int64{100, 101} {
		ids, e := repo.MessageDocumentIDs(ctx, 1, id)
		require.NoError(t, e)
		require.Equal(t, []int64{12, 10}, ids)
	}
	legacy, err := repo.MessageDocumentIDs(ctx, 1, 111)
	require.NoError(t, err)
	require.Equal(t, []int64{10}, legacy, "automatic source 13 is not an explicit attachment")
	_, err = db.Exec(`DELETE FROM web_chat_documents WHERE id=12`)
	require.NoError(t, err)
	ids, err := repo.MessageDocumentIDs(ctx, 1, 101)
	require.NoError(t, err)
	require.Equal(t, []int64{12, 10}, ids, "deleted file intent must not disappear with its FK link")
	require.ErrorIs(t, repo.LinkMessageDocuments(ctx, 1, 120, ids), service.ErrWebChatDocumentNotReady)
	require.ErrorIs(t, repo.LinkMessageDocuments(ctx, 1, 120, []int64{13}), service.ErrWebChatDocumentNotReady)
	require.ErrorIs(t, repo.LinkMessageDocuments(ctx, 2, 200, []int64{10}), service.ErrWebChatDocumentNotReady)
	_, err = db.Exec(`UPDATE web_chat_documents SET enabled=false WHERE id=10`)
	require.NoError(t, err)
	require.ErrorIs(t, repo.LinkMessageDocuments(ctx, 1, 120, []int64{10}), service.ErrWebChatDocumentNotReady)
	other, err := repo.MessageDocumentIDs(ctx, 2, 101)
	require.NoError(t, err)
	require.Empty(t, other)
}
