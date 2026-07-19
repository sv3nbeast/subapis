package service

import (
	"math"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const MaxSubscriptionModelQuotaRules = 50

type SubscriptionModelUsage = domain.SubscriptionModelUsage

func NormalizeSubscriptionModelQuotaRatios(subscriptionType string, ratios map[string]float64) (map[string]float64, error) {
	if subscriptionType != SubscriptionTypeSubscription || len(ratios) == 0 {
		return map[string]float64{}, nil
	}
	if len(ratios) > MaxSubscriptionModelQuotaRules {
		return nil, infraerrors.BadRequest("MODEL_QUOTA_RULES_LIMIT", "model_quota_ratios supports at most 50 models")
	}

	normalized := make(map[string]float64, len(ratios))
	for rawModel, ratio := range ratios {
		model := NormalizeSubscriptionQuotaModel(rawModel)
		if model == "" || len(model) > 128 || !isSafeSubscriptionQuotaModel(model) {
			return nil, infraerrors.BadRequest("MODEL_QUOTA_MODEL_INVALID", "model_quota_ratios contains an invalid model ID")
		}
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 || ratio > 1 {
			return nil, infraerrors.BadRequest("MODEL_QUOTA_RATIO_INVALID", "model_quota_ratios values must be greater than 0 and no greater than 1")
		}
		if _, exists := normalized[model]; exists {
			return nil, infraerrors.BadRequest("MODEL_QUOTA_MODEL_DUPLICATE", "model_quota_ratios contains duplicate normalized model IDs")
		}
		normalized[model] = ratio
	}
	return normalized, nil
}

func ValidateSubscriptionModelQuotaBase(ratios map[string]float64, limits ...*float64) error {
	if len(ratios) == 0 {
		return nil
	}
	for _, limit := range limits {
		if limit != nil && *limit > 0 {
			return nil
		}
	}
	return infraerrors.BadRequest("MODEL_QUOTA_BASE_REQUIRED", "model_quota_ratios requires at least one positive subscription quota limit")
}

func MatchSubscriptionModelQuota(ratios map[string]float64, requestedModel string) (model string, ratio float64, ok bool) {
	requested := NormalizeSubscriptionQuotaModel(requestedModel)
	if requested == "" || len(ratios) == 0 {
		return "", 0, false
	}
	if ratio, ok := ratios[requested]; ok {
		return requested, ratio, true
	}

	keys := make([]string, 0, len(ratios))
	for key := range ratios {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, key := range keys {
		if subscriptionQuotaModelFamilyMatch(requested, key) {
			return key, ratios[key], true
		}
	}
	return "", 0, false
}

func NormalizeSubscriptionQuotaModel(model string) string {
	model = normalizeAntigravityModelName(model)
	model = strings.ReplaceAll(model, "_", "-")
	model = strings.TrimPrefix(model, "anthropic.")
	return strings.TrimSpace(model)
}

func subscriptionQuotaModelFamilyMatch(requested, configured string) bool {
	if !strings.HasPrefix(requested, configured) || len(requested) == len(configured) {
		return false
	}
	switch requested[len(configured)] {
	case '-', '[', ':':
		return true
	default:
		return false
	}
}

func isSafeSubscriptionQuotaModel(model string) bool {
	for _, r := range model {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-', r == '.', r == ':', r == '/', r == '[', r == ']':
		default:
			return false
		}
	}
	return true
}
