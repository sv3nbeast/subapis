package service

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIsAnthropicSoftRateLimitResponse(t *testing.T) {
	account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"try again"}}`)

	t.Run("explicit resetless rate limit is soft", func(t *testing.T) {
		require.True(t, IsAnthropicSoftRateLimitResponse(account, http.StatusTooManyRequests, http.Header{}, body))
	})

	t.Run("ambiguous body is not soft", func(t *testing.T) {
		require.False(t, IsAnthropicSoftRateLimitResponse(account, http.StatusTooManyRequests, http.Header{}, []byte(`{"error":{"message":"extra usage"}}`)))
	})

	t.Run("valid five hour reset is hard", func(t *testing.T) {
		headers := http.Header{}
		headers.Set("anthropic-ratelimit-unified-5h-reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
		require.False(t, IsAnthropicSoftRateLimitResponse(account, http.StatusTooManyRequests, headers, body))
	})

	t.Run("fable model window is hard even without reset", func(t *testing.T) {
		headers := http.Header{}
		headers.Set("anthropic-ratelimit-unified-7d_oi-status", "rejected")
		require.False(t, IsAnthropicSoftRateLimitResponse(account, http.StatusTooManyRequests, headers, body))
	})

	t.Run("non Anthropic account is not soft", func(t *testing.T) {
		openAI := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		require.False(t, IsAnthropicSoftRateLimitResponse(openAI, http.StatusTooManyRequests, http.Header{}, body))
	})
}

func TestAnthropicSoftRateLimitRetryDelay(t *testing.T) {
	require.Equal(t, 5*time.Second, AnthropicSoftRateLimitRetryDelay())
}
