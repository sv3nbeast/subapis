package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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
	errWebAgentImageTooLarge = errors.New("generated image exceeds the artifact size budget")
	errWebAgentImageFormat   = errors.New("generated image is not a supported format")
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
	// Editing an existing image is a separate capability; accepting a source here
	// would silently ignore it and return an unrelated image.
	if task.SourceArtifactID != nil {
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
	output, err := e.caller.GenerateImage(ctx, session, key, task.Prompt)
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
