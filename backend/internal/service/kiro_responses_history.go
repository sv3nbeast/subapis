package service

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

const (
	kiroResponsesHistoryTTL      = 30 * 24 * time.Hour
	kiroResponsesMaxHistoryDepth = 64
)

var globalKiroResponsesHistoryStore = &kiroResponsesHistoryStore{
	items: make(map[string]kiroResponsesHistoryEntry),
	now:   time.Now,
}

type kiroResponsesHistoryStore struct {
	mu    sync.Mutex
	items map[string]kiroResponsesHistoryEntry
	now   func() time.Time
}

type kiroResponsesHistoryEntry struct {
	ID                 string
	PreviousResponseID string
	Model              string
	Instructions       string
	Input              json.RawMessage
	Output             []apicompat.ResponsesOutput
	StoredAt           time.Time
}

func (s *kiroResponsesHistoryStore) load(id string) (kiroResponsesHistoryEntry, bool) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" || s == nil {
		return kiroResponsesHistoryEntry{}, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.items[trimmed]
	if !ok {
		return kiroResponsesHistoryEntry{}, false
	}
	if !entry.StoredAt.IsZero() && s.now().Sub(entry.StoredAt) > kiroResponsesHistoryTTL {
		delete(s.items, trimmed)
		return kiroResponsesHistoryEntry{}, false
	}
	return entry, true
}

func (s *kiroResponsesHistoryStore) save(entry kiroResponsesHistoryEntry) {
	if s == nil || strings.TrimSpace(entry.ID) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if entry.StoredAt.IsZero() {
		entry.StoredAt = s.now()
	}
	s.items[entry.ID] = entry
}

func (s *kiroResponsesHistoryStore) expand(prevID string) (json.RawMessage, []apicompat.AnthropicMessage) {
	prev, ok := s.load(prevID)
	if !ok {
		return nil, nil
	}

	chain := s.collectChain(prev)
	messages := make([]apicompat.AnthropicMessage, 0, len(chain)*2)
	var instructions []string
	for _, entry := range chain {
		if strings.TrimSpace(entry.Instructions) != "" {
			instructions = append(instructions, strings.TrimSpace(entry.Instructions))
		}
		if _, inputMessages, err := apicompat.ResponsesInputToAnthropicForKiro(entry.Input); err == nil {
			messages = append(messages, inputMessages...)
		}
		messages = append(messages, kiroResponsesOutputToAnthropicMessages(entry.Output)...)
	}
	var system json.RawMessage
	if len(instructions) > 0 {
		system, _ = json.Marshal(strings.Join(instructions, "\n\n"))
	}
	return system, apicompat.MergeAnthropicMessagesForKiro(messages)
}

func (s *kiroResponsesHistoryStore) collectChain(prev kiroResponsesHistoryEntry) []kiroResponsesHistoryEntry {
	stack := []kiroResponsesHistoryEntry{prev}
	visited := map[string]bool{prev.ID: true}
	cursor := prev

	for depth := 0; depth < kiroResponsesMaxHistoryDepth; depth++ {
		nextID := strings.TrimSpace(cursor.PreviousResponseID)
		if nextID == "" || visited[nextID] {
			break
		}
		ancestor, ok := s.load(nextID)
		if !ok {
			break
		}
		visited[ancestor.ID] = true
		stack = append(stack, ancestor)
		cursor = ancestor
	}

	for i, j := 0, len(stack)-1; i < j; i, j = i+1, j-1 {
		stack[i], stack[j] = stack[j], stack[i]
	}
	return stack
}

func kiroResponsesOutputToAnthropicMessages(items []apicompat.ResponsesOutput) []apicompat.AnthropicMessage {
	if len(items) == 0 {
		return nil
	}

	out := make([]apicompat.AnthropicMessage, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			text := strings.TrimSpace(joinKiroResponsesTextParts(item.Content))
			if text == "" {
				continue
			}
			content, _ := json.Marshal([]apicompat.AnthropicContentBlock{{Type: "text", Text: text}})
			role := item.Role
			if role == "" {
				role = "assistant"
			}
			if role != "assistant" && role != "user" {
				role = "assistant"
			}
			out = append(out, apicompat.AnthropicMessage{Role: role, Content: content})

		case "function_call":
			input := json.RawMessage("{}")
			if strings.TrimSpace(item.Arguments) != "" {
				input = json.RawMessage(item.Arguments)
			}
			id := strings.TrimSpace(item.CallID)
			if id == "" {
				id = strings.TrimSpace(item.ID)
			}
			content, _ := json.Marshal([]apicompat.AnthropicContentBlock{{
				Type:  "tool_use",
				ID:    id,
				Name:  item.Name,
				Input: input,
			}})
			out = append(out, apicompat.AnthropicMessage{Role: "assistant", Content: content})
		}
	}
	return out
}

func joinKiroResponsesTextParts(parts []apicompat.ResponsesContentPart) string {
	var b strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "output_text", "text", "input_text":
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func mergeKiroResponsesSystem(historySystem, currentSystem json.RawMessage) json.RawMessage {
	history := rawSystemText(historySystem)
	current := rawSystemText(currentSystem)
	switch {
	case history == "":
		return currentSystem
	case current == "":
		out, _ := json.Marshal(history)
		return out
	default:
		out, _ := json.Marshal(history + "\n\n" + current)
		return out
	}
}

func rawSystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var blocks []apicompat.AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		texts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				texts = append(texts, strings.TrimSpace(block.Text))
			}
		}
		return strings.Join(texts, "\n\n")
	}
	return ""
}
