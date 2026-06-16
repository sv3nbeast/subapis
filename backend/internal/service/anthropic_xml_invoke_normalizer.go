package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	anthropicInvokeStartTag      = "<invoke"
	anthropicInvokeEndTag        = "</invoke>"
	anthropicEscapedInvokeStart  = "&lt;invoke"
	anthropicEscapedInvokeEnd    = "&lt;/invoke&gt;"
	anthropicParameterEndTag     = "</parameter>"
	anthropicXMLToolIDRandomSize = 12
)

func shouldBridgeAnthropicXMLInvoke(ctx context.Context) bool {
	// Real Claude Code clients already consume native Anthropic tool_use events.
	// Converting model text like <invoke name="Read"> into a tool_use can make
	// Claude Code execute unintended repeated tool calls.
	if !IsClaudeCodeClient(ctx) {
		return true
	}
	// Claude Desktop 3P / Agent SDK clients identify with claude-cli but surface
	// XML invoke text instead of executing it unless the gateway bridges it back
	// to Anthropic tool_use events.
	return shouldBridgeAnthropicXMLInvokeForClaudeCodeUA(ClaudeCodeUserAgent(ctx))
}

func shouldBridgeAnthropicXMLInvokeForClaudeCodeUA(ua string) bool {
	normalized := strings.ToLower(strings.TrimSpace(ua))
	return strings.Contains(normalized, "external, cli") ||
		strings.Contains(normalized, "claude-desktop-3p") ||
		strings.Contains(normalized, "agent-sdk/")
}

type anthropicXMLInvokeCall struct {
	name  string
	input map[string]any
}

type anthropicXMLInvokePart struct {
	text string
	call *anthropicXMLInvokeCall
}

func normalizeAnthropicXMLInvokeResponseBody(body []byte) ([]byte, bool) {
	if !json.Valid(body) || !bodyMayContainAnthropicXMLInvoke(body) {
		return body, false
	}

	content := gjson.GetBytes(body, "content")
	if !content.IsArray() {
		return body, false
	}

	var blocks []map[string]any
	changed := false
	content.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() != "text" {
			var block map[string]any
			if err := json.Unmarshal([]byte(item.Raw), &block); err == nil {
				blocks = append(blocks, block)
			}
			return true
		}

		text := item.Get("text").String()
		parts, pending := drainAnthropicXMLInvokeParts(text)
		if !anthropicXMLInvokePartsContainCall(parts) {
			var block map[string]any
			if err := json.Unmarshal([]byte(item.Raw), &block); err == nil {
				blocks = append(blocks, block)
			}
			return true
		}

		changed = true
		for _, part := range parts {
			if part.call != nil {
				blocks = append(blocks, map[string]any{
					"type":  "tool_use",
					"id":    generateAnthropicXMLInvokeToolID(part.call.name),
					"name":  part.call.name,
					"input": part.call.input,
				})
				continue
			}
			if part.text != "" {
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": part.text,
				})
			}
		}
		if pending != "" {
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": pending,
			})
		}
		return true
	})

	if !changed {
		return body, false
	}
	updated, err := sjson.SetBytes(body, "content", blocks)
	if err != nil {
		return body, false
	}
	updated, _ = sjson.SetBytes(updated, "stop_reason", "tool_use")
	return updated, true
}

type anthropicXMLInvokeStreamNormalizer struct {
	activeTextBlocks       map[int]*anthropicXMLInvokeStreamTextBlock
	syntheticClosedIndexes map[int]struct{}
	pendingText            string
	indexShift             int
	sawToolUse             bool
}

type anthropicXMLInvokeStreamTextBlock struct {
	upstreamIndex   int
	downstreamIndex int
	hasStart        bool
	closed          bool
}

func newAnthropicXMLInvokeStreamNormalizer() *anthropicXMLInvokeStreamNormalizer {
	return &anthropicXMLInvokeStreamNormalizer{
		activeTextBlocks:       make(map[int]*anthropicXMLInvokeStreamTextBlock),
		syntheticClosedIndexes: make(map[int]struct{}),
	}
}

