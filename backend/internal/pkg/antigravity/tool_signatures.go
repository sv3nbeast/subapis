package antigravity

import (
	"encoding/json"
	"strings"
)

// ExtractToolUseSignaturesFromClaudeResponse 提取 Claude 响应中 tool_use.id -> signature 的映射。
func ExtractToolUseSignaturesFromClaudeResponse(body []byte) (map[string]string, error) {
	var resp ClaudeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}

	signatures := make(map[string]string)
	for _, item := range resp.Content {
		if item.Type != "tool_use" {
			continue
		}
		toolID := strings.TrimSpace(item.ID)
		signature := strings.TrimSpace(item.Signature)
		if toolID == "" || signature == "" {
			continue
		}
		signatures[toolID] = signature
	}
	return signatures, nil
}
