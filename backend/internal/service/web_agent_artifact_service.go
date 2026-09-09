package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrWebAgentArtifactNotFound = infraerrors.NotFound("WEB_AGENT_ARTIFACT_NOT_FOUND", "artifact not found")
var ErrWebAgentStorageLimit = infraerrors.BadRequest("WEB_AGENT_STORAGE_LIMIT", "artifact storage limit reached")
var ErrWebAgentStorageIdentity = infraerrors.ServiceUnavailable("WEB_AGENT_STORAGE_IDENTITY", "artifact storage identity mismatch or legacy storage requires verified adoption")

type WebAgentArtifact struct {
	Stage        *WebAgentBlobStage  `json:"-"`
	Generation   *WebAgentGeneration `json:"-"`
	ID           int64               `json:"id"`
	TaskID       int64               `json:"task_id"`
	UserID       int64               `json:"-"`
	SessionID    int64               `json:"session_id"`
	LineageID    string              `json:"lineage_id"`
	Version      int                 `json:"version"`
	ParentID     *int64              `json:"parent_id,omitempty"`
	Kind         string              `json:"kind"`
	Title        string              `json:"title"`
	Filename     string              `json:"filename"`
	MIME         string              `json:"mime"`
	BlobKey      string              `json:"-"`
	PreviewKey   string              `json:"-"`
	SizeBytes    int64               `json:"size_bytes"`
	PreviewBytes int64               `json:"-"`
	SHA256       string              `json:"sha256"`
	Spec         json.RawMessage     `json:"-"`
	CreatedAt    time.Time           `json:"created_at"`
}

type WebAgentGeneration struct {
	Template        *WebAgentTemplateSnapshot `json:"template,omitempty"`
	Sources         []WebChatSource           `json:"sources,omitempty"`
	ClientRequestID string                    `json:"client_request_id,omitempty"`
	RequestID       string                    `json:"request_id,omitempty"`
	Usage           *WebChatUsage             `json:"usage,omitempty"`
}
type WebAgentArtifactRepository interface {
	// Publish atomically saves the artifact version and completes its live task.
	// nil,nil means cancellation won; no file is published.
	PublishArtifact(context.Context, *WebAgentTask, *WebAgentArtifact) (*WebAgentArtifact, error)
	GetArtifact(context.Context, int64, int64) (*WebAgentArtifact, error)
	ListArtifacts(context.Context, int64, int64, int64) ([]WebAgentArtifact, error)
	ArtifactVersions(context.Context, int64, int64, int64) ([]WebAgentArtifact, error)
}

func (a *WebAgentArtifact) Validate() error {
	if a == nil {
		return ErrWebAgentInvalid
	}
	format, ok := webAgentArtifactTypes[a.Kind]
	if !ok {
		return ErrWebAgentInvalid
	}
	if !utf8.ValidString(a.Title) || strings.TrimSpace(a.Title) == "" || utf8.RuneCountInString(a.Title) > 120 ||
		a.Filename != webAgentArtifactFilename(a.Title, format.ext) || a.MIME != format.mime ||
		!webAgentBlobKey.MatchString(a.BlobKey) || !strings.HasSuffix(a.BlobKey, "."+format.ext) ||
		!webAgentBlobKey.MatchString(a.PreviewKey) || !strings.HasSuffix(a.PreviewKey, ".pdf") ||
		a.SizeBytes <= 0 || a.PreviewBytes <= 0 || a.SizeBytes > webAgentArtifactMaxBytes || a.PreviewBytes > webAgentArtifactMaxBytes ||
		a.SizeBytes+a.PreviewBytes > webAgentArtifactMaxBytes || len(a.Spec) > 1<<20 || !json.Valid(a.Spec) {
		return ErrWebAgentInvalid
	}
	hash, err := hex.DecodeString(a.SHA256)
	if err != nil || len(hash) != 32 {
		return ErrWebAgentInvalid
	}
	var spec struct{ Kind, Title string }
	if json.Unmarshal(a.Spec, &spec) != nil || spec.Kind != a.Kind || spec.Title != a.Title {
		return ErrWebAgentInvalid
	}
	return nil
}

type WebAgentArtifactService struct {
	repo  WebAgentArtifactRepository
	chat  *WebChatService
	store WebAgentBlobStore
}