func (n *anthropicXMLInvokeStreamNormalizer) handleEvent(event map[string]any) ([]map[string]any, bool, bool) {
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
	case "message_delta":
		return n.handleMessageDelta(event)
	default:
		return nil, false, false
	}
}

func (n *anthropicXMLInvokeStreamNormalizer) flushPendingEvents() []map[string]any {
	if n == nil || len(n.activeTextBlocks) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(n.activeTextBlocks))
	for index := range n.activeTextBlocks {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	var out []map[string]any
	for _, index := range indexes {
		stopEvent := map[string]any{
			"type":  "content_block_stop",
			"index": index,
		}
		generated, handled, changed := n.handleEvent(stopEvent)
		if handled {
			out = append(out, generated...)
			continue
		}
		if changed {
			out = append(out, stopEvent)
		}
	}
	return out
}

func (n *anthropicXMLInvokeStreamNormalizer) handleContentBlockStart(event map[string]any) ([]map[string]any, bool, bool) {
	index, ok := anthropicSSEEventIndex(event)
	if !ok {
		return nil, false, false
	}
	downstreamIndex := index + n.indexShift
	if downstreamIndex != index {
		event["index"] = downstreamIndex
	}
	contentBlock, ok := event["content_block"].(map[string]any)
	if !ok || contentBlock["type"] != "text" {
		delete(n.activeTextBlocks, index)
		return nil, false, downstreamIndex != index
	}
	n.activeTextBlocks[index] = &anthropicXMLInvokeStreamTextBlock{
		upstreamIndex:   index,
		downstreamIndex: downstreamIndex,
		hasStart:        true,
	}
	initialText := askUserQuestionStringFromAny(contentBlock["text"])
	if initialText != "" && shouldBufferAnthropicXMLInvokeText(initialText) {
		startEvent := cloneAnthropicSSEEvent(event)
		if startBlock, ok := startEvent["content_block"].(map[string]any); ok {
			startBlock["text"] = ""
		}
		deltaEvent := map[string]any{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type": "text_delta",
				"text": initialText,
			},
		}
		generated, handled, changed := n.handleContentBlockDelta(deltaEvent)
		if handled {
			if len(generated) > 0 && generated[0]["type"] == "content_block_stop" {
				if n.indexShift > 0 {
					n.indexShift--
				}
				return remapAnthropicSSEEventIndexes(generated[1:], -1), true, true
			}
			return append([]map[string]any{startEvent}, generated...), true, true
		}
		if changed {
			return []map[string]any{startEvent, deltaEvent}, true, true
		}
	}
	return nil, false, downstreamIndex != index
}

func (n *anthropicXMLInvokeStreamNormalizer) handleContentBlockDelta(event map[string]any) ([]map[string]any, bool, bool) {
	index, ok := anthropicSSEEventIndex(event)
	if !ok {
		return nil, false, false
	}
	block, ok := n.activeTextBlocks[index]
	if !ok {
		return n.handleSyntheticContentBlockDelta(index, event)
	}
	event["index"] = block.downstreamIndex
	delta, ok := event["delta"].(map[string]any)
	if !ok || delta["type"] != "text_delta" {
		return nil, false, block.downstreamIndex != index
	}
	text := askUserQuestionStringFromAny(delta["text"])
	if text == "" && n.pendingText == "" {
		return nil, false, block.downstreamIndex != index
	}

	combined := n.pendingText + text
	n.pendingText = ""
	parts, pending := drainAnthropicXMLInvokeParts(combined)
	hasCall := anthropicXMLInvokePartsContainCall(parts)
	if !hasCall && pending == "" {
		if isAnthropicXMLInvokePreambleOnly(combined) {
			n.pendingText = combined
			return nil, true, true
		}
		if combined == text {
			return nil, false, block.downstreamIndex != index
		}
		delta["text"] = combined
		return nil, false, true
	}

	generated := make([]map[string]any, 0, len(parts)*3+1)
	for _, part := range parts {
		if part.call == nil {
			if part.text == "" {
				continue
			}
			if block.closed {
				textIndex := n.nextInsertedBlockIndex(block.upstreamIndex)
				generated = append(generated, anthropicXMLInvokeTextBlockEvents(part.text, textIndex)...)
				continue
			}
			textEvent := cloneAnthropicSSEEvent(event)
			if textDelta, ok := textEvent["delta"].(map[string]any); ok {
				textDelta["text"] = part.text
				generated = append(generated, textEvent)
			}
			continue
		}
		if !block.closed {
			if block.hasStart {
				generated = append(generated, map[string]any{
					"type":  "content_block_stop",
					"index": block.downstreamIndex,
				})
			}
			block.closed = true
		}
		toolIndex := n.nextInsertedBlockIndex(block.upstreamIndex)
		n.sawToolUse = true
		generated = append(generated, anthropicXMLInvokeToolUseEvents(*part.call, toolIndex)...)
	}
	n.pendingText = pending
	if len(generated) > 0 {
		n.syntheticClosedIndexes[index] = struct{}{}
	}
	return generated, true, true
}

