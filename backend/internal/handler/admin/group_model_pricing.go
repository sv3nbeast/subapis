package admin

import "github.com/Wei-Shaw/sub2api/internal/service"

func groupPricingRequestToService(input []channelModelPricingRequest, platform string) []service.ChannelModelPricing {
	output := pricingRequestToService(input, true)
	for i := range input {
		if input[i].Platform == "" {
			output[i].Platform = platform
		}
	}
	return output
}

func optionalGroupPricingRequestToService(input *[]channelModelPricingRequest, platform string) *[]service.ChannelModelPricing {
	if input == nil {
		return nil
	}
	output := groupPricingRequestToService(*input, platform)
	return &output
}
