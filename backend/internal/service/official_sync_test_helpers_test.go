//go:build unit

package service

import "context"

func newStubPricingServiceFromMap(data map[string]*LiteLLMModelPricing) *PricingService {
	return &PricingService{pricingData: data}
}

type forwardedIPMigrationRepoStub struct {
	SettingRepository
	values         map[string]string
	updates        map[string]string
	getMultipleErr error
	setMultipleErr error
}

func (s *forwardedIPMigrationRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *forwardedIPMigrationRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if s.getMultipleErr != nil {
		return nil, s.getMultipleErr
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (s *forwardedIPMigrationRepoStub) SetMultiple(_ context.Context, values map[string]string) error {
	if s.setMultipleErr != nil {
		return s.setMultipleErr
	}
	s.updates = make(map[string]string, len(values))
	for key, value := range values {
		s.values[key] = value
		s.updates[key] = value
	}
	return nil
}