func (n *anthropicXMLInvokeStreamNormalizer) handleSyntheticContentBlockDelta(index int, event map[string]any) ([]map[string]any, bool, bool) {
	downstreamIndex := index + n.indexShift
	delta, ok := event["delta"].(map[string]any)
	if !ok || delta["type"] != "text_delta" {
		if downstreamIndex != index {
			event["index"] = downstreamIndex
			return nil, false, true
		}
		return nil, false, false
	}
	text := askUserQuestionStringFromAny(delta["text"])
	if text == "" && n.pendingText == "" {
		if downstreamIndex != index {
			event["index"] = downstreamIndex
			return nil, false, true
		}
		return nil, false, false
	}

	combined := n.pendingText + text
	n.pendingText = ""
	parts, pending := drainAnthropicXMLInvokeParts(combined)
	hasCall := anthropicXMLInvokePartsContainCall(parts)
	if !hasCall && pending == "" {
		if isAnthropicXMLInvokePreambleOnly(combined) {
			n.pendingText = combined
			return nil, true, true
		}
		if combined == text {
			if downstreamIndex != index {
				event["index"] = downstreamIndex
				return nil, false, true
			}
			return nil, false, false
		}
		event["index"] = downstreamIndex
		delta["text"] = combined
		return nil, false, true
	}

	generated := make([]map[string]any, 0, len(parts)*3+1)
	nextSyntheticIndex := func() int {
		current := index + n.indexShift
		n.indexShift++
		return current
	}
	for _, part := range parts {
		if part.call == nil {
			if part.text == "" {
				continue
			}
			generated = append(generated, anthropicXMLInvokeTextBlockEvents(part.text, nextSyntheticIndex())...)
			continue
		}
		n.sawToolUse = true
		generated = append(generated, anthropicXMLInvokeToolUseEvents(*part.call, nextSyntheticIndex())...)
	}
	n.pendingText = pending
	return generated, true, true
}

func (n *anthropicXMLInvokeStreamNormalizer) handleContentBlockStop(event map[string]any) ([]map[string]any, bool, bool) {
	index, ok := anthropicSSEEventIndex(event)
	if !ok {
		return nil, false, false
	}
	block, ok := n.activeTextBlocks[index]
	if !ok {
		if n.pendingText != "" {
			pending := n.pendingText
			n.pendingText = ""
			n.syntheticClosedIndexes[index] = struct{}{}
			return anthropicXMLInvokeTextBlockEvents(pending, index+n.indexShift), true, true
		}
		if _, closed := n.syntheticClosedIndexes[index]; closed {
			delete(n.syntheticClosedIndexes, index)
			return nil, true, true
		}
		return nil, false, false
	}
	delete(n.activeTextBlocks, index)
	if n.pendingText != "" {
		pending := n.pendingText
		n.pendingText = ""
		if block.closed {
			textIndex := n.nextInsertedBlockIndex(block.upstreamIndex)
			return []map[string]any{
				{
					"type":  "content_block_start",
					"index": textIndex,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				},
				{
					"type":  "content_block_delta",
					"index": textIndex,
					"delta": map[string]any{
						"type": "text_delta",
						"text": pending,
					},
				},
				{
					"type":  "content_block_stop",
					"index": textIndex,
				},
			}, true, true
		}
		event["index"] = block.downstreamIndex
		return []map[string]any{
			{
				"type":  "content_block_delta",
				"index": block.downstreamIndex,
				"delta": map[string]any{
					"type": "text_delta",
					"text": pending,
				},
			},
			event,
		}, true, true
	}
	if block.closed {
		return nil, true, true
	}
	if block.downstreamIndex != index {
		event["index"] = block.downstreamIndex
		return nil, false, true
	}
	return nil, false, false
}

