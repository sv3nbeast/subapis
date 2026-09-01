package handler

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TempUnscheduler 用于 HandleFailoverError 中同账号重试耗尽后的临时封禁。
// GatewayService 隐式实现此接口。
type TempUnscheduler interface {
	TempUnscheduleRetryableError(ctx context.Context, accountID int64, failoverErr *service.UpstreamFailoverError)
}

// AnthropicSoftRateLimitCommitter persists a soft Claude 429 only after the
// dedicated same-account retry has failed. It is optional so existing test
// doubles and non-Claude gateway implementations remain source compatible.
type AnthropicSoftRateLimitCommitter interface {
	CommitAnthropicSoftRateLimit(ctx context.Context, accountID int64, failoverErr *service.UpstreamFailoverError, account ...*service.Account)
}

// FailoverAction 表示 failover 错误处理后的下一步动作
type FailoverAction int

const (
	// FailoverContinue 继续循环（同账号重试或切换账号，调用方统一 continue）
	FailoverContinue FailoverAction = iota
	// FailoverExhausted 切换次数耗尽（调用方应返回错误响应）
	FailoverExhausted
	// FailoverCanceled context 已取消（调用方应直接 return）
	FailoverCanceled
)

const (
	// maxSameAccountRetries 同账号重试次数上限（针对 RetryableOnSameAccount 错误）
	maxSameAccountRetries = 3
	// sameAccountRetryDelay 同账号重试间隔
	sameAccountRetryDelay      = 500 * time.Millisecond
	maxRequestScopedRetryDelay = 8 * time.Second
	// anthropicSoftRateLimitRetryCount 同一请求对 Claude 软 429 只允许一次
	anthropicSoftRateLimitRetryCount = 1
	// anthropicSoftRateLimitMaxAccounts limits a request-scoped/reset-less 429
	// cohort. If two independent credentials reject the same request shape,
	// scanning the rest of the pool only amplifies latency and false cooldowns.
	anthropicSoftRateLimitMaxAccounts = 2
	// singleAccountBackoffDelay 单账号分组 503 退避重试固定延时。
	// Service 层在 SingleAccountRetry 模式下已做充分原地重试（最多 3 次、总等待 30s），
	// Handler 层只需短暂间隔后重新进入 Service 层即可。
	singleAccountBackoffDelay = 2 * time.Second
	// maxProfitVetoAttempts limits request-scoped retries rejected by group profit control.
	maxProfitVetoAttempts = 10
	// A context-usage-only EOF is driven by the translated conversation shape,
	// not credential health. Probe at most one peer account to distinguish an
	// account-local anomaly, then stop instead of scanning the entire pool.
	kiroMetadataOnlyEOFMaxAccounts = 2

	// Legacy Kiro behavior is retained behind off/observe so rollout can be
	// reverted without a binary rollback. Enforce mode never enters this path.
	kiro429SoftSwitchThreshold   = 2
	kiro429HardRetryLimit        = 12
	kiro429DecisionRetrySame     = "retry_same"
	kiro429DecisionSoftSwitch    = "soft_switch"
	kiro429DecisionResumeCurrent = "resume_current_no_next"
	kiro429DecisionHardExclude   = "hard_exclude"
	kiro429DecisionExclude       = "exclude_account"
	kiro429DecisionExhausted     = "exhausted"
)

func sameAccountRetryDelayFor(failoverErr *service.UpstreamFailoverError, retryCount int) time.Duration {
	if failoverErr == nil || !failoverErr.RequestScopedTransient || retryCount <= 1 {
		return sameAccountRetryDelay
	}
	delay := sameAccountRetryDelay
	for i := 1; i < retryCount; i++ {
		if delay >= maxRequestScopedRetryDelay/2 {
			return maxRequestScopedRetryDelay
		}
		delay *= 2
	}
	return delay
}

const profitVetoExhaustedMessage = "No available accounts: all candidates rejected by group profit control"

// Kept as a variable so failover state tests can exercise the transition
// without sleeping five real seconds. Production value is fixed by the
// service policy and must not be changed at runtime.
var anthropicSoftRateLimitRetryDelay = service.AnthropicSoftRateLimitRetryDelay()

