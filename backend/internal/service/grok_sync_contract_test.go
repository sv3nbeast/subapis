package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGrokAffinityUsesRealSessionHashEntry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("session_id", "same-session")
	svc := &OpenAIGatewayService{}
	a, b := []byte(`{"model":"grok-4.5"}`), []byte(`{"model":"grok-4.3"}`)
	c.Set("api_key", &APIKey{Group: &Group{Platform: PlatformGrok}})
	require.NotEqual(t, svc.GenerateSessionHash(c, a), svc.GenerateSessionHash(c, b))
	require.Equal(t, svc.GenerateSessionHash(c, a), svc.GenerateSessionHash(c, a))
	c.Set("api_key", &APIKey{Group: &Group{Platform: PlatformOpenAI}})
	require.Equal(t, svc.GenerateSessionHash(c, a), svc.GenerateSessionHash(c, b))
}

type grokSnapshotContractRepo struct {
	AccountRepository
	updates map[string]any
}

func (r *grokSnapshotContractRepo) UpdateExtra(_ context.Context, _ int64, extra map[string]any) error {
	r.updates = extra
	return nil
}
func TestGrokSchedulerSnapshotIsPersistedFromUsageEntry(t *testing.T) {
	limit, remaining, reset := int64(100), int64(5), time.Now().Add(time.Hour).Unix()
	repo := &grokSnapshotContractRepo{}
	svc := &OpenAIGatewayService{accountRepo: repo}
	svc.updateGrokUsageSnapshot(context.Background(), 9, &xai.QuotaSnapshot{
		Requests: &xai.QuotaWindow{Limit: &limit, Remaining: &remaining, ResetUnix: &reset},
	})
	require.NotNil(t, repo.updates[grokQuotaSnapshotExtraKey])
	require.InDelta(t, 95.0, repo.updates["grok_sched_utilization"], 0.001)
	require.True(t, EvaluateAccountSchedulingThreshold(&Account{Platform: PlatformGrok, Extra: repo.updates},
		map[string]int{PlatformGrok: 90}, time.Now()).ShouldPause)
	svc.updateGrokUsageSnapshot(context.Background(), 9, &xai.QuotaSnapshot{
		Requests: &xai.QuotaWindow{Limit: &limit, Remaining: &remaining},
	})
	require.Contains(t, repo.updates, "grok_sched_reset_at")
	require.Nil(t, repo.updates["grok_sched_reset_at"], "clear the previous window reset when the upstream omits it")
}
