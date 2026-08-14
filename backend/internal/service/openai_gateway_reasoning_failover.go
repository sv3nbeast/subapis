package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// SanitizeOpenAICrossModeFailoverReasoning drops provider-specific encrypted
// reasoning input items when failover crosses from passthrough to translated mode.
func SanitizeOpenAICrossModeFailoverReasoning(body []byte) ([]byte, bool, error) {
	if len(body) == 0 || !gjson.GetBytes(body, "input").Exists() {
		return body, false, nil
	}
	var decoded map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return body, false, fmt.Errorf("decode cross-mode failover body: %w", err)
	}
	if !dropOpenAIEncryptedReasoningInputItems(decoded) {
		return body, false, nil
	}
	out, err := marshalOpenAIUpstreamJSON(decoded)
	if err != nil {
		return body, false, fmt.Errorf("serialize cross-mode failover body: %w", err)
	}
	return out, true, nil
}

func dropOpenAIEncryptedReasoningInputItems(reqBody map[string]any) bool {
	inputValue, ok := reqBody["input"]
	if !ok {
		return false
	}
	filter := func(input []any) ([]any, bool) {
		filtered := make([]any, 0, len(input))
		changed := false
		for _, item := range input {
			if isOpenAIEncryptedReasoningInputItem(item) {
				changed = true
				continue
			}
			filtered = append(filtered, item)
		}
		return filtered, changed
	}
	switch input := inputValue.(type) {
	case []any:
		filtered, changed := filter(input)
		if !changed {
			return false
		}
		if len(filtered) == 0 {
			delete(reqBody, "input")
		} else {
			reqBody["input"] = filtered
		}
		return true
	case map[string]any:
		if !isOpenAIEncryptedReasoningInputItem(input) {
			return false
		}
		delete(reqBody, "input")
		return true
	default:
		return false
	}
}

func isOpenAIEncryptedReasoningInputItem(item any) bool {
	inputItem, ok := item.(map[string]any)
	if !ok {
		return false
	}
	itemType, _ := inputItem["type"].(string)
	switch strings.TrimSpace(itemType) {
	case "reasoning", "compaction", "compaction_summary":
	default:
		return false
	}
	_, encrypted := inputItem["encrypted_content"]
	return encrypted
}