// FailoverState 跨循环迭代共享的 failover 状态
type FailoverState struct {
	SwitchCount                  int
	MaxSwitches                  int
	FailedAccountIDs             map[int64]struct{}
	SameAccountRetryCount        map[int64]int
	AnthropicSoft429Retries      map[int64]int
	AnthropicSoft429Accounts     map[int64]struct{}
	AntigravityQuota429Accounts  map[int64]struct{}
	KiroMetadataOnlyEOFAccounts  map[int64]struct{}
	Kiro429RetryCount            map[int64]int
	Kiro429SoftExcludedIDs       map[int64]struct{}
	Kiro429LastSoftExcluded      int64
	PreSemanticTimeoutCount      int
	AvoidEmailDomainSuffixes     map[string]struct{}
	ModelCapacityRetryState      *service.ModelCapacityRetryState
	LastFailoverErr              *service.UpstreamFailoverError
	LastNonRateLimitErr          *service.UpstreamFailoverError
	ForceAccountID               int64
	ForceCacheBilling            bool
	KiroResilienceEnforced       bool
	KiroAttempted                bool
	KiroAnthropicFallbackEnabled bool
	KiroAnthropicFallbackActive  bool
	KiroAnthropicFallbackUsed    bool
	AnthropicFallbackAttempts    int
	FallbackSessionKey           string
	KiroWaitReselectUsed         bool
	hasBoundSession              bool
	earliestKiroRetryAt          time.Time
	lastNonRateLimitWasKiro      bool
	profitVetoedAccountIDs       map[int64]struct{}
	profitVetoCount              int
}

// NewFailoverState 创建 failover 状态
func NewFailoverState(maxSwitches int, hasBoundSession bool) *FailoverState {
	return &FailoverState{
		MaxSwitches:                 maxSwitches,
		FailedAccountIDs:            make(map[int64]struct{}),
		SameAccountRetryCount:       make(map[int64]int),
		AnthropicSoft429Retries:     make(map[int64]int),
		AnthropicSoft429Accounts:    make(map[int64]struct{}),
		AntigravityQuota429Accounts: make(map[int64]struct{}),
		KiroMetadataOnlyEOFAccounts: make(map[int64]struct{}),
		Kiro429RetryCount:           make(map[int64]int),
		Kiro429SoftExcludedIDs:      make(map[int64]struct{}),
		AvoidEmailDomainSuffixes:    make(map[string]struct{}),
		ModelCapacityRetryState:     service.NewModelCapacityRetryState(0),
		hasBoundSession:             hasBoundSession,
		profitVetoedAccountIDs:      make(map[int64]struct{}),
	}
}

func (s *FailoverState) RecordProfitVeto(accountID int64) FailoverAction {
	if s == nil {
		return FailoverExhausted
	}
	if s.FailedAccountIDs == nil {
		s.FailedAccountIDs = make(map[int64]struct{})
	}
	s.FailedAccountIDs[accountID] = struct{}{}
	if s.profitVetoedAccountIDs == nil {
		s.profitVetoedAccountIDs = make(map[int64]struct{})
	}
	s.profitVetoedAccountIDs[accountID] = struct{}{}
	s.profitVetoCount++
	if s.profitVetoCount >= maxProfitVetoAttempts {
		return FailoverExhausted
	}
	return FailoverContinue
}

func (s *FailoverState) ProfitVetoCount() int {
	if s == nil {
		return 0
	}
	return s.profitVetoCount
}

func (s *FailoverState) allExclusionsAreProfitVetoed() bool {
	if s == nil || len(s.profitVetoedAccountIDs) == 0 || len(s.FailedAccountIDs) == 0 {
		return false
	}
	for id := range s.FailedAccountIDs {
		if _, ok := s.profitVetoedAccountIDs[id]; !ok {
			return false
		}
	}
	return true
}

func (s *FailoverState) RecordAvoidEmailDomainSuffix(suffix string) {
	if s == nil {
		return
	}
	normalized := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(suffix, "@")))
	if normalized == "" {
		return
	}
	if s.AvoidEmailDomainSuffixes == nil {
		s.AvoidEmailDomainSuffixes = make(map[string]struct{})
	}
	s.AvoidEmailDomainSuffixes[normalized] = struct{}{}
}

func (s *FailoverState) AvoidEmailDomainSuffixesList() []string {
	if s == nil || len(s.AvoidEmailDomainSuffixes) == 0 {
		return nil
	}
	values := make([]string, 0, len(s.AvoidEmailDomainSuffixes))
	for suffix := range s.AvoidEmailDomainSuffixes {
		values = append(values, suffix)
	}
	sort.Strings(values)
	return values
}

