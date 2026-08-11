package kiro

import (
	"encoding/json"
	"strings"
)

type BufferedStreamResult struct {
	StopReason            string
	WebSearchQuery        string
	WebSearchToolUseID    string
	HasWebSearchToolUse   bool
	WebSearchToolUseIndex int
}

func GenerateSearchIndicatorEvents(query, toolUseID string, results *WebSearchResults, startIndex int) [][]byte {
	searchContent := make([]map[string]any, 0)
	if results != nil {
		for _, result := range results.Results {
			snippet := ""
			if result.Snippet != nil {
				snippet = strings.TrimSpace(*result.Snippet)
			}
			searchContent = append(searchContent, map[string]any{
				"type":              "web_search_result",
				"title":             result.Title,
				"url":               result.URL,
				"encrypted_content": snippet,
				"page_age":          nil,
			})
		}
	}

	inputJSON, _ := json.Marshal(map[string]string{"query": query})

	events := []map[string]any{
		{
			"type":  "content_block_start",
			"index": startIndex,
			"content_block": map[string]any{
				"type":  "server_tool_use",
				"id":    toolUseID,
				"name":  "web_search",
				"input": map[string]any{},
			},
		},
		{
			"type":  "content_block_delta",
			"index": startIndex,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": string(inputJSON),
			},
		},
		{
			"type":  "content_block_stop",
			"index": startIndex,
		},
		{
			"type":  "content_block_start",
			"index": startIndex + 1,
			"content_block": map[string]any{
				"type":        "web_search_tool_result",
				"tool_use_id": toolUseID,
				"content":     searchContent,
			},
		},
		{
			"type":  "content_block_stop",
			"index": startIndex + 1,
		},
	}

	result := make([][]byte, 0, len(events))
	for _, event := range events {
		eventType, _ := event["type"].(string)
		payload, _ := json.Marshal(event)
		result = append(result, []byte("event: "+eventType+"\ndata: "+string(payload)+"\n\n"))
	}
	return result
}

func AnalyzeBufferedStream(chunks [][]byte) BufferedStreamResult {
	result := BufferedStreamResult{WebSearchToolUseIndex: -1}
	var currentToolName string
	currentToolIndex := -1
	var toolInputBuilder strings.Builder

	for _, chunk := range chunks {
		lines := strings.Split(string(chunk), "\n")
		for _, line := range lines {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if payload == "" || payload == "[DONE]" {
				continue
			}

			var event map[string]any
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}

			switch eventType, _ := event["type"].(string); eventType {
			case "message_delta":
				if delta, ok := event["delta"].(map[string]any); ok {
					if stopReason, ok := delta["stop_reason"].(string); ok && strings.TrimSpace(stopReason) != "" {
						result.StopReason = stopReason
					}
				}
			case "content_block_start":
				contentBlock, ok := event["content_block"].(map[string]any)
				if !ok {
					continue
				}
				blockType, _ := contentBlock["type"].(string)
				if blockType != "tool_use" {
					continue
				}
				currentToolName, _ = contentBlock["name"].(string)
				currentToolName = strings.ToLower(strings.TrimSpace(currentToolName))
				if idx, ok := event["index"].(float64); ok {
					currentToolIndex = int(idx)
				}
				if toolUseID, ok := contentBlock["id"].(string); ok && isWebSearchToolName(currentToolName, "") {
					result.WebSearchToolUseID = strings.TrimSpace(toolUseID)
				}
				toolInputBuilder.Reset()
			case "content_block_delta":
				if currentToolName == "" {
					continue
				}
				delta, ok := event["delta"].(map[string]any)
				if !ok {
					continue
				}
				deltaType, _ := delta["type"].(string)
				if deltaType != "input_json_delta" {
					continue
				}
				if partialJSON, ok := delta["partial_json"].(string); ok {
					_, _ = toolInputBuilder.WriteString(partialJSON)
				}
			case "content_block_stop":
				if !isWebSearchToolName(currentToolName, "") {
					currentToolName = ""
					currentToolIndex = -1
					toolInputBuilder.Reset()
					continue
				}
				result.HasWebSearchToolUse = true
				result.WebSearchToolUseIndex = currentToolIndex
				var input map[string]string
				if err := json.Unmarshal([]byte(toolInputBuilder.String()), &input); err == nil {
					result.WebSearchQuery = strings.TrimSpace(input["query"])
				}
				currentToolName = ""
				currentToolIndex = -1
				toolInputBuilder.Reset()
			}
		}
	}

	return result
}

func FilterChunksForClient(chunks [][]byte, webSearchToolUseIndex, indexOffset int) [][]byte {
	filtered := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		adjusted, shouldForward := filterSSEChunk(chunk, webSearchToolUseIndex, indexOffset, true)
		if shouldForward {
			filtered = append(filtered, adjusted)
		}
	}
	return filtered
}

