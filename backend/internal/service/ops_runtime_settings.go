package service

import (
	"context"
	"encoding/json"
	"log"
	"math/rand/v2"
	"strings"
	"time"
)

const (
	opsRuntimeSettingsRefreshInterval = 30 * time.Second
	opsRuntimeSettingsRefreshJitter   = 20
	opsRuntimeSettingsRefreshTimeout  = 3 * time.Second
	opsRuntimeSettingsFailureLogEvery = time.Minute
)

type opsRuntimeSettingsSnapshot struct {
	monitoringEnabled bool
	advanced          OpsAdvancedSettings
}

type OpsRuntimeSettingsRefreshHealth struct {
	Running      bool   `json:"running"`
	SuccessTotal uint64 `json:"success_total"`
	FailureTotal uint64 `json:"failure_total"`
}

func parseOpsMonitoringEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "disabled":
		return false
	default:
		return true
	}
}

func (s *OpsService) initRuntimeSettings(ctx context.Context) {
	if s == nil {
		return
	}
	defaults := defaultOpsAdvancedSettings()
	s.runtimeSettings.Store(&opsRuntimeSettingsSnapshot{monitoringEnabled: true, advanced: *defaults})
	_ = s.RefreshRuntimeSettings(ctx)
}

func (s *OpsService) RefreshRuntimeSettings(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.runtimeSettingsMu.Lock()
	defer s.runtimeSettingsMu.Unlock()
	values, err := getOpsRuntimeSettingsValues(ctx, s.settingRepo)
	if err != nil {
		return err
	}
	monitoringEnabled := true
	if raw, ok := values[SettingKeyOpsMonitoringEnabled]; ok {
		monitoringEnabled = parseOpsMonitoringEnabled(raw)
	}
	advanced := defaultOpsAdvancedSettings()
	if raw, ok := values[SettingKeyOpsAdvancedSettings]; ok {
		if err := json.Unmarshal([]byte(raw), advanced); err != nil {
			advanced = defaultOpsAdvancedSettings()
		}
	}
	normalizeOpsAdvancedSettings(advanced)
	s.runtimeSettings.Store(&opsRuntimeSettingsSnapshot{monitoringEnabled: monitoringEnabled, advanced: *advanced})
	return nil
}

// getOpsRuntimeSettingsValues keeps startup resilient to narrow test/dynamic
// repositories that only implement GetValue while still using the production
// GetMultiple path whenever it is available.
func getOpsRuntimeSettingsValues(ctx context.Context, repo SettingRepository) (values map[string]string, err error) {
	keys := []string{SettingKeyOpsMonitoringEnabled, SettingKeyOpsAdvancedSettings}
	defer func() {
		if recover() == nil {
			return
		}
		values = make(map[string]string, len(keys))
		for _, key := range keys {
			value, getErr := repo.GetValue(ctx, key)
			if getErr == nil {
				values[key] = value
			}
		}
		err = nil
	}()
	return repo.GetMultiple(ctx, keys)
}

func (s *OpsService) StartRuntimeSettingsRefresh(ctx context.Context) {
	s.startRuntimeSettingsRefresh(ctx, opsRuntimeSettingsRefreshInterval, opsRuntimeSettingsRefreshJitter, opsRuntimeSettingsRefreshTimeout)
}

func (s *OpsService) startRuntimeSettingsRefresh(ctx context.Context, interval time.Duration, jitterPercent int, timeout time.Duration) {
	if s == nil || s.settingRepo == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = opsRuntimeSettingsRefreshInterval
	}
	if timeout <= 0 {
		timeout = opsRuntimeSettingsRefreshTimeout
	}
	if jitterPercent < 0 {
		jitterPercent = 0
	} else if jitterPercent > 100 {
		jitterPercent = 100
	}
	s.runtimeRefreshMu.Lock()
	if s.runtimeRefreshCancel != nil {
		s.runtimeRefreshMu.Unlock()
		return
	}
	refreshCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.runtimeRefreshCancel = cancel
	s.runtimeRefreshDone = done
	s.runtimeRefreshRunning.Store(true)
	s.runtimeRefreshMu.Unlock()

	go func() {
		defer close(done)
		defer s.runtimeRefreshRunning.Store(false)
		for {
			timer := time.NewTimer(jitterDuration(interval, jitterPercent))
			select {
			case <-refreshCtx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}
			attemptCtx, attemptCancel := context.WithTimeout(refreshCtx, timeout)
			err := s.RefreshRuntimeSettings(attemptCtx)
			attemptCancel()
			if err != nil {
				s.runtimeRefreshFailure.Add(1)
				s.logRuntimeSettingsRefreshFailure(err)
				continue
			}
			s.runtimeRefreshSuccess.Add(1)
		}
	}()
}

func jitterDuration(base time.Duration, percent int) time.Duration {
	if base <= 0 || percent <= 0 {
		return base
	}
	delta := float64(percent) / 100
	factor := 1 - delta + rand.Float64()*(2*delta)
	if factor <= 0 {
		return base
	}
	return time.Duration(float64(base) * factor)
}

func (s *OpsService) logRuntimeSettingsRefreshFailure(err error) {
	if s == nil || err == nil {
		return
	}
	now := time.Now().Unix()
	for {
		last := s.runtimeRefreshLastFailureLog.Load()
		if last != 0 && now-last < int64(opsRuntimeSettingsFailureLogEvery/time.Second) {
			return
		}
		if s.runtimeRefreshLastFailureLog.CompareAndSwap(last, now) {
			log.Printf("[Ops] runtime settings refresh failed: %v", err)
			return
		}
	}
}

func (s *OpsService) StopRuntimeSettingsRefresh() {
	if s == nil {
		return
	}
	s.runtimeRefreshMu.Lock()
	cancel, done := s.runtimeRefreshCancel, s.runtimeRefreshDone
	s.runtimeRefreshMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	s.runtimeRefreshMu.Lock()
	if s.runtimeRefreshDone == done {
		s.runtimeRefreshCancel = nil
		s.runtimeRefreshDone = nil
	}
	s.runtimeRefreshMu.Unlock()
}

func (s *OpsService) RuntimeSettingsRefreshHealth() OpsRuntimeSettingsRefreshHealth {
	if s == nil {
		return OpsRuntimeSettingsRefreshHealth{}
	}
	return OpsRuntimeSettingsRefreshHealth{Running: s.runtimeRefreshRunning.Load(), SuccessTotal: s.runtimeRefreshSuccess.Load(), FailureTotal: s.runtimeRefreshFailure.Load()}
}

func (s *OpsService) SetMonitoringEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.runtimeSettingsMu.Lock()
	defer s.runtimeSettingsMu.Unlock()
	current := s.runtimeSettings.Load()
	next := &opsRuntimeSettingsSnapshot{monitoringEnabled: enabled, advanced: *defaultOpsAdvancedSettings()}
	if current != nil {
		next.advanced = current.advanced
	}
	s.runtimeSettings.Store(next)
}

func (s *OpsService) storeAdvancedSettingsSnapshot(cfg *OpsAdvancedSettings) {
	if s == nil || cfg == nil {
		return
	}
	s.runtimeSettingsMu.Lock()
	defer s.runtimeSettingsMu.Unlock()
	current := s.runtimeSettings.Load()
	next := &opsRuntimeSettingsSnapshot{monitoringEnabled: true, advanced: *cfg}
	if current != nil {
		next.monitoringEnabled = current.monitoringEnabled
	}
	s.runtimeSettings.Store(next)
}
