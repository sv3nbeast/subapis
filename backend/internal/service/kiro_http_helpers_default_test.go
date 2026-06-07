package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewKiroJSONRequestSetsExplicitHostLikeKiroGo(t *testing.T) {
	account := &Account{
		ID:       901,
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
	}

	req, err := newKiroJSONRequest(
		context.Background(),
		"https://q.us-east-1.amazonaws.com/generateAssistantResponse",
		[]byte(`{"ok":true}`),
		"access-token",
		"account-key",
		buildKiroMachineID(account),
		"",
		account,
	)
	require.NoError(t, err)
	require.Equal(t, "q.us-east-1.amazonaws.com", req.URL.Host)
	require.Equal(t, req.URL.Host, req.Host)
}
