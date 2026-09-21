//go:build unit

package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// hiddenRateLimitRepoStub 让 ListByGroup 返回全部账号（共享 stub 默认返回 nil）。
type hiddenRateLimitRepoStub struct {
	*mockAccountRepoForPlatform
}

func (s *hiddenRateLimitRepoStub) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	return s.accounts, nil
}

// 生产事故回归（2026-09-21，分组 subapis-Max 5x）：
// 分组内支持 claude-opus-4-8 的 Kiro 混合调度账号全部处于账号级 60s/月度限流
// （rate_limit_reset_at > now，构建调度快照时被整体排除），可见列表只剩一个
// 只支持 claude-fable-5 的原生 Anthropic 账号。失败统计必须把这些"隐藏"的
// 限流账号计入 model_rate_limited，使 handler 分类为可重试的 429，
// 而不是永久性的 400 "Requested model is not supported"。
func TestSelectionFailureCountsHiddenRateLimitedSupporters(t *testing.T) {
	t.Parallel()

	groupID := int64(10)
	reset := time.Now().Add(45 * time.Second)
	visible := []Account{
		{
			ID: 1, Platform: PlatformAnthropic, Status: StatusActive, Schedulable: true,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"claude-fable-5": "claude-fable-5"},
			},
		},
	}
	hiddenKiro := Account{
		ID: 2, Platform: PlatformKiro, Status: StatusActive, Schedulable: true,
		RateLimitResetAt: &reset,
		Extra:            map[string]any{"mixed_scheduling": true},
		Credentials: map[string]any{
			"model_mapping": map[string]any{"claude-opus-4-8": "claude-opus-4.8"},
		},
	}
	// 同样在限流，但不支持该模型：不得计入。
	hiddenOther := Account{
		ID: 3, Platform: PlatformKiro, Status: StatusActive, Schedulable: true,
		RateLimitResetAt: &reset,
		Extra:            map[string]any{"mixed_scheduling": true},
		Credentials: map[string]any{
			"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4.6"},
		},
	}

	base := &mockAccountRepoForPlatform{
		accounts:     append(append([]Account{}, visible...), hiddenKiro, hiddenOther),
		accountsByID: map[int64]*Account{},
	}
	for i := range base.accounts {
		base.accountsByID[base.accounts[i].ID] = &base.accounts[i]
	}
	accountRepo := &hiddenRateLimitRepoStub{mockAccountRepoForPlatform: base}
	svc := &GatewayService{accountRepo: accountRepo, cfg: testConfig()}

	stats := svc.logDetailedSelectionFailure(
		context.Background(), &groupID, "", "claude-opus-4-8", PlatformAnthropic, visible, nil, true)

	require.Equal(t, 1, stats.ModelUnsupported, "the visible fable-5-only account stays unsupported")
	require.Equal(t, 1, stats.ModelRateLimited, "hidden cooling supporter must be counted, non-supporter must not")

	summary := summarizeSelectionFailureStats(stats)
	require.True(t, strings.Contains(summary, "model_rate_limited=1"), summary)

	// 纯不支持场景不受影响：没有隐藏限流账号时保持 0。
	statsFable := svc.logDetailedSelectionFailure(
		context.Background(), &groupID, "", "claude-fable-5-2", PlatformAnthropic, visible, nil, true)
	require.Equal(t, 0, statsFable.ModelRateLimited)
}
