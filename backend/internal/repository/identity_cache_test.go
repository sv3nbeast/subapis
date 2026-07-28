//go:build unit

package repository

import (
	"math"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFingerprintKey(t *testing.T) {
	tests := []struct {
		name      string
		accountID int64
		form      service.UAForm
		expected  string
	}{
		{
			name:      "normal_account_id",
			accountID: 123,
			form:      service.UAFormPlainCLI,
			expected:  "fingerprint:123:plain",
		},
		{
			name:      "zero_account_id",
			accountID: 0,
			form:      service.UAFormPlainCLI,
			expected:  "fingerprint:0:plain",
		},
		{
			name:      "negative_account_id",
			accountID: -1,
			form:      service.UAFormAgentSDK,
			expected:  "fingerprint:-1:agent-sdk",
		},
		{
			name:      "max_int64",
			accountID: math.MaxInt64,
			form:      service.UAFormAgentSDK,
			expected:  "fingerprint:9223372036854775807:agent-sdk",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fingerprintKey(tc.accountID, tc.form)
			require.Equal(t, tc.expected, got)
		})
	}
}
