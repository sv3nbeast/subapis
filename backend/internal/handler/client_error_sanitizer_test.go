package handler

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeClientErrorMessage_RedactsInternalProviderName(t *testing.T) {
	tests := []struct {
		name   string
		status int
		input  string
		want   string
	}{
		{
			name:   "service unavailable",
			status: http.StatusServiceUnavailable,
			input:  "Kiro upstream failover budget exhausted",
			want:   clientUpstreamTemporarilyUnavailableMessage,
		},
		{
			name:   "rate limited",
			status: http.StatusTooManyRequests,
			input:  "KIRO upstream is temporarily rate limited",
			want:   clientUpstreamTemporarilyRateLimitedMessage,
		},
		{
			name:   "unrelated message preserved",
			status: http.StatusBadGateway,
			input:  "Upstream request failed",
			want:   "Upstream request failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, sanitizeClientErrorMessage(tt.status, tt.input))
		})
	}
}
