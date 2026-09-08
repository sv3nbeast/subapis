//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func agentStorageFixture(t *testing.T) (service.WebAgentRepository, service.WebAgentArtifactRepository, service.WebAgentStorageRepository, *service.WebAgentFileStore, *sql.DB, string) {
	t.Helper()
	tasks, db := agentIntegrationRepo(t)
	repo := NewWebChatRepository(db)
	artifacts := repo.(service.WebAgentArtifactRepository)
	storage := repo.(service.WebAgentStorageRepository)
	root := filepath.Join(t.TempDir(), "private-store")
	store, err := service.NewWebAgentFileStore(root)
	require.NoError(t, err)
	require.NoError(t, storage.RegisterArtifactStore(context.Background(), store.StorageID()))
	return tasks, artifacts, storage, store, db, root
}
func writeReadyStorageFixture(t *testing.T, tasks service.WebAgentRepository, storage service.WebAgentStorageRepository, store *service.WebAgentFileStore, key string) (*service.WebAgentTask, *service.WebAgentArtifact) {
	t.Helper()
	ctx := context.Background()
	_, err := tasks.CreateTask(ctx, agentTask(key))
	require.NoError(t, err)
	task, err := tasks.ClaimTask(ctx, "lease-"+key, 30*time.Second)
	require.NoError(t, err)
	stage, err := storage.ReserveArtifactStorage(ctx, task)
	require.NoError(t, err)
	a := artifactCandidate()
	a.Stage = stage
	a.BlobKey = stage.BlobKey
	a.PreviewKey = stage.PreviewKey
	require.NoError(t, store.WithLock(ctx, false, func() error {
		if e := storage.CheckArtifactStorage(ctx, task, stage); e != nil {
			return e
		}
		if e := store.PutKey(ctx, a.BlobKey, []byte(strings.Repeat("f", int(a.SizeBytes)))); e != nil {
			return e
		}
		if e := store.PutKey(ctx, a.PreviewKey, []byte(strings.Repeat("p", int(a.PreviewBytes)))); e != nil {
			return e
		}
		return storage.ReadyArtifactStorage(ctx, task, stage, a)
	}))
	return task, a
}
func TestWebAgentStoragePublicationDeletionAndQuotaRelease(t *testing.T) {
	tasks, artifacts, storage, store, _, root := agentStorageFixture(t)
	ctx := context.Background()
	task, a := writeReadyStorageFixture(t, tasks, storage, store, "storage-published")
	published, err := artifacts.PublishArtifact(ctx, task, a)
	require.NoError(t, err)
	n, err := service.CollectWebAgentStorage(ctx, storage, store)
	require.NoError(t, err)
	require.Zero(t, n)
	_, err = os.Stat(filepath.Join(root, a.BlobKey))
	require.NoError(t, err)
	used, err := storage.ArtifactStorageUsage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, a.SizeBytes+a.PreviewBytes+int64(len(a.Spec)), used)
	require.ErrorIs(t, storage.DeleteArtifact(ctx, 2, published.ID), service.ErrWebAgentArtifactNotFound)
	require.NoError(t, storage.DeleteArtifact(ctx, 1, published.ID))
	require.NoError(t, storage.DeleteArtifact(ctx, 1, published.ID))
	_, err = artifacts.GetArtifact(ctx, 1, published.ID)
	require.ErrorIs(t, err, service.ErrWebAgentArtifactNotFound)
	pending, err := storage.ArtifactStorageUsage(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, used, pending, "quota is held until physical removal is confirmed")
	require.NoError(t, store.WithLock(ctx, true, func() error {
		_, e := storage.CollectArtifactStorage(ctx, func(*service.WebAgentBlobStage) error { return errors.New("synthetic disk failure") })
		require.ErrorContains(t, e, "synthetic disk failure")
		return nil
	}))
	n, err = service.CollectWebAgentStorage(ctx, storage, store)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	_, err = os.Stat(filepath.Join(root, a.BlobKey))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(root, a.PreviewKey))
	require.True(t, os.IsNotExist(err))
	used, err = storage.ArtifactStorageUsage(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, used)
	n, err = service.CollectWebAgentStorage(ctx, storage, store)
	require.NoError(t, err)
	require.Zero(t, n)
}
func TestWebAgentStorageSurvivesOwnerDeletionAndDoesNotGuessFileOwnership(t *testing.T) {
	tasks, artifacts, storage, store, db, root := agentStorageFixture(t)
	ctx := context.Background()
	task, a := writeReadyStorageFixture(t, tasks, storage, store, "storage-deleted-owner")
	_, err := artifacts.PublishArtifact(ctx, task, a)
	require.NoError(t, err)
	unregistered, err := store.Put(ctx, "docx", []byte("not owned by the journal"))
	require.NoError(t, err)
	_, err = db.Exec(`DELETE FROM users WHERE id=1`)
	require.NoError(t, err)
	n, err := service.CollectWebAgentStorage(ctx, storage, store)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	_, err = os.Stat(filepath.Join(root, a.BlobKey))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(root, unregistered))
	require.NoError(t, err, "never delete an unregistered file based on its UUID-like name")
}
func TestWebAgentStorageRejectsDifferentVolumeIdentity(t *testing.T) {
	tasks, _, storage, store, db, _ := agentStorageFixture(t)
	ctx := context.Background()
	_, a := writeReadyStorageFixture(t, tasks, storage, store, "storage-root-identity")
	other, err := service.NewWebAgentFileStore(filepath.Join(t.TempDir(), "other-store"))
	require.NoError(t, err)
	require.NotEqual(t, store.StorageID(), other.StorageID())
	_, err = service.CollectWebAgentStorage(ctx, storage, other)
	require.ErrorIs(t, err, service.ErrWebAgentStorageIdentity)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM web_agent_blob_stages WHERE id=$1`, a.Stage.ID).Scan(&count))
	require.Equal(t, 1, count)
}
func TestWebAgentStorageReservesCapacityBeforeModelExecution(t *testing.T) {
	tasks, _, storage, _, db, _ := agentStorageFixture(t)
	ctx := context.Background()
	for i := 0; i < 15; i++ {
		_, err := db.Exec(`INSERT INTO web_agent_blob_stages(task_id,user_id,lease_token,blob_key,preview_key,state,reserved_bytes)VALUES($1,1,'orphan',$2,$3,'abandoned',$4)`, 1000+i, uuid.NewString()+".docx", uuid.NewString()+".pdf", service.WebAgentStorageReservationBytes)
		require.NoError(t, err)
	}
	_, err := tasks.CreateTask(ctx, agentTask("storage-full"))
	require.NoError(t, err)
	task, err := tasks.ClaimTask(ctx, "storage-full-lease", 30*time.Second)
	require.NoError(t, err)
	_, err = storage.ReserveArtifactStorage(ctx, task)
	require.ErrorIs(t, err, service.ErrWebAgentStorageLimit)
}
func TestWebAgentStorageCleanupCannotOvertakeAnInFlightWrite(t *testing.T) {
	tasks, _, storage, store, db, root := agentStorageFixture(t)
	ctx := context.Background()
	_, err := tasks.CreateTask(ctx, agentTask("slow-write"))
	require.NoError(t, err)
	task, err := tasks.ClaimTask(ctx, "slow-write-lease", 30*time.Second)
	require.NoError(t, err)
	stage, err := storage.ReserveArtifactStorage(ctx, task)
	require.NoError(t, err)
	writerCtx, cancelWriter := context.WithCancel(ctx)
	defer cancelWriter()
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	go func() {
		finished <- store.WithLock(writerCtx, false, func() error {
			if e := storage.CheckArtifactStorage(ctx, task, stage); e != nil {
				return e
			}
			file, e := os.OpenFile(filepath.Join(root, stage.BlobKey), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			defer file.Close()
			close(entered)
			<-release
			// Simulate an already-started filesystem write finishing after DB/context cancellation.
			_, e = file.WriteString("late filesystem completion")
			return e
		})
	}()
	select {
	case <-entered:
	case e := <-finished:
		t.Fatalf("writer failed: %v", e)
	case <-time.After(3 * time.Second):
		t.Fatal("writer not started")
	}
	cancelWriter()
	_, err = db.Exec(`UPDATE web_agent_tasks SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	cleanupCtx, cancelCleanup := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = service.CollectWebAgentStorage(cleanupCtx, storage, store)
	cancelCleanup()
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(release)
	require.NoError(t, <-finished)
	n, err := service.CollectWebAgentStorage(ctx, storage, store)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.ErrorIs(t, storage.CheckArtifactStorage(ctx, task, stage), service.ErrWebAgentLeaseLost)
	_, err = os.Stat(filepath.Join(root, stage.BlobKey))
	require.True(t, os.IsNotExist(err))
}

