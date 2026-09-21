package handler

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type selectionErrorClassification struct {
	Handled         bool
	StatusCode      int
	ErrorType       string
	Message         string
	SkipMonitoring  bool
	BusinessLimited bool
	RetryAfter      time.Duration
}

var selectionFailureSummaryPattern = regexp.MustCompile(
	`total=(\d+)\s+eligible=(\d+)\s+excluded=(\d+)\s+unschedulable=(\d+)\s+platform_filtered=(\d+)\s+model_unsupported=(\d+)\s+model_rate_limited=(\d+)\s+model_capacity_cooling=(\d+)`,
)

func classifySelectionError(err error) selectionErrorClassification {
	if err == nil {
		return selectionErrorClassification{}
	}
	var cooldownErr *service.KiroCooldownExhaustedError
	if errors.As(err, &cooldownErr) {
		errorType := "upstream_error"
		message := clientUpstreamTemporarilyUnavailableMessage
		if cooldownErr.StatusCode == 429 {
			errorType = "rate_limit_error"
			message = clientUpstreamTemporarilyRateLimitedMessage
		}
		return selectionErrorClassification{
			Handled:    true,
			StatusCode: cooldownErr.StatusCode,
			ErrorType:  errorType,
			Message:    message,
			RetryAfter: cooldownErr.RetryAfter,
		}
	}

	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)

	if strings.Contains(lower, "channel pricing restriction") {
		return selectionErrorClassification{
			Handled:         true,
			StatusCode:      400,
			ErrorType:       "invalid_request_error",
			Message:         "Requested model is not supported by this API key/group",
			BusinessLimited: true,
		}
	}

	// 限流优先于"模型不支持"：只要有支持该模型的账号处于临时限流（含被调度快照
	// 排除的账号级冷却），就必须返回可重试的 429，而不是永久性的 400。
	if strings.Contains(lower, "supporting model:") && isPureRateLimitedSelectionSummary(lower) {
		return selectionErrorClassification{
			Handled:        true,
			StatusCode:     429,
			ErrorType:      "rate_limit_error",
			Message:        "Requested model is temporarily rate limited upstream, please retry later",
			SkipMonitoring: false,
		}
	}

	if strings.Contains(lower, "supporting model:") && isPureUnsupportedSelectionSummary(lower) {
		return selectionErrorClassification{
			Handled:        true,
			StatusCode:     400,
			ErrorType:      "invalid_request_error",
			Message:        "Requested model is not supported by this API key/group",
			SkipMonitoring: true,
		}
	}

	if strings.Contains(lower, "supporting model:") && isPureModelCapacityCoolingSelectionSummary(lower) {
		return selectionErrorClassification{
			Handled:        true,
			StatusCode:     503,
			ErrorType:      "upstream_error",
			Message:        "Requested model is temporarily unavailable upstream, please retry later",
			SkipMonitoring: false,
		}
	}

	return selectionErrorClassification{}
}

func applySelectionErrorMonitoringClassification(c *gin.Context, cls selectionErrorClassification) {
	if c == nil {
		return
	}
	if cls.RetryAfter > 0 {
		seconds := int((cls.RetryAfter + time.Second - 1) / time.Second)
		c.Header("Retry-After", strconv.Itoa(max(seconds, 1)))
	}
	if cls.SkipMonitoring {
		c.Set(service.OpsSkipPassthroughKey, true)
	}
	if cls.BusinessLimited {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
	}
}

func isPureUnsupportedSelectionSummary(msg string) bool {
	stats, ok := parseSelectionFailureSummary(msg)
	if !ok {
		return false
	}

	return stats.eligible == 0 &&
		stats.modelUnsupported > 0 &&
		stats.modelRateLimited == 0 &&
		stats.modelCapacityCooling == 0
}

// isPureRateLimitedSelectionSummary 判断本次选号失败是否应归类为临时限流。
// 允许 model_unsupported > 0：列表里存在不支持该模型的账号不影响结论——
// 只要还有支持该模型的账号在限流冷却（modelRateLimited 含被快照排除的
// 账号级冷却计数），恢复后请求就能成功，属于可重试场景。
func isPureRateLimitedSelectionSummary(msg string) bool {
	stats, ok := parseSelectionFailureSummary(msg)
	if !ok {
		return false
	}

	return stats.eligible == 0 &&
		stats.modelRateLimited > 0 &&
		stats.modelCapacityCooling == 0
}

func isPureModelCapacityCoolingSelectionSummary(msg string) bool {
	stats, ok := parseSelectionFailureSummary(msg)
	if !ok {
		return false
	}

	return stats.eligible == 0 &&
		stats.modelUnsupported == 0 &&
		stats.modelRateLimited == 0 &&
		stats.modelCapacityCooling > 0
}

type selectionFailureSummaryStats struct {
	eligible             int
	modelUnsupported     int
	modelRateLimited     int
	modelCapacityCooling int
}

func parseSelectionFailureSummary(msg string) (selectionFailureSummaryStats, bool) {
	matches := selectionFailureSummaryPattern.FindStringSubmatch(msg)
	if len(matches) != 9 {
		return selectionFailureSummaryStats{}, false
	}

	parse := func(idx int) int {
		v, _ := strconv.Atoi(matches[idx])
		return v
	}

	return selectionFailureSummaryStats{
		eligible:             parse(2),
		modelUnsupported:     parse(6),
		modelRateLimited:     parse(7),
		modelCapacityCooling: parse(8),
	}, true
}
