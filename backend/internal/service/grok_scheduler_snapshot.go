package service

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"time"
)

func buildGrokSchedulerExtraUpdates(snapshot *xai.QuotaSnapshot) map[string]any {
	if snapshot == nil {
		return nil
	}
	util, reset, ok := grokSnapshotUtilization(snapshot)
	if !ok {
		return nil
	}
	updates := map[string]any{
		"grok_sched_utilization":      util,
		"grok_sched_usage_updated_at": time.Now().UTC().Format(time.RFC3339),
		// Do not combine fresh utilization with an older window's reset.
		"grok_sched_reset_at": nil,
	}
	if reset != nil {
		// 防御：调度阈值暂停时长由 grok_sched_reset_at 决定。若上游返回脏的
		// reset 头（例如把相对毫秒 "6000" 误当相对秒解析出 ~33h 的未来时刻），
		// 不设上限会把耗尽账号长时间锁死。xAI 配额窗口不会超过一天，因此对
		// 未来时刻做 grokMaxSchedulingResetHorizon 钳制；过去/无效值直接不写。
		now := time.Now()
		if reset.After(now) {
			capped := *reset
			if horizon := now.Add(grokMaxSchedulingResetHorizon); capped.After(horizon) {
				capped = horizon
			}
			updates["grok_sched_reset_at"] = capped.UTC().Format(time.RFC3339)
		}
	}
	return updates
}

// grokSnapshotUtilization returns the highest window utilization (0-100) across
// the requests/tokens quota windows and the reset time of that window.
func grokSnapshotUtilization(snapshot *xai.QuotaSnapshot) (float64, *time.Time, bool) {
	if snapshot == nil {
		return 0, nil, false
	}
	best := -1.0
	var bestReset *time.Time
	consider := func(window *xai.QuotaWindow) {
		if window == nil || window.Limit == nil || *window.Limit <= 0 || window.Remaining == nil {
			return
		}
		remaining := *window.Remaining
		if remaining < 0 {
			remaining = 0
		}
		util := (1 - float64(remaining)/float64(*window.Limit)) * 100
		if util < 0 {
			util = 0
		}
		if util > 100 {
			util = 100
		}
		if util > best {
			best = util
			if window.ResetUnix != nil {
				t := time.Unix(*window.ResetUnix, 0).UTC()
				bestReset = &t
			} else {
				bestReset = nil
			}
		}
	}
	consider(snapshot.Requests)
	consider(snapshot.Tokens)
	if best < 0 {
		return 0, nil, false
	}
	return best, bestReset, true
}

// grokMaxSchedulingResetHorizon bounds how far into the future a Grok
// scheduling-threshold pause (grok_sched_reset_at) may be set, so a malformed
// upstream reset header can't park an over-threshold account for days. xAI quota
// windows do not exceed ~a day.
const grokMaxSchedulingResetHorizon = 25 * time.Hour
