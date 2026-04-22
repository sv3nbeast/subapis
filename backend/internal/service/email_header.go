package service

import "strings"

// sanitizeEmailHeader strips CR/LF to avoid SMTP header injection.
func sanitizeEmailHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}
