package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

const SettingKeyAccountSchedulingThresholds = "account_scheduling_thresholds"

const (
	accountSchedulingThresholdsCacheTTL  = 60 * time.Second
	accountSchedulingThresholdsErrorTTL  = 5 * time.Second
	accountSchedulingThresholdsDBTimeout = 5 * time.Second
)

type cachedAccountSchedulingThresholds struct {
	thresholds map[string]int
	expiresAt  int64
}

var accountSchedulingThresholdsCache atomic.Value
var accountSchedulingThresholdsSF singleflight.Group

func defaultAccountSchedulingThresholds() map[string]int {
	out := make(map[string]int, len(AllowedSchedulingThresholdPlatforms))
	for _, platform := range AllowedSchedulingThresholdPlatforms {
		out[platform] = 100
	}
	return out
}

func cloneAccountSchedulingThresholds(input map[string]int) map[string]int {
	out := defaultAccountSchedulingThresholds()
	for key, value := range input {
		out[key] = value
	}
	return out
}

func parseAccountSchedulingThresholdsSetting(raw string) (map[string]int, error) {
	out := defaultAccountSchedulingThresholds()
	if strings.TrimSpace(raw) == "" {
		return out, nil
	}
	var parsed map[string]int
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return out, err
	}
	for _, platform := range AllowedSchedulingThresholdPlatforms {
		if value, ok := parsed[platform]; ok && value >= 1 && value <= 100 {
			out[platform] = value
		}
	}
	return out, nil
}

func (s *SettingService) GetAccountSchedulingThresholds(ctx context.Context) map[string]int {
	if s == nil || s.settingRepo == nil {
		return defaultAccountSchedulingThresholds()
	}
	if cached, ok := accountSchedulingThresholdsCache.Load().(*cachedAccountSchedulingThresholds); ok && cached != nil && len(cached.thresholds) > 0 && time.Now().UnixNano() < cached.expiresAt {
		return cloneAccountSchedulingThresholds(cached.thresholds)
	}
	value, _, _ := accountSchedulingThresholdsSF.Do(SettingKeyAccountSchedulingThresholds, func() (any, error) {
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountSchedulingThresholdsDBTimeout)
		defer cancel()
		raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyAccountSchedulingThresholds)
		thresholds := defaultAccountSchedulingThresholds()
		ttl := accountSchedulingThresholdsCacheTTL
		if err == nil {
			if parsed, parseErr := parseAccountSchedulingThresholdsSetting(raw); parseErr == nil {
				thresholds = parsed
			}
		} else {
			ttl = accountSchedulingThresholdsErrorTTL
		}
		accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{thresholds: cloneAccountSchedulingThresholds(thresholds), expiresAt: time.Now().Add(ttl).UnixNano()})
		return thresholds, nil
	})
	if thresholds, ok := value.(map[string]int); ok {
		return cloneAccountSchedulingThresholds(thresholds)
	}
	return defaultAccountSchedulingThresholds()
}
