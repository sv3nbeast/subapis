//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmailDomainSuffixFromEmail(t *testing.T) {
	require.Equal(t, "example.com", EmailDomainSuffixFromEmail("user@example.com"))
	require.Equal(t, "example.com", EmailDomainSuffixFromEmail(" USER@EXAMPLE.COM "))
	require.Empty(t, EmailDomainSuffixFromEmail("invalid-email"))
}

func TestShouldPreferDifferentEmailDomainSuffixForFailover(t *testing.T) {
	t.Run("antigravity 503 model capacity exhausted", func(t *testing.T) {
		err := &UpstreamFailoverError{
			StatusCode:   503,
			ResponseBody: []byte(`{"error":{"message":"No capacity available for model claude-opus-4-6-thinking on the server","details":[{"reason":"MODEL_CAPACITY_EXHAUSTED"}]}}`),
		}
		require.True(t, ShouldPreferDifferentEmailDomainSuffixForFailover(PlatformAntigravity, err))
	})

	t.Run("antigravity 429 exhausted quota message", func(t *testing.T) {
		err := &UpstreamFailoverError{
			StatusCode:   429,
			ResponseBody: []byte(`{"error":{"message":"You have exhausted your capacity on this model. Please try again later."}}`),
		}
		require.True(t, ShouldPreferDifferentEmailDomainSuffixForFailover(PlatformAntigravity, err))
	})

	t.Run("non antigravity ignored", func(t *testing.T) {
		err := &UpstreamFailoverError{
			StatusCode:   503,
			ResponseBody: []byte(`{"error":{"message":"No capacity available for model claude-opus-4-6-thinking on the server","details":[{"reason":"MODEL_CAPACITY_EXHAUSTED"}]}}`),
		}
		require.False(t, ShouldPreferDifferentEmailDomainSuffixForFailover(PlatformAnthropic, err))
	})

	t.Run("generic 503 ignored", func(t *testing.T) {
		err := &UpstreamFailoverError{
			StatusCode:   503,
			ResponseBody: []byte(`{"error":{"message":"Upstream service temporarily unavailable"}}`),
		}
		require.False(t, ShouldPreferDifferentEmailDomainSuffixForFailover(PlatformAntigravity, err))
	})
}
