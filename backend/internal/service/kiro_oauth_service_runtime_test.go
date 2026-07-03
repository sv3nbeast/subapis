package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKiroOAuthServiceRefreshTokenRejectsIDCMissingClientCredentialsRuntime(t *testing.T) {
	svc := NewKiroOAuthService(nil)

	_, err := svc.RefreshToken(context.Background(), &KiroRefreshTokenInput{
		AuthMethod:   "idc",
		RefreshToken: "refresh-token",
		ClientID:     "client-id",
	})

	require.EqualError(t, err, "kiro idc refresh requires client_id and client_secret")
}

func TestResolveKiroRefreshAuthMethodInfersIDCFromClientCredentialsRuntime(t *testing.T) {
	require.Equal(t, "idc", resolveKiroRefreshAuthMethod("", "client-id", "client-secret"))
	require.Equal(t, "social", resolveKiroRefreshAuthMethod("", "client-id", ""))
	require.Equal(t, "social", resolveKiroRefreshAuthMethod("", "", "client-secret"))
	require.Equal(t, "social", resolveKiroRefreshAuthMethod("", "", ""))
	require.Equal(t, "idc", resolveKiroRefreshAuthMethod("IDC", "", ""))
}
