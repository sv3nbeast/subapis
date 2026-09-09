package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type sessionLookupRepo struct {
	service.WebChatRepository
	owner int64
}

func (r *sessionLookupRepo) GetSession(_ context.Context, userID, id int64) (*service.WebChatSession, error) {
	r.owner = userID
	if userID != 7 || id != 11 {
		return nil, service.ErrWebChatSessionNotFound
	}
	return &service.WebChatSession{ID: 11, UserID: 7, Title: "owned conversation"}, nil
}
func TestWebChatSessionLookupUsesAuthenticatedOwner(t *testing.T) {
	repo := &sessionLookupRepo{}
	h := NewWebChatHandler(service.NewWebChatService(repo, nil, nil, nil, agentHandlerRuntime{}), nil, nil)
	for _, tc := range []struct {
		path string
		auth bool
		want int
	}{
		{"/sessions/11", false, 401}, {"/sessions/-1", true, 400}, {"/sessions/12", true, 404}, {"/sessions/11?user_id=99", true, 200},
	} {
		r := gin.New()
		if tc.auth {
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
		}
		r.GET("/sessions/:id", h.GetSession)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		require.Equal(t, tc.want, w.Code)
		if tc.want == 200 {
			require.Contains(t, w.Body.String(), "owned conversation")
			require.Equal(t, int64(7), repo.owner)
		}
	}
}
