package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGatewayServiceGetAccessTokenReadsKiroAPIKeyAliases(t *testing.T) {
	svc := &GatewayService{}

	tests := []struct {
		name        string
		credentials map[string]any
		wantToken   string
	}{
		{
			name:        "snake case",
			credentials: map[string]any{"kiro_api_key": "ksk_snake"},
			wantToken:   "ksk_snake",
		},
		{
			name:        "camel case",
			credentials: map[string]any{"kiroApiKey": "ksk_camel"},
			wantToken:   "ksk_camel",
		},
		{
			name:        "legacy api_key",
			credentials: map[string]any{"api_key": "ksk_legacy"},
			wantToken:   "ksk_legacy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{
				Platform:    PlatformKiro,
				Type:        AccountTypeAPIKey,
				Credentials: tt.credentials,
			}

			token, tokenType, err := svc.GetAccessToken(context.Background(), account)

			require.NoError(t, err)
			require.Equal(t, tt.wantToken, token)
			require.Equal(t, "apikey", tokenType)
		})
	}
}
