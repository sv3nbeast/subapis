package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// imageEditStore serves both the artifact blob store and the staged store the
// executor publishes through.
type imageEditRepo struct {
	artifactServiceRepoStub
	WebAgentStorageRepository
	stage *WebAgentBlobStage
}

func (r *imageEditRepo) RegisterArtifactStore(context.Context, string) error { return nil }
func (r *imageEditRepo) ReserveArtifactStorage(_ context.Context, t *WebAgentTask) (*WebAgentBlobStage, error) {
	r.stage = &WebAgentBlobStage{ID: 1, TaskID: t.ID, UserID: t.UserID, LeaseToken: t.LeaseToken, State: "allocated", BlobKey: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"}
	return r.stage, nil
}
func (r *imageEditRepo) CheckArtifactStorage(context.Context, *WebAgentTask, *WebAgentBlobStage) error {
	return nil
}
func (r *imageEditRepo) ReadyArtifactStorage(_ context.Context, _ *WebAgentTask, _ *WebAgentBlobStage, a *WebAgentArtifact) error {
	return a.Validate()
}
func (r *imageEditRepo) AbandonArtifactStorage(context.Context, int64, int64, string) error {
	return nil
}

type imageEditCaller struct {
	calls  int
	inputs []WebAgentImageInput
}

func (c *imageEditCaller) GenerateImage(_ context.Context, _ *WebChatSession, _ *APIKey, _ string, inputs []WebAgentImageInput) (*WebAgentImageOutput, error) {
	c.calls++
	c.inputs = inputs
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("edited")...)
	return &WebAgentImageOutput{Data: png, MIME: "image/png", Extension: "png", Generation: &WebAgentGeneration{RequestID: "edit-1"}}, nil
}

type imageEditDocs struct {
	webChatDocumentRepoTestDouble
	doc  *WebChatDocument
	data []byte
}

func (r *imageEditDocs) GetDocument(_ context.Context, userID, id int64) (*WebChatDocument, error) {
	if r.doc == nil || r.doc.UserID != userID || r.doc.ID != id {
		return nil, ErrWebChatDocumentNotFound
	}
	copied := *r.doc
	return &copied, nil
}

type imageEditDocStore struct{ data []byte }

func (s imageEditDocStore) Upload(context.Context, string, io.Reader, string) (int64, error) {
	return 0, nil
}
func (s imageEditDocStore) Download(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.data)), nil
}
func (s imageEditDocStore) Delete(context.Context, string) error { return nil }
func (s imageEditDocStore) HeadBucket(context.Context) error     { return nil }

func imageEditTask(t *testing.T) *WebAgentTask {
	t.Helper()
	session, err := agentTestChat().GetSession(context.Background(), 1, 2)
	require.NoError(t, err)
	snapshot, err := json.Marshal(map[string]any{"session": session})
	require.NoError(t, err)
	group := session.GroupID
	return &WebAgentTask{ID: 11, UserID: 1, SessionID: 2, GroupID: &group, Model: "test-model",
		Kind: "image", Prompt: "add a hat", SessionSnapshot: snapshot, DeadlineAt: time.Now().Add(time.Minute)}
}

func imageEditChat(t *testing.T, docs *imageEditDocs) *WebChatService {
	t.Helper()
	chat := NewWebChatService(agentChatStub{}, agentPlannerKeyRepo{}, &agentPlannerKeys{}, agentCatalogStub{}, agentRuntimeStub{})
	if docs == nil {
		return chat
	}
	settings := newWebChatDocumentSettingsTestDouble()
	settings.values[SettingKeyWebChatFilesEnabled] = "true"
	settings.values[settingKeyWebChatDocumentS3] = `{"bucket":"b","access_key_id":"k","secret_access_key":"s"}`
	documents := NewWebChatDocumentService(docs, settings, passthroughSecretEncryptor{}, func(context.Context, *WebChatDocumentS3Config) (WebChatDocumentStore, error) {
		return imageEditDocStore{data: docs.data}, nil
	})
	chat.SetDocumentService(documents)
	return chat
}

// An image edit reads its inputs from a generated artifact and/or uploaded
// images, and sends the bytes of both to the model.
func TestWebAgentImageEditLoadsInputsFromArtifactAndUpload(t *testing.T) {
	sourcePNG := append([]byte("\x89PNG\r\n\x1a\n"), []byte("source")...)
	uploadJPEG := append([]byte{0xFF, 0xD8, 0xFF}, []byte("upload")...)

	store, err := NewWebAgentFileStore(filepath.Join(t.TempDir(), "private-artifacts"))
	require.NoError(t, err)
	key := "dddddddd-dddd-dddd-dddd-dddddddddddd"
	require.NoError(t, store.PutKey(context.Background(), key, sourcePNG))

	sourceID := int64(9)
	docs := &imageEditDocs{
		doc:  &WebChatDocument{ID: 4, UserID: 1, SessionID: ptrInt64(2), Extension: ".jpg", SizeBytes: int64(len(uploadJPEG)), Status: WebChatDocumentStatusReady, Enabled: true},
		data: uploadJPEG,
	}
	repo := &imageEditRepo{artifactServiceRepoStub: artifactServiceRepoStub{artifact: &WebAgentArtifact{
		ID: sourceID, UserID: 1, SessionID: 2, Kind: "image", MIME: "image/png", BlobKey: key, SizeBytes: int64(len(sourcePNG)),
	}}}
	caller := &imageEditCaller{}
	task := imageEditTask(t)
	task.SourceArtifactID = &sourceID
	task.DocumentIDs = []int64{4}

	executor := NewWebAgentImageExecutor(imageEditChat(t, docs), caller, store, repo)
	artifact, err := executor.Execute(context.Background(), task, func(string, string) error { return nil })
	require.NoError(t, err)
	require.Equal(t, 1, caller.calls)
	require.Len(t, caller.inputs, 2, "both the generated source and the upload must reach the model")
	require.Equal(t, sourcePNG, caller.inputs[0].Data)
	require.Equal(t, "image/png", caller.inputs[0].MIME)
	require.Equal(t, uploadJPEG, caller.inputs[1].Data)
	require.Equal(t, "image/jpeg", caller.inputs[1].MIME, "the MIME follows the bytes, not the file name")
	require.Equal(t, "image/png", artifact.MIME)
}

