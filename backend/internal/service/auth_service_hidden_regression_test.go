package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type authRegressionUserRepo struct {
	user *User
}

func (r *authRegressionUserRepo) Create(context.Context, *User) error { panic("unexpected Create call") }
func (r *authRegressionUserRepo) GetByID(_ context.Context, id int64) (*User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, ErrUserNotFound
	}
	return r.user, nil
}
func (r *authRegressionUserRepo) GetByEmail(_ context.Context, email string) (*User, error) {
	if r.user == nil || r.user.Email != email {
		return nil, ErrUserNotFound
	}
	return r.user, nil
}
func (r *authRegressionUserRepo) GetFirstAdmin(context.Context) (*User, error) {
	panic("unexpected GetFirstAdmin call")
}
func (r *authRegressionUserRepo) Update(context.Context, *User) error { panic("unexpected Update call") }
func (r *authRegressionUserRepo) Delete(context.Context, int64) error { panic("unexpected Delete call") }
func (r *authRegressionUserRepo) GetUserAvatar(context.Context, int64) (*UserAvatar, error) {
	panic("unexpected GetUserAvatar call")
}
func (r *authRegressionUserRepo) UpsertUserAvatar(context.Context, int64, UpsertUserAvatarInput) (*UserAvatar, error) {
	panic("unexpected UpsertUserAvatar call")
}
func (r *authRegressionUserRepo) DeleteUserAvatar(context.Context, int64) error {
	panic("unexpected DeleteUserAvatar call")
}
func (r *authRegressionUserRepo) List(context.Context, pagination.PaginationParams) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}
func (r *authRegressionUserRepo) ListWithFilters(context.Context, pagination.PaginationParams, UserListFilters) ([]User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}
func (r *authRegressionUserRepo) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserIDs call")
}
func (r *authRegressionUserRepo) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	panic("unexpected GetLatestUsedAtByUserID call")
}
func (r *authRegressionUserRepo) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	panic("unexpected UpdateUserLastActiveAt call")
}
func (r *authRegressionUserRepo) UpdateBalance(context.Context, int64, float64) error {
	panic("unexpected UpdateBalance call")
}
func (r *authRegressionUserRepo) DeductBalance(context.Context, int64, float64) error {
	panic("unexpected DeductBalance call")
}
func (r *authRegressionUserRepo) UpdateConcurrency(context.Context, int64, int) error {
	panic("unexpected UpdateConcurrency call")
}
func (r *authRegressionUserRepo) ExistsByEmail(context.Context, string) (bool, error) {
	panic("unexpected ExistsByEmail call")
}
func (r *authRegressionUserRepo) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	panic("unexpected RemoveGroupFromAllowedGroups call")
}
func (r *authRegressionUserRepo) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected AddGroupToAllowedGroups call")
}
func (r *authRegressionUserRepo) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	panic("unexpected RemoveGroupFromUserAllowedGroups call")
}
func (r *authRegressionUserRepo) ListUserAuthIdentities(context.Context, int64) ([]UserAuthIdentityRecord, error) {
	panic("unexpected ListUserAuthIdentities call")
}
func (r *authRegressionUserRepo) UnbindUserAuthProvider(context.Context, int64, string) error {
	panic("unexpected UnbindUserAuthProvider call")
}
func (r *authRegressionUserRepo) UpdateTotpSecret(context.Context, int64, *string) error {
	panic("unexpected UpdateTotpSecret call")
}
func (r *authRegressionUserRepo) EnableTotp(context.Context, int64) error {
	panic("unexpected EnableTotp call")
}
func (r *authRegressionUserRepo) DisableTotp(context.Context, int64) error {
	panic("unexpected DisableTotp call")
}

func newAuthServiceForRegressionTests(repo UserRepository) *AuthService {
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret",
			ExpireHour: 1,
		},
	}
	return NewAuthService(nil, repo, nil, nil, cfg, nil, nil, nil, nil, nil, nil)
}

func TestGenerateTokenUsesResolvedTokenVersion(t *testing.T) {
	user := &User{
		ID:           1,
		Email:        "resolved@test.com",
		PasswordHash: "hashed-password",
		Role:         RoleUser,
		Status:       StatusActive,
		TokenVersion: 1,
	}

	svc := newAuthServiceForRegressionTests(&authRegressionUserRepo{user: user})
	token, err := svc.GenerateToken(user)
	require.NoError(t, err)

	claims, err := svc.ValidateToken(token)
	require.NoError(t, err)
	require.Equal(t, resolvedTokenVersion(user), claims.TokenVersion)
}

func TestRefreshTokenUsesResolvedTokenVersionComparison(t *testing.T) {
	user := &User{
		ID:           2,
		Email:        "refresh@test.com",
		PasswordHash: "hashed-password",
		Role:         RoleUser,
		Status:       StatusActive,
		TokenVersion: 1,
	}

	svc := newAuthServiceForRegressionTests(&authRegressionUserRepo{user: user})
	token, err := svc.GenerateToken(user)
	require.NoError(t, err)

	newToken, err := svc.RefreshToken(context.Background(), token)
	require.NoError(t, err)
	require.NotEmpty(t, newToken)
}

func TestIsReservedEmailIncludesWeChatSyntheticDomain(t *testing.T) {
	tests := []struct {
		email string
		want  bool
	}{
		{email: fmt.Sprintf("a%s", LinuxDoConnectSyntheticEmailDomain), want: true},
		{email: fmt.Sprintf("b%s", OIDCConnectSyntheticEmailDomain), want: true},
		{email: fmt.Sprintf("c%s", WeChatConnectSyntheticEmailDomain), want: true},
		{email: "user@example.com", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.email, func(t *testing.T) {
			require.Equal(t, tc.want, isReservedEmail(tc.email))
		})
	}
}
