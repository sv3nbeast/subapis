package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

func estimateAnthropicCountTokens(parsed *ParsedRequest) int {
	if parsed == nil {
		return 0
	}

	total := 0
	total += estimateTokensForAny(parsed.System)

	for _, message := range parsed.Messages {
		total += estimateTokensForAny(extractMessageContentForTokenEstimate(message))
	}

	if total < 0 {
		return 0
	}
	return total
}

func extractMessageContentForTokenEstimate(message any) any {
	msgMap, ok := message.(map[string]any)
	if !ok {
		return nil
	}
	return msgMap["content"]
}

func estimateTokensForAny(value any) int {
	switch v := value.(type) {
	case nil:
		return 0
	case string:
		return estimateTokensForText(v)
	case []any:
		total := 0
		for _, item := range v {
			total += estimateTokensForAny(item)
		}
		return total
	case map[string]any:
		return estimateTokensForMap(v)
	case json.RawMessage:
		return estimateTokensForJSON(v)
	default:
		return estimateTokensForText(fmt.Sprint(v))
	}
}

func estimateTokensForMap(value map[string]any) int {
	blockType, _ := value["type"].(string)
	switch blockType {
	case "text":
		return estimateTokensForText(stringFromMap(value, "text"))
	case "thinking":
		return estimateTokensForText(stringFromMap(value, "thinking"))
	case "tool_result":
		total := 0
		total += estimateTokensForAny(value["content"])
		return total
	case "tool_use":
		return estimateTokensForText(stringFromMap(value, "name"))
	}

	total := 0
	for _, candidate := range []string{"text", "thinking"} {
		total += estimateTokensForText(stringFromMap(value, candidate))
	}
	if content, ok := value["content"]; ok {
		total += estimateTokensForAny(content)
	}
	return total
}

func estimateTokensForJSON(body []byte) int {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return estimateTokensForText(string(body))
	}

	total := 0

	parsed := gjson.ParseBytes(body)
	if parsed.IsArray() {
		parsed.ForEach(func(_, value gjson.Result) bool {
			total += estimateTokensForJSON([]byte(value.Raw))
			return true
		})
		return total
	}

	blockType := parsed.Get("type").String()
	switch blockType {
	case "text":
		total += estimateTokensForText(parsed.Get("text").String())
	case "thinking":
		total += estimateTokensForText(parsed.Get("thinking").String())
	case "tool_result":
		content := parsed.Get("content")
		if content.Exists() {
			total += estimateTokensForJSON([]byte(content.Raw))
		}
	case "tool_use":
		total += estimateTokensForText(parsed.Get("name").String())
	default:
		for _, path := range []string{"text", "thinking"} {
			if t := strings.TrimSpace(parsed.Get(path).String()); t != "" {
				total += estimateTokensForText(t)
			}
		}
		if content := parsed.Get("content"); content.Exists() {
			total += estimateTokensForJSON([]byte(content.Raw))
		}
	}

	return total
}

func stringFromMap(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if value, ok := m[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}
