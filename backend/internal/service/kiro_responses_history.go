package service

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	dir:   defaultKiroResponsesHistoryDir(),
}

type kiroResponsesHistoryStore struct {
	mu           sync.Mutex
	items        map[string]kiroResponsesHistoryEntry
	now          func() time.Time
	dir          string
	lastPurgedAt time.Time
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

type kiroResponsesHistoryDiskEntry struct {
	ID                 string                      `json:"id"`
	PreviousResponseID string                      `json:"previous_response_id,omitempty"`
	Model              string                      `json:"model,omitempty"`
	Instructions       string                      `json:"instructions,omitempty"`
	Input              json.RawMessage             `json:"input,omitempty"`
	Output             []apicompat.ResponsesOutput `json:"output,omitempty"`
	StoredAt           int64                       `json:"stored_at"`
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
		diskEntry, diskOK := s.loadFromDiskLocked(trimmed)
		if !diskOK {
			return kiroResponsesHistoryEntry{}, false
		}
		entry = diskEntry
		s.items[trimmed] = diskEntry
	}
	if s.entryExpired(entry) {
		delete(s.items, trimmed)
		s.removeDiskEntryLocked(trimmed)
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
		entry.StoredAt = s.currentTime()
	}
	s.items[entry.ID] = entry
	_ = s.saveToDiskLocked(entry)
	s.purgeExpiredDiskEntriesLocked()
}

func (s *kiroResponsesHistoryStore) expand(prevID string) (json.RawMessage, []apicompat.AnthropicMessage) {
	prev, ok := s.load(prevID)
	if !ok {
		return nil, nil
	}
	return s.expandEntry(prev)
}

func (s *kiroResponsesHistoryStore) expandRequired(prevID string) (json.RawMessage, []apicompat.AnthropicMessage, bool) {
	prev, ok := s.load(prevID)
	if !ok {
		return nil, nil, false
	}
	system, messages := s.expandEntry(prev)
	return system, messages, true
}

func (s *kiroResponsesHistoryStore) expandEntry(prev kiroResponsesHistoryEntry) (json.RawMessage, []apicompat.AnthropicMessage) {
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

func (s *kiroResponsesHistoryStore) entryExpired(entry kiroResponsesHistoryEntry) bool {
	if entry.StoredAt.IsZero() {
		return false
	}
	return s.currentTime().Sub(entry.StoredAt) > kiroResponsesHistoryTTL
}

func (s *kiroResponsesHistoryStore) saveToDiskLocked(entry kiroResponsesHistoryEntry) error {
	dir := strings.TrimSpace(s.dir)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	doc := kiroResponsesHistoryDiskEntry{
		ID:                 entry.ID,
		PreviousResponseID: entry.PreviousResponseID,
		Model:              entry.Model,
		Instructions:       entry.Instructions,
		Input:              append(json.RawMessage(nil), entry.Input...),
		Output:             entry.Output,
		StoredAt:           entry.StoredAt.Unix(),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	path := s.diskPath(entry.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *kiroResponsesHistoryStore) loadFromDiskLocked(id string) (kiroResponsesHistoryEntry, bool) {
	dir := strings.TrimSpace(s.dir)
	if dir == "" {
		return kiroResponsesHistoryEntry{}, false
	}
	data, err := os.ReadFile(s.diskPath(id))
	if err != nil {
		return kiroResponsesHistoryEntry{}, false
	}
	var doc kiroResponsesHistoryDiskEntry
	if err := json.Unmarshal(data, &doc); err != nil {
		return kiroResponsesHistoryEntry{}, false
	}
	if strings.TrimSpace(doc.ID) == "" {
		doc.ID = id
	}
	entry := kiroResponsesHistoryEntry{
		ID:                 doc.ID,
		PreviousResponseID: doc.PreviousResponseID,
		Model:              doc.Model,
		Instructions:       doc.Instructions,
		Input:              append(json.RawMessage(nil), doc.Input...),
		Output:             doc.Output,
	}
	if doc.StoredAt > 0 {
		entry.StoredAt = time.Unix(doc.StoredAt, 0)
	}
	if s.entryExpired(entry) {
		s.removeDiskEntryLocked(id)
		return kiroResponsesHistoryEntry{}, false
	}
	return entry, true
}

func (s *kiroResponsesHistoryStore) removeDiskEntryLocked(id string) {
	dir := strings.TrimSpace(s.dir)
	if dir == "" {
		return
	}
	_ = os.Remove(s.diskPath(id))
}

func (s *kiroResponsesHistoryStore) purgeExpiredDiskEntriesLocked() {
	dir := strings.TrimSpace(s.dir)
	if dir == "" {
		return
	}
	now := s.currentTime()
	if !s.lastPurgedAt.IsZero() && now.Sub(s.lastPurgedAt) < time.Hour {
		return
	}
	s.lastPurgedAt = now

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-kiroResponsesHistoryTTL)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		full := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(full)
		}
	}
}

func (s *kiroResponsesHistoryStore) diskPath(id string) string {
	return filepath.Join(strings.TrimSpace(s.dir), sanitizeKiroResponsesHistoryID(id)+".json")
}

func sanitizeKiroResponsesHistoryID(id string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_' || r == '-':
			return r
		default:
			return -1
		}
	}, id)
	if cleaned == "" {
		return "invalid"
	}
	return cleaned
}

func defaultKiroResponsesHistoryDir() string {
	if dir := strings.TrimSpace(os.Getenv("KIRO_RESPONSES_HISTORY_DIR")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("DATA_DIR")); dir != "" {
		return filepath.Join(dir, "kiro-responses")
	}
	if info, err := os.Stat("/app/data"); err == nil && info.IsDir() {
		return filepath.Join("/app/data", "kiro-responses")
	}
	return filepath.Join(".", "data", "kiro-responses")
}

func newKiroResponsesHistoryStoreForDir(dir string) *kiroResponsesHistoryStore {
	return &kiroResponsesHistoryStore{
		items: make(map[string]kiroResponsesHistoryEntry),
		now:   time.Now,
		dir:   dir,
	}
}

func (s *kiroResponsesHistoryStore) currentTime() time.Time {
	if s == nil || s.now == nil {
		return time.Now()
	}
	return s.now()
}
