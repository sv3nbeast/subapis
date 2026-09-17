package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Progress step names reach the browser's task feed, matching the Office
// executor's Chinese step labels so both render the same way.
const (
	webAgentImageStepGenerate = "生成图片"
	webAgentImageStepStore    = "保存成果"

	webAgentImageFallbackTitle = "生成的图片"
)

var (
	errWebAgentImageTooLarge     = errors.New("generated image exceeds the artifact size budget")
	errWebAgentImageFormat       = errors.New("generated image is not a supported format")
	errWebAgentImageInputType    = errors.New("only an image can be used as an image edit input")
	errWebAgentImageInputSize    = errors.New("input image exceeds the artifact size budget")
	errWebAgentImageInputGarbled = errors.New("input image is not a supported image format")
)

// Artifact digests are hex-encoded SHA-256 over the stored bytes.
func webAgentBlobDigest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// An image task calls the gateway's images endpoint and stores what it returns.
// It runs no renderer: the returned bytes are already the deliverable, so this
// executor needs neither the Office renderer nor a plan schema.
type WebAgentImageExecutor struct {
	chat    *WebChatService
	caller  WebAgentImageCaller
	store   WebAgentBlobStore
	repo    WebAgentArtifactRepository
	storage WebAgentStorageRepository
	staged  WebAgentStagedBlobStore
}

func NewWebAgentImageExecutor(chat *WebChatService, caller WebAgentImageCaller, store WebAgentBlobStore, repo WebAgentArtifactRepository) *WebAgentImageExecutor {
	storage, _ := repo.(WebAgentStorageRepository)
	staged, _ := store.(WebAgentStagedBlobStore)
	return &WebAgentImageExecutor{chat: chat, caller: caller, store: store, repo: repo, storage: storage, staged: staged}
}

// inputImages resolves an edit's source images to bytes: a previously generated
// artifact and/or uploaded images. Both are already owner-scoped by the task
// row, and both are re-checked here against this task's user and session.
//
// Every failure is explicit. Dropping an unreadable input and generating from
// the prompt alone would return an unrelated image and still bill for it.
func (e *WebAgentImageExecutor) inputImages(ctx context.Context, task *WebAgentTask, session *WebChatSession) ([]WebAgentImageInput, error) {
	if task.SourceArtifactID == nil && len(task.DocumentIDs) == 0 {
		return nil, nil
	}
	inputs := make([]WebAgentImageInput, 0, webAgentSourceImageCount(task.SourceArtifactID)+len(task.DocumentIDs))
	if task.SourceArtifactID != nil {
		source, err := e.repo.GetArtifact(ctx, task.UserID, *task.SourceArtifactID)
		if err != nil {
			return nil, err
		}
		if source.UserID != task.UserID || source.SessionID != task.SessionID || source.Kind != task.Kind {
			return nil, ErrWebAgentArtifactNotFound
		}
		if _, ok := webAgentImageFormats[source.MIME]; !ok {
			return nil, webAgentFailure("source_unavailable", errWebAgentImageInputType)
		}
		data, err := webAgentReadBlob(ctx, e.store, source.BlobKey, source.SizeBytes)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, WebAgentImageInput{Data: data, MIME: source.MIME})
	}
	documents := e.chat.documents
	if len(task.DocumentIDs) > 0 && (documents == nil || !documents.FeatureEnabled(ctx)) {
		return nil, ErrWebChatFilesDisabled
	}
	for _, id := range task.DocumentIDs {
		doc, err := documents.Get(ctx, task.UserID, id)
		if err != nil {
			return nil, err
		}
		if doc.UserID != task.UserID || !doc.Enabled || doc.Status != WebChatDocumentStatusReady || doc.DeletedAt != nil {
			return nil, ErrWebChatDocumentNotReady
		}
		inSession := doc.SessionID != nil && *doc.SessionID == task.SessionID
		inProject := doc.ProjectID != nil && session.ProjectID != nil && *doc.ProjectID == *session.ProjectID
		if !inSession && !inProject {
			return nil, ErrWebChatDocumentNotFound
		}
		// A text document carries nothing an image model can edit. Refuse instead
		// of sending a prompt-only request that answers an unrelated image.
		if !IsWebChatImageDocument(doc.Extension) {
			return nil, webAgentFailure("document_unavailable", errWebAgentImageInputType)
		}
		if doc.SizeBytes <= 0 || doc.SizeBytes > webAgentArtifactMaxBytes {
			return nil, webAgentFailure("document_unavailable", errWebAgentImageInputSize)
		}
		_, reader, err := documents.OpenDownload(ctx, task.UserID, id)
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(reader, doc.SizeBytes+1))
		_ = reader.Close()
		if err != nil {
			return nil, err
		}
		mime, _, ok := sniffWebAgentImageFormat(data)
		if int64(len(data)) != doc.SizeBytes || !ok {
			return nil, webAgentFailure("document_unavailable", errWebAgentImageInputGarbled)
		}
		inputs = append(inputs, WebAgentImageInput{Data: data, MIME: mime})
	}
	if len(inputs) > WebAgentImageMaxInputImages {
		return nil, ErrWebAgentInvalid
	}
	return inputs, nil
}

