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

func GenerateSearchToolUseEvents(query, toolUseID string, index int) [][]byte {
	inputJSON, _ := json.Marshal(map[string]string{"query": query})
	events := []map[string]any{
		{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "server_tool_use",
				"id":    toolUseID,
				"name":  "web_search",
				"input": map[string]any{},
			},
		},
		{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": string(inputJSON),
			},
		},
		{
			"type":  "content_block_stop",
			"index": index,
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

func GenerateSearchToolResultEvents(toolUseID string, results *WebSearchResults, errorCode string, index int) [][]byte {
	events := []map[string]any{
		{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":        "web_search_tool_result",
				"tool_use_id": toolUseID,
				"caller":      map[string]any{"type": "direct"},
				"content":     buildWebSearchToolResultContent(results, errorCode),
			},
		},
		{
			"type":  "content_block_stop",
			"index": index,
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

func GenerateSearchIndicatorEvents(query, toolUseID string, results *WebSearchResults, startIndex int) [][]byte {
	return GenerateSearchIndicatorEventsWithError(query, toolUseID, results, "", startIndex)
}

func GenerateSearchIndicatorEventsWithError(query, toolUseID string, results *WebSearchResults, errorCode string, startIndex int) [][]byte {
	result := GenerateSearchToolUseEvents(query, toolUseID, startIndex)
	result = append(result, GenerateSearchToolResultEvents(toolUseID, results, errorCode, startIndex+1)...)
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
		if len(payload) == 0 || string(payload) == "[DONE]" {
			continue
		}

		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil {
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
			// Native server_tool_use/result blocks must never inherit the
			// preceding custom tool's input accumulator.
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
		adjusted, shouldForward := filterSSEChunk(frame, webSearchToolUseIndex, indexOffset, true)
		if shouldForward {
			filtered = append(filtered, adjusted)
		}
	}
	return filtered
}

// splitSSEFrames joins arbitrary writer fragments and restores complete SSE
// records before any event-level filtering or index rewriting is attempted.
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
	return filterSSEChunkWithServerToolUsage(chunk, -1, offset, false, webSearchRequests, 0)
}

// FinalizeWebSearchSSEChunks rewrites the final Kiro turn into the already-open
// Anthropic stream and attaches citation deltas to its final text block.
// Claude begins a cited text block with citations:[] and emits citation deltas
// before the text they qualify, so preserve that ordering here.
func FinalizeWebSearchSSEChunks(chunks [][]byte, offset, webSearchRequests int, searches []SearchIndicator) [][]byte {
	adjusted := make([][]byte, 0, len(chunks)+4)
	for _, chunk := range chunks {
		rewritten, shouldForward := AdjustSSEChunkWithWebSearchUsage(chunk, offset, webSearchRequests)
		if shouldForward {
			adjusted = append(adjusted, rewritten)
		}
	}

	textIndex, text := finalSSETextBlock(adjusted)
	if textIndex < 0 {
		return adjusted
	}
	citations := buildWebSearchCitations(searches, text)
	if len(citations) == 0 {
		return adjusted
	}

	result := make([][]byte, 0, len(adjusted)+len(citations))
	inserted := false
	for _, chunk := range adjusted {
		if !inserted {
			if citedStart, ok := addCitationsToSSETextBlockStart(chunk, textIndex); ok {
				result = append(result, citedStart)
				for _, citation := range citations {
					result = append(result, marshalSSEEvent("content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": textIndex,
						"delta": map[string]any{
							"type":     "citations_delta",
							"citation": citation,
						},
					}))
				}
				inserted = true
				continue
			}
		}
		result = append(result, chunk)
	}
	return result
}

func addCitationsToSSETextBlockStart(chunk []byte, index int) ([]byte, bool) {
	payload := firstSSEJSONPayload(chunk)
	if len(payload) == 0 {
		return nil, false
	}
	var event map[string]any
	if json.Unmarshal(payload, &event) != nil || event["type"] != "content_block_start" {
		return nil, false
	}
	idx, ok := event["index"].(float64)
	if !ok || int(idx) != index {
		return nil, false
	}
	block, _ := event["content_block"].(map[string]any)
	if blockType, _ := block["type"].(string); blockType != "text" {
		return nil, false
	}
	block["citations"] = []any{}
	return marshalSSEEvent("content_block_start", event), true
}

func finalSSETextBlock(chunks [][]byte) (int, string) {
	textBlocks := make(map[int]*strings.Builder)
	lastTextIndex := -1
	for _, chunk := range chunks {
		payload := firstSSEJSONPayload(chunk)
		if len(payload) == 0 {
			continue
		}
		var event map[string]any
		if json.Unmarshal(payload, &event) != nil {
			continue
		}
		index, ok := event["index"].(float64)
		if !ok {
			continue
		}
		idx := int(index)
		switch eventType, _ := event["type"].(string); eventType {
		case "content_block_start":
			block, _ := event["content_block"].(map[string]any)
			if blockType, _ := block["type"].(string); blockType == "text" {
				builder := &strings.Builder{}
				if initial, _ := block["text"].(string); initial != "" {
					_, _ = builder.WriteString(initial)
				}
				textBlocks[idx] = builder
				lastTextIndex = idx
			}
		case "content_block_delta":
			delta, _ := event["delta"].(map[string]any)
			if deltaType, _ := delta["type"].(string); deltaType == "text_delta" {
				if builder := textBlocks[idx]; builder != nil {
					if value, _ := delta["text"].(string); value != "" {
						_, _ = builder.WriteString(value)
					}
				}
			}
		}
	}
	if lastTextIndex < 0 || textBlocks[lastTextIndex] == nil {
		return -1, ""
	}
	return lastTextIndex, textBlocks[lastTextIndex].String()
}

func firstSSEJSONPayload(chunk []byte) []byte {
	for _, line := range strings.Split(string(chunk), "\n") {
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if payload != "" && payload != "[DONE]" {
				return []byte(payload)
			}
		}
	}
	return nil
}