// A prompt-only image task still works and sends no inputs.
func TestWebAgentImageWithoutInputsStaysTextToImage(t *testing.T) {
	store, err := NewWebAgentFileStore(filepath.Join(t.TempDir(), "private-artifacts"))
	require.NoError(t, err)
	caller := &imageEditCaller{}
	executor := NewWebAgentImageExecutor(imageEditChat(t, nil), caller, store, &imageEditRepo{})
	_, err = executor.Execute(context.Background(), imageEditTask(t), func(string, string) error { return nil })
	require.NoError(t, err)
	require.Equal(t, 1, caller.calls)
	require.Empty(t, caller.inputs)
}

// An unusable input fails the task instead of silently degrading to
// text-to-image, which would return an unrelated image and still bill for it.
func TestWebAgentImageEditFailsRatherThanDroppingAnUnusableInput(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("source")...)
	sourceID := int64(9)

	t.Run("text document", func(t *testing.T) {
		store, err := NewWebAgentFileStore(filepath.Join(t.TempDir(), "private-artifacts"))
		require.NoError(t, err)
		docs := &imageEditDocs{
			doc:  &WebChatDocument{ID: 4, UserID: 1, SessionID: ptrInt64(2), Extension: ".pdf", SizeBytes: 9, Status: WebChatDocumentStatusReady, Enabled: true},
			data: []byte("%PDF-1.4 "),
		}
		caller := &imageEditCaller{}
		task := imageEditTask(t)
		task.DocumentIDs = []int64{4}
		executor := NewWebAgentImageExecutor(imageEditChat(t, docs), caller, store, &imageEditRepo{})
		_, err = executor.Execute(context.Background(), task, func(string, string) error { return nil })
		require.Error(t, err)
		require.Equal(t, "document_unavailable", webAgentFailureCode(err))
		require.Zero(t, caller.calls, "no model call may be billed for an unusable input")
	})

	t.Run("non-image artifact", func(t *testing.T) {
		store, err := NewWebAgentFileStore(filepath.Join(t.TempDir(), "private-artifacts"))
		require.NoError(t, err)
		key, err := store.Put(context.Background(), "docx", []byte("office"))
		require.NoError(t, err)
		repo := &imageEditRepo{artifactServiceRepoStub: artifactServiceRepoStub{artifact: &WebAgentArtifact{
			ID: sourceID, UserID: 1, SessionID: 2, Kind: "image", MIME: webAgentArtifactTypes["document"].mime, BlobKey: key, SizeBytes: 6,
		}}}
		caller := &imageEditCaller{}
		task := imageEditTask(t)
		task.SourceArtifactID = &sourceID
		executor := NewWebAgentImageExecutor(imageEditChat(t, nil), caller, store, repo)
		_, err = executor.Execute(context.Background(), task, func(string, string) error { return nil })
		require.Error(t, err)
		require.Equal(t, "source_unavailable", webAgentFailureCode(err))
		require.Zero(t, caller.calls)
	})

	t.Run("truncated artifact blob", func(t *testing.T) {
		store, err := NewWebAgentFileStore(filepath.Join(t.TempDir(), "private-artifacts"))
		require.NoError(t, err)
		key := "dddddddd-dddd-dddd-dddd-dddddddddddd"
		require.NoError(t, store.PutKey(context.Background(), key, png))
		repo := &imageEditRepo{artifactServiceRepoStub: artifactServiceRepoStub{artifact: &WebAgentArtifact{
			ID: sourceID, UserID: 1, SessionID: 2, Kind: "image", MIME: "image/png", BlobKey: key, SizeBytes: int64(len(png)) + 1,
		}}}
		caller := &imageEditCaller{}
		task := imageEditTask(t)
		task.SourceArtifactID = &sourceID
		executor := NewWebAgentImageExecutor(imageEditChat(t, nil), caller, store, repo)
		_, err = executor.Execute(context.Background(), task, func(string, string) error { return nil })
		require.ErrorContains(t, err, "integrity mismatch")
		require.Zero(t, caller.calls)
	})

	t.Run("source from another session", func(t *testing.T) {
		store, err := NewWebAgentFileStore(filepath.Join(t.TempDir(), "private-artifacts"))
		require.NoError(t, err)
		key := "dddddddd-dddd-dddd-dddd-dddddddddddd"
		require.NoError(t, store.PutKey(context.Background(), key, png))
		repo := &imageEditRepo{artifactServiceRepoStub: artifactServiceRepoStub{artifact: &WebAgentArtifact{
			ID: sourceID, UserID: 1, SessionID: 99, Kind: "image", MIME: "image/png", BlobKey: key, SizeBytes: int64(len(png)),
		}}}
		caller := &imageEditCaller{}
		task := imageEditTask(t)
		task.SourceArtifactID = &sourceID
		executor := NewWebAgentImageExecutor(imageEditChat(t, nil), caller, store, repo)
		_, err = executor.Execute(context.Background(), task, func(string, string) error { return nil })
		require.ErrorIs(t, err, ErrWebAgentArtifactNotFound)
		require.Zero(t, caller.calls)
	})
}

func ptrInt64(v int64) *int64 { return &v }
