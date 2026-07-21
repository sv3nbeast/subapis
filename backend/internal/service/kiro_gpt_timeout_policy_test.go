package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestKiroGPTTimeoutPolicyDisablesGatewayDeadlinesOnlyForBridgeModels(t *testing.T) {
	groupID := int64(33)
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{
		KiroResilience: config.GatewayKiroResilienceConfig{
			Mode:                         config.KiroResilienceModeEnforce,
			GroupIDs:                     []int64{groupID},
			ResponseHeaderTimeoutSeconds: 30,
			FirstSemanticTimeoutSeconds:  60,
			FailoverBudgetSeconds:        105,
		},
	}}}

	for _, model := range OpenAIKiroBridgeModels {
		t.Run(model, func(t *testing.T) {
			parent, cancelParent := context.WithCancel(context.Background())
			ctx := WithKiroGPTTimeoutsDisabled(parent, model)
			require.True(t, KiroGPTTimeoutsDisabled(ctx))

			tracked, err := svc.StartKiroResilienceTracking(ctx, &groupID, &Account{Platform: PlatformKiro})
			require.NoError(t, err)
			require.False(t, kiroResilienceBudgetActive(tracked), "Kiro GPT must not create a request-wide budget")
			require.Zero(t, svc.kiroResponseHeaderTimeoutForRequest(tracked, &groupID, 1), "Kiro GPT must not create a response-header timeout")
			require.Zero(t, svc.kiroFirstSemanticTimeoutForRequest(tracked, &groupID, 1), "Kiro GPT must not create a first-semantic timeout")

			semanticCtx, cancelSemantic, stopTimer, err := svc.kiroPreSemanticContext(tracked, &groupID, 1)
			require.NoError(t, err)
			_, hasDeadline := semanticCtx.Deadline()
			require.False(t, hasDeadline, "Kiro GPT must not create a first-semantic deadline")

			cancelParent()
			select {
			case <-semanticCtx.Done():
				require.ErrorIs(t, semanticCtx.Err(), context.Canceled)
			case <-time.After(time.Second):
				t.Fatal("parent cancellation did not propagate through Kiro GPT timeout bypass")
			}
			stopTimer()
			cancelSemantic(context.Canceled)
		})
	}

	t.Run("Kiro Claude remains protected", func(t *testing.T) {
		ctx := WithKiroGPTTimeoutsDisabled(context.Background(), "claude-sonnet-4-6")
		require.False(t, KiroGPTTimeoutsDisabled(ctx))
		tracked, err := svc.StartKiroResilienceTracking(ctx, &groupID, &Account{Platform: PlatformKiro})
		require.NoError(t, err)
		require.True(t, kiroResilienceBudgetActive(tracked))
		require.Equal(t, 30*time.Second, svc.kiroResponseHeaderTimeoutForRequest(tracked, &groupID, 1))
		require.Equal(t, 60*time.Second, svc.kiroFirstSemanticTimeoutForRequest(tracked, &groupID, 1))

		semanticCtx, cancelSemantic, stopTimer, err := svc.kiroPreSemanticContext(tracked, &groupID, 1)
		require.NoError(t, err)
		_, hasDeadline := semanticCtx.Deadline()
		require.False(t, hasDeadline, "first-semantic protection uses a cancel timer, not context.WithDeadline")
		stopTimer()
		cancelSemantic(context.Canceled)
	})
}
