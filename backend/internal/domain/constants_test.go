package domain

import "testing"

func TestDefaultAntigravityModelMapping_ImageCompatibilityAliases(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-opus-4-7":                "claude-opus-4-6-thinking",
		"claude-opus-4-7-thinking":       "claude-opus-4-6-thinking",
		"claude-haiku-4-6":               "claude-sonnet-4-6",
		"gemini-2.5-flash-image":         "gemini-2.5-flash-image",
		"gemini-2.5-flash-image-preview": "gemini-2.5-flash-image",
		"gemini-3.1-flash-image":         "gemini-3.1-flash-image",
		"gemini-3.1-flash-image-preview": "gemini-3.1-flash-image",
		"gemini-3-pro-image":             "gemini-3.1-flash-image",
		"gemini-3-pro-image-preview":     "gemini-3.1-flash-image",
	}

	for from, want := range cases {
		got, ok := DefaultAntigravityModelMapping[from]
		if !ok {
			t.Fatalf("expected mapping for %q to exist", from)
		}
		if got != want {
			t.Fatalf("unexpected mapping for %q: got %q want %q", from, got, want)
		}
	}
}

func TestDefaultAntigravityModelMapping_DoesNotDefaultOpus48(t *testing.T) {
	t.Parallel()

	if got, ok := DefaultAntigravityModelMapping["claude-opus-4-8"]; ok {
		t.Fatalf("unexpected default Antigravity claude-opus-4-8 mapping: got %q", got)
	}
}

func TestDefaultBedrockModelMapping_DefaultsOpus48(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-opus-4-8":          "us.anthropic.claude-opus-4-8-v1",
		"claude-opus-4-8-thinking": "us.anthropic.claude-opus-4-8-v1",
		"claude-sonnet-5":          "anthropic.claude-sonnet-5",
	}
	for from, want := range cases {
		if got, ok := DefaultBedrockModelMapping[from]; !ok || got != want {
			t.Fatalf("unexpected default Bedrock %q mapping: got %q exists=%v want %q", from, got, ok, want)
		}
	}
}

func TestDefaultKiroModelMapping_DefaultsSonnet5(t *testing.T) {
	t.Parallel()

	if got, ok := DefaultKiroModelMapping["claude-sonnet-5"]; !ok || got != "claude-sonnet-5" {
		t.Fatalf("unexpected default Kiro claude-sonnet-5 mapping: got %q exists=%v", got, ok)
	}
}

func TestDefaultKiroModelMapping_ContainsClaude45ShortAliases(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"claude-opus-4-5":                     "claude-opus-4.5",
		"claude-opus-4-5-thinking":            "claude-opus-4.5",
		"claude-opus-4-5-20251101":            "claude-opus-4.5",
		"claude-opus-4-5-20251101-thinking":   "claude-opus-4.5",
		"claude-sonnet-4-5":                   "claude-sonnet-4.5",
		"claude-sonnet-4-5-thinking":          "claude-sonnet-4.5",
		"claude-sonnet-4-5-20250929":          "claude-sonnet-4.5",
		"claude-sonnet-4-5-20250929-thinking": "claude-sonnet-4.5",
		"claude-haiku-4-5":                    "claude-haiku-4.5",
		"claude-haiku-4-5-thinking":           "claude-haiku-4.5",
		"claude-haiku-4-5-20251001":           "claude-haiku-4.5",
		"claude-haiku-4-5-20251001-thinking":  "claude-haiku-4.5",
	}
	for from, want := range cases {
		if got, ok := DefaultKiroModelMapping[from]; !ok || got != want {
			t.Fatalf("unexpected default Kiro %q mapping: got %q exists=%v want %q", from, got, ok, want)
		}
	}
}