func isSSEContentBlockStop(chunk []byte, index int) bool {
	payload := firstSSEJSONPayload(chunk)
	if len(payload) == 0 {
		return false
	}
	var event map[string]any
	if json.Unmarshal(payload, &event) != nil || event["type"] != "content_block_stop" {
		return false
	}
	idx, ok := event["index"].(float64)
	return ok && int(idx) == index
}

func marshalSSEEvent(eventType string, event map[string]any) []byte {
	payload, _ := json.Marshal(event)
	return []byte("event: " + eventType + "\ndata: " + string(payload) + "\n\n")
}

// AdjustSSEChunkWithCodeExecutionUsage reuses the final Kiro terminal envelope
// while reporting the server-side Python executions performed by Sub2API.
func AdjustSSEChunkWithCodeExecutionUsage(chunk []byte, offset, codeExecutionRequests int) ([]byte, bool) {
	return filterSSEChunkWithServerToolUsage(chunk, -1, offset, false, 0, codeExecutionRequests)
}

func MaxContentBlockIndex(chunks [][]byte) int {
	maxIndex := -1
	for _, chunk := range splitSSEFrames(chunks) {
		payload := firstSSEJSONPayload(chunk)
		if len(payload) == 0 || string(payload) == "[DONE]" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(payload, &event); err != nil {
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

func filterSSEChunk(chunk []byte, webSearchToolUseIndex, indexOffset int, suppressMessageTerminal bool) ([]byte, bool) {
	return filterSSEChunkWithServerToolUsage(chunk, webSearchToolUseIndex, indexOffset, suppressMessageTerminal, 0, 0)
}

func filterSSEChunkWithWebSearchUsage(chunk []byte, webSearchToolUseIndex, indexOffset int, suppressMessageTerminal bool, webSearchRequests int) ([]byte, bool) {
	return filterSSEChunkWithServerToolUsage(chunk, webSearchToolUseIndex, indexOffset, suppressMessageTerminal, webSearchRequests, 0)
}

func filterSSEChunkWithServerToolUsage(chunk []byte, webSearchToolUseIndex, indexOffset int, suppressMessageTerminal bool, webSearchRequests, codeExecutionRequests int) ([]byte, bool) {
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
			adjusted := adjustEventPayload(payload, indexOffset, webSearchToolUseIndex, webSearchRequests, codeExecutionRequests)
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
	// Kiro expresses a refinement as a private client tool_use. The adapter
	// consumes that block to execute the next MCP search, then exposes the next
	// call as a fresh Anthropic server_tool_use/result pair. Forwarding the
	// private block would leave a client-visible tool_use with no tool_result
	// and make Claude Code treat the server-side search loop as unfinished.
	if webSearchToolUseIndex >= 0 {
		switch eventType {
		case "content_block_start", "content_block_delta", "content_block_stop":
			if index, ok := event["index"].(float64); ok && int(index) == webSearchToolUseIndex {
				return true
			}
		}
	}
	return false
}

func adjustEventPayload(payload string, indexOffset, suppressedContentBlockIndex, webSearchRequests, codeExecutionRequests int) string {
	if payload == "" || (indexOffset == 0 && suppressedContentBlockIndex < 0 && webSearchRequests <= 0 && codeExecutionRequests <= 0) {
		return payload
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return payload
	}
	changed := false
	switch eventType, _ := event["type"].(string); eventType {
	case "content_block_start", "content_block_delta", "content_block_stop":
		if idx, ok := event["index"].(float64); ok {
			sourceIndex := int(idx)
			adjustedIndex := sourceIndex + indexOffset
			// Removing the private refinement block must also close its index
			// slot. Otherwise a later narrative block keeps its original index
			// and the client observes a gap in the Anthropic SSE sequence.
			if suppressedContentBlockIndex >= 0 && sourceIndex > suppressedContentBlockIndex {
				adjustedIndex--
			}
			if adjustedIndex != sourceIndex {
				event["index"] = adjustedIndex
				changed = true
			}
		}
	case "message_delta":
		if webSearchRequests > 0 || codeExecutionRequests > 0 {
			usage, ok := event["usage"].(map[string]any)
			if !ok {
				usage = map[string]any{}
				event["usage"] = usage
			}
			serverUsage, _ := usage["server_tool_use"].(map[string]any)
			if serverUsage == nil {
				serverUsage = map[string]any{}
				usage["server_tool_use"] = serverUsage
			}
			if webSearchRequests > 0 {
				serverUsage["web_search_requests"] = webSearchRequests
			}
			if codeExecutionRequests > 0 {
				serverUsage["code_execution_requests"] = codeExecutionRequests
			}
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
