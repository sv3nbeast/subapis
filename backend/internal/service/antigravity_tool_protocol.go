package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

type antigravityPendingToolUse struct {
	messageIndex int
	toolUseIDs   map[string]struct{}
}

// normalizeClaudeToolProtocolForAntigravity 对 Claude 工具链历史做轻量且严格的邻接修复。
// 只处理会导致 Cloud Code 拒绝的明显异常：
// 1. 没有紧邻前置 tool_use 的 orphan tool_result 会被降级为 text
// 2. assistant 发出 tool_use 后，下一条若不是 user/tool_result，则把该 tool_use 降级为 text
// 3. 多 tool_use 场景下，只保留已收到 tool_result 的那部分，缺失响应的 tool_use 降级为 text
func normalizeClaudeToolProtocolForAntigravity(req *antigravity.ClaudeRequest) (bool, error) {
	if req == nil || len(req.Messages) == 0 {
		return false, nil
	}

	type parsedMessage struct {
		hasBlocks bool
		blocks    []antigravity.ContentBlock
	}

	parsed := make([]parsedMessage, len(req.Messages))
	for i, msg := range req.Messages {
		if len(msg.Content) == 0 {
			continue
		}

		var textContent string
		if err := json.Unmarshal(msg.Content, &textContent); err == nil {
			continue
		}

		var blocks []antigravity.ContentBlock
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			return false, fmt.Errorf("parse content blocks for message %d: %w", i, err)
		}
		parsed[i] = parsedMessage{
			hasBlocks: true,
			blocks:    blocks,
		}
	}

	changed := false
	var pending *antigravityPendingToolUse

	for i := range req.Messages {
		handledPendingUserMessage := false
		if pending != nil {
			if req.Messages[i].Role != "user" {
				if downgradePendingToolUsesToText(parsed[pending.messageIndex].blocks, pending.toolUseIDs) {
					changed = true
				}
				pending = nil
			} else {
				matchedIDs := make(map[string]struct{})
				if parsed[i].hasBlocks {
					if downgradeOrphanToolResultsToText(parsed[i].blocks, pending.toolUseIDs, matchedIDs) {
						changed = true
					}
				}
				if downgradeMissingToolUseResponsesToText(parsed[pending.messageIndex].blocks, pending.toolUseIDs, matchedIDs) {
					changed = true
				}
				handledPendingUserMessage = true
				pending = nil
			}
		}

		if parsed[i].hasBlocks && !handledPendingUserMessage && downgradeOrphanToolResultsToText(parsed[i].blocks, nil, nil) {
			changed = true
		}

		if req.Messages[i].Role != "assistant" || !parsed[i].hasBlocks {
			continue
		}

		if toolUseIDs := collectToolUseIDs(parsed[i].blocks); len(toolUseIDs) > 0 {
			pending = &antigravityPendingToolUse{
				messageIndex: i,
				toolUseIDs:   toolUseIDs,
			}
		}
	}

	if pending != nil && downgradePendingToolUsesToText(parsed[pending.messageIndex].blocks, pending.toolUseIDs) {
		changed = true
	}

	if !changed {
		return false, nil
	}

	for i := range parsed {
		if !parsed[i].hasBlocks {
			continue
		}
		normalized, err := json.Marshal(parsed[i].blocks)
		if err != nil {
			return false, fmt.Errorf("marshal normalized content blocks for message %d: %w", i, err)
		}
		req.Messages[i].Content = normalized
	}

	return true, nil
}

func collectToolUseIDs(blocks []antigravity.ContentBlock) map[string]struct{} {
	toolUseIDs := make(map[string]struct{})
	for _, block := range blocks {
		if block.Type != "tool_use" {
			continue
		}
		toolID := strings.TrimSpace(block.ID)
		if toolID == "" {
			continue
		}
		toolUseIDs[toolID] = struct{}{}
	}
	if len(toolUseIDs) == 0 {
		return nil
	}
	return toolUseIDs
}

func downgradePendingToolUsesToText(blocks []antigravity.ContentBlock, pending map[string]struct{}) bool {
	return downgradeMissingToolUseResponsesToText(blocks, pending, nil)
}

func downgradeMissingToolUseResponsesToText(blocks []antigravity.ContentBlock, pending map[string]struct{}, matched map[string]struct{}) bool {
	if len(pending) == 0 {
		return false
	}
	changed := false
	for i := range blocks {
		if blocks[i].Type != "tool_use" {
			continue
		}
		toolID := strings.TrimSpace(blocks[i].ID)
		if toolID == "" {
			continue
		}
		if _, exists := pending[toolID]; !exists {
			continue
		}
		if matched != nil {
			if _, ok := matched[toolID]; ok {
				continue
			}
		}
		blocks[i] = toolUseBlockAsText(blocks[i])
		changed = true
	}
	return changed
}

func downgradeOrphanToolResultsToText(blocks []antigravity.ContentBlock, pending map[string]struct{}, matched map[string]struct{}) bool {
	changed := false
	for i := range blocks {
		if blocks[i].Type != "tool_result" {
			continue
		}
		toolUseID := strings.TrimSpace(blocks[i].ToolUseID)
		if len(pending) > 0 {
			if _, ok := pending[toolUseID]; ok {
				if matched != nil && toolUseID != "" {
					matched[toolUseID] = struct{}{}
				}
				continue
			}
		}
		blocks[i] = toolResultBlockAsText(blocks[i])
		changed = true
	}
	return changed
}

func toolUseBlockAsText(block antigravity.ContentBlock) antigravity.ContentBlock {
	text := "(tool_use)"
	if name := strings.TrimSpace(block.Name); name != "" {
		text += " name=" + name
	}
	if id := strings.TrimSpace(block.ID); id != "" {
		text += " id=" + id
	}
	if inputBytes, err := json.Marshal(block.Input); err == nil {
		inputText := strings.TrimSpace(string(inputBytes))
		if inputText != "" && inputText != "null" {
			text += " input=" + inputText
		}
	}
	return antigravity.ContentBlock{
		Type: "text",
		Text: text,
	}
}

func toolResultBlockAsText(block antigravity.ContentBlock) antigravity.ContentBlock {
	text := "(tool_result)"
	if toolUseID := strings.TrimSpace(block.ToolUseID); toolUseID != "" {
		text += " tool_use_id=" + toolUseID
	}
	resultText := stringifyAntigravityToolResultContent(block.Content)
	if resultText != "" {
		text += "\n" + resultText
	}
	return antigravity.ContentBlock{
		Type: "text",
		Text: text,
	}
}

func stringifyAntigravityToolResultContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}

	var str string
	if err := json.Unmarshal(content, &str); err == nil {
		return strings.TrimSpace(str)
	}

	var blocks []map[string]any
	if err := json.Unmarshal(content, &blocks); err == nil {
		var texts []string
		for _, block := range blocks {
			if text, ok := block["text"].(string); ok {
				text = strings.TrimSpace(text)
				if text != "" {
					texts = append(texts, text)
				}
			}
		}
		return strings.Join(texts, "\n")
	}

	return strings.TrimSpace(string(content))
}
