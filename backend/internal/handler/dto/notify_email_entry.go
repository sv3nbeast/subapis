package dto

import "github.com/Wei-Shaw/sub2api/internal/service"

type NotifyEmailEntry struct {
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
	Verified bool   `json:"verified"`
}

func NotifyEmailEntriesFromService(entries []service.NotifyEmailEntry) []NotifyEmailEntry {
	if entries == nil {
		return nil
	}
	out := make([]NotifyEmailEntry, len(entries))
	for i, entry := range entries {
		out[i] = NotifyEmailEntry{
			Email:    entry.Email,
			Disabled: entry.Disabled,
			Verified: entry.Verified,
		}
	}
	return out
}

func NotifyEmailEntriesToService(entries []NotifyEmailEntry) []service.NotifyEmailEntry {
	if entries == nil {
		return nil
	}
	out := make([]service.NotifyEmailEntry, len(entries))
	for i, entry := range entries {
		out[i] = service.NotifyEmailEntry{
			Email:    entry.Email,
			Disabled: entry.Disabled,
			Verified: entry.Verified,
		}
	}
	return out
}