func (n *anthropicXMLInvokeStreamNormalizer) handleMessageDelta(event map[string]any) ([]map[string]any, bool, bool) {
	if n == nil || !n.sawToolUse || event == nil {
		return nil, false, false
	}
	delta, ok := event["delta"].(map[string]any)
	if !ok {
		return nil, false, false
	}
	stopReason, _ := delta["stop_reason"].(string)
	if stopReason != "" && stopReason != "end_turn" {
		return nil, false, false
	}
	delta["stop_reason"] = "tool_use"
	return nil, false, true
}

func (n *anthropicXMLInvokeStreamNormalizer) nextInsertedBlockIndex(upstreamIndex int) int {
	n.indexShift++
	return upstreamIndex + n.indexShift
}

func drainAnthropicXMLInvokeText(text string) (string, []anthropicXMLInvokeCall, string) {
	parts, pending := drainAnthropicXMLInvokeParts(text)
	var out strings.Builder
	var calls []anthropicXMLInvokeCall
	for _, part := range parts {
		if part.call != nil {
			calls = append(calls, *part.call)
			continue
		}
		_, _ = out.WriteString(part.text)
	}
	return out.String(), calls, pending
}

func drainAnthropicXMLInvokeParts(text string) ([]anthropicXMLInvokePart, string) {
	var parts []anthropicXMLInvokePart
	index := 0
	for index < len(text) {
		rawStart := strings.Index(text[index:], anthropicInvokeStartTag)
		escapedStart := strings.Index(text[index:], anthropicEscapedInvokeStart)
		if rawStart == -1 && escapedStart == -1 {
			if text[index:] != "" {
				visible, pending := splitAnthropicXMLInvokeStartPrefix(text[index:])
				if visible != "" {
					parts = append(parts, anthropicXMLInvokePart{text: visible})
				}
				if pending != "" {
					return parts, pending
				}
			}
			break
		}

		escaped := false
		startRel := rawStart
		if startRel == -1 || (escapedStart != -1 && escapedStart < startRel) {
			startRel = escapedStart
			escaped = true
		}
		start := index + startRel

		if text[index:start] != "" {
			if visible := stripTrailingAnthropicXMLInvokePreamble(text[index:start]); visible != "" {
				parts = append(parts, anthropicXMLInvokePart{text: visible})
			}
		}
		if escaped {
			call, end, ok := parseEscapedAnthropicXMLInvokeAt(text, start)
			if !ok {
				return parts, text[start:]
			}
			callCopy := call
			parts = append(parts, anthropicXMLInvokePart{call: &callCopy})
			index = end
			continue
		}

		call, end, ok := parseAnthropicXMLInvokeAt(text, start)
		if !ok {
			return parts, text[start:]
		}
		callCopy := call
		parts = append(parts, anthropicXMLInvokePart{call: &callCopy})
		index = end
	}
	return parts, ""
}

func splitAnthropicXMLInvokeStartPrefix(text string) (string, string) {
	if text == "" {
		return "", ""
	}
	pendingLen := longestAnthropicXMLInvokeStartPrefixSuffix(text)
	if pendingLen == 0 {
		return text, ""
	}
	return stripTrailingAnthropicXMLInvokePreamble(text[:len(text)-pendingLen]), text[len(text)-pendingLen:]
}

func stripTrailingAnthropicXMLInvokePreamble(text string) string {
	if text == "" {
		return ""
	}
	trimmedRight := strings.TrimRightFunc(text, unicode.IsSpace)
	if !strings.EqualFold(trimmedRight, "call") {
		lastNewline := strings.LastIndexAny(trimmedRight, "\r\n")
		if lastNewline < 0 {
			return text
		}
		if !strings.EqualFold(strings.TrimSpace(trimmedRight[lastNewline+1:]), "call") {
			return text
		}
		return trimmedRight[:lastNewline+1]
	}
	return ""
}

