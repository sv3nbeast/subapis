package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/stretchr/testify/require"
)

func TestKiroResponsesHistoryStorePersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	store := newKiroResponsesHistoryStoreForDir(dir)
	store.save(kiroResponsesHistoryEntry{
		ID:           "resp_disk",
		Model:        "claude-sonnet-4-6",
		Instructions: "keep this",
		Input:        json.RawMessage(`[{"type":"input_text","text":"hello"}]`),
		Output: []apicompat.ResponsesOutput{{
			Type: "message",
			Role: "assistant",
			Content: []apicompat.ResponsesContentPart{{
				Type: "output_text",
				Text: "world",
			}},
		}},
	})

	reloaded := newKiroResponsesHistoryStoreForDir(dir)
	entry, ok := reloaded.load("resp_disk")
	require.True(t, ok)
	require.Equal(t, "resp_disk", entry.ID)
	require.Equal(t, "keep this", entry.Instructions)
	require.JSONEq(t, `[{"type":"input_text","text":"hello"}]`, string(entry.Input))
	require.Len(t, entry.Output, 1)
	require.Equal(t, "world", entry.Output[0].Content[0].Text)
}

func TestKiroResponsesHistoryStoreExpiresDiskEntry(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	store := newKiroResponsesHistoryStoreForDir(dir)
	store.now = func() time.Time { return now }
	store.save(kiroResponsesHistoryEntry{
		ID:       "resp_old",
		Input:    json.RawMessage(`[]`),
		StoredAt: now.Add(-kiroResponsesHistoryTTL - time.Second),
	})

	reloaded := newKiroResponsesHistoryStoreForDir(dir)
	reloaded.now = func() time.Time { return now }
	_, ok := reloaded.load("resp_old")
	require.False(t, ok)
	require.NoFileExists(t, filepath.Join(dir, "resp_old.json"))
}

func TestKiroResponsesHistoryStoreSanitizesDiskPath(t *testing.T) {
	dir := t.TempDir()
	store := newKiroResponsesHistoryStoreForDir(dir)
	store.save(kiroResponsesHistoryEntry{
		ID:    "../resp/evil",
		Input: json.RawMessage(`[]`),
	})

	require.NoFileExists(t, filepath.Join(dir, "..", "resp", "evil.json"))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "respevil.json", entries[0].Name())
}

func TestKiroResponsesHistoryStorePurgesExpiredDiskEntriesOnSave(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "resp_stale.json")
	require.NoError(t, os.WriteFile(oldPath, []byte(`{"id":"resp_stale","stored_at":1}`), 0o600))

	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	staleTime := now.Add(-kiroResponsesHistoryTTL - time.Hour)
	require.NoError(t, os.Chtimes(oldPath, staleTime, staleTime))

	store := newKiroResponsesHistoryStoreForDir(dir)
	store.now = func() time.Time { return now }
	store.save(kiroResponsesHistoryEntry{
		ID:    "resp_fresh",
		Input: json.RawMessage(`[]`),
	})

	require.NoFileExists(t, oldPath)
	require.FileExists(t, filepath.Join(dir, "resp_fresh.json"))
}
