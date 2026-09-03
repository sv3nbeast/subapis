package service

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

type ErrInvalidOpenAIServiceTier struct {
	Value string
}

func (e *ErrInvalidOpenAIServiceTier) Error() string {
	return fmt.Sprintf("invalid service_tier %q: must be one of auto, default, fast, flex, priority, scale", e.Value)
}

const invalidOpenAIServiceTierValueMaxLen = 64

func boundInvalidOpenAIServiceTierValue(raw string) string {
	if len(raw) <= invalidOpenAIServiceTierValueMaxLen {
		return raw
	}
	return raw[:invalidOpenAIServiceTierValueMaxLen] + "..."
}

func ValidateOpenAIServiceTierField(body []byte) (string, error) {
	tierResult := gjson.GetBytes(body, "service_tier")
	if !tierResult.Exists() || tierResult.Type == gjson.Null {
		return "", nil
	}
	if tierResult.Type != gjson.String {
		return "", &ErrInvalidOpenAIServiceTier{Value: "<non-string>"}
	}
	raw := strings.TrimSpace(tierResult.String())
	if raw == "" {
		return "", &ErrInvalidOpenAIServiceTier{Value: raw}
	}
	norm := normalizedOpenAIServiceTierValue(raw)
	if norm == "" {
		return "", &ErrInvalidOpenAIServiceTier{Value: boundInvalidOpenAIServiceTierValue(raw)}
	}
	return norm, nil
}
