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
				"type":    "web_search_tool_result",
				"content": searchContent,
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
	currentToolID := ""
	var toolInputBuilder strings.Builder
	resetTool := func() {
		currentToolName = ""
		currentToolIndex = -1
		currentToolID = ""
		toolInputBuilder.Reset()
	}

	for _, frame := range splitSSEFrames(chunks) {
		payload := firstSSEJSONPayload(frame)
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
			// A turn may contain native server_tool_use/result blocks followed by
			// a translated custom tool_use block (or vice versa). Always reset the
			// active block first; otherwise input deltas from the next block can be
			// appended to the previous web-search query.
			resetTool()
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
			currentToolID, _ = contentBlock["id"].(string)
			if isWebSearchToolName(currentToolName, "") && strings.TrimSpace(result.WebSearchToolUseID) == "" {
				result.WebSearchToolUseID = strings.TrimSpace(currentToolID)
			}
			if input, ok := contentBlock["input"].(map[string]any); ok {
				if query, ok := input["query"].(string); ok {
					_, _ = toolInputBuilder.WriteString(`{"query":`)
					encoded, _ := json.Marshal(query)
					_, _ = toolInputBuilder.Write(encoded)
					_, _ = toolInputBuilder.WriteString(`}`)
				}
			}
		case "content_block_delta":
			if currentToolName == "" {
				continue
			}
			if idx, ok := event["index"].(float64); ok && currentToolIndex >= 0 && int(idx) != currentToolIndex {
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
			if currentToolName == "" {
				continue
			}
			if idx, ok := event["index"].(float64); ok && currentToolIndex >= 0 && int(idx) != currentToolIndex {
				continue
			}
			if isWebSearchToolName(currentToolName, "") {
				var input map[string]string
				query := ""
				if err := json.Unmarshal([]byte(toolInputBuilder.String()), &input); err == nil {
					query = strings.TrimSpace(input["query"])
				}
				if query != "" && !result.HasWebSearchToolUse {
					result.HasWebSearchToolUse = true
					result.WebSearchToolUseIndex = currentToolIndex
					result.WebSearchQuery = query
				}
			}
			resetTool()
		}
	}

	return result
}

func FilterChunksForClient(chunks [][]byte, webSearchToolUseIndex, indexOffset int) [][]byte {
	frames := splitSSEFrames(chunks)
	filtered := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		adjusted, shouldForward := filterSSEChunk(frame, webSearchToolUseIndex, indexOffset)
		if shouldForward {
			filtered = append(filtered, adjusted)
		}
	}
	return filtered
}

// splitSSEFrames joins arbitrary writer fragments and restores complete SSE
// records. The translator writes one record today, but io.Writer is allowed to
// split a write at any byte; filtering a fragment cannot reliably identify the
// matching event/data pair.
func splitSSEFrames(chunks [][]byte) [][]byte {
	var wire strings.Builder
	for _, chunk := range chunks {
		if len(chunk) > 0 {
			_, _ = wire.Write(chunk)
		}
	}
	normalized := strings.ReplaceAll(wire.String(), "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	if normalized == "" {
		return nil
	}
	parts := strings.Split(normalized, "\n\n")
	frames := make([][]byte, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		frames = append(frames, []byte(part+"\n\n"))
	}
	return frames
}

func firstSSEJSONPayload(chunk []byte) string {
	for _, line := range strings.Split(string(chunk), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
		if payload != "" && payload != "[DONE]" {
			return payload
		}
	}
	return ""
}

func AdjustSSEChunk(chunk []byte, offset int) ([]byte, bool) {
	return filterSSEChunk(chunk, -1, offset)
}

func MaxContentBlockIndex(chunks [][]byte) int {
	maxIndex := -1
	for _, chunk := range splitSSEFrames(chunks) {
		payload := firstSSEJSONPayload(chunk)
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
	return maxIndex
}

func filterSSEChunk(chunk []byte, webSearchToolUseIndex, indexOffset int) ([]byte, bool) {
	lines := strings.Split(string(chunk), "\n")
	var builder strings.Builder
	hasContent := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "event: ") {
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "data: ") {
				payload := strings.TrimSpace(strings.TrimPrefix(lines[i+1], "data: "))
				if shouldSuppressEventPayload(payload, webSearchToolUseIndex) {
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
			if shouldSuppressEventPayload(payload, webSearchToolUseIndex) {
				continue
			}
			adjusted := adjustEventPayload(payload, indexOffset)
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

func shouldSuppressEventPayload(payload string, webSearchToolUseIndex int) bool {
	if payload == "" {
		return false
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return false
	}
	eventType, _ := event["type"].(string)
	if eventType == "message_start" || eventType == "message_delta" || eventType == "message_stop" {
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

func adjustEventPayload(payload string, indexOffset int) string {
	if payload == "" || indexOffset == 0 {
		return payload
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return payload
	}
	switch eventType, _ := event["type"].(string); eventType {
	case "content_block_start", "content_block_delta", "content_block_stop":
		if idx, ok := event["index"].(float64); ok {
			event["index"] = int(idx) + indexOffset
			if adjusted, err := json.Marshal(event); err == nil {
				return string(adjusted)
			}
		}
	}
	return payload
}
