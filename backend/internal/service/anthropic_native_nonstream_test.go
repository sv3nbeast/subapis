package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/sjson"
)

func claudeCodeAgentClassifierBodyForTest() []byte {
	return []byte(`{"model":"claude-opus-5","max_tokens":1024,"stream":false,"metadata":{"user_id":"user_test_account__session_test"},"thinking":{"type":"disabled"},"system":[{"type":"text","text":"` + claudeCodeAgentClassifierSystemPrefix + ` Classify the state.","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":"Current state: working"}]}`)
}

func TestIsClaudeCodeAgentClassifierRequest(t *testing.T) {
	body := claudeCodeAgentClassifierBodyForTest()
	require.True(t, IsClaudeCodeAgentClassifierRequest(body))

	variantPrompt := `Classify the background agent.

THE FOUR STATES
- WORKING
- BLOCKED
- DONE
- FAILED

OUTPUT - respond with ONLY this JSON, no code fences:
{
  "state": "<working|blocked|done|failed>",
  "detail": "<short status>"
}`
	variant, err := sjson.SetBytes(body, "system.0.text", variantPrompt)
	require.NoError(t, err)
	variant, err = sjson.SetBytes(variant, "max_tokens", 32768)
	require.NoError(t, err)
	variant, err = sjson.SetBytes(variant, "thinking.type", "enabled")
	require.NoError(t, err)
	require.True(t, IsClaudeCodeAgentClassifierRequest(variant), "classifier detection must not depend on one CLI version, thinking budget, or exact preamble")

	tests := map[string]struct {
		path  string
		value any
	}{
		"streaming":         {path: "stream", value: true},
		"token cap too low": {path: "max_tokens", value: 1023},
		"tools present":     {path: "tools.0.name", value: "Bash"},
		"wrong system":      {path: "system.0.text", value: "ordinary system prompt"},
		"two messages":      {path: "messages.1.role", value: "assistant"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			changed, err := sjson.SetBytes(body, tt.path, tt.value)
			require.NoError(t, err)
			require.False(t, IsClaudeCodeAgentClassifierRequest(changed))
		})
	}

	for name, prompt := range map[string]string{
		"states marker only": `THE FOUR STATES`,
		"output marker only": `OUTPUT - respond with ONLY this JSON, no code fences:`,
		"schema marker only": `{"state":"<working|blocked|done|failed>"}`,
		"missing schema":     `THE FOUR STATES\nOUTPUT - respond with ONLY this JSON, no code fences:`,
	} {
		t.Run(name, func(t *testing.T) {
			changed, err := sjson.SetBytes(body, "system.0.text", prompt)
			require.NoError(t, err)
			require.False(t, IsClaudeCodeAgentClassifierRequest(changed))
		})
	}

	for _, path := range []string{"metadata", "system", "messages"} {
		t.Run("missing "+path, func(t *testing.T) {
			changed, err := sjson.DeleteBytes(body, path)
			require.NoError(t, err)
			require.False(t, IsClaudeCodeAgentClassifierRequest(changed))
		})
	}
}
