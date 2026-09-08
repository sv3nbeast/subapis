//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func artifactCandidate() *service.WebAgentArtifact {
	return &service.WebAgentArtifact{Kind: "document", Title: "Draft", Filename: "Draft.docx",
		MIME:    "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		BlobKey: uuid.NewString() + ".docx", PreviewKey: uuid.NewString() + ".pdf", SizeBytes: 100, PreviewBytes: 100,
		SHA256: strings.Repeat("a", 64), Spec: json.RawMessage(`{"kind":"document","title":"Draft"}`)}
}
func readyArtifactCandidate(t *testing.T, repo service.WebAgentArtifactRepository, task *service.WebAgentTask) *service.WebAgentArtifact {
	t.Helper()
	storage := repo.(service.WebAgentStorageRepository)
	stage, err := storage.ReserveArtifactStorage(context.Background(), task)
	require.NoError(t, err)
	a := artifactCandidate()
	a.Stage = stage
	a.BlobKey = stage.BlobKey
	a.PreviewKey = stage.PreviewKey
	require.NoError(t, storage.ReadyArtifactStorage(context.Background(), task, stage, a))
	return a
}
func TestWebAgentArtifactPublicationAndVersions(t *testing.T) {
	tasks, db := agentIntegrationRepo(t)
	repo := NewWebChatRepository(db).(service.WebAgentArtifactRepository)
	ctx := context.Background()
	created, err := tasks.CreateTask(ctx, agentTask("artifact-one"))
	require.NoError(t, err)
	claimed, err := tasks.ClaimTask(ctx, "artifact-lease-one", 30*time.Second)
	require.NoError(t, err)
	a, err := repo.PublishArtifact(ctx, claimed, readyArtifactCandidate(t, repo, claimed))
	require.NoError(t, err)
	require.Equal(t, 1, a.Version)
	finished, err := tasks.GetTask(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, service.WebAgentSucceeded, finished.Status)
	require.Contains(t, string(finished.Result), "artifact_id")
	_, err = repo.GetArtifact(ctx, 2, a.ID)
	require.ErrorIs(t, err, service.ErrWebAgentArtifactNotFound)
	_, err = repo.ArtifactVersions(ctx, 2, a.ID, 0)
	require.ErrorIs(t, err, service.ErrWebAgentArtifactNotFound)
	next := agentTask("artifact-two")
	next.SourceArtifactID = &a.ID
	_, err = tasks.CreateTask(ctx, next)
	require.NoError(t, err)
	claimed, err = tasks.ClaimTask(ctx, "artifact-lease-two", 30*time.Second)
	require.NoError(t, err)
	b, err := repo.PublishArtifact(ctx, claimed, readyArtifactCandidate(t, repo, claimed))
	require.NoError(t, err)
	require.Equal(t, 2, b.Version)
	require.Equal(t, a.LineageID, b.LineageID)
	require.Equal(t, &a.ID, b.ParentID)
	versions, err := repo.ArtifactVersions(ctx, 1, b.ID, 0)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	require.Equal(t, b.ID, versions[0].ID)
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "blob_key")
	require.NotContains(t, string(raw), string(b.Spec))
}
func TestWebAgentArtifactCancelAndStaleWorkerCannotPublish(t *testing.T) {
	tasks, db := agentIntegrationRepo(t)
	repo := NewWebChatRepository(db).(service.WebAgentArtifactRepository)
	ctx := context.Background()
	created, err := tasks.CreateTask(ctx, agentTask("artifact-cancel"))
	require.NoError(t, err)
	claimed, err := tasks.ClaimTask(ctx, "artifact-lease", 30*time.Second)
	require.NoError(t, err)
	stale := *claimed
	candidate := readyArtifactCandidate(t, repo, claimed)
	candidate.Generation = &service.WebAgentGeneration{RequestID: "cancelled-generation"}
	stale.LeaseToken = "not-the-owner"
	_, err = repo.PublishArtifact(ctx, &stale, candidate)
	require.ErrorIs(t, err, service.ErrWebAgentLeaseLost)
	_, err = tasks.CancelTask(ctx, 1, created.ID)
	require.NoError(t, err)
	a, err := repo.PublishArtifact(ctx, claimed, candidate)
	require.NoError(t, err)
	require.Nil(t, a)
	finished, err := tasks.GetTask(ctx, 1, created.ID)
	require.NoError(t, err)
	require.Equal(t, service.WebAgentCancelled, finished.Status)
	require.Contains(t, string(finished.Result), "cancelled-generation")
	require.NotContains(t, string(finished.Result), "artifact_id")
	list, err := repo.ListArtifacts(ctx, 1, 0, 0)
	require.NoError(t, err)
	require.Empty(t, list)
	events, err := tasks.ListTaskEvents(ctx, 1, created.ID, 0)
	require.NoError(t, err)
	require.Equal(t, "task.cancelled", events[len(events)-1].Type)
}
func TestWebAgentArtifactSourceCannotCrossUsersOrDisappearSilently(t *testing.T) {
	tasks, db := agentIntegrationRepo(t)
	repo := NewWebChatRepository(db).(service.WebAgentArtifactRepository)
	ctx := context.Background()
	_, err := tasks.CreateTask(ctx, agentTask("source-original"))
	require.NoError(t, err)
	claimed, err := tasks.ClaimTask(ctx, "source-lease", 30*time.Second)
	require.NoError(t, err)
	source, err := repo.PublishArtifact(ctx, claimed, readyArtifactCandidate(t, repo, claimed))
	require.NoError(t, err)
	wrong := agentTask("source-other-user")
	wrong.UserID = 2
	wrong.SessionID = 3
	wrong.SourceArtifactID = &source.ID
	_, err = tasks.CreateTask(ctx, wrong)
	require.ErrorIs(t, err, service.ErrWebAgentArtifactNotFound)
	revision := agentTask("source-revision")
	revision.SourceArtifactID = &source.ID
	_, err = tasks.CreateTask(ctx, revision)
	require.NoError(t, err)
	claimed, err = tasks.ClaimTask(ctx, "revision-lease", 30*time.Second)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE web_agent_artifacts SET deleted_at=now() WHERE id=$1`, source.ID)
	require.NoError(t, err)
	_, err = repo.PublishArtifact(ctx, claimed, readyArtifactCandidate(t, repo, claimed))
	require.ErrorIs(t, err, service.ErrWebAgentArtifactNotFound)
}
