package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeAccountConcurrencyPreservesGrokOAuthPositiveValue(t *testing.T) {
	require.Equal(t, 10, normalizeAccountConcurrency(PlatformGrok, AccountTypeOAuth, 10))
	require.Equal(t, 1, normalizeAccountConcurrency(PlatformGrok, AccountTypeOAuth, 0))
	require.Equal(t, 1, normalizeAccountConcurrency(PlatformGrok, AccountTypeOAuth, -5))
}
