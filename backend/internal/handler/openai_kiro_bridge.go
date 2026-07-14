package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func openAIKiroBridgeModel(requestedModel string, mapping service.ChannelMappingResult) string {
	if mapping.Mapped {
		return strings.TrimSpace(mapping.MappedModel)
	}
	return strings.TrimSpace(requestedModel)
}

func isOpenAIKiroBridgeResponsesRequest(c *gin.Context, requestPlatform string, model string) bool {
	return requestPlatform == service.PlatformOpenAI && model == service.OpenAIKiroBridgeModel && isBareOpenAIResponsesPath(c)
}

func isOpenAIKiroBridgeChatRequest(c *gin.Context, requestPlatform string, model string) bool {
	if requestPlatform != service.PlatformOpenAI || model != service.OpenAIKiroBridgeModel || c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	path := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	return strings.HasSuffix(path, "/chat/completions")
}

func parseOpenAIKiroBridgeRequest(c *gin.Context, apiKey *service.APIKey, body []byte, protocol string) (*service.ParsedRequest, error) {
	parsed, err := service.ParseGatewayRequest(service.NewRequestBodyRef(body), protocol)
	if err != nil {
		return nil, err
	}
	if parsed == nil {
		return nil, errors.New("kiro bridge request parsing returned nil")
	}
	attachAPIKeyGroupToParsedRequest(parsed, apiKey)
	if c != nil {
		var apiKeyID int64
		if apiKey != nil {
			apiKeyID = apiKey.ID
		}
		parsed.SessionContext = &service.SessionContext{
			ClientIP:  ip.GetClientIP(c),
			UserAgent: c.GetHeader("User-Agent"),
			APIKeyID:  apiKeyID,
		}
		parsed.ExplicitSessionID = explicitStickySessionIDFromHeaders(c)
	}
	return parsed, nil
}

func (h *OpenAIGatewayHandler) forwardOpenAIResponses(
	ctx context.Context,
	c *gin.Context,
	account *service.Account,
	body []byte,
	parsed *service.ParsedRequest,
) (*service.OpenAIForwardResult, error) {
	if account == nil || account.Platform != service.PlatformKiro {
		return h.gatewayService.Forward(ctx, c, account, body)
	}
	if h.kiroBridgeService == nil || parsed == nil {
		return nil, errors.New("kiro bridge service is unavailable")
	}
	result, err := h.kiroBridgeService.ForwardAsResponses(ctx, c, account, body, parsed)
	return openAIForwardResultFromGateway(result), err
}

func (h *OpenAIGatewayHandler) forwardOpenAIChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *service.Account,
	body []byte,
	parsed *service.ParsedRequest,
	promptCacheKey string,
) (*service.OpenAIForwardResult, error) {
	if account == nil || account.Platform != service.PlatformKiro {
		return h.gatewayService.ForwardAsChatCompletions(ctx, c, account, body, promptCacheKey, "")
	}
	if h.kiroBridgeService == nil || parsed == nil {
		return nil, errors.New("kiro bridge service is unavailable")
	}
	result, err := h.kiroBridgeService.ForwardAsChatCompletions(ctx, c, account, body, parsed)
	return openAIForwardResultFromGateway(result), err
}

func openAIForwardResultFromGateway(result *service.ForwardResult) *service.OpenAIForwardResult {
	if result == nil {
		return nil
	}
	return &service.OpenAIForwardResult{
		RequestID:  result.RequestID,
		ResponseID: result.ResponseID,
		Usage: service.OpenAIUsage{
			// OpenAI usage reports total input and RecordUsage subtracts cache
			// buckets. Gateway ClaudeUsage stores those buckets separately.
			InputTokens:              result.Usage.InputTokens + result.Usage.CacheCreationInputTokens + result.Usage.CacheReadInputTokens,
			OutputTokens:             result.Usage.OutputTokens,
			CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
			ImageOutputTokens:        result.Usage.ImageOutputTokens,
			KiroCredits:              result.Usage.KiroCredits,
		},
		Model:              result.Model,
		BillingModel:       result.Model,
		UpstreamModel:      result.UpstreamModel,
		ReasoningEffort:    result.ReasoningEffort,
		Stream:             result.Stream,
		Duration:           result.Duration,
		FirstTokenMs:       result.FirstTokenMs,
		ClientDisconnect:   result.ClientDisconnect,
		ImageCount:         result.ImageCount,
		ImageSize:          result.ImageSize,
		ImageOutputSize:    result.ImageOutputSize,
		ImageOutputSizes:   result.ImageOutputSizes,
		ImageSizeSource:    result.ImageSizeSource,
		ImageSizeBreakdown: result.ImageSizeBreakdown,
	}
}
