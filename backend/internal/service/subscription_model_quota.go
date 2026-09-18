package service

import (
	"math"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const MaxSubscriptionModelQuotaRules = 50
const MaxSubscriptionModelQuotaGroupModels = 50

type SubscriptionModelUsage = domain.SubscriptionModelUsage

type SubscriptionModelQuotaMatch struct {
	UsageKey string
	Label    string
	Ratio    float64
}

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

func NormalizeSubscriptionModelQuotaGroups(subscriptionType string, groups []SubscriptionModelQuotaGroup, ratios map[string]float64) ([]SubscriptionModelQuotaGroup, error) {
	if subscriptionType != SubscriptionTypeSubscription || len(groups) == 0 {
		return []SubscriptionModelQuotaGroup{}, nil
	}
	if len(groups) > MaxSubscriptionModelQuotaRules {
		return nil, infraerrors.BadRequest("MODEL_QUOTA_GROUPS_LIMIT", "model_quota_groups supports at most 50 groups")
	}

	normalized := make([]SubscriptionModelQuotaGroup, 0, len(groups))
	seenIDs := make(map[string]struct{}, len(groups))
	seenModels := make(map[string]struct{})
	for _, rawGroup := range groups {
		id := strings.TrimSpace(strings.ToLower(rawGroup.ID))
		name := strings.TrimSpace(rawGroup.Name)
		if id == "" || len(id) > 64 || !isSafeSubscriptionQuotaGroupID(id) {
			return nil, infraerrors.BadRequest("MODEL_QUOTA_GROUP_ID_INVALID", "model_quota_groups contains an invalid group ID")
		}
		if _, exists := seenIDs[id]; exists {
			return nil, infraerrors.BadRequest("MODEL_QUOTA_GROUP_ID_DUPLICATE", "model_quota_groups contains duplicate group IDs")
		}
		if name == "" || len(name) > 100 {
			return nil, infraerrors.BadRequest("MODEL_QUOTA_GROUP_NAME_INVALID", "model_quota_groups contains an invalid group name")
		}
		if math.IsNaN(rawGroup.Ratio) || math.IsInf(rawGroup.Ratio, 0) || rawGroup.Ratio <= 0 || rawGroup.Ratio > 1 {
			return nil, infraerrors.BadRequest("MODEL_QUOTA_GROUP_RATIO_INVALID", "model_quota_groups ratios must be greater than 0 and no greater than 1")
		}
		if len(rawGroup.Models) < 2 || len(rawGroup.Models) > MaxSubscriptionModelQuotaGroupModels {
			return nil, infraerrors.BadRequest("MODEL_QUOTA_GROUP_MODELS_INVALID", "model_quota_groups must contain between 2 and 50 models")
		}

		models := make([]string, 0, len(rawGroup.Models))
		for _, rawModel := range rawGroup.Models {
			model := NormalizeSubscriptionQuotaModel(rawModel)
			if model == "" || len(model) > 128 || !isSafeSubscriptionQuotaModel(model) {
				return nil, infraerrors.BadRequest("MODEL_QUOTA_MODEL_INVALID", "model_quota_groups contains an invalid model ID")
			}
			if _, exists := seenModels[model]; exists {
				return nil, infraerrors.BadRequest("MODEL_QUOTA_GROUP_MODEL_DUPLICATE", "a model can belong to only one model_quota_group")
			}
			for ratioModel := range ratios {
				if subscriptionQuotaModelsOverlap(model, ratioModel) {
					return nil, infraerrors.BadRequest("MODEL_QUOTA_RULE_OVERLAP", "a model family cannot have both an individual quota and a shared quota")
				}
			}
			seenModels[model] = struct{}{}
			models = append(models, model)
		}
		sort.Strings(models)
		seenIDs[id] = struct{}{}
		normalized = append(normalized, SubscriptionModelQuotaGroup{ID: id, Name: name, Models: models, Ratio: rawGroup.Ratio})
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].ID < normalized[j].ID })
	return normalized, nil
}

func ValidateSubscriptionModelQuotaBase(ratios map[string]float64, limits ...*float64) error {
	return ValidateSubscriptionQuotaRuleBase(len(ratios) > 0, limits...)
}

func ValidateSubscriptionQuotaRuleBase(hasRules bool, limits ...*float64) error {
	if !hasRules {
		return nil
	}
	for _, limit := range limits {
		if limit != nil && *limit > 0 {
			return nil
		}
	}
	return infraerrors.BadRequest("MODEL_QUOTA_BASE_REQUIRED", "subscription model quota rules require at least one positive subscription quota limit")
}

func MatchSubscriptionQuotaRule(groups []SubscriptionModelQuotaGroup, ratios map[string]float64, requestedModel string) (SubscriptionModelQuotaMatch, bool) {
	requested := NormalizeSubscriptionQuotaModel(requestedModel)
	if requested == "" {
		return SubscriptionModelQuotaMatch{}, false
	}
	for _, group := range groups {
		for _, model := range group.Models {
			if requested == model {
				return SubscriptionModelQuotaMatch{UsageKey: "group:" + group.ID, Label: group.Name, Ratio: group.Ratio}, true
			}
		}
	}
	model, ratio, ok := MatchSubscriptionModelQuota(ratios, requested)
	if !ok {
		return SubscriptionModelQuotaMatch{}, false
	}
	return SubscriptionModelQuotaMatch{UsageKey: model, Label: model, Ratio: ratio}, true
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
	model = strings.TrimSpace(model)
	if mapped, ok := defaultAnthropicModelAliases[model]; ok {
		return mapped
	}
	return model
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

func subscriptionQuotaModelsOverlap(first, second string) bool {
	return first == second || subscriptionQuotaModelFamilyMatch(first, second) || subscriptionQuotaModelFamilyMatch(second, first)
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

func isSafeSubscriptionQuotaGroupID(id string) bool {
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
