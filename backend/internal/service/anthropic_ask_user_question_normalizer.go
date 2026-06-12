package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func normalizeAnthropicAskUserQuestionResponseBody(body []byte) ([]byte, bool) {
	return normalizeAnthropicAskUserQuestionResponseBodyWithRewrite(body, nil)
}

func normalizeAnthropicAskUserQuestionResponseBodyWithRewrite(body []byte, toolNameRewrite *ToolNameRewrite) ([]byte, bool) {
	if len(bytes.TrimSpace(body)) == 0 || !gjson.ValidBytes(body) {
		return body, false
	}

	content := gjson.GetBytes(body, "content")
	if !content.IsArray() {
		return body, false
	}

	updated := body
	changed := false
	content.ForEach(func(key, item gjson.Result) bool {
		if item.Get("type").String() != "tool_use" || !isAnthropicAskUserQuestionToolName(item.Get("name").String(), toolNameRewrite) {
			return true
		}
		input := item.Get("input")
		if !input.IsObject() {
			return true
		}
		normalized, inputChanged := normalizeAskUserQuestionArguments(input.Raw)
		if !inputChanged {
			return true
		}
		next, err := sjson.SetRawBytes(updated, "content."+strconv.Itoa(int(key.Int()))+".input", []byte(normalized))
		if err != nil {
			return true
		}
		updated = next
		changed = true
		return true
	})

	if !changed {
		return body, false
	}
	return updated, true
}

type anthropicAskUserQuestionStreamNormalizer struct {
	blocks          map[int]*anthropicAskUserQuestionStreamBlock
	toolNameRewrite *ToolNameRewrite
}

type anthropicAskUserQuestionStreamBlock struct {
	partialJSON string
}

func newAnthropicAskUserQuestionStreamNormalizer(toolNameRewrite *ToolNameRewrite) *anthropicAskUserQuestionStreamNormalizer {
	return &anthropicAskUserQuestionStreamNormalizer{
		blocks:          make(map[int]*anthropicAskUserQuestionStreamBlock),
		toolNameRewrite: toolNameRewrite,
	}
}

func (n *anthropicAskUserQuestionStreamNormalizer) handleEvent(event map[string]any) ([]map[string]any, bool, bool) {
	if n == nil || event == nil {
		return nil, false, false
	}

	eventType, _ := event["type"].(string)
	switch eventType {
	case "content_block_start":
		return n.handleContentBlockStart(event)
	case "content_block_delta":
		return n.handleContentBlockDelta(event)
	case "content_block_stop":
		return n.handleContentBlockStop(event)
	default:
		return nil, false, false
	}
}

func (n *anthropicAskUserQuestionStreamNormalizer) handleContentBlockStart(event map[string]any) ([]map[string]any, bool, bool) {
	index, ok := anthropicSSEEventIndex(event)
	if !ok {
		return nil, false, false
	}

	contentBlock, ok := event["content_block"].(map[string]any)
	if !ok {
		delete(n.blocks, index)
		return nil, false, false
	}
	if contentBlock["type"] != "tool_use" || !n.isAskUserQuestionToolName(askUserQuestionStringFromAny(contentBlock["name"])) {
		delete(n.blocks, index)
		return nil, false, false
	}

	n.blocks[index] = &anthropicAskUserQuestionStreamBlock{}
	input, ok := contentBlock["input"].(map[string]any)
	if !ok {
		return nil, false, false
	}
	normalizedInput, changed := normalizeAskUserQuestionInputMap(input)
	if !changed {
		return nil, false, false
	}
	contentBlock["input"] = normalizedInput
	return nil, false, true
}

func (n *anthropicAskUserQuestionStreamNormalizer) handleContentBlockDelta(event map[string]any) ([]map[string]any, bool, bool) {
	index, ok := anthropicSSEEventIndex(event)
	if !ok {
		return nil, false, false
	}
	block, ok := n.blocks[index]
	if !ok {
		return nil, false, false
	}

	delta, ok := event["delta"].(map[string]any)
	if !ok || delta["type"] != "input_json_delta" {
		return nil, false, false
	}

	block.partialJSON += askUserQuestionStringFromAny(delta["partial_json"])
	return nil, true, true
}

func (n *anthropicAskUserQuestionStreamNormalizer) handleContentBlockStop(event map[string]any) ([]map[string]any, bool, bool) {
	index, ok := anthropicSSEEventIndex(event)
	if !ok {
		return nil, false, false
	}
	block, ok := n.blocks[index]
	if !ok {
		return nil, false, false
	}
	delete(n.blocks, index)

	events := make([]map[string]any, 0, 2)
	if block.partialJSON != "" {
		partialJSON := block.partialJSON
		if normalized, changed := normalizeAskUserQuestionArguments(partialJSON); changed {
			partialJSON = normalized
		}
		events = append(events, map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": partialJSON,
			},
		})
	}
	events = append(events, event)
	return events, true, true
}

func normalizeAskUserQuestionInputMap(input map[string]any) (map[string]any, bool) {
	raw, err := json.Marshal(input)
	if err != nil {
		return input, false
	}
	normalized, changed := normalizeAskUserQuestionArguments(string(raw))
	if !changed {
		return input, false
	}
	var normalizedInput map[string]any
	if err := json.Unmarshal([]byte(normalized), &normalizedInput); err != nil {
		return input, false
	}
	return normalizedInput, true
}

func (n *anthropicAskUserQuestionStreamNormalizer) isAskUserQuestionToolName(name string) bool {
	return isAnthropicAskUserQuestionToolName(name, n.toolNameRewrite)
}

func isAnthropicAskUserQuestionToolName(name string, toolNameRewrite *ToolNameRewrite) bool {
	if isAskUserQuestionTool(name) {
		return true
	}
	if toolNameRewrite == nil {
		return false
	}
	if realName, ok := toolNameRewrite.Reverse[name]; ok && isAskUserQuestionTool(realName) {
		return true
	}
	if realName, ok := toolNameRewrite.ReverseFields[name]; ok && isAskUserQuestionTool(realName) {
		return true
	}
	return false
}

func anthropicSSEEventIndex(event map[string]any) (int, bool) {
	switch v := event["index"].(type) {
	case float64:
		if v < 0 {
			return 0, false
		}
		return int(v), true
	case int:
		if v < 0 {
			return 0, false
		}
		return v, true
	case json.Number:
		i, err := strconv.Atoi(v.String())
		if err != nil || i < 0 {
			return 0, false
		}
		return i, true
	default:
		return 0, false
	}
}

func anthropicSSEBlockFromEvent(event map[string]any) (string, bool) {
	eventType := askUserQuestionStringFromAny(event["type"])
	if eventType == "" {
		return "", false
	}
	data, err := json.Marshal(event)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, data), true
}

func askUserQuestionStringFromAny(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
