package service

import (
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
)

const (
	anthropicNativeNonStreamCompaction      = "compaction"
	anthropicNativeNonStreamAgentClassifier = "agent_classifier"

	claudeCodeAgentClassifierSystemPrefix = "A user kicked off a Claude Code agent to do a coding task and walked away."

	claudeCodeAgentClassifierStatesMarker = "thefourstates"
	claudeCodeAgentClassifierOutputMarker = "respondwithonlythisjson"
	claudeCodeAgentClassifierSchemaMarker = `"state":"<working|blocked|done|failed>"`
)

// IsClaudeCodeAgentClassifierRequest recognizes Claude Code's native non-stream
// agent-classifier protocol. Structural checks keep ordinary sync requests out;
// semantic markers tolerate minor prompt wording and formatting changes.
func IsClaudeCodeAgentClassifierRequest(body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	if stream := gjson.GetBytes(body, "stream"); stream.Exists() && stream.Bool() {
		return false
	}
	maxTokens := gjson.GetBytes(body, "max_tokens").Int()
	if maxTokens < 1024 {
		return false
	}
	if strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()) == "" {
		return false
	}

	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() || len(messages.Array()) != 1 || messages.Get("0.role").String() != "user" {
		return false
	}
	if tools := gjson.GetBytes(body, "tools"); tools.Exists() && (!tools.IsArray() || len(tools.Array()) != 0) {
		return false
	}
	system := gjson.GetBytes(body, "system")
	if !system.IsArray() {
		return false
	}
	for _, block := range system.Array() {
		if block.Get("type").String() != "text" {
			continue
		}
		if isClaudeCodeAgentClassifierSystemText(block.Get("text").String()) {
			return true
		}
	}
	return false
}

// IsClaudeCodeCompactionRequest reports whether the official Stainless helper
// header identifies Claude Code's native non-streaming compaction request.
func IsClaudeCodeCompactionRequest(helperHeader string) bool {
	return classifyAnthropicStainlessHelper(helperHeader) == anthropicStainlessHelperCompaction
}

func isClaudeCodeAgentClassifierSystemText(text string) bool {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, claudeCodeAgentClassifierSystemPrefix) {
		return true
	}

	compact := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, text)
	return strings.Contains(compact, claudeCodeAgentClassifierStatesMarker) &&
		strings.Contains(compact, claudeCodeAgentClassifierOutputMarker) &&
		strings.Contains(compact, claudeCodeAgentClassifierSchemaMarker)
}
