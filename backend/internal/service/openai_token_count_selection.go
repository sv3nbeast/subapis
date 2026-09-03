package service

import "context"

func (s *OpenAIGatewayService) SelectAccountForTokenCount(ctx context.Context, groupID *int64, sessionHash, requestedModel string, requiredCapability OpenAIEndpointCapability, platform string) (*Account, error) {
	ctx = WithOpenAIProfitControlSuppressed(ctx)
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	return s.selectAccountForModelWithExclusions(ctx, groupID, platform, sessionHash, requestedModel, nil, false, 0, requiredCapability, false)
}
