package service

import "github.com/tidwall/gjson"

func openAIResponsesCompletedEventIsEmpty(data []byte, usage *OpenAIUsage) bool {
	if len(data) == 0 || !gjson.ValidBytes(data) {
		return false
	}
	if usage != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 ||
		usage.ImageInputTokens > 0 || usage.ImageOutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 || usage.CacheReadInputTokens > 0) {
		return false
	}
	if gjson.GetBytes(data, "usage").Exists() || gjson.GetBytes(data, "response.usage").Exists() {
		return false
	}
	if gjson.GetBytes(data, "error").Exists() || gjson.GetBytes(data, "response.error").Exists() {
		return false
	}
	if output := gjson.GetBytes(data, "response.output"); output.Exists() && output.IsArray() && len(output.Array()) > 0 {
		return false
	}
	return true
}
