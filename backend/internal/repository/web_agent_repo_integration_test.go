//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func agentIntegrationRepo(t *testing.T) (service.WebAgentRepository, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("WEB_AGENT_TEST_DSN")
	if dsn == "" {
		t.Skip("WEB_AGENT_TEST_DSN is not configured")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Contains(t, []string{"localhost", "127.0.0.1"}, u.Hostname(), "integration tests may only use a local disposable database")
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := "web_agent_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	q.Set("application_name", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { db.Close(); _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); admin.Close() })
	_, err = db.Exec(`CREATE TABLE users(id bigint PRIMARY KEY);
	  CREATE TABLE groups(id bigint PRIMARY KEY);
	  CREATE TABLE web_chat_sessions(id bigint PRIMARY KEY,user_id bigint NOT NULL,project_id bigint,deleted_at timestamptz);
	  CREATE TABLE web_chat_projects(id bigint PRIMARY KEY,user_id bigint NOT NULL,deleted_at timestamptz);
	  CREATE TABLE web_chat_documents(id bigint PRIMARY KEY,user_id bigint NOT NULL,session_id bigint,project_id bigint,status text,enabled boolean,deleted_at timestamptz);
	  INSERT INTO users VALUES(1),(2); INSERT INTO groups VALUES(7);
	  INSERT INTO web_chat_sessions(id,user_id)VALUES(2,1),(3,2);
	  INSERT INTO web_chat_documents(id,user_id,session_id,status,enabled)VALUES(10,1,2,'ready',true),(11,2,3,'ready',true);`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/234_web_agent_tasks.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	artifactsMigration, err := os.ReadFile("../../migrations/235_web_agent_artifacts.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(artifactsMigration))
	require.NoError(t, err)
	storageMigration, err := os.ReadFile("../../migrations/236_web_agent_blob_lifecycle.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(storageMigration))
	require.NoError(t, err)
	return NewWebChatRepository(db).(service.WebAgentRepository), db
}
func agentTask(key string) *service.WebAgentTask {
	group := int64(7)
	return &service.WebAgentTask{UserID: 1, SessionID: 2, GroupID: &group, Model: "test-model", Kind: "document",
		Prompt: "Task fixture", DocumentIDs: []int64{}, SessionSnapshot: json.RawMessage(`{}`),
		IdempotencyKey: key, RequestHash: key, DeadlineAt: time.Now().Add(time.Minute)}
}
func TestWebAgentRepositoryOwnerAndIdempotency(t *testing.T) {
	repo, _ := agentIntegrationRepo(t)
	ctx := context.Background()
	a, err := repo.CreateTask(ctx, agentTask("request-one"))
	require.NoError(t, err)
	again, err := repo.CreateTask(ctx, agentTask("request-one"))
	require.NoError(t, err)
	require.Equal(t, a.ID, again.ID)
	conflict := agentTask("request-one")
	conflict.RequestHash = "different"
	_, err = repo.CreateTask(ctx, conflict)
	require.ErrorIs(t, err, service.ErrWebAgentConflict)
	_, err = repo.GetTask(ctx, 2, a.ID)
	require.ErrorIs(t, err, service.ErrWebAgentNotFound)
	_, err = repo.CancelTask(ctx, 2, a.ID)
	require.ErrorIs(t, err, service.ErrWebAgentNotFound)
	_, err = repo.ListTaskEvents(ctx, 2, a.ID, 0)
	require.ErrorIs(t, err, service.ErrWebAgentNotFound)
	list, err := repo.ListTasks(ctx, 2, 0, 0)
	require.NoError(t, err)
	require.Empty(t, list)
	wrong := agentTask("wrong-owner")
	wrong.SessionID = 3
	_, err = repo.CreateTask(ctx, wrong)
	require.ErrorIs(t, err, service.ErrWebChatSessionNotFound)
	wrong = agentTask("wrong-document")
	wrong.DocumentIDs = []int64{11}
	_, err = repo.CreateTask(ctx, wrong)
	require.ErrorIs(t, err, service.ErrWebChatDocumentNotFound)
	valid := agentTask("valid-document")
	valid.DocumentIDs = []int64{10}
	_, err = repo.CreateTask(ctx, valid)
	require.NoError(t, err)
}
func TestWebAgentRepositoryConcurrentAdmission(t *testing.T) {
	repo, _ := agentIntegrationRepo(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make(chan int64, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task, err := repo.CreateTask(ctx, agentTask("same-request"))
			if err != nil {
				errs <- err
			} else {
				ids <- task.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	require.Empty(t, errs)
	var id int64
	for n := range ids {
		if id == 0 {
			id = n
		}
		require.Equal(t, id, n)
	}
	_, err := repo.CreateTask(ctx, agentTask("request-two"))
	require.NoError(t, err)
	_, err = repo.CreateTask(ctx, agentTask("request-three"))
	require.NoError(t, err)
	_, err = repo.CreateTask(ctx, agentTask("request-four"))
	require.ErrorIs(t, err, service.ErrWebAgentBusy)
}
func TestWebAgentRepositoryExclusiveLeaseAndTerminal(t *testing.T) {
	repo, _ := agentIntegrationRepo(t)
	ctx := context.Background()
	task, err := repo.CreateTask(ctx, agentTask("lease-request"))
	require.NoError(t, err)
	var wg sync.WaitGroup
	claims := make(chan *service.WebAgentTask, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, e := repo.ClaimTask(ctx, uuid.NewString(), 30*time.Second)
			if e != nil {
				errs <- e
			}
			if claimed != nil {
				claims <- claimed
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	require.Empty(t, errs)
	require.Len(t, claims, 1)
	claimed := <-claims
	require.Equal(t, task.ID, claimed.ID)
	require.ErrorIs(t, repo.FinishTask(ctx, task.ID, "stale-token", service.WebAgentSucceeded, json.RawMessage(`{}`), ""), service.ErrWebAgentLeaseLost)
	require.NoError(t, repo.RenewTaskLease(ctx, task.ID, claimed.LeaseToken, 30*time.Second))
	require.NoError(t, repo.AppendTaskStep(ctx, task.ID, claimed.LeaseToken, "step.started", json.RawMessage(`{"name":"render"}`)))
	require.NoError(t, repo.AppendTaskStep(ctx, task.ID, claimed.LeaseToken, "step.completed", json.RawMessage(`{"name":"render"}`)))
	require.NoError(t, repo.FinishTask(ctx, task.ID, claimed.LeaseToken, service.WebAgentSucceeded, json.RawMessage(`{"artifact_id":1}`), ""))
	require.ErrorIs(t, repo.FinishTask(ctx, task.ID, claimed.LeaseToken, service.WebAgentSucceeded, json.RawMessage(`{}`), ""), service.ErrWebAgentLeaseLost)
	events, err := repo.ListTaskEvents(ctx, 1, task.ID, 0)
	require.NoError(t, err)
	require.Len(t, events, 5)
	last, err := repo.ListTaskEvents(ctx, 1, task.ID, events[3].ID)
	require.NoError(t, err)
	require.Len(t, last, 1)
	require.Equal(t, "task.succeeded", last[0].Type)
}
func TestWebAgentRepositoryCancellationAndExpiredExecution(t *testing.T) {
	repo, db := agentIntegrationRepo(t)
	ctx := context.Background()
	queued, err := repo.CreateTask(ctx, agentTask("cancel-queued"))
	require.NoError(t, err)
	cancelled, err := repo.CancelTask(ctx, 1, queued.ID)
	require.NoError(t, err)
	require.Equal(t, service.WebAgentCancelled, cancelled.Status)
	_, err = repo.CancelTask(ctx, 1, queued.ID)
	require.NoError(t, err)
	events, err := repo.ListTaskEvents(ctx, 1, queued.ID, 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	running, err := repo.CreateTask(ctx, agentTask("cancel-running"))
	require.NoError(t, err)
	claimed, err := repo.ClaimTask(ctx, "lease-1", 30*time.Second)
	require.NoError(t, err)
	_, err = repo.CancelTask(ctx, 1, running.ID)
	require.NoError(t, err)
	require.ErrorIs(t, repo.RenewTaskLease(ctx, running.ID, claimed.LeaseToken, 30*time.Second), service.ErrWebAgentLeaseLost)
	require.NoError(t, repo.FinishTask(ctx, running.ID, claimed.LeaseToken, service.WebAgentSucceeded, json.RawMessage(`{"ignored":true}`), ""))
	got, err := repo.GetTask(ctx, 1, running.ID)
	require.NoError(t, err)
	require.Equal(t, service.WebAgentCancelled, got.Status)
	require.Empty(t, got.Result)
	expired, err := repo.CreateTask(ctx, agentTask("expired-running"))
	require.NoError(t, err)
	_, err = repo.ClaimTask(ctx, "lease-2", 30*time.Second)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE web_agent_tasks SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, expired.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ExpireTasks(ctx))
	got, err = repo.GetTask(ctx, 1, expired.ID)
	require.NoError(t, err)
	require.Equal(t, service.WebAgentInterrupted, got.Status)
	next, err := repo.ClaimTask(ctx, "lease-3", 30*time.Second)
	require.NoError(t, err)
	require.Nil(t, next, "never replay an uncertain execution")
}

func TestWebAgentRepositoryCancelFinishRaceHasOneTerminal(t *testing.T) {
	repo, _ := agentIntegrationRepo(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		task, err := repo.CreateTask(ctx, agentTask(uuid.NewString()))
		require.NoError(t, err)
		claimed, err := repo.ClaimTask(ctx, uuid.NewString(), 30*time.Second)
		require.NoError(t, err)
		done := make(chan error, 2)
		go func() { _, e := repo.CancelTask(ctx, 1, task.ID); done <- e }()
		go func() {
			done <- repo.FinishTask(ctx, task.ID, claimed.LeaseToken, service.WebAgentSucceeded, json.RawMessage(`{}`), "")
		}()
		for j := 0; j < 2; j++ {
			require.NoError(t, <-done)
		}
		got, err := repo.GetTask(ctx, 1, task.ID)
		require.NoError(t, err)
		require.True(t, got.Terminal())
		events, err := repo.ListTaskEvents(ctx, 1, task.ID, 0)
		require.NoError(t, err)
		terminals := 0
		for _, e := range events {
			if e.Type == "task.succeeded" || e.Type == "task.cancelled" {
				terminals++
			}
		}
		require.Equal(t, 1, terminals)
	}
}

func TestWebAgentRepositoryDeletedSessionAndStepLimit(t *testing.T) {
	repo, db := agentIntegrationRepo(t)
	ctx := context.Background()
	task, err := repo.CreateTask(ctx, agentTask("steps-request"))
	require.NoError(t, err)
	claimed, err := repo.ClaimTask(ctx, "step-lease", 30*time.Second)
	require.NoError(t, err)
	for i := 0; i < service.WebAgentMaxSteps; i++ {
		require.NoError(t, repo.AppendTaskStep(ctx, task.ID, claimed.LeaseToken, "step.started", json.RawMessage(`{}`)))
		require.NoError(t, repo.AppendTaskStep(ctx, task.ID, claimed.LeaseToken, "step.completed", json.RawMessage(`{}`)))
	}
	require.ErrorIs(t, repo.AppendTaskStep(ctx, task.ID, claimed.LeaseToken, "step.started", json.RawMessage(`{}`)), service.ErrWebAgentLeaseLost)
	_, err = db.Exec(`UPDATE web_chat_sessions SET deleted_at=now() WHERE id=2`)
	require.NoError(t, err)
	require.ErrorIs(t, repo.FinishTask(ctx, task.ID, claimed.LeaseToken, service.WebAgentSucceeded, json.RawMessage(`{}`), ""), service.ErrWebAgentLeaseLost)
	require.NoError(t, repo.ExpireTasks(ctx))
	var status string
	require.NoError(t, db.QueryRow(`SELECT status FROM web_agent_tasks WHERE id=$1`, task.ID).Scan(&status))
	require.Equal(t, service.WebAgentCancelled, status)
	_, err = repo.GetTask(ctx, 1, task.ID)
	require.ErrorIs(t, err, service.ErrWebAgentNotFound)
}
