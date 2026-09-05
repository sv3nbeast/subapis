package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCNRecordUsageDoesNotFallbackToClaudeListPricing(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepseek} {
		for _, explicitPrice := range []bool{false, true} {
			t.Run(platform+map[bool]string{true: "/explicit", false: "/unpriced"}[explicitPrice], func(t *testing.T) {
				usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
				svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
				group := &Group{ID: 99, Platform: platform, RateMultiplier: 1}
				if explicitPrice {
					price := 0.000001
					group.ModelPricing = []ChannelModelPricing{{
						Platform: platform, Models: []string{"claude-opus-4-6"}, BillingMode: BillingModeToken,
						InputPrice: &price, OutputPrice: &price,
					}}
					svc.resolver = NewModelPricingResolver(nil, svc.billingService)
				}
				err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{
						RequestID: "cn-alias", Model: "claude-opus-4-6",
						Usage: OpenAIUsage{InputTokens: 100, OutputTokens: 20},
					},
					APIKey: &APIKey{ID: 1, Group: group}, User: &User{ID: 2},
					Account: &Account{ID: 3, Platform: platform, Type: AccountTypeAPIKey},
				})
				require.NoError(t, err)
				require.NotNil(t, usageRepo.lastLog, "unpriced results must retain usage evidence")
				if explicitPrice {
					require.InDelta(t, 0.00012, usageRepo.lastLog.TotalCost, 1e-10)
				} else {
					require.Zero(t, usageRepo.lastLog.TotalCost, "never charge CN aliases at Claude list prices")
				}
			})
		}
	}
}
