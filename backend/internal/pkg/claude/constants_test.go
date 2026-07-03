package claude

import (
	"strings"
	"testing"
)

func TestClaudeCodeFingerprintConstantsStayInSync(t *testing.T) {
	ua := DefaultHeaders["User-Agent"]
	if !strings.Contains(ua, "claude-cli/"+CLICurrentVersion) {
		t.Fatalf("CLICurrentVersion %q must match DefaultHeaders User-Agent %q", CLICurrentVersion, ua)
	}
	if !strings.Contains(ua, "(external, cli)") {
		t.Fatalf("User-Agent must preserve configured cli entrypoint marker: %q", ua)
	}
	if DefaultHeaders["X-App"] != "cli" {
		t.Fatalf("X-App = %q, want cli", DefaultHeaders["X-App"])
	}
	if DefaultHeaders["Anthropic-Dangerous-Direct-Browser-Access"] != "true" {
		t.Fatalf("Anthropic-Dangerous-Direct-Browser-Access = %q, want true", DefaultHeaders["Anthropic-Dangerous-Direct-Browser-Access"])
	}

	required := []string{
		"X-Stainless-Lang",
		"X-Stainless-Package-Version",
		"X-Stainless-OS",
		"X-Stainless-Arch",
		"X-Stainless-Runtime",
		"X-Stainless-Runtime-Version",
		"X-Stainless-Retry-Count",
		"X-Stainless-Timeout",
	}
	for _, key := range required {
		if strings.TrimSpace(DefaultHeaders[key]) == "" {
			t.Fatalf("DefaultHeaders[%q] must not be empty", key)
		}
	}
}

func TestClaudeCodeMimicryBetaConstants(t *testing.T) {
	required := []string{
		BetaClaudeCode,
		BetaOAuth,
		BetaInterleavedThinking,
		BetaContextManagement,
		BetaPromptCachingScope,
		BetaMidConversationSystem,
		BetaAdvancedToolUse,
		BetaEffort,
		BetaExtendedCacheTTL,
	}
	for _, token := range required {
		if !strings.Contains(DefaultBetaHeader, token) {
			t.Fatalf("DefaultBetaHeader missing %q: %s", token, DefaultBetaHeader)
		}
	}
	for _, token := range []string{BetaMidConversationSystem, BetaEffort, BetaStructuredOutputs} {
		if !strings.Contains(HaikuBetaHeader, token) {
			t.Fatalf("HaikuBetaHeader missing %q: %s", token, HaikuBetaHeader)
		}
	}
	if strings.Contains(APIKeyBetaHeader, BetaOAuth) {
		t.Fatalf("APIKeyBetaHeader must not include OAuth-only beta %q", BetaOAuth)
	}
	if strings.Contains(HaikuBetaHeader, BetaClaudeCode) {
		t.Fatalf("HaikuBetaHeader must not include Claude Code beta: %s", HaikuBetaHeader)
	}
}

func TestDefaultModelsContainSonnet5WithoutDuplicates(t *testing.T) {
	seen := make(map[string]bool, len(DefaultModels))
	for _, model := range DefaultModels {
		if seen[model.ID] {
			t.Fatalf("DefaultModels contains duplicate model ID %q", model.ID)
		}
		seen[model.ID] = true
	}

	if !seen["claude-sonnet-5"] {
		t.Fatalf("DefaultModels must include claude-sonnet-5")
	}
}
