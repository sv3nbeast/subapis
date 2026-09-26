package service

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// anthropicClaudeFamilyVersionRe captures family, major and an optional one or
// two digit minor. A dated snapshot such as claude-opus-4-20250514 therefore
// parses as Opus 4.0 rather than taking the date for the minor version. Dotted
// Kiro upstream IDs (claude-opus-4.8, claude-opus-5.5) parse the same way.
var anthropicClaudeFamilyVersionRe = regexp.MustCompile(`^claude-(opus|sonnet|fable)-(\d+)(?:[-.](\d{1,2}))?(?:$|[-.\[])`)

// isAnthropicAdaptiveOnlyThinkingModel reports whether the model takes thinking
// only as {type: "adaptive"} with output_config.effort: Opus 4.7 and newer,
// Sonnet 5 and newer, and every Fable. Opus 5.5 rejects {type: "enabled"} with
// a 400 (verified 2026-09-26); the documentation lists the same for Opus
// 4.7/4.8, Sonnet 5 and Fable 5.x, and all of them accept adaptive.
func isAnthropicAdaptiveOnlyThinkingModel(model string) bool {
	matches := anthropicClaudeFamilyVersionRe.FindStringSubmatch(strings.ToLower(strings.TrimSpace(model)))
	if matches == nil {
		return false
	}
	major, _ := strconv.Atoi(matches[2])
	minor := 0
	if matches[3] != "" {
		minor, _ = strconv.Atoi(matches[3])
	}
	switch matches[1] {
	case "fable":
		return true
	case "sonnet":
		return major >= 5
	case "opus":
		return major > 4 || (major == 4 && minor >= 7)
	default:
		return false
	}
}

// normalizeAnthropicAdaptiveOnlyThinkingRequest fixes the thinking block that
// the OpenAI-compatible conversion builds from reasoning.effort. That block is
// {type: "enabled", budget_tokens: N}, which adaptive-only models reject, and
// on Kiro the budget is re-derived into a lower effort (high arrived as
// medium). The requested effort already travels in output_config.effort, so
// only the thinking type changes.
//
// display is set to "summarized" because these models default to "omitted":
// without it the converted reasoning_content / reasoning summary is empty.
func normalizeAnthropicAdaptiveOnlyThinkingRequest(req *apicompat.AnthropicRequest) {
	if req == nil || req.Thinking == nil || !isAnthropicAdaptiveOnlyThinkingModel(req.Model) {
		return
	}
	switch strings.ToLower(strings.TrimSpace(req.Thinking.Type)) {
	case "enabled":
		req.Thinking.Type = "adaptive"
		req.Thinking.BudgetTokens = 0
	case "adaptive":
		req.Thinking.BudgetTokens = 0
	default:
		return
	}
	if strings.TrimSpace(req.Thinking.Display) == "" {
		req.Thinking.Display = "summarized"
	}
}
