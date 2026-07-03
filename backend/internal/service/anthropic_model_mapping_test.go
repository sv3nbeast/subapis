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
			name: "oauth falls back to default alias mapping",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeOAuth,
			},
			requestedModel: "claude-opus-4-7-thinking",
			wantModel:      "claude-opus-4-7",
			wantSource:     "alias",
		},
		{
			name: "oauth falls back to opus 4.6 thinking default alias mapping",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeOAuth,
			},
			requestedModel: "claude-opus-4-6-thinking",
			wantModel:      "claude-opus-4-6",
			wantSource:     "alias",
		},
		{
			name: "api key falls back to dotted opus 4.6 thinking default alias mapping",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeAPIKey,
			},
			requestedModel: "claude-opus-4.6-thinking",
			wantModel:      "claude-opus-4-6",
			wantSource:     "alias",
		},
		{
			name: "api key falls back to dotted default alias mapping",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeAPIKey,
			},
			requestedModel: "claude-opus-4.7",
			wantModel:      "claude-opus-4-7",
			wantSource:     "alias",
		},
		{
			name: "oauth falls back to opus 4.8 dotted default alias mapping",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeOAuth,
			},
			requestedModel: "claude-opus-4.8",
			wantModel:      "claude-opus-4-8",
			wantSource:     "alias",
		},
		{
			name: "oauth falls back to opus 4.8 thinking default alias mapping",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeOAuth,
			},
			requestedModel: "claude-opus-4-8-thinking",
			wantModel:      "claude-opus-4-8",
			wantSource:     "alias",
		},
		{
			name: "oauth normalizes sonnet 5 thinking suffix without enabling thinking alias",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeOAuth,
			},
			requestedModel: "claude-sonnet-5-thinking",
			wantModel:      "claude-sonnet-5",
			wantSource:     "alias",
		},
		{
			name: "api key falls back to dotted opus 4.8 thinking default alias mapping",
			account: &Account{
				Platform: PlatformAnthropic,
				Type:     AccountTypeAPIKey,
			},
			requestedModel: "claude-opus-4.8-thinking",
			wantModel:      "claude-opus-4-8",
			wantSource:     "alias",
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

func TestIsAnthropicThinkingModelAlias(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "claude-opus-4-6-thinking", want: true},
		{model: "claude-opus-4.6-thinking", want: true},
		{model: "claude-opus-4-7-thinking", want: true},
		{model: "claude-opus-4.7-thinking", want: true},
		{model: "claude-opus-4-8-thinking", want: true},
		{model: "claude-opus-4.8-thinking", want: true},
		{model: "claude-opus-4-6", want: false},
		{model: "claude-opus-4.6", want: false},
		{model: "claude-opus-4-7", want: false},
		{model: "claude-opus-4.7", want: false},
		{model: "claude-opus-4-8", want: false},
		{model: "claude-opus-4.8", want: false},
		{model: "claude-sonnet-4-5-thinking", want: false},
		{model: "claude-sonnet-5-thinking", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := isAnthropicThinkingModelAlias(tt.model); got != tt.want {
				t.Fatalf("isAnthropicThinkingModelAlias(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}
