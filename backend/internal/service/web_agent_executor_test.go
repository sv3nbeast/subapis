package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type agentPlanFunc func(context.Context, *WebAgentTask, *WebAgentArtifact) (*WebAgentPlan, error)

func (f agentPlanFunc) Plan(ctx context.Context, t *WebAgentTask, a *WebAgentArtifact) (*WebAgentPlan, error) {
	return f(ctx, t, a)
}

type agentRenderFunc func(context.Context, string, json.RawMessage) (*WebAgentRenderedArtifact, error)

func (f agentRenderFunc) Render(ctx context.Context, k string, s json.RawMessage) (*WebAgentRenderedArtifact, error) {
	return f(ctx, k, s)
}

type failingAgentPreviewStore struct{ WebAgentStagedBlobStore }

func (s failingAgentPreviewStore) PutKey(ctx context.Context, key string, data []byte) error {
	if strings.HasSuffix(key, ".pdf") {
		return errors.New("synthetic storage failure")
	}
	return s.WebAgentStagedBlobStore.PutKey(ctx, key, data)
}

type stagedExecutorRepo struct {
	artifactServiceRepoStub
	WebAgentStorageRepository
	stage *WebAgentBlobStage
}

func (r *stagedExecutorRepo) RegisterArtifactStore(context.Context, string) error { return nil }
func (r *stagedExecutorRepo) ReserveArtifactStorage(_ context.Context, t *WebAgentTask) (*WebAgentBlobStage, error) {
	r.stage = &WebAgentBlobStage{ID: 1, TaskID: t.ID, UserID: t.UserID, LeaseToken: t.LeaseToken, State: "allocated", BlobKey: "ffffffff-ffff-ffff-ffff-ffffffffffff.docx", PreviewKey: "ffffffff-ffff-ffff-ffff-ffffffffffff.pdf"}
	return r.stage, nil
}
func (r *stagedExecutorRepo) CheckArtifactStorage(context.Context, *WebAgentTask, *WebAgentBlobStage) error {
	if r.stage.State != "allocated" {
		return ErrWebAgentLeaseLost
	}
	return nil
}
func (r *stagedExecutorRepo) ReadyArtifactStorage(_ context.Context, _ *WebAgentTask, _ *WebAgentBlobStage, a *WebAgentArtifact) error {
	if err := a.Validate(); err != nil {
		return err
	}
	r.stage.State = "ready"
	return nil
}
func (r *stagedExecutorRepo) AbandonArtifactStorage(context.Context, int64, int64, string) error {
	if r.stage != nil {
		r.stage.State = "abandoned"
	}
	return nil
}
func (r *stagedExecutorRepo) CollectArtifactStorage(_ context.Context, remove func(*WebAgentBlobStage) error) (int, error) {
	if r.stage == nil || r.stage.State != "abandoned" {
		return 0, nil
	}
	if err := remove(r.stage); err != nil {
		return 0, err
	}
	r.stage = nil
	return 1, nil
}

func TestWebAgentExecutorCleansFailedPartialFileWithoutAnotherModelCall(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private-artifacts")
	store, err := NewWebAgentFileStore(root)
	require.NoError(t, err)
	calls := 0
	planner := agentPlanFunc(func(context.Context, *WebAgentTask, *WebAgentArtifact) (*WebAgentPlan, error) {
		calls++
		return &WebAgentPlan{Spec: json.RawMessage(`{"kind":"document","title":"test"}`), Generation: &WebAgentGeneration{RequestID: "one-generation"}}, nil
	})
	// Synthetic transport output here tests cleanup, not Office format validity.
	renderer := agentRenderFunc(func(context.Context, string, json.RawMessage) (*WebAgentRenderedArtifact, error) {
		return &WebAgentRenderedArtifact{Extension: "docx", MIME: webAgentArtifactTypes["document"].mime, File: []byte("file"), PreviewPDF: []byte("%PDF-preview"), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("file")))}, nil
	})
	repo := &stagedExecutorRepo{}
	executor := NewWebAgentOfficeExecutor(planner, renderer, failingAgentPreviewStore{store}, repo)
	a, err := executor.Execute(context.Background(), agentWorkerFixture().task, func(string, string) error { return nil })
	require.ErrorContains(t, err, "synthetic storage failure")
	require.Equal(t, 1, calls)
	require.Equal(t, "one-generation", a.Generation.RequestID)
	require.Equal(t, "abandoned", repo.stage.State)
	require.NoError(t, executor.Discard(context.Background(), a), "repeated abandonment must be harmless")
	n, err := CollectWebAgentStorage(context.Background(), repo, store)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	files, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, file := range files {
		require.False(t, webAgentBlobKey.MatchString(file.Name()))
	}
}

type trackingAgentExecutor struct {
	calls, discards int
	action          func()
}

func (e *trackingAgentExecutor) Execute(context.Context, *WebAgentTask, func(string, string) error) (*WebAgentArtifact, error) {
	e.calls++
	if e.action != nil {
		e.action()
	}
	return agentTestArtifact(), nil
}
func (e *trackingAgentExecutor) Discard(context.Context, *WebAgentArtifact) error {
	e.discards++
	return nil
}

type uncertainAgentPublisher struct {
	*agentTaskStub
	committed bool
	publishes int
	finishes  int
}

