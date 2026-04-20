package handler

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type selectionErrorClassification struct {
	Handled        bool
	StatusCode     int
	ErrorType      string
	Message        string
	SkipMonitoring bool
}

var selectionFailureSummaryPattern = regexp.MustCompile(
	`total=(\d+)\s+eligible=(\d+)\s+excluded=(\d+)\s+unschedulable=(\d+)\s+platform_filtered=(\d+)\s+model_unsupported=(\d+)\s+model_rate_limited=(\d+)\s+model_capacity_cooling=(\d+)`,
)

func classifySelectionError(err error) selectionErrorClassification {
	if err == nil {
		return selectionErrorClassification{}
	}

	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)

	if strings.Contains(lower, "channel pricing restriction") {
		return selectionErrorClassification{
			Handled:        true,
			StatusCode:     400,
			ErrorType:      "invalid_request_error",
			Message:        "Requested model is not supported by this API key/group",
			SkipMonitoring: true,
		}
	}

	if strings.Contains(lower, "no available openai accounts supporting model:") ||
		strings.Contains(lower, "no available gemini accounts supporting model:") {
		return selectionErrorClassification{
			Handled:        true,
			StatusCode:     400,
			ErrorType:      "invalid_request_error",
			Message:        "Requested model is not supported by this API key/group",
			SkipMonitoring: true,
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

	if strings.Contains(lower, "supporting model:") && isPureRateLimitedSelectionSummary(lower) {
		return selectionErrorClassification{
			Handled:        true,
			StatusCode:     429,
			ErrorType:      "rate_limit_error",
			Message:        "Requested model is temporarily rate limited upstream, please retry later",
			SkipMonitoring: false,
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
	if c == nil || !cls.SkipMonitoring {
		return
	}
	c.Set(service.OpsSkipPassthroughKey, true)
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

func isPureRateLimitedSelectionSummary(msg string) bool {
	stats, ok := parseSelectionFailureSummary(msg)
	if !ok {
		return false
	}

	return stats.eligible == 0 &&
		stats.modelUnsupported == 0 &&
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
