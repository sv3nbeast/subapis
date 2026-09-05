package service

import (
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func normalizeGroupModelPricing(platform string, pricing []ChannelModelPricing) ([]ChannelModelPricing, error) {
	out := make([]ChannelModelPricing, len(pricing))
	for i := range pricing {
		out[i] = pricing[i].Clone()
		out[i].ID = 0
		out[i].ChannelID = 0
		if out[i].TimePricing != nil && len(out[i].TimePricing.Periods) > 0 {
			return nil, infraerrors.BadRequest(
				"GROUP_MODEL_TIME_PRICING_UNSUPPORTED",
				"group model pricing does not support time pricing",
			)
		}
		if strings.TrimSpace(out[i].Platform) == "" {
			out[i].Platform = platform
		}
		for j := range out[i].Models {
			out[i].Models[j] = strings.TrimSpace(out[i].Models[j])
		}
		if len(out[i].Models) == 0 {
			return nil, infraerrors.New(http.StatusBadRequest, "GROUP_MODEL_PRICING_MODELS_REQUIRED", "group model pricing entry requires at least one model")
		}
	}
	if err := validatePricingEntries(out); err != nil {
		return nil, err
	}
	return out, nil
}
