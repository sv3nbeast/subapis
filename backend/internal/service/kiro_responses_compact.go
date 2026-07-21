package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const kiroCompactSummaryInstruction = `Create a compact, lossless continuation summary of the conversation above for another model instance. Preserve the user's intent, decisions, constraints, exact technical identifiers, code changes, tool results, pending work, and unresolved errors. Do not continue the task. Return only the continuation summary.`

var (
	errKiroCompactTokenNotFound = errors.New("kiro compact token not found")
	errKiroCompactEmptySummary  = errors.New("kiro compact upstream returned an empty summary")
)

type kiroResponsesScope struct {
	APIKeyID int64
	GroupID  int64
}

type kiroCompactResponseOptions struct {
	Scope kiroResponsesScope
}

func kiroResponsesScopeForRequest(c *gin.Context, parsed *ParsedRequest) kiroResponsesScope {
	scope := kiroResponsesScope{}
	if parsed != nil {
		if parsed.SessionContext != nil {
			scope.APIKeyID = parsed.SessionContext.APIKeyID
		}
		if parsed.GroupID != nil {
			scope.GroupID = *parsed.GroupID
		}
	}
	if scope.APIKeyID == 0 {
		scope.APIKeyID = getAPIKeyIDFromContext(c)
	}
	if scope.GroupID == 0 {
		scope.GroupID = getOpenAIGroupIDFromContext(c)
	}
	return scope
}

func (s *kiroResponsesHistoryStore) saveCompact(scope kiroResponsesScope, summary string) (string, error) {
	if s == nil {
		return "", errors.New("kiro responses history store is unavailable")
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "", errKiroCompactEmptySummary
	}

	token := "kiro_cmp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	entry := kiroResponsesHistoryEntry{
		ID:             token,
		APIKeyID:       scope.APIKeyID,
		GroupID:        scope.GroupID,
		CompactSummary: summary,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	entry.StoredAt = s.currentTime()
	s.items[token] = entry
	if err := s.saveToDiskLocked(entry); err != nil {
		delete(s.items, token)
		return "", fmt.Errorf("persist kiro compact token: %w", err)
	}
	s.purgeExpiredDiskEntriesLocked()
	return token, nil
}

func (s *kiroResponsesHistoryStore) loadCompact(token string, scope kiroResponsesScope) (string, bool) {
	entry, ok := s.load(token)
	if !ok || strings.TrimSpace(entry.CompactSummary) == "" {
		return "", false
	}
	if entry.APIKeyID != scope.APIKeyID || entry.GroupID != scope.GroupID {
		return "", false
	}
	return strings.TrimSpace(entry.CompactSummary), true
}

func expandKiroCompactionInput(input json.RawMessage, scope kiroResponsesScope) (json.RawMessage, error) {
	if !hasKiroCompactionInput(input) {
		return input, nil
	}
	return transformKiroCompactionInput(input, scope, false)
}

func hasKiroCompactionInput(input json.RawMessage) bool {
	if !bytes.Contains(input, []byte("compaction")) {
		return false
	}
	root := gjson.ParseBytes(input)
	if root.IsArray() {
		found := false
		root.ForEach(func(_, item gjson.Result) bool {
			if strings.TrimSpace(item.Get("type").String()) == "compaction" {
				found = true
				return false
			}
			return true
		})
		return found
	}
	return root.IsObject() && strings.TrimSpace(root.Get("type").String()) == "compaction"
}

func prepareKiroCompactInput(input json.RawMessage, scope kiroResponsesScope) (json.RawMessage, error) {
	return transformKiroCompactionInput(input, scope, true)
}

func transformKiroCompactionInput(input json.RawMessage, scope kiroResponsesScope, compactRequest bool) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(input)
	items := make([]json.RawMessage, 0, 8)
	switch {
	case len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")):
	case trimmed[0] == '[':
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("parse kiro compact input: %w", err)
		}
	case trimmed[0] == '{':
		items = append(items, append(json.RawMessage(nil), trimmed...))
	case trimmed[0] == '"':
		if !compactRequest {
			return input, nil
		}
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, fmt.Errorf("parse kiro compact string input: %w", err)
		}
		message, _ := json.Marshal(map[string]any{"role": "user", "content": text})
		items = append(items, message)
	default:
		return nil, errors.New("unsupported kiro compact input shape")
	}

	transformed := make([]json.RawMessage, 0, len(items)+1)
	for _, item := range items {
		itemType := strings.TrimSpace(gjson.GetBytes(item, "type").String())
		switch itemType {
		case "compaction_trigger":
			if compactRequest {
				continue
			}
		case "compaction":
			token := strings.TrimSpace(gjson.GetBytes(item, "encrypted_content").String())
			if token == "" {
				return nil, errKiroCompactTokenNotFound
			}
			summary, ok := globalKiroResponsesHistoryStore.loadCompact(token, scope)
			if !ok {
				return nil, errKiroCompactTokenNotFound
			}
			expanded, _ := json.Marshal(map[string]any{
				"role": "developer",
				"content": []map[string]string{{
					"type": "input_text",
					"text": "Previous conversation continuation summary:\n" + summary,
				}},
			})
			transformed = append(transformed, expanded)
			continue
		}
		transformed = append(transformed, append(json.RawMessage(nil), item...))
	}

	if compactRequest {
		instruction, _ := json.Marshal(map[string]any{
			"role": "user",
			"content": []map[string]string{{
				"type": "input_text",
				"text": kiroCompactSummaryInstruction,
			}},
		})
		transformed = append(transformed, instruction)
	}
	return json.Marshal(transformed)
}

func buildKiroCompactOutput(output []apicompat.ResponsesOutput, scope kiroResponsesScope) ([]apicompat.ResponsesOutput, error) {
	summary := extractKiroCompactSummary(output)
	if summary == "" {
		return nil, errKiroCompactEmptySummary
	}
	token, err := globalKiroResponsesHistoryStore.saveCompact(scope, summary)
	if err != nil {
		return nil, err
	}
	return []apicompat.ResponsesOutput{{
		Type:             "compaction",
		ID:               "cmp_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Status:           "completed",
		EncryptedContent: token,
	}}, nil
}

func extractKiroCompactSummary(output []apicompat.ResponsesOutput) string {
	parts := make([]string, 0, len(output))
	for _, item := range output {
		switch item.Type {
		case "message":
			if text := strings.TrimSpace(joinKiroResponsesTextParts(item.Content)); text != "" {
				parts = append(parts, text)
			}
		case "reasoning":
			for _, summary := range item.Summary {
				if text := strings.TrimSpace(summary.Text); text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}
