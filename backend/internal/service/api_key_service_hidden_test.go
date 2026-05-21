//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIKeyService_Update_HiddenWebChatKeyRejected(t *testing.T) {
	repo := &apiKeyRepoStub{
		apiKey: &APIKey{
			ID:       88,
			UserID:   7,
			Key:      "sk-web-chat-hidden",
			Source:   APIKeySourceWebChat,
			IsHidden: true,
		},
	}
	svc := &APIKeyService{apiKeyRepo: repo}
	name := "should-not-update"

	got, err := svc.Update(context.Background(), 88, 7, UpdateAPIKeyRequest{Name: &name})
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
}
