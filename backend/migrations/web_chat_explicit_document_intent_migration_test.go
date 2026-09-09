package migrations

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWebChatExplicitDocumentIntentMigrationIsIdempotent(t *testing.T) {
	sql, err := FS.ReadFile("237_web_chat_explicit_document_intent.sql")
	require.NoError(t, err)
	text := string(sql)
	require.Contains(t, text, "ADD COLUMN IF NOT EXISTS explicit_document_ids")
	require.Contains(t, text, "IF NOT EXISTS")
	require.Contains(t, text, "pg_constraint")
	require.NotContains(t, text, "ALTER TABLE web_chat_messages ADD CONSTRAINT web_chat_explicit_documents_array\nCHECK")
}
