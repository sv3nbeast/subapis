package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// PasskeyEnabled reports the effective runtime switch. Deployment-level
// WebAuthn configuration remains the security boundary.
func (s *SettingService) PasskeyEnabled(ctx context.Context) (bool, error) {
	if !s.passkeyConfigured() {
		return false, nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyPasskeyEnabled)
	if errors.Is(err, ErrSettingNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read passkey setting: %w", err)
	}
	return value == "true", nil
}

// PasskeyConfiguration returns non-secret relying-party configuration for the
// admin status UI.
func (s *SettingService) PasskeyConfiguration() (configured bool, rpID string, origins []string) {
	if s == nil || s.cfg == nil {
		return false, "", []string{}
	}
	origins = append([]string{}, s.cfg.WebAuthn.RPOrigins...)
	return s.cfg.WebAuthn.Enabled, strings.TrimSpace(s.cfg.WebAuthn.RPID), origins
}

func (s *SettingService) passkeyConfigured() bool {
	return s != nil && s.cfg != nil && s.cfg.WebAuthn.Enabled
}

func (s *SettingService) passkeySettingEnabled(settings map[string]string) bool {
	if !s.passkeyConfigured() {
		return false
	}
	value, ok := settings[SettingKeyPasskeyEnabled]
	if !ok {
		return true
	}
	return value == "true"
}