func isAnthropicXMLInvokePreambleOnly(text string) bool {
	return strings.EqualFold(strings.TrimSpace(text), "call")
}

func shouldBufferAnthropicXMLInvokeText(text string) bool {
	if text == "" {
		return false
	}
	return bodyMayContainAnthropicXMLInvoke([]byte(text)) ||
		isAnthropicXMLInvokePreambleOnly(text) ||
		longestAnthropicXMLInvokeStartPrefixSuffix(text) > 0
}

func longestAnthropicXMLInvokeStartPrefixSuffix(text string) int {
	longest := 0
	for _, marker := range []string{anthropicInvokeStartTag, anthropicEscapedInvokeStart} {
		maxLen := len(marker) - 1
		if len(text) < maxLen {
			maxLen = len(text)
		}
		for n := 1; n <= maxLen; n++ {
			if n > longest && strings.HasSuffix(text, marker[:n]) {
				longest = n
			}
		}
	}
	return longest
}

func anthropicXMLInvokePartsContainCall(parts []anthropicXMLInvokePart) bool {
	for _, part := range parts {
		if part.call != nil {
			return true
		}
	}
	return false
}

func parseAnthropicXMLInvokeAt(text string, start int) (anthropicXMLInvokeCall, int, bool) {
	if start < 0 || start >= len(text) || !strings.HasPrefix(text[start:], anthropicInvokeStartTag) {
		return anthropicXMLInvokeCall{}, 0, false
	}
	tagEndRel := strings.Index(text[start:], ">")
	if tagEndRel == -1 {
		return anthropicXMLInvokeCall{}, 0, false
	}
	tagEnd := start + tagEndRel
	openTag := text[start : tagEnd+1]
	name := strings.TrimSpace(parseAnthropicXMLAttribute(openTag, "name"))
	if name == "" {
		return anthropicXMLInvokeCall{}, 0, false
	}
	bodyStart := tagEnd + 1
	closeRel := strings.Index(text[bodyStart:], anthropicInvokeEndTag)
	if closeRel == -1 {
		return anthropicXMLInvokeCall{}, 0, false
	}
	bodyEnd := bodyStart + closeRel
	return anthropicXMLInvokeCall{
		name:  name,
		input: parseAnthropicXMLInvokeParameters(text[bodyStart:bodyEnd]),
	}, bodyEnd + len(anthropicInvokeEndTag), true
}

func parseEscapedAnthropicXMLInvokeAt(text string, start int) (anthropicXMLInvokeCall, int, bool) {
	if start < 0 || start >= len(text) || !strings.HasPrefix(text[start:], anthropicEscapedInvokeStart) {
		return anthropicXMLInvokeCall{}, 0, false
	}
	closeRel := strings.Index(text[start:], anthropicEscapedInvokeEnd)
	if closeRel == -1 {
		return anthropicXMLInvokeCall{}, 0, false
	}
	end := start + closeRel + len(anthropicEscapedInvokeEnd)
	unescaped := unescapeAnthropicXML(text[start:end])
	call, parsedEnd, ok := parseAnthropicXMLInvokeAt(unescaped, 0)
	if !ok || parsedEnd != len(unescaped) {
		return anthropicXMLInvokeCall{}, 0, false
	}
	return call, end, true
}

func parseAnthropicXMLInvokeParameters(body string) map[string]any {
	input := map[string]any{}
	searchFrom := 0
	for {
		startRel := strings.Index(body[searchFrom:], "<parameter")
		if startRel == -1 {
			break
		}
		start := searchFrom + startRel
		tagEndRel := strings.Index(body[start:], ">")
		if tagEndRel == -1 {
			break
		}
		tagEnd := start + tagEndRel
		name := strings.TrimSpace(parseAnthropicXMLAttribute(body[start:tagEnd+1], "name"))
		contentStart := tagEnd + 1
		closeRel := strings.Index(body[contentStart:], anthropicParameterEndTag)
		if closeRel == -1 {
			break
		}
		contentEnd := contentStart + closeRel
		if name != "" {
			input[name] = unescapeAnthropicXML(strings.TrimSpace(body[contentStart:contentEnd]))
		}
		searchFrom = contentEnd + len(anthropicParameterEndTag)
	}
	return input
}

