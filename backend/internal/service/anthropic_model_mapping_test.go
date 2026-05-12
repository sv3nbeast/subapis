package service

import "testing"

func TestResolveAnthropicUpstreamModel(t *testing.T) {
	tests := []struct {
		name           string
		account        *Account
		requestedModel string
		wantModel      string
		wantSource     string
	}{
		{
			name: "oauth account mapping is honored before builtin normalization",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeOAuth,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-opus-4-7-thinking": "claude-opus-4-7",
					},
				},
			},
			requestedModel: "claude-opus-4-7-thinking",
			wantModel:      "claude-opus-4-7",
			wantSource:     "account",
		},
		{
			name: "oauth falls back to claude builtin normalization",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeOAuth,
			},
			requestedModel: "claude-sonnet-4-5",
			wantModel:      "claude-sonnet-4-5-20250929",
			wantSource:     "prefix",
		},
		{
			name: "service account mapping wins over vertex normalization",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeServiceAccount,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-opus-4-7-thinking": "claude-opus-4-7",
					},
				},
			},
			requestedModel: "claude-opus-4-7-thinking",
			wantModel:      "claude-opus-4-7",
			wantSource:     "account",
		},
		{
			name: "api key mapping is still honored",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeAPIKey,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-opus-4-7-thinking": "claude-opus-4-7",
					},
				},
			},
			requestedModel: "claude-opus-4-7-thinking",
			wantModel:      "claude-opus-4-7",
			wantSource:     "account",
		},
		{
			name: "unmapped oauth model is passed through",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeOAuth,
			},
			requestedModel: "claude-opus-4-7-thinking",
			wantModel:      "claude-opus-4-7-thinking",
			wantSource:     "",
		},
		{
			name:           "nil account returns original",
			account:        nil,
			requestedModel: "claude-opus-4-7-thinking",
			wantModel:      "claude-opus-4-7-thinking",
			wantSource:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAnthropicUpstreamModel(tt.account, tt.requestedModel)
			if got.Model != tt.wantModel || got.Source != tt.wantSource {
				t.Fatalf("resolveAnthropicUpstreamModel() = (%q, %q), want (%q, %q)", got.Model, got.Source, tt.wantModel, tt.wantSource)
			}
		})
	}
}
