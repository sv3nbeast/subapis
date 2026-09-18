package service

import (
	"net/http"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
)

const (
	anthropicNativeNonStreamCompaction      = "compaction"
	anthropicNativeNonStreamAgentClassifier = "agent_classifier"

	claudeCodeCompactionRequestHeader = "x-cc-compaction-request"
	claudeCodeContextCompactedHeader  = "x-cc-context-compacted"
	claudeCodeCompactionLegacy        = "legacy"
	claudeCodeCompactionManual        = "manual"
	claudeCodeCompactionAuto          = "auto"
	claudeCodeCompactionReactive      = "reactive"
	claudeCodeCompactionOther         = "other"

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

// IsClaudeCodeCompactionRequest preserves the legacy helper-only API.
func IsClaudeCodeCompactionRequest(helperHeader string) bool {
	return classifyAnthropicStainlessHelper(helperHeader) == anthropicStainlessHelperCompaction
}

// ClaudeCodeCompactionRequestKind returns a bounded classification for the
// official Claude Code compaction headers. Claude Code 2.1.260+ sends
// x-cc-compaction-request on both streaming and non-streaming summary calls;
// older releases used x-stainless-helper: compaction.
func ClaudeCodeCompactionRequestKind(headers http.Header) string {
	if headers == nil {
		return ""
	}
	newKind := classifyClaudeCodeCompactionRequest(headers.Get(claudeCodeCompactionRequestHeader))
	if isRecognizedClaudeCodeCompactionKind(newKind) {
		return newKind
	}
	if IsClaudeCodeCompactionRequest(headers.Get(anthropicStainlessHelperHeader)) {
		return claudeCodeCompactionLegacy
	}
	return newKind
}

// IsClaudeCodeCompactionHeaders accepts only known official wire values.
// Arbitrary non-empty values remain observable as "other" but cannot bypass
// the native non-stream guard.
func IsClaudeCodeCompactionHeaders(headers http.Header) bool {
	kind := ClaudeCodeCompactionRequestKind(headers)
	return kind == claudeCodeCompactionLegacy || isRecognizedClaudeCodeCompactionKind(kind)
}

func classifyClaudeCodeCompactionRequest(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return ""
	case claudeCodeCompactionManual:
		return claudeCodeCompactionManual
	case claudeCodeCompactionAuto:
		return claudeCodeCompactionAuto
	case claudeCodeCompactionReactive:
		return claudeCodeCompactionReactive
	default:
		return claudeCodeCompactionOther
	}
}

func isRecognizedClaudeCodeCompactionKind(kind string) bool {
	return kind == claudeCodeCompactionManual ||
		kind == claudeCodeCompactionAuto ||
		kind == claudeCodeCompactionReactive
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