func (r *uncertainAgentPublisher) PublishArtifact(context.Context, *WebAgentTask, *WebAgentArtifact) (*WebAgentArtifact, error) {
	r.publishes++
	if r.committed {
		r.finished = WebAgentSucceeded
	}
	return nil, io.ErrUnexpectedEOF
}
func (r *uncertainAgentPublisher) FinishTask(context.Context, int64, string, string, json.RawMessage, string) error {
	r.finishes++
	return nil
}
func agentWorkerFixture() *agentTaskStub {
	group := int64(7)
	return &agentTaskStub{task: &WebAgentTask{ID: 11, UserID: 1, SessionID: 2, GroupID: &group, Model: "test-model", Kind: "document", DeadlineAt: time.Now().Add(time.Minute)}}
}
func TestWebAgentWorkerDoesNotDeleteFilesAfterUncertainPublication(t *testing.T) {
	for _, committed := range []bool{false, true} {
		repo := &uncertainAgentPublisher{agentTaskStub: agentWorkerFixture(), committed: committed}
		executor := &trackingAgentExecutor{}
		worker := NewWebAgentService(repo, agentTestChat(), executor)
		did, err := worker.RunOnce(context.Background())
		require.True(t, did)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.Zero(t, executor.discards, "acknowledgement loss does not prove rollback")
		require.Zero(t, repo.finishes)
		did, err = worker.RunOnce(context.Background())
		require.False(t, did)
		require.NoError(t, err)
		require.Equal(t, 1, executor.calls)
		require.Equal(t, 1, repo.publishes)
		if committed {
			require.Equal(t, WebAgentSucceeded, repo.finished)
		}
	}
}
func TestWebAgentWorkerDiscardsWhenCancellationWinsPublication(t *testing.T) {
	repo := agentWorkerFixture()
	executor := &trackingAgentExecutor{action: func() { repo.mu.Lock(); repo.task.Status = WebAgentCancelRequested; repo.mu.Unlock() }}
	worker := NewWebAgentService(repo, agentTestChat(), executor)
	_, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, WebAgentCancelled, repo.finished)
	require.Equal(t, 1, executor.discards)
}

type recordingAgentFailure struct {
	*agentTaskStub
	result json.RawMessage
	code   string
}

func (r *recordingAgentFailure) FinishTask(_ context.Context, _ int64, _ string, status string, result json.RawMessage, code string) error {
	r.finished, r.result, r.code = status, result, code
	return nil
}
func TestWebAgentFailedGenerationRetainsTraceWithoutPublishingPartialArtifact(t *testing.T) {
	repo := &recordingAgentFailure{agentTaskStub: agentWorkerFixture()}
	executor := agentExecutorFunc(func(context.Context, *WebAgentTask, func(string, string) error) (*WebAgentArtifact, error) {
		a := agentTestArtifact()
		a.Generation = &WebAgentGeneration{RequestID: "trace-of-incomplete-generation"}
		return a, webAgentFailure("model_incomplete", errors.New("synthetic incomplete response"))
	})
	worker := NewWebAgentService(repo, agentTestChat(), executor)
	_, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, WebAgentFailed, repo.finished)
	require.Equal(t, "model_incomplete", repo.code)
	require.Contains(t, string(repo.result), "trace-of-incomplete-generation")
	require.NotContains(t, string(repo.result), "artifact_id")
	require.NotContains(t, string(repo.result), "blob_key")
	require.NotContains(t, string(repo.result), "spec")
}

type knownFailurePublisher struct{ *recordingAgentFailure }

func (r knownFailurePublisher) PublishArtifact(context.Context, *WebAgentTask, *WebAgentArtifact) (*WebAgentArtifact, error) {
	return nil, ErrWebAgentStorageLimit
}
func TestWebAgentKnownPublicationRejectionFailsAndCleansUp(t *testing.T) {
	recorded := &recordingAgentFailure{agentTaskStub: agentWorkerFixture()}
	repo := knownFailurePublisher{recorded}
	executor := &trackingAgentExecutor{}
	worker := NewWebAgentService(repo, agentTestChat(), executor)
	_, err := worker.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, executor.discards)
	require.Equal(t, WebAgentFailed, recorded.finished)
	require.Equal(t, "storage_limit", recorded.code)
}

type fullArtifactStorage struct{ stagedExecutorRepo }

func (r *fullArtifactStorage) ReserveArtifactStorage(context.Context, *WebAgentTask) (*WebAgentBlobStage, error) {
	return nil, ErrWebAgentStorageLimit
}
func TestWebAgentExecutorDoesNotCallModelWhenStorageReservationFails(t *testing.T) {
	store, err := NewWebAgentFileStore(filepath.Join(t.TempDir(), "private-store"))
	require.NoError(t, err)
	calls := 0
	planner := agentPlanFunc(func(context.Context, *WebAgentTask, *WebAgentArtifact) (*WebAgentPlan, error) {
		calls++
		return nil, nil
	})
	renderer := agentRenderFunc(func(context.Context, string, json.RawMessage) (*WebAgentRenderedArtifact, error) {
		t.Error("renderer must not run")
		return nil, nil
	})
	executor := NewWebAgentOfficeExecutor(planner, renderer, store, &fullArtifactStorage{})
	_, err = executor.Execute(context.Background(), agentWorkerFixture().task, func(string, string) error { return nil })
	require.ErrorIs(t, err, ErrWebAgentStorageLimit)
	require.Zero(t, calls)
}