// HandleFailoverError 处理 UpstreamFailoverError，返回下一步动作。
// 包含：缓存计费判断、同账号重试、临时封禁、切换计数、Antigravity 延时。
func (s *FailoverState) HandleFailoverError(
	ctx context.Context,
	gatewayService TempUnscheduler,
	accountID int64,
	platform string,
	failoverErr *service.UpstreamFailoverError,
	selectedAccount ...*service.Account,
) FailoverAction {
	if ctx != nil && ctx.Err() != nil {
		return FailoverCanceled
	}
	s.LastFailoverErr = failoverErr
	if failoverErr == nil || !failoverErr.ShouldRetryNextAccount() {
		return FailoverExhausted
	}
	s.ForceAccountID = 0
	if s.KiroResilienceEnforced {
		if platform == service.PlatformKiro {
			s.KiroAttempted = true
			s.recordEarliestKiroRetryAfter(failoverErr)
		}
		if !isKiro429Failover(platform, failoverErr) {
			s.LastNonRateLimitErr = failoverErr
			s.lastNonRateLimitWasKiro = platform == service.PlatformKiro
		}
	}

	// 缓存计费判断
	if !(platform == service.PlatformKiro && s.KiroResilienceEnforced) && needForceCacheBilling(s.hasBoundSession, failoverErr) {
		s.ForceCacheBilling = true
	}

	// Anthropic's reset-less rate_limit_error is a soft signal. Keep the
	// account eligible, pin selection to it, and wait once before considering
	// failover. The rate-limit side effect is intentionally deferred until the
	// retry fails; otherwise the scheduler would exclude the account we need to
	// probe.
	if platform == service.PlatformAnthropic && service.IsAnthropicSoftRateLimitFailover(failoverErr) {
		return s.handleAnthropicSoftRateLimit(ctx, gatewayService, accountID, failoverErr, selectedAccount...)
	}
	if platform == service.PlatformAntigravity && failoverErr.AntigravityQuota429 {
		return s.handleAntigravityQuota429(ctx, accountID, failoverErr)
	}
	if platform == service.PlatformKiro && failoverErr.Reason == service.GatewayFailureReasonKiroMetadataOnlyEOF {
		return s.handleKiroMetadataOnlyEOF(ctx, accountID, failoverErr)
	}

	if isKiro429Failover(platform, failoverErr) {
		if !s.KiroResilienceEnforced {
			return s.handleLegacyKiro429Failover(ctx, accountID, failoverErr)
		}
		s.recordKiro429Failover(ctx, accountID, failoverErr)
	}
	// Kiro service layer already exhausts the account's endpoint list. Replaying
	// the same account here can duplicate generation and multiply long waits.
	if platform == service.PlatformKiro && s.KiroResilienceEnforced {
		failoverErr.RetryableOnSameAccount = false
	}
	if failoverErr.FailoverProhibited {
		return FailoverExhausted
	}
	// 首语义超时发生在客户端收到任何字节之前，可以安全重放一次到
	// 备用账号；限制为两轮，避免多个慢账号串行等待穿透 Cloudflare 的
	// 120 秒代理读取窗口。Kiro 自己的 resilience 错误不设置该标记，
	// 继续使用其专用预算和账号池状态机。
	if failoverErr.PreSemanticTimeout {
		s.PreSemanticTimeoutCount++
		s.FailedAccountIDs[accountID] = struct{}{}
		if s.PreSemanticTimeoutCount >= 2 || s.SwitchCount >= s.MaxSwitches {
			return FailoverExhausted
		}
		s.SwitchCount++
		logger.FromContext(ctx).Warn("gateway.failover_presemantic_switch",
			zap.Int64("account_id", accountID),
			zap.Int("attempt", s.PreSemanticTimeoutCount),
			zap.Int("max_attempts", 2),
			zap.Int("switch_count", s.SwitchCount),
		)
		return FailoverContinue
	}

	// 同账号重试：对 RetryableOnSameAccount 的临时性错误，先在同一账号上重试
	if failoverErr.RetryableOnSameAccount && s.SameAccountRetryCount[accountID] < maxSameAccountRetries {
		s.SameAccountRetryCount[accountID]++
		retryDelay := sameAccountRetryDelayFor(failoverErr, s.SameAccountRetryCount[accountID])
		logger.FromContext(ctx).Warn("gateway.failover_same_account_retry",
			zap.Int64("account_id", accountID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("same_account_retry_count", s.SameAccountRetryCount[accountID]),
			zap.Int("same_account_retry_max", maxSameAccountRetries),
		)
		if !sleepWithContext(ctx, retryDelay) {
			return FailoverCanceled
		}
		return FailoverContinue
	}

	// 同账号重试用尽，执行临时封禁
	if failoverErr.RetryableOnSameAccount && !failoverErr.SuppressTempUnschedule {
		gatewayService.TempUnscheduleRetryableError(ctx, accountID, failoverErr)
	}

	// 加入失败列表
	s.FailedAccountIDs[accountID] = struct{}{}

	// 检查是否耗尽
	if s.SwitchCount >= s.MaxSwitches {
		if platform == service.PlatformKiro {
			s.applyEarliestKiroRetryAfter(failoverErr)
		}
		return FailoverExhausted
	}

	// 递增切换计数
	s.SwitchCount++
	logger.FromContext(ctx).Warn("gateway.failover_switch_account",
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("max_switches", s.MaxSwitches),
		zap.String("switch_reason", string(failoverErr.FailureKind)),
		zap.Int64("retry_after_ms", failoverErr.RetryAfter.Milliseconds()),
	)

	return FailoverContinue
}

func (s *FailoverState) handleKiroMetadataOnlyEOF(ctx context.Context, accountID int64, failoverErr *service.UpstreamFailoverError) FailoverAction {
	if s.KiroMetadataOnlyEOFAccounts == nil {
		s.KiroMetadataOnlyEOFAccounts = make(map[int64]struct{})
	}
	s.KiroAttempted = true
	if _, repeated := s.KiroMetadataOnlyEOFAccounts[accountID]; repeated {
		return FailoverExhausted
	}
	s.KiroMetadataOnlyEOFAccounts[accountID] = struct{}{}
	s.FailedAccountIDs[accountID] = struct{}{}
	failoverErr.RetryableOnSameAccount = false
	failoverErr.RequestScopedTransient = true
	failoverErr.SuppressTempUnschedule = true

	if len(s.KiroMetadataOnlyEOFAccounts) >= kiroMetadataOnlyEOFMaxAccounts || s.SwitchCount >= s.MaxSwitches {
		if len(s.KiroMetadataOnlyEOFAccounts) >= kiroMetadataOnlyEOFMaxAccounts {
			// Two independent credentials accepted the same logical request and
			// returned only context metadata. Surface a request-specific upstream
			// processing failure instead of a generic gateway 503. Keep the original
			// incomplete-stream kind for internal evidence and accounting.
			failoverErr.Reason = service.GatewayFailureReasonKiroContentProcessingFailed
			failoverErr.ClientStatusCode = http.StatusUnprocessableEntity
			failoverErr.ClientMessage = service.KiroUpstreamContentProcessingFailedClientMessage
			failoverErr.NextAccountAction = service.NextAccountStop
			failoverErr.RetryAfter = 0
		}
		logger.FromContext(ctx).Warn("gateway.kiro_metadata_only_eof_exhausted",
			zap.String("request_id", requestIDFromContext(ctx)),
			zap.Int64("account_id", accountID),
			zap.Int("attempted_account_count", len(s.KiroMetadataOnlyEOFAccounts)),
			zap.Int("max_account_count", kiroMetadataOnlyEOFMaxAccounts),
			zap.Int("switch_count", s.SwitchCount),
		)
		return FailoverExhausted
	}

	s.SwitchCount++
	logger.FromContext(ctx).Warn("gateway.kiro_metadata_only_eof_switch_peer",
		zap.String("request_id", requestIDFromContext(ctx)),
		zap.Int64("account_id", accountID),
		zap.Int("attempted_account_count", len(s.KiroMetadataOnlyEOFAccounts)),
		zap.Int("max_account_count", kiroMetadataOnlyEOFMaxAccounts),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("max_switches", s.MaxSwitches),
	)
	return FailoverContinue
}

func (s *FailoverState) handleAntigravityQuota429(
	ctx context.Context,
	accountID int64,
	failoverErr *service.UpstreamFailoverError,
) FailoverAction {
	const maxAccounts = 2
	if s.AntigravityQuota429Accounts == nil {
		s.AntigravityQuota429Accounts = make(map[int64]struct{})
	}
	s.AntigravityQuota429Accounts[accountID] = struct{}{}
	s.FailedAccountIDs[accountID] = struct{}{}
	failoverErr.RetryableOnSameAccount = false
	failoverErr.SuppressTempUnschedule = true

	if len(s.AntigravityQuota429Accounts) >= maxAccounts || s.SwitchCount >= s.MaxSwitches {
		logger.FromContext(ctx).Warn("gateway.antigravity_quota_429_cohort_exhausted",
			zap.Int64("account_id", accountID),
			zap.Int("attempted_account_count", len(s.AntigravityQuota429Accounts)),
			zap.Int("max_account_count", maxAccounts),
			zap.Int("switch_count", s.SwitchCount),
		)
		return FailoverExhausted
	}

	s.SwitchCount++
	logger.FromContext(ctx).Warn("gateway.antigravity_quota_429_switch_account",
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("attempted_account_count", len(s.AntigravityQuota429Accounts)),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("max_switches", s.MaxSwitches),
	)
	return FailoverContinue
}

func (s *FailoverState) handleAnthropicSoftRateLimit(
	ctx context.Context,
	gatewayService TempUnscheduler,
	accountID int64,
	failoverErr *service.UpstreamFailoverError,
	selectedAccount ...*service.Account,
) FailoverAction {
	if s.AnthropicSoft429Retries == nil {
		s.AnthropicSoft429Retries = make(map[int64]int)
	}
	if s.AnthropicSoft429Accounts == nil {
		s.AnthropicSoft429Accounts = make(map[int64]struct{})
	}
	s.AnthropicSoft429Accounts[accountID] = struct{}{}
	retryCount := s.AnthropicSoft429Retries[accountID]
	if retryCount < anthropicSoftRateLimitRetryCount {
		s.AnthropicSoft429Retries[accountID] = retryCount + 1
		s.ForceAccountID = accountID
		logger.FromContext(ctx).Warn("gateway.anthropic_soft_429_same_account_retry",
			zap.Int64("account_id", accountID),
			zap.Int("retry_count", retryCount+1),
			zap.Int("retry_max", anthropicSoftRateLimitRetryCount),
			zap.Duration("retry_delay", anthropicSoftRateLimitRetryDelay),
		)
		if !sleepWithContext(ctx, anthropicSoftRateLimitRetryDelay) {
			return FailoverCanceled
		}
		return FailoverContinue
	}

	// This is the second soft 429 for the account in this request. Commit the
	// adaptive 10/30/60 second cooldown before excluding it from selection.
	if committer, ok := gatewayService.(AnthropicSoftRateLimitCommitter); ok {
		committer.CommitAnthropicSoftRateLimit(ctx, accountID, failoverErr, selectedAccount...)
	} else {
		logger.FromContext(ctx).Warn("gateway.anthropic_soft_429_commit_unavailable",
			zap.Int64("account_id", accountID),
		)
	}
	failoverErr.RetryableOnSameAccount = false
	s.FailedAccountIDs[accountID] = struct{}{}

	if len(s.AnthropicSoft429Accounts) >= anthropicSoftRateLimitMaxAccounts || s.SwitchCount >= s.MaxSwitches {
		logger.FromContext(ctx).Warn("gateway.anthropic_soft_429_cohort_exhausted",
			zap.Int64("account_id", accountID),
			zap.Int("attempted_account_count", len(s.AnthropicSoft429Accounts)),
			zap.Int("max_account_count", anthropicSoftRateLimitMaxAccounts),
			zap.Int("switch_count", s.SwitchCount),
		)
		return FailoverExhausted
	}
	s.SwitchCount++
	logger.FromContext(ctx).Warn("gateway.failover_switch_account",
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("max_switches", s.MaxSwitches),
		zap.String("switch_reason", string(failoverErr.FailureKind)),
		zap.Int64("retry_after_ms", failoverErr.RetryAfter.Milliseconds()),
	)
	return FailoverContinue
}

func (s *FailoverState) recordEarliestKiroRetryAfter(failoverErr *service.UpstreamFailoverError) {
	if s == nil || failoverErr == nil || failoverErr.RetryAfter <= 0 {
		return
	}
	retryAt := time.Now().Add(failoverErr.RetryAfter)
	if s.earliestKiroRetryAt.IsZero() || retryAt.Before(s.earliestKiroRetryAt) {
		s.earliestKiroRetryAt = retryAt
	}
	s.applyEarliestKiroRetryAfter(failoverErr)
}

func (s *FailoverState) applyEarliestKiroRetryAfter(failoverErr *service.UpstreamFailoverError) {
	if s == nil || failoverErr == nil || s.earliestKiroRetryAt.IsZero() {
		return
	}
	remaining := time.Until(s.earliestKiroRetryAt)
	if remaining < time.Second {
		remaining = time.Second
	}
	failoverErr.RetryAfter = remaining
}

func (s *FailoverState) handleLegacyKiro429Failover(ctx context.Context, accountID int64, failoverErr *service.UpstreamFailoverError) FailoverAction {
	if s.Kiro429RetryCount == nil {
		s.Kiro429RetryCount = make(map[int64]int)
	}
	if s.Kiro429SoftExcludedIDs == nil {
		s.Kiro429SoftExcludedIDs = make(map[int64]struct{})
	}
	s.Kiro429RetryCount[accountID]++
	retryCount := s.Kiro429RetryCount[accountID]

	decision := kiro429DecisionRetrySame
	switch {
	case retryCount < kiro429SoftSwitchThreshold:
		s.ForceAccountID = accountID
	case retryCount == kiro429SoftSwitchThreshold:
		decision = kiro429DecisionSoftSwitch
		s.Kiro429SoftExcludedIDs[accountID] = struct{}{}
		s.Kiro429LastSoftExcluded = accountID
		s.FailedAccountIDs[accountID] = struct{}{}
	case retryCount < kiro429HardRetryLimit:
		s.ForceAccountID = accountID
	default:
		decision = kiro429DecisionHardExclude
		delete(s.Kiro429SoftExcludedIDs, accountID)
		if s.Kiro429LastSoftExcluded == accountID {
			s.Kiro429LastSoftExcluded = 0
		}
		s.FailedAccountIDs[accountID] = struct{}{}
	}

	logger.FromContext(ctx).Warn("gateway.kiro_429_retry_decision",
		zap.String("request_id", requestIDFromContext(ctx)),
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("retry_count", retryCount),
		zap.Int("soft_switch_threshold", kiro429SoftSwitchThreshold),
		zap.Int("hard_retry_limit", kiro429HardRetryLimit),
		zap.String("decision", decision),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("failed_account_count", len(s.FailedAccountIDs)),
	)
	return FailoverContinue
}

func (s *FailoverState) recordKiro429Failover(ctx context.Context, accountID int64, failoverErr *service.UpstreamFailoverError) {
	if s.Kiro429RetryCount == nil {
		s.Kiro429RetryCount = make(map[int64]int)
	}
	s.Kiro429RetryCount[accountID]++

	logger.FromContext(ctx).Warn("gateway.kiro_429_retry_decision",
		zap.String("request_id", requestIDFromContext(ctx)),
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", failoverErr.StatusCode),
		zap.Int("retry_count", s.Kiro429RetryCount[accountID]),
		zap.String("decision", kiro429DecisionExclude),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("failed_account_count", len(s.FailedAccountIDs)),
	)
}

// HandleSelectionExhausted 处理选号失败（所有候选账号都在排除列表中）时的退避重试决策。
// 针对 Antigravity 单账号分组的 503 (MODEL_CAPACITY_EXHAUSTED) 场景：
// 清除排除列表、等待退避后重新选号。
//
// 返回 FailoverContinue 时，调用方应设置 SingleAccountRetry context 并 continue。
// 返回 FailoverExhausted 时，调用方应返回错误响应。
// 返回 FailoverCanceled 时，调用方应直接 return。
func (s *FailoverState) HandleSelectionExhausted(ctx context.Context, selectionErrs ...error) FailoverAction {
	s.recordKiroSelectionCooldown(selectionErrs...)
	// Never clear the exclusion set and replay a metadata-only EOF on the same
	// sole credential. That response already proves the request was accepted and
	// classified without producing semantics; repeating the identical payload is
	// deterministic and defeats the two-account request budget above.
	if s.LastFailoverErr != nil && s.LastFailoverErr.Reason == service.GatewayFailureReasonKiroMetadataOnlyEOF {
		return FailoverExhausted
	}
	if s.KiroResilienceEnforced && s.KiroAttempted && s.LastNonRateLimitErr != nil {
		s.LastFailoverErr = s.LastNonRateLimitErr
		if s.lastNonRateLimitWasKiro {
			s.applyEarliestKiroRetryAfter(s.LastFailoverErr)
		}
		return FailoverExhausted
	}
	if s.LastFailoverErr != nil && s.LastFailoverErr.KiroRateLimited {
		if !s.KiroResilienceEnforced {
			if s.resumeLegacyKiro429SoftExcludedAccount(ctx, s.Kiro429LastSoftExcluded) {
				return FailoverContinue
			}
			softExcludedIDs := make([]int64, 0, len(s.Kiro429SoftExcludedIDs))
			for accountID := range s.Kiro429SoftExcludedIDs {
				softExcludedIDs = append(softExcludedIDs, accountID)
			}
			sort.Slice(softExcludedIDs, func(i, j int) bool { return softExcludedIDs[i] < softExcludedIDs[j] })
			for _, accountID := range softExcludedIDs {
				if s.resumeLegacyKiro429SoftExcludedAccount(ctx, accountID) {
					return FailoverContinue
				}
			}
		}
		logger.FromContext(ctx).Warn("gateway.kiro_429_retry_decision",
			zap.String("request_id", requestIDFromContext(ctx)),
			zap.String("decision", kiro429DecisionExhausted),
			zap.Int("switch_count", s.SwitchCount),
			zap.Int("failed_account_count", len(s.FailedAccountIDs)),
		)
		if len(s.LastFailoverErr.ResponseBody) == 0 {
			s.LastFailoverErr.ResponseBody = []byte(`{"error":{"type":"rate_limit_error","message":"` + clientUpstreamTemporarilyRateLimitedMessage + `"}}`)
		}
		s.applyEarliestKiroRetryAfter(s.LastFailoverErr)
		return FailoverExhausted
	}
	if s.KiroAttempted && s.LastFailoverErr != nil && s.LastFailoverErr.FailureKind != "" {
		return FailoverExhausted
	}
	// The one-shot Anthropic retry pins selection to the original account. A
	// concurrent request can legitimately cool that account during the five
	// second wait, in which case the forced selector has no candidate even
	// though another account is available. Drop only that forced retry and
	// continue normal failover instead of surfacing a false no-account error.
	if s.releaseUnavailableAnthropicSoftRetry(ctx) {
		return FailoverContinue
	}

	if s.LastFailoverErr != nil &&
		s.LastFailoverErr.StatusCode == http.StatusServiceUnavailable &&
		s.SwitchCount <= s.MaxSwitches {

		// 排除列表全由利润门否决贡献时，清空后会被原样恢复：退避重试拿不到
		// 任何新候选，而利润否决不推进 SwitchCount，退避条件将永远成立。
		// 这里直接判定耗尽，避免每 2s 空转一轮的活锁。
		if s.allExclusionsAreProfitVetoed() {
			logger.FromContext(ctx).Warn("gateway.failover_selection_exhausted_by_profit_veto",
				zap.Int("profit_veto_count", s.profitVetoCount),
				zap.Int("excluded_accounts", len(s.FailedAccountIDs)),
			)
			return FailoverExhausted
		}

		logger.FromContext(ctx).Warn("gateway.failover_single_account_backoff",
			zap.Duration("backoff_delay", singleAccountBackoffDelay),
			zap.Int("switch_count", s.SwitchCount),
			zap.Int("max_switches", s.MaxSwitches),
		)
		if !sleepWithContext(ctx, singleAccountBackoffDelay) {
			return FailoverCanceled
		}
		logger.FromContext(ctx).Warn("gateway.failover_single_account_retry",
			zap.Int("switch_count", s.SwitchCount),
			zap.Int("max_switches", s.MaxSwitches),
		)
		s.FailedAccountIDs = make(map[int64]struct{})
		// 利润门否决的账号不参与退避重试的解除：判定依据（冻结的下游倍率）在
		// 同一请求内不变，放它们回池只会被再次否决。
		for id := range s.profitVetoedAccountIDs {
			s.FailedAccountIDs[id] = struct{}{}
		}
		return FailoverContinue
	}
	return FailoverExhausted
}

func (s *FailoverState) releaseUnavailableAnthropicSoftRetry(ctx context.Context) bool {
	if s == nil || s.ForceAccountID <= 0 || s.LastFailoverErr == nil ||
		!service.IsAnthropicSoftRateLimitFailover(s.LastFailoverErr) ||
		s.AnthropicSoft429Retries[s.ForceAccountID] < anthropicSoftRateLimitRetryCount {
		return false
	}

	accountID := s.ForceAccountID
	s.ForceAccountID = 0
	if s.FailedAccountIDs == nil {
		s.FailedAccountIDs = make(map[int64]struct{})
	}
	s.FailedAccountIDs[accountID] = struct{}{}
	if s.SwitchCount >= s.MaxSwitches {
		return false
	}
	s.SwitchCount++
	logger.FromContext(ctx).Warn("gateway.anthropic_soft_429_forced_retry_unavailable",
		zap.Int64("account_id", accountID),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("max_switches", s.MaxSwitches),
	)
	return true
}

func (s *FailoverState) recordKiroSelectionCooldown(selectionErrs ...error) {
	if s == nil || !s.KiroResilienceEnforced || len(selectionErrs) == 0 || selectionErrs[0] == nil {
		return
	}
	var cooldownErr *service.KiroCooldownExhaustedError
	if !errors.As(selectionErrs[0], &cooldownErr) || cooldownErr == nil || cooldownErr.RetryAfter <= 0 {
		return
	}
	failureKind := service.UpstreamFailureRateLimited
	if cooldownErr.StatusCode != http.StatusTooManyRequests {
		failureKind = service.UpstreamFailureTransportError
	}
	selectionFailover := &service.UpstreamFailoverError{
		StatusCode:  cooldownErr.StatusCode,
		FailureKind: failureKind,
		RetryAfter:  cooldownErr.RetryAfter,
		Cause:       cooldownErr,
	}
	s.recordEarliestKiroRetryAfter(selectionFailover)
	if failureKind != service.UpstreamFailureRateLimited {
		s.LastNonRateLimitErr = selectionFailover
		s.lastNonRateLimitWasKiro = true
	}
}

func (s *FailoverState) RecordAlternatePlatformFailure(failoverErr *service.UpstreamFailoverError) {
	if s == nil || !s.KiroResilienceEnforced || failoverErr == nil || failoverErr.StatusCode == http.StatusTooManyRequests {
		return
	}
	s.LastNonRateLimitErr = failoverErr
	s.lastNonRateLimitWasKiro = false
}

func (s *FailoverState) resumeLegacyKiro429SoftExcludedAccount(ctx context.Context, accountID int64) bool {
	if accountID <= 0 {
		return false
	}
	if _, ok := s.Kiro429SoftExcludedIDs[accountID]; !ok {
		return false
	}
	if s.Kiro429RetryCount[accountID] >= kiro429HardRetryLimit {
		return false
	}
	delete(s.FailedAccountIDs, accountID)
	delete(s.Kiro429SoftExcludedIDs, accountID)
	if s.Kiro429LastSoftExcluded == accountID {
		s.Kiro429LastSoftExcluded = 0
	}
	s.ForceAccountID = accountID
	logger.FromContext(ctx).Warn("gateway.kiro_429_retry_decision",
		zap.String("request_id", requestIDFromContext(ctx)),
		zap.Int64("account_id", accountID),
		zap.Int("upstream_status", s.LastFailoverErr.StatusCode),
		zap.Int("retry_count", s.Kiro429RetryCount[accountID]),
		zap.Int("soft_switch_threshold", kiro429SoftSwitchThreshold),
		zap.Int("hard_retry_limit", kiro429HardRetryLimit),
		zap.String("decision", kiro429DecisionResumeCurrent),
		zap.Int("switch_count", s.SwitchCount),
		zap.Int("failed_account_count", len(s.FailedAccountIDs)),
	)
	return true
}

func (s *FailoverState) SelectionContext(ctx context.Context, groupID *int64, bridgeOldKeys bool) context.Context {
	if s == nil || s.ForceAccountID <= 0 {
		return ctx
	}
	return service.WithForcedAccountID(ctx, s.ForceAccountID, bridgeOldKeys)
}

func (s *FailoverState) HasKiro429Retries() bool {
	if s == nil {
		return false
	}
	for _, count := range s.Kiro429RetryCount {
		if count > 0 {
			return true
		}
	}
	return false
}

func (s *FailoverState) HasFailedAccountID(accountID int64) bool {
	if s == nil || accountID <= 0 {
		return false
	}
	_, failed := s.FailedAccountIDs[accountID]
	return failed
}

// needForceCacheBilling 判断 failover 时是否需要强制缓存计费。
// 粘性会话切换账号、或上游明确标记时，将 input_tokens 转为 cache_read 计费。
func needForceCacheBilling(hasBoundSession bool, failoverErr *service.UpstreamFailoverError) bool {
	return hasBoundSession || (failoverErr != nil && failoverErr.ForceCacheBilling)
}

func isKiro429Failover(platform string, failoverErr *service.UpstreamFailoverError) bool {
	return platform == service.PlatformKiro &&
		failoverErr != nil &&
		failoverErr.KiroRateLimited &&
		failoverErr.StatusCode == http.StatusTooManyRequests
}

func requestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if requestID, _ := ctx.Value(ctxkey.RequestID).(string); strings.TrimSpace(requestID) != "" {
		return strings.TrimSpace(requestID)
	}
	if requestID, _ := ctx.Value(ctxkey.ClientRequestID).(string); strings.TrimSpace(requestID) != "" {
		return strings.TrimSpace(requestID)
	}
	return ""
}

// failoverClientGone stops account rotation once the downstream request is canceled.
func failoverClientGone(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.Context().Err() == nil {
		return false
	}
	if service.StopOpenAICompactSSEKeepaliveCommitted(c) {
		return true
	}
	if !c.Writer.Written() {
		c.Status(statusClientClosedRequest)
	}
	return true
}

// sleepWithContext 等待指定时长，返回 false 表示 context 已取消。
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