func parseAnthropicXMLAttribute(tag, attr string) string {
	idx := strings.Index(tag, attr)
	for idx >= 0 {
		if idx > 0 && isAnthropicXMLNameChar(rune(tag[idx-1])) {
			next := strings.Index(tag[idx+len(attr):], attr)
			if next == -1 {
				return ""
			}
			idx += len(attr) + next
			continue
		}
		pos := idx + len(attr)
		for pos < len(tag) && unicode.IsSpace(rune(tag[pos])) {
			pos++
		}
		if pos >= len(tag) || tag[pos] != '=' {
			next := strings.Index(tag[pos:], attr)
			if next == -1 {
				return ""
			}
			idx = pos + next
			continue
		}
		pos++
		for pos < len(tag) && unicode.IsSpace(rune(tag[pos])) {
			pos++
		}
		if pos >= len(tag) {
			return ""
		}
		quote := tag[pos]
		if quote != '"' && quote != '\'' {
			return ""
		}
		pos++
		end := strings.IndexByte(tag[pos:], quote)
		if end == -1 {
			return ""
		}
		return unescapeAnthropicXML(tag[pos : pos+end])
	}
	return ""
}

func unescapeAnthropicXML(text string) string {
	replacer := strings.NewReplacer(
		"&lt;", "<",
		"&gt;", ">",
		"&amp;", "&",
		"&quot;", `"`,
		"&#34;", `"`,
		"&apos;", "'",
		"&#39;", "'",
	)
	return replacer.Replace(text)
}

func isAnthropicXMLNameChar(r rune) bool {
	return r == '_' || r == '-' || r == ':' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func anthropicXMLInvokeToolUseEvents(call anthropicXMLInvokeCall, index int) []map[string]any {
	toolID := generateAnthropicXMLInvokeToolID(call.name)
	inputJSON, _ := json.Marshal(call.input)
	return []map[string]any{
		{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    toolID,
				"name":  call.name,
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
}

func anthropicXMLInvokeTextBlockEvents(text string, index int) []map[string]any {
	return []map[string]any{
		{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		},
		{
			"type":  "content_block_delta",
			"index": index,
			"delta": map[string]any{
				"type": "text_delta",
				"text": text,
			},
		},
		{
			"type":  "content_block_stop",
			"index": index,
		},
	}
}

func cloneAnthropicSSEEvent(event map[string]any) map[string]any {
	data, err := json.Marshal(event)
	if err != nil {
		return event
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return event
	}
	return cloned
}

func remapAnthropicSSEEventIndexes(events []map[string]any, offset int) []map[string]any {
	if offset == 0 || len(events) == 0 {
		return events
	}
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		cloned := cloneAnthropicSSEEvent(event)
		if index, ok := anthropicSSEEventIndex(cloned); ok {
			cloned["index"] = index + offset
		}
		out = append(out, cloned)
	}
	return out
}

func generateAnthropicXMLInvokeToolID(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	return fmt.Sprintf("toolu_%s_%s", sanitizeAnthropicXMLInvokeToolName(name), randomAnthropicXMLInvokeID(anthropicXMLToolIDRandomSize))
}

func sanitizeAnthropicXMLInvokeToolName(name string) string {
	var out strings.Builder
	for _, r := range name {
		if r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			_, _ = out.WriteRune(r)
		}
	}
	if out.Len() == 0 {
		return "tool"
	}
	return out.String()
}

func bodyMayContainAnthropicXMLInvoke(body []byte) bool {
	text := string(body)
	return strings.Contains(text, "<invoke") ||
		strings.Contains(text, "&lt;invoke") ||
		strings.Contains(text, `\u003cinvoke`) ||
		strings.Contains(text, `\u003c/`) ||
		strings.Contains(text, `\u003cparameter`)
}

func randomAnthropicXMLInvokeID(nBytes int) string {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(buf)
}
