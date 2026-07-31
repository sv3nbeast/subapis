package service

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type anthropicSoft429ModelRepo struct {
	AccountRepository
	accountID int64
	modelKey  string
	resetAt   time.Time
}

func (r *anthropicSoft429ModelRepo) SetModelRateLimit(_ context.Context, accountID int64, modelKey string, resetAt time.Time, _ ...string) error {
	r.accountID = accountID
	r.modelKey = modelKey
	r.resetAt = resetAt
	return nil
}

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

func TestCommitAnthropicSoftRateLimitUsesRequestedModelScope(t *testing.T) {
	repo := &anthropicSoft429ModelRepo{}
	rateLimits := NewRateLimitService(repo, nil, nil, nil, nil)
	svc := &GatewayService{accountRepo: repo, rateLimitService: rateLimits}
	account := &Account{ID: 71, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	failoverErr := &UpstreamFailoverError{
		StatusCode:             http.StatusTooManyRequests,
		ResponseBody:           []byte(`{"error":{"type":"rate_limit_error","message":"Error"}}`),
		ResponseHeaders:        http.Header{},
		AnthropicSoftRateLimit: true,
		RequestedModel:         "claude-sonnet-5",
		RetryableOnSameAccount: true,
	}

	before := time.Now()
	svc.CommitAnthropicSoftRateLimit(context.Background(), account.ID, failoverErr, account)
	after := time.Now()

	require.True(t, failoverErr.AnthropicSoftRateLimitCommitted)
	require.Equal(t, account.ID, repo.accountID)
	require.Equal(t, "claude-sonnet-5", repo.modelKey)
	require.True(t, !repo.resetAt.Before(before.Add(10*time.Second)) && !repo.resetAt.After(after.Add(10*time.Second)))
	require.Nil(t, account.RateLimitResetAt, "soft model rejection must not mutate the account-wide cooldown")
}

func TestAnthropicStainlessHelperDiagnosticsAreBounded(t *testing.T) {
	require.Equal(t, anthropicStainlessHelperCompaction, classifyAnthropicStainlessHelper("compaction"))
	require.Equal(t, anthropicStainlessHelperToolRunner, classifyAnthropicStainlessHelper("BetaToolRunner,CustomTool"))
	require.Equal(t, anthropicStainlessHelperOther, safeHeaderValueForLog("x-stainless-helper", "private-helper-name"))
	require.NotContains(t, safeHeaderValueForLog("x-stainless-helper", "private-helper-name"), "private-helper-name")
}
