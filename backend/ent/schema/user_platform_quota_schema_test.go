package schema

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserPlatformQuotaPlatformValidatorAllowsEveryGatewayPlatform(t *testing.T) {
	var validators []func(string) error
	for _, entField := range (UserPlatformQuota{}).Fields() {
		descriptor := entField.Descriptor()
		if descriptor.Name != "platform" {
			continue
		}
		for _, rawValidator := range descriptor.Validators {
			if validator, ok := rawValidator.(func(string) error); ok {
				validators = append(validators, validator)
			}
		}
	}
	require.NotEmpty(t, validators)
	for _, platform := range []string{
		"anthropic", "openai", "gemini", "antigravity", "kiro", "droid", "grok",
		"kimi", "zhipu", "deepseek",
	} {
		t.Run(platform, func(t *testing.T) {
			for _, validator := range validators {
				require.NoError(t, validator(platform))
			}
		})
	}
	rejected := false
	for _, validator := range validators {
		if validator("glm") != nil {
			rejected = true
			break
		}
	}
	require.True(t, rejected, "platform alias glm must not pass the zhipu platform validator")
}