func NewWebAgentArtifactService(repo WebAgentArtifactRepository, chat *WebChatService, store WebAgentBlobStore) *WebAgentArtifactService {
	return &WebAgentArtifactService{repo: repo, chat: chat, store: store}
}
func (s *WebAgentArtifactService) readable(ctx context.Context) error {
	if s == nil || s.repo == nil || s.chat == nil {
		return ErrWebAgentUnavailable
	}
	if !s.chat.FeatureEnabled(ctx) {
		return ErrWebChatDisabled
	}
	return nil
}
func (s *WebAgentArtifactService) Get(ctx context.Context, userID, id int64) (*WebAgentArtifact, error) {
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	return s.repo.GetArtifact(ctx, userID, id)
}
func (s *WebAgentArtifactService) List(ctx context.Context, userID, sessionID, before int64) ([]WebAgentArtifact, error) {
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	if sessionID < 0 || before < 0 {
		return nil, ErrWebAgentInvalid
	}
	return s.repo.ListArtifacts(ctx, userID, sessionID, before)
}
func (s *WebAgentArtifactService) Versions(ctx context.Context, userID, id, before int64) ([]WebAgentArtifact, error) {
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	if before < 0 {
		return nil, ErrWebAgentInvalid
	}
	return s.repo.ArtifactVersions(ctx, userID, id, before)
}
func (s *WebAgentArtifactService) Delete(ctx context.Context, userID, id int64) error {
	if err := s.readable(ctx); err != nil {
		return err
	}
	if userID <= 0 || id <= 0 {
		return ErrWebAgentInvalid
	}
	repo, ok := s.repo.(WebAgentStorageRepository)
	if !ok {
		return ErrWebAgentUnavailable
	}
	return repo.DeleteArtifact(ctx, userID, id)
}
func (s *WebAgentArtifactService) StorageUsage(ctx context.Context, userID int64) (*WebAgentStorageUsage, error) {
	if err := s.readable(ctx); err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrWebAgentInvalid
	}
	repo, ok := s.repo.(WebAgentStorageRepository)
	if !ok {
		return nil, ErrWebAgentUnavailable
	}
	used, err := repo.ArtifactStorageUsage(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &WebAgentStorageUsage{UsedBytes: used, LimitBytes: WebAgentUserStorageBytes, TaskReservationBytes: WebAgentStorageReservationBytes}, nil
}
func (s *WebAgentArtifactService) Download(ctx context.Context, userID, id int64, preview bool) (*WebAgentArtifact, io.ReadCloser, int64, error) {
	artifact, err := s.Get(ctx, userID, id)
	if err != nil {
		return nil, nil, 0, err
	}
	if s.store == nil {
		return nil, nil, 0, ErrWebAgentUnavailable
	}
	if storage, ok := s.repo.(WebAgentStorageRepository); ok {
		store, ok := s.store.(WebAgentStagedBlobStore)
		if !ok {
			return nil, nil, 0, ErrWebAgentUnavailable
		}
		if err = storage.RegisterArtifactStore(ctx, store.StorageID()); err != nil {
			return nil, nil, 0, err
		}
	}
	key, expected := artifact.BlobKey, artifact.SizeBytes
	if preview {
		key, expected = artifact.PreviewKey, artifact.PreviewBytes
	}
	reader, size, err := s.store.Open(ctx, key)
	if err != nil {
		return nil, nil, 0, ErrWebAgentArtifactNotFound
	}
	if size != expected {
		reader.Close()
		return nil, nil, 0, errors.New("artifact integrity mismatch")
	}
	return artifact, reader, size, nil
}
func webAgentArtifactFilename(title, ext string) string {
	runes := []rune(strings.TrimSpace(title))
	if len(runes) > 80 {
		runes = runes[:80]
	}
	var out strings.Builder
	for _, r := range runes {
		if unicode.IsControl(r) || strings.ContainsRune("/\\:*?\"<>|", r) {
			out.WriteRune('_')
		} else {
			out.WriteRune(r)
		}
	}
	name := strings.Trim(out.String(), ". ")
	if name == "" {
		name = "artifact"
	}
	return name + "." + ext
}
