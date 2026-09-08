package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type agentHandlerRepo struct {
	service.WebChatRepository
	service.WebAgentRepository
	userID int64
}

func (r *agentHandlerRepo) GetTask(_ context.Context, userID, id int64) (*service.WebAgentTask, error) {
	r.userID = userID
	return &service.WebAgentTask{ID: id, UserID: userID, Status: service.WebAgentQueued, LeaseToken: "private-lease", SessionSnapshot: []byte(`{"private":"snapshot"}`)}, nil
}

type agentHandlerRuntime struct{}

func (agentHandlerRuntime) GetWebChatRuntime(context.Context) service.WebChatRuntime {
	return service.WebChatRuntime{Enabled: true}
}
func TestWebAgentHandlerUsesAuthenticatedOwner(t *testing.T) {
	repo := &agentHandlerRepo{}
	chat := service.NewWebChatService(repo, nil, nil, nil, agentHandlerRuntime{})
	h := NewWebChatHandler(chat, nil, nil)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
	r.GET("/tasks/:task_id", h.GetTask)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/tasks/11?user_id=999", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(7), repo.userID)
	require.NotContains(t, w.Body.String(), "private-lease")
	require.NotContains(t, w.Body.String(), "snapshot")
}
func TestWebAgentHandlerGatesAndBounds(t *testing.T) {
	h := NewWebChatHandler(nil, nil, nil)
	for _, tc := range []struct {
		path, body string
		auth       bool
		want       int
	}{
		{"/sessions/2/tasks", `{"kind":"document","prompt":"hi","idempotency_key":"request-one"}`, false, 401},
		{"/sessions/-1/tasks", `{}`, true, 400},
		{"/sessions/2/tasks", `{"kind":"document","prompt":"hi","idempotency_key":"request-one"}`, true, 503},
		{"/sessions/2/tasks", `{"prompt":"` + strings.Repeat("x", 140000) + `"}`, true, 400},
	} {
		r := gin.New()
		if tc.auth {
			r.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7}) })
		}
		r.POST("/sessions/:id/tasks", h.CreateTask)
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Equal(t, tc.want, w.Code)
	}
}
