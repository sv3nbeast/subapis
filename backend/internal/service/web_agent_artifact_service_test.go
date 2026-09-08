package service

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type artifactServiceRepoStub struct {
	WebAgentArtifactRepository
	artifact *WebAgentArtifact
}

func (r artifactServiceRepoStub) GetArtifact(_ context.Context, userID, id int64) (*WebAgentArtifact, error) {
	if r.artifact == nil || r.artifact.UserID != userID || r.artifact.ID != id {
		return nil, ErrWebAgentArtifactNotFound
	}
	copy := *r.artifact
	return &copy, nil
}

func TestWebAgentArtifactDownloadChecksOwnerBeforeOpeningStorage(t *testing.T) {
	ctx := context.Background()
	store, err := NewWebAgentFileStore(filepath.Join(t.TempDir(), "private-artifacts"))
	require.NoError(t, err)
	key, err := store.Put(ctx, "docx", []byte("test file"))
	require.NoError(t, err)
	preview, err := store.Put(ctx, "pdf", []byte("%PDF-preview"))
	require.NoError(t, err)
	a := &WebAgentArtifact{ID: 3, UserID: 1, BlobKey: key, PreviewKey: preview, SizeBytes: 9, PreviewBytes: 12}
	svc := NewWebAgentArtifactService(artifactServiceRepoStub{artifact: a}, agentTestChat(), store)
	for _, isPreview := range []bool{false, true} {
		_, reader, _, err := svc.Download(ctx, 2, 3, isPreview)
		require.ErrorIs(t, err, ErrWebAgentArtifactNotFound)
		require.Nil(t, reader)
		_, reader, size, err := svc.Download(ctx, 1, 3, isPreview)
		require.NoError(t, err)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close())
		require.Equal(t, size, int64(len(data)))
		if isPreview {
			require.Equal(t, "%PDF-preview", string(data))
		} else {
			require.Equal(t, "test file", string(data))
		}
	}
	a.SizeBytes++
	_, reader, _, err := svc.Download(ctx, 1, 3, false)
	require.ErrorContains(t, err, "integrity mismatch")
	require.Nil(t, reader)
}

func TestWebAgentArtifactMetadataDoesNotExposeStorageOrSpecification(t *testing.T) {
	a := &WebAgentArtifact{ID: 3, UserID: 99, BlobKey: "secret-file-key", PreviewKey: "secret-preview-key", Spec: json.RawMessage(`{"private":"content"}`)}
	raw, err := json.Marshal(a)
	require.NoError(t, err)
	for _, secret := range []string{"secret-file-key", "secret-preview-key", "private", "user_id", "spec"} {
		require.NotContains(t, string(raw), secret)
	}
	require.Equal(t, "_hello__world.docx", webAgentArtifactFilename("\n/hello\r/world\n", "docx"))
	require.LessOrEqual(t, len([]rune(webAgentArtifactFilename(strings.Repeat("文", 100), "pptx"))), 85)
}
