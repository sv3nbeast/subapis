package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMixedSchedulingKiroDroidRules(t *testing.T) {
	require.Equal(t,
		[]string{PlatformAnthropic, PlatformAntigravity, PlatformKiro, PlatformDroid},
		mixedSchedulingQueryPlatforms(PlatformAnthropic),
	)
	require.Equal(t,
		[]string{PlatformGemini, PlatformAntigravity},
		mixedSchedulingQueryPlatforms(PlatformGemini),
	)

	tests := []struct {
		name     string
		account  Account
		native   string
		expected bool
	}{
		{
			name:     "anthropic native account is allowed",
			account:  Account{Platform: PlatformAnthropic},
			native:   PlatformAnthropic,
			expected: true,
		},
		{
			name:     "kiro with mixed scheduling is allowed in anthropic mixed bucket",
			account:  Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}},
			native:   PlatformAnthropic,
			expected: true,
		},
		{
			name:     "droid with mixed scheduling is allowed in anthropic mixed bucket",
			account:  Account{Platform: PlatformDroid, Extra: map[string]any{"mixed_scheduling": true}},
			native:   PlatformAnthropic,
			expected: true,
		},
		{
			name:     "kiro without mixed scheduling is filtered from anthropic mixed bucket",
			account:  Account{Platform: PlatformKiro},
			native:   PlatformAnthropic,
			expected: false,
		},
		{
			name:     "droid without mixed scheduling is filtered from anthropic mixed bucket",
			account:  Account{Platform: PlatformDroid},
			native:   PlatformAnthropic,
			expected: false,
		},
		{
			name:     "antigravity with mixed scheduling is allowed in gemini mixed bucket",
			account:  Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}},
			native:   PlatformGemini,
			expected: true,
		},
		{
			name:     "kiro with mixed scheduling is filtered from gemini mixed bucket",
			account:  Account{Platform: PlatformKiro, Extra: map[string]any{"mixed_scheduling": true}},
			native:   PlatformGemini,
			expected: false,
		},
		{
			name:     "droid with mixed scheduling is filtered from gemini mixed bucket",
			account:  Account{Platform: PlatformDroid, Extra: map[string]any{"mixed_scheduling": true}},
			native:   PlatformGemini,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, isAccountAllowedInMixedScheduling(&tt.account, tt.native))
		})
	}
}
