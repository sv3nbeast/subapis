package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type artifactHandlerRepo struct {
	service.WebChatRepository
	service.WebAgentArtifactRepository
	userID int64
}

func (r *artifactHandlerRepo) GetArtifact(_ context.Context, userID, id int64) (*service.WebAgentArtifact, error) {
	r.userID = userID
	if id != 11 {
		return nil, service.ErrWebAgentArtifactNotFound
	}
	return &service.WebAgentArtifact{ID: id, UserID: userID, BlobKey: "private-file", PreviewKey: "private-preview", Spec: json.RawMessage(`{"private":"specification"}`)}, nil
}
func TestWebAgentArtifactHandlerOwnerAndBounds(t *testing.T) {
	repo := &artifactHandlerRepo{}
	h := NewWebChatHandler(service.NewWebChatService(repo, nil, nil, nil, agentHandlerRuntime{}), nil, nil)
	for _, tc := range []struct {
		path string
		auth bool
		want int
	}{
		{"/artifacts/11", false, 401}, {"/artifacts/-1", true, 400},
		{"/artifacts/11?user_id=999", true, 200}, {"/artifacts/12/download", true, 404},
		{"/artifacts/11/download", true, 503},
	} {
		r := gin.New()
		if tc.auth {
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
		}
		r.GET("/artifacts/:artifact_id", h.GetArtifact)
		r.GET("/artifacts/:artifact_id/download", h.DownloadArtifact)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equal(t, tc.want, w.Code, tc.path)
		require.NotContains(t, w.Body.String(), "private-")
		require.NotContains(t, w.Body.String(), "specification")
	}
	require.Equal(t, int64(7), repo.userID)
}
