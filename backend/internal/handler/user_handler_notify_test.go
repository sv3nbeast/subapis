package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userNotifyRepoStub struct {
	user *service.User
}

func (r *userNotifyRepoStub) Create(context.Context, *service.User) error { return nil }
func (r *userNotifyRepoStub) GetByID(_ context.Context, id int64) (*service.User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, service.ErrUserNotFound
	}
	cloned := *r.user
	cloned.BalanceNotifyExtraEmails = append([]service.NotifyEmailEntry(nil), r.user.BalanceNotifyExtraEmails...)
	return &cloned, nil
}
func (r *userNotifyRepoStub) GetByEmail(context.Context, string) (*service.User, error) {
	return nil, service.ErrUserNotFound
}
func (r *userNotifyRepoStub) GetFirstAdmin(context.Context) (*service.User, error) {
	return nil, service.ErrUserNotFound
}
func (r *userNotifyRepoStub) Update(_ context.Context, user *service.User) error {
	cloned := *user
	cloned.BalanceNotifyExtraEmails = append([]service.NotifyEmailEntry(nil), user.BalanceNotifyExtraEmails...)
	r.user = &cloned
	return nil
}
func (r *userNotifyRepoStub) Delete(context.Context, int64) error { return nil }
func (r *userNotifyRepoStub) List(context.Context, pagination.PaginationParams) ([]service.User, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *userNotifyRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, service.UserListFilters) ([]service.User, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (r *userNotifyRepoStub) UpdateBalance(context.Context, int64, float64) error { return nil }
func (r *userNotifyRepoStub) DeductBalance(context.Context, int64, float64) error { return nil }
func (r *userNotifyRepoStub) UpdateConcurrency(context.Context, int64, int) error { return nil }
func (r *userNotifyRepoStub) ExistsByEmail(context.Context, string) (bool, error) { return false, nil }
func (r *userNotifyRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	return 0, nil
}
func (r *userNotifyRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error { return nil }
func (r *userNotifyRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	return nil
}
func (r *userNotifyRepoStub) UpdateTotpSecret(context.Context, int64, *string) error { return nil }
func (r *userNotifyRepoStub) EnableTotp(context.Context, int64) error                { return nil }
func (r *userNotifyRepoStub) DisableTotp(context.Context, int64) error               { return nil }

type userNotifyResponse struct {
	Code int `json:"code"`
	Data struct {
		ID                       int64 `json:"id"`
		BalanceNotifyExtraEmails []struct {
			Email string `json:"email"`
		} `json:"balance_notify_extra_emails"`
	} `json:"data"`
}

func TestRemoveNotifyEmailReturnsUpdatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	repo := &userNotifyRepoStub{
		user: &service.User{
			ID:    42,
			Email: "owner@example.com",
			BalanceNotifyExtraEmails: []service.NotifyEmailEntry{
				{Email: "remove@example.com", Disabled: false, Verified: true},
				{Email: "keep@example.com", Disabled: false, Verified: true},
			},
		},
	}
	userSvc := service.NewUserService(repo, nil, nil, nil)
	handler := NewUserHandler(userSvc, nil, nil)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
		c.Next()
	})
	router.DELETE("/user/notify-email", handler.RemoveNotifyEmail)

	req := httptest.NewRequest(http.MethodDelete, "/user/notify-email", bytes.NewBufferString(`{"email":"remove@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp userNotifyResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, int64(42), resp.Data.ID)
	require.Len(t, resp.Data.BalanceNotifyExtraEmails, 1)
	require.Equal(t, "keep@example.com", resp.Data.BalanceNotifyExtraEmails[0].Email)

	require.NotNil(t, repo.user)
	require.Len(t, repo.user.BalanceNotifyExtraEmails, 1)
	require.Equal(t, "keep@example.com", repo.user.BalanceNotifyExtraEmails[0].Email)
}
