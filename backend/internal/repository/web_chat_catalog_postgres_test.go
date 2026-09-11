package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Opt-in against a LOCAL test database. Everything is confined to a unique
// disposable schema; never points at or migrates the production schema.
func TestWebChatCatalogPostgresAtomicRoutingAndBilling(t *testing.T) {
	dsn := os.Getenv("WEB_CHAT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("WEB_CHAT_TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	schema := fmt.Sprintf("codex_chat_%d", time.Now().UnixNano())
	_, err = db.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	defer db.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	_, err = db.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `CREATE TABLE users(id bigint PRIMARY KEY); CREATE TABLE groups(id bigint PRIMARY KEY,name text,platform text); CREATE TABLE api_keys(id bigserial PRIMARY KEY,user_id bigint,group_id bigint,deleted_at timestamptz); CREATE TABLE usage_logs(id bigserial PRIMARY KEY,user_id bigint,group_id bigint,request_id text,actual_cost numeric);`)
	require.NoError(t, err)
	for _, name := range []string{"139_add_web_chat.sql", "173_enhance_web_chat_productivity.sql", "174_web_chat_ai_workspace.sql", "234_web_chat_message_target.sql", "234_web_chat_message_target.sql"} {
		data, err := os.ReadFile("../../migrations/" + name)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, string(data))
		require.NoError(t, err, name)
	}
	_, err = db.ExecContext(ctx, `ALTER TABLE web_chat_sessions ADD COLUMN knowledge_enabled boolean NOT NULL DEFAULT true; ALTER TABLE web_chat_messages ADD COLUMN sources jsonb NOT NULL DEFAULT '[]'; INSERT INTO users VALUES(7),(9); INSERT INTO groups VALUES(2,'Claude','anthropic'),(3,'OpenAI','openai'); INSERT INTO web_chat_sessions(id,user_id,group_id,model) VALUES(8,7,2,'old');`)
	require.NoError(t, err)
	repo := newWebChatRepositoryWithSQL(db)
	_, first, err := repo.CreateTurn(ctx, 7, 8, "prefix", "title", nil, service.WebChatTarget{GroupID: 2, Model: "claude-opus-5"})
	require.NoError(t, err)
	_, _, err = repo.CreateTurn(ctx, 7, 8, "concurrent", "title", nil, service.WebChatTarget{GroupID: 3, Model: "gpt-6-astra"})
	require.ErrorIs(t, err, service.ErrWebChatSessionBusy)
	session, err := repo.GetSession(ctx, 7, 8)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-5", session.Model)
	_, err = repo.UpdateMessageResult(ctx, 7, first.ID, "first answer", "completed", "", "req-one", service.WebChatUsage{Model: "claude-opus-5", Platform: "anthropic", GroupID: 2})
	require.NoError(t, err)
	_, second, err := repo.CreateTurn(ctx, 7, 8, "suffix", "title", nil, service.WebChatTarget{GroupID: 3, Model: "gpt-6-astra"})
	require.NoError(t, err)
	_, err = repo.UpdateMessageResult(ctx, 7, second.ID, "second answer", "completed", "", "req-two", service.WebChatUsage{Model: "gpt-6-astra", Platform: "openai", GroupID: 3})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO usage_logs(user_id,group_id,request_id,actual_cost) VALUES(7,2,'req-one',0.125),(9,2,'req-one',99),(7,3,'req-one',99),(7,3,'req-two',0.25)`)
	require.NoError(t, err)
	items, err := repo.ListMessages(ctx, 7, 8)
	require.NoError(t, err)
	require.Len(t, items, 4)
	require.Equal(t, "claude-opus-5", items[1].Model)
	require.Equal(t, 0.125, *items[1].ActualCost)
	require.Equal(t, "gpt-6-astra", items[3].Model)
	require.Equal(t, 0.25, *items[3].ActualCost)
	contextItems, err := repo.RecentMessages(ctx, 7, 8, 40)
	require.NoError(t, err)
	require.Len(t, contextItems, 4)
	require.Equal(t, "prefix", contextItems[0].Content)
	_, err = repo.RegenerateTurn(ctx, 7, 8, second.ID, service.WebChatTarget{GroupID: 2, Model: "claude-opus-5"})
	require.NoError(t, err)
	session, err = repo.GetSession(ctx, 7, 8)
	require.NoError(t, err)
	require.Equal(t, "claude-opus-5", session.Model)
	_, _, err = repo.CreateTurn(ctx, 9, 8, "forged", "", nil, service.WebChatTarget{GroupID: 3, Model: "gpt-6-astra"})
	require.ErrorIs(t, err, service.ErrWebChatSessionNotFound)
}