// AdjustSSEChunk rewrites a final buffered Kiro turn into the already-open
// Anthropic response stream. The outer web-search adapter owns message_start,
// while the final translated turn still owns the single message_delta and
// message_stop terminal pair. Intermediate turns use FilterChunksForClient and
// deliberately suppress their terminal envelope.
func AdjustSSEChunk(chunk []byte, offset int) ([]byte, bool) {
	return AdjustSSEChunkWithWebSearchUsage(chunk, offset, 0)
}

// AdjustSSEChunkWithWebSearchUsage preserves the final terminal envelope and
// reports successful server-side web-search calls in the same usage object as
// Anthropic. A zero count is a byte-compatible no-op for non-search usage.
func AdjustSSEChunkWithWebSearchUsage(chunk []byte, offset, webSearchRequests int) ([]byte, bool) {
	return filterSSEChunkWithWebSearchUsage(chunk, -1, offset, false, webSearchRequests)
}

func MaxContentBlockIndex(chunks [][]byte) int {
	maxIndex := -1
	for _, chunk := range chunks {
		lines := strings.Split(string(chunk), "\n")
		for _, line := range lines {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}
			switch eventType, _ := event["type"].(string); eventType {
			case "content_block_start", "content_block_delta", "content_block_stop":
				if idx, ok := event["index"].(float64); ok && int(idx) > maxIndex {
					maxIndex = int(idx)
				}
			}
		}
	}
	return maxIndex
}

func filterSSEChunk(chunk []byte, webSearchToolUseIndex, indexOffset int, suppressMessageTerminal bool) ([]byte, bool) {
	return filterSSEChunkWithWebSearchUsage(chunk, webSearchToolUseIndex, indexOffset, suppressMessageTerminal, 0)
}

func filterSSEChunkWithWebSearchUsage(chunk []byte, webSearchToolUseIndex, indexOffset int, suppressMessageTerminal bool, webSearchRequests int) ([]byte, bool) {
	lines := strings.Split(string(chunk), "\n")
	var builder strings.Builder
	hasContent := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "event: ") {
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "data: ") {
				payload := strings.TrimSpace(strings.TrimPrefix(lines[i+1], "data: "))
				if shouldSuppressEventPayload(payload, webSearchToolUseIndex, suppressMessageTerminal) {
					i++
					continue
				}
			}
			_, _ = builder.WriteString(line + "\n")
			hasContent = true
			continue
		}

		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if payload == "[DONE]" {
				continue
			}
			if shouldSuppressEventPayload(payload, webSearchToolUseIndex, suppressMessageTerminal) {
				continue
			}
			adjusted := adjustEventPayload(payload, indexOffset, webSearchRequests)
			if adjusted == "" {
				continue
			}
			_, _ = builder.WriteString("data: " + adjusted + "\n")
			hasContent = true
			continue
		}

		_, _ = builder.WriteString(line + "\n")
		if strings.TrimSpace(line) != "" {
			hasContent = true
		}
	}

	if !hasContent {
		return nil, false
	}
	return []byte(builder.String()), true
}

func shouldSuppressEventPayload(payload string, webSearchToolUseIndex int, suppressMessageTerminal bool) bool {
	if payload == "" {
		return false
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return false
	}
	eventType, _ := event["type"].(string)
	if eventType == "message_start" {
		return true
	}
	if suppressMessageTerminal && (eventType == "message_delta" || eventType == "message_stop") {
		return true
	}
	if webSearchToolUseIndex < 0 {
		return false
	}
	if idx, ok := event["index"].(float64); ok && int(idx) == webSearchToolUseIndex {
		return true
	}
	return false
}

func adjustEventPayload(payload string, indexOffset, webSearchRequests int) string {
	if payload == "" || (indexOffset == 0 && webSearchRequests <= 0) {
		return payload
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return payload
	}
	changed := false
	switch eventType, _ := event["type"].(string); eventType {
	case "content_block_start", "content_block_delta", "content_block_stop":
		if indexOffset != 0 {
			if idx, ok := event["index"].(float64); ok {
				event["index"] = int(idx) + indexOffset
				changed = true
			}
		}
	case "message_delta":
		if webSearchRequests > 0 {
			usage, ok := event["usage"].(map[string]any)
			if !ok {
				usage = map[string]any{}
				event["usage"] = usage
			}
			usage["server_tool_use"] = map[string]any{"web_search_requests": webSearchRequests}
			changed = true
		}
	}
	if changed {
		if adjusted, err := json.Marshal(event); err == nil {
			return string(adjusted)
		}
	}
	return payload
}
