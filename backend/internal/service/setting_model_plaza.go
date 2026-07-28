package service

import "context"

// ModelPlazaRuntime is the lightweight view consumed by the public plaza handler.
type ModelPlazaRuntime struct {
	Enabled     bool
	RequireAuth bool
	Description string
}

func (s *SettingService) GetModelPlazaRuntime(ctx context.Context) ModelPlazaRuntime {
	vals, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyModelPlazaEnabled,
		SettingKeyModelPlazaRequireAuth,
		SettingKeyModelPlazaDescription,
	})
	if err != nil {
		return ModelPlazaRuntime{Enabled: false}
	}
	return ModelPlazaRuntime{
		Enabled:     vals[SettingKeyModelPlazaEnabled] == "true",
		RequireAuth: vals[SettingKeyModelPlazaRequireAuth] == "true",
		Description: vals[SettingKeyModelPlazaDescription],
	}
}
