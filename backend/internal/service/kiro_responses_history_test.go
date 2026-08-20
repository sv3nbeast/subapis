package service

import (
	"encoding/json"
	"fmt"
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

func TestKiroResponsesHistoryStoreEphemeralEntryStaysInMemory(t *testing.T) {
	dir := t.TempDir()
	store := newKiroResponsesHistoryStoreForDir(dir)
	store.saveEphemeral(kiroResponsesHistoryEntry{
		ID:    "resp_prewarm_ephemeral",
		Input: json.RawMessage(`[{"type":"input_text","text":"prewarm"}]`),
	})

	entry, ok := store.load("resp_prewarm_ephemeral")
	require.True(t, ok)
	require.JSONEq(t, `[{"type":"input_text","text":"prewarm"}]`, string(entry.Input))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "transport-only prewarm must not create one disk file per client session")

	reloaded := newKiroResponsesHistoryStoreForDir(dir)
	_, ok = reloaded.load("resp_prewarm_ephemeral")
	require.False(t, ok)
}

func TestKiroResponsesHistoryStoreEphemeralEntriesAreBoundedAndExpire(t *testing.T) {
	boundedStore := newKiroResponsesHistoryStoreForDir(t.TempDir())
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	boundedStore.now = func() time.Time { return now }
	for idx := 0; idx < kiroResponsesMaxEphemeral+10; idx++ {
		boundedStore.saveEphemeral(kiroResponsesHistoryEntry{
			ID:       fmt.Sprintf("resp_ephemeral_%04d", idx),
			Input:    json.RawMessage(`[]`),
			StoredAt: now.Add(time.Duration(idx) * time.Millisecond),
		})
	}
	require.LessOrEqual(t, len(boundedStore.items), kiroResponsesMaxEphemeral)

	expiryStore := newKiroResponsesHistoryStoreForDir(t.TempDir())
	expiryStore.now = func() time.Time { return now }
	expiryStore.saveEphemeral(kiroResponsesHistoryEntry{ID: "resp_expiring", Input: json.RawMessage(`[]`)})
	now = now.Add(kiroResponsesEphemeralTTL + time.Second)
	_, ok := expiryStore.load("resp_expiring")
	require.False(t, ok)
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