func TestWebAgentStoragePublicationRechecksLeaseAfterJournalLockWait(t *testing.T) {
	tasks, artifacts, storage, store, db, _ := agentStorageFixture(t)
	ctx := context.Background()
	task, a := writeReadyStorageFixture(t, tasks, storage, store, "publish-lock-expiry")
	block, err := db.Begin()
	require.NoError(t, err)
	defer block.Rollback()
	_, err = block.Exec(`SELECT id FROM web_agent_blob_stages WHERE id=$1 FOR UPDATE`, a.Stage.ID)
	require.NoError(t, err)
	publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, e := artifacts.PublishArtifact(publishCtx, task, a); finished <- e }()
	var application string
	require.NoError(t, db.QueryRow(`SELECT current_setting('application_name')`).Scan(&application))
	require.Eventually(t, func() bool {
		var waiting bool
		e := db.QueryRowContext(publishCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE '%SELECT id FROM web_agent_blob_stages%')`, application).Scan(&waiting)
		return e == nil && waiting
	}, 2*time.Second, 10*time.Millisecond, "publication must actually be blocked on the journal row")
	_, err = db.Exec(`UPDATE web_agent_tasks SET lease_expires_at=clock_timestamp()-interval '1 millisecond' WHERE id=$1`, task.ID)
	require.NoError(t, err)
	require.NoError(t, block.Commit())
	require.ErrorIs(t, <-finished, service.ErrWebAgentLeaseLost)
	list, err := artifacts.ListArtifacts(ctx, 1, 0, 0)
	require.NoError(t, err)
	require.Empty(t, list)
	n, err := service.CollectWebAgentStorage(ctx, storage, store)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestWebAgentStorageDeletedVersionNumbersAreNotReused(t *testing.T) {
	tasks, artifacts, storage, store, db, _ := agentStorageFixture(t)
	ctx := context.Background()
	task, a := writeReadyStorageFixture(t, tasks, storage, store, "version-root")
	root, err := artifacts.PublishArtifact(ctx, task, a)
	require.NoError(t, err)
	var lastID int64
	for version := 2; version <= 3; version++ {
		request := agentTask("version-" + string(rune('0'+version)))
		request.SourceArtifactID = &root.ID
		_, err = tasks.CreateTask(ctx, request)
		require.NoError(t, err)
		claimed, e := tasks.ClaimTask(ctx, uuid.NewString(), 30*time.Second)
		require.NoError(t, e)
		candidate := readyArtifactCandidate(t, artifacts, claimed)
		published, e := artifacts.PublishArtifact(ctx, claimed, candidate)
		require.NoError(t, e)
		require.Equal(t, version, published.Version)
		if version == 2 {
			lastID = published.ID
			require.NoError(t, storage.DeleteArtifact(ctx, 1, published.ID))
			_, e = service.CollectWebAgentStorage(ctx, storage, store)
			require.NoError(t, e)
		}
	}
	var spec string
	require.NoError(t, db.QueryRow(`SELECT spec FROM web_agent_artifacts WHERE id=$1`, lastID).Scan(&spec))
	require.Equal(t, "{}", spec)
}

func TestWebAgentStorageTransactionsUseFreshSnapshots(t *testing.T) {
	_, db := agentIntegrationRepo(t)
	db.SetMaxOpenConns(1)
	_, err := db.Exec(`SET default_transaction_isolation='repeatable read'`)
	require.NoError(t, err)
	tx, err := NewWebChatRepository(db).(*webChatRepository).beginAgentTransaction(context.Background())
	require.NoError(t, err)
	var isolation string
	require.NoError(t, tx.QueryRow(`SHOW transaction_isolation`).Scan(&isolation))
	require.Equal(t, "read committed", isolation)
	require.NoError(t, tx.Commit())
	_, err = db.Exec(`RESET default_transaction_isolation`)
	require.NoError(t, err)
}
