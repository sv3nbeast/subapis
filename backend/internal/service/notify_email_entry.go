package service

import (
	"encoding/json"
	"strings"
)

// NotifyEmailEntry represents a notification email with per-email state.
type NotifyEmailEntry struct {
	Email    string `json:"email"`
	Disabled bool   `json:"disabled"`
	Verified bool   `json:"verified"`
}

// ParseNotifyEmails parses either the legacy []string JSON or the newer
// []NotifyEmailEntry JSON into structured entries.
func ParseNotifyEmails(raw string) []NotifyEmailEntry {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}

	var entries []NotifyEmailEntry
	if err := json.Unmarshal([]byte(raw), &entries); err == nil && len(entries) > 0 && !isLegacyNotifyEmailArray(raw) {
		return entries
	}

	var emails []string
	if err := json.Unmarshal([]byte(raw), &emails); err == nil {
		out := make([]NotifyEmailEntry, 0, len(emails))
		for _, email := range emails {
			email = strings.TrimSpace(email)
			if email == "" {
				continue
			}
			out = append(out, NotifyEmailEntry{
				Email:    email,
				Disabled: false,
				Verified: true,
			})
		}
		return out
	}

	return nil
}

func isLegacyNotifyEmailArray(raw string) bool {
	var arr []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &arr); err != nil || len(arr) == 0 {
		return false
	}
	first := strings.TrimSpace(string(arr[0]))
	return len(first) > 0 && first[0] == '"'
}

func MarshalNotifyEmails(entries []NotifyEmailEntry) string {
	if len(entries) == 0 {
		return "[]"
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(data)
}