// webAgentReadBlob reads a stored blob whose length must match the recorded one:
// a short read would send a truncated image the provider silently reinterprets.
func webAgentReadBlob(ctx context.Context, store WebAgentBlobStore, key string, expected int64) ([]byte, error) {
	if expected <= 0 || expected > webAgentArtifactMaxBytes {
		return nil, webAgentFailure("source_unavailable", errWebAgentImageInputSize)
	}
	reader, size, err := store.Open(ctx, key)
	if err != nil {
		return nil, ErrWebAgentArtifactNotFound
	}
	defer reader.Close()
	if size != expected {
		return nil, webAgentFailure("source_unavailable", errors.New("input image integrity mismatch"))
	}
	data, err := io.ReadAll(io.LimitReader(reader, expected+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != expected {
		return nil, webAgentFailure("source_unavailable", errors.New("input image integrity mismatch"))
	}
	return data, nil
}

func (e *WebAgentImageExecutor) Execute(ctx context.Context, task *WebAgentTask, progress func(string, string) error) (artifact *WebAgentArtifact, err error) {
	if e == nil || e.chat == nil || e.caller == nil || e.store == nil || e.repo == nil || e.storage == nil || e.staged == nil {
		return nil, ErrWebAgentUnavailable
	}
	if task == nil || progress == nil || task.Kind != "image" {
		return nil, ErrWebAgentInvalid
	}
	if !e.chat.FeatureEnabled(ctx) {
		return nil, ErrWebChatDisabled
	}
	if task.UserID <= 0 || task.GroupID == nil || len(task.SessionSnapshot) > 1<<20 {
		return nil, ErrWebAgentInvalid
	}
	if webAgentSourceImageCount(task.SourceArtifactID)+len(task.DocumentIDs) > WebAgentImageMaxInputImages {
		return nil, ErrWebAgentInvalid
	}

	artifact = &WebAgentArtifact{Kind: task.Kind, TaskID: task.ID, UserID: task.UserID, SessionID: task.SessionID}
	ready := false
	defer func() {
		if !ready {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = e.Discard(cleanup, artifact)
		}
	}()

	if _, err = e.chat.GetSession(ctx, task.UserID, task.SessionID); err != nil {
		return artifact, err
	}
	group, _, err := e.chat.validateGroupModel(ctx, task.UserID, *task.GroupID, task.Model)
	if err != nil {
		return artifact, err
	}
	var snapshot struct {
		Session *WebChatSession `json:"session"`
	}
	if json.Unmarshal(task.SessionSnapshot, &snapshot) != nil || snapshot.Session == nil {
		return artifact, ErrWebAgentInvalid
	}
	session := snapshot.Session
	if session.ID != task.SessionID || session.UserID != task.UserID || session.GroupID != *task.GroupID || session.Model != task.Model {
		return artifact, ErrWebAgentInvalid
	}
	session.Platform = group.Platform

	// Resolve the edit inputs before reserving storage or minting a key: an
	// unreadable input must fail before anything is spent.
	inputs, err := e.inputImages(ctx, task, session)
	if err != nil {
		return artifact, err
	}

	// Reserve storage before spending money, so a full quota fails cheaply.
	if err = e.storage.RegisterArtifactStore(ctx, e.staged.StorageID()); err != nil {
		return artifact, err
	}
	stage, err := e.storage.ReserveArtifactStorage(ctx, task)
	if err != nil {
		return artifact, err
	}
	artifact.Stage = stage
	artifact.BlobKey, artifact.PreviewKey = stage.BlobKey, stage.PreviewKey

	if err = progress("started", webAgentImageStepGenerate); err != nil {
		return artifact, err
	}
	key, err := e.chat.ensureManagedKey(ctx, task.UserID, group)
	if err != nil {
		return artifact, err
	}
	if key == nil || key.UserID != task.UserID || key.GroupID == nil || *key.GroupID != *task.GroupID {
		return artifact, ErrWebAgentUnavailable
	}
	output, err := e.caller.GenerateImage(ctx, session, key, task.Prompt, inputs)
	if output != nil {
		artifact.Generation = output.Generation
	}
	if err != nil {
		return artifact, err
	}
	if output == nil || len(output.Data) == 0 {
		return artifact, ErrWebAgentInvalid
	}
	if int64(len(output.Data)) > webAgentArtifactMaxBytes {
		return artifact, webAgentFailure("output_budget_exceeded", errWebAgentImageTooLarge)
	}
	// Providers differ here: gpt-image answers PNG while grok-imagine answers
	// JPEG for the same request. The artifact records what actually came back,
	// which is why its key carries no extension.
	if _, ok := webAgentImageFormats[output.MIME]; !ok {
		return artifact, webAgentFailure("model_response_invalid", errWebAgentImageFormat)
	}
	artifact.Title = webAgentImageTitle(task.Prompt)
	artifact.MIME = output.MIME
	artifact.Filename = webAgentArtifactFilename(artifact.Title, output.Extension)
	artifact.SHA256 = webAgentBlobDigest(output.Data)
	artifact.SizeBytes, artifact.PreviewBytes = int64(len(output.Data)), 0
	if err = progress("completed", webAgentImageStepGenerate); err != nil {
		return artifact, err
	}

	if err = progress("started", webAgentImageStepStore); err != nil {
		return artifact, err
	}
	err = e.staged.WithLock(ctx, false, func() error {
		if err := e.storage.CheckArtifactStorage(ctx, task, stage); err != nil {
			return err
		}
		if err := e.staged.PutKey(ctx, stage.BlobKey, output.Data); err != nil {
			return err
		}
		return e.storage.ReadyArtifactStorage(ctx, task, stage, artifact)
	})
	if err != nil {
		return artifact, webAgentFailure("storage_failed", err)
	}
	if err = artifact.Validate(); err != nil {
		return artifact, err
	}
	if err = progress("completed", webAgentImageStepStore); err != nil {
		return artifact, err
	}
	ready = true
	return artifact, nil
}

func (e *WebAgentImageExecutor) Discard(ctx context.Context, a *WebAgentArtifact) error {
	if a == nil || a.Stage == nil {
		return nil
	}
	return e.storage.AbandonArtifactStorage(ctx, a.Stage.ID, a.Stage.TaskID, a.Stage.LeaseToken)
}

// The prompt is the only title material an image task has. Keep one line within
// the artifact title budget instead of storing a wall of text.
func webAgentImageTitle(prompt string) string {
	title := strings.TrimSpace(strings.Join(strings.Fields(prompt), " "))
	if title == "" {
		return webAgentImageFallbackTitle
	}
	const limit = 60
	if utf8.RuneCountInString(title) > limit {
		runes := []rune(title)
		title = strings.TrimSpace(string(runes[:limit]))
	}
	if title == "" {
		return webAgentImageFallbackTitle
	}
	return title
}
