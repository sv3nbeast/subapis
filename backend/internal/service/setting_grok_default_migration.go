package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// MigrateGrokDefaultTextModel upgrades only the previously shipped built-in
// default. Explicit operator choices remain untouched.
func (s *SettingService) MigrateGrokDefaultTextModel(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), codexRestrictionPolicyDBTimeout)
	defer cancel()

	value, err := s.settingRepo.GetValue(dbCtx, SettingKeyGrokDefaultTextModel)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil
		}
		return fmt.Errorf("get %s setting: %w", SettingKeyGrokDefaultTextModel, err)
	}
	if strings.TrimSpace(value) != "grok-4.5" {
		return nil
	}
	if err := s.settingRepo.Set(dbCtx, SettingKeyGrokDefaultTextModel, "grok-4.6"); err != nil {
		return fmt.Errorf("set %s setting: %w", SettingKeyGrokDefaultTextModel, err)
	}
	return nil
}
