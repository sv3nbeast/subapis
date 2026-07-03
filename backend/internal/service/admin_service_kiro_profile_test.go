package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminServiceEnsureKiroProfileArnResolvesAndPersists(t *testing.T) {
	account := &Account{
		ID:          1201,
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{
		responses: []*http.Response{
			newKiroProfileJSONResponse(http.StatusOK, `{"profiles":[{"arn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/ADMIN"}]}`),
		},
	}
	svc := NewAdminService(nil, nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	impl, ok := svc.(*adminServiceImpl)
	require.True(t, ok)
	impl.SetKiroProfileResolverDeps(upstream, &TLSFingerprintProfileService{})

	got := svc.EnsureKiroProfileArn(context.Background(), account)

	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/ADMIN", got)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, got, repo.lastCredentials["profile_arn"])
	require.Equal(t, got, account.GetCredential("profile_arn"))
}

func TestAdminServiceEnsureKiroProfileArnSkipsExistingRealArn(t *testing.T) {
	account := &Account{
		ID:       1202,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-west-2:123456789012:profile/EXISTING",
		},
	}
	repo := &kiroProfileRepo{account: account}
	upstream := &kiroProfileHTTPUpstream{}
	svc := NewAdminService(nil, nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	impl, ok := svc.(*adminServiceImpl)
	require.True(t, ok)
	impl.SetKiroProfileResolverDeps(upstream, &TLSFingerprintProfileService{})

	got := svc.EnsureKiroProfileArn(context.Background(), account)

	require.Equal(t, "arn:aws:codewhisperer:us-west-2:123456789012:profile/EXISTING", got)
	require.Empty(t, upstream.requests)
	require.Zero(t, repo.updateCredentialsCalls)
}
