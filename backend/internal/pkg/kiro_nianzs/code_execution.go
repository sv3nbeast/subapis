package kiro

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

const (
	legacyCodeExecutionToolType = "code_execution_20250522"
	codeExecutionToolName       = "code_execution"
)

// CodeExecutionResult is the protocol-neutral result returned by the isolated
// execution worker. The legacy Anthropic server tool exposes the same fields.
type CodeExecutionResult struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ReturnCode int    `json:"return_code"`
	ErrorCode  string `json:"error_code,omitempty"`
}

type CodeExecutionCall struct {
	ToolUseID string
	Code      string
	Index     int
}

type CodeExecutionIndicator struct {
	ServerToolUseID string
	Code            string
	Result          CodeExecutionResult
	Before          []any
	After           []any
}

// IsOnlyLegacyCodeExecutionTool reports whether the request is the legacy
// Python-only Anthropic server tool. Mixed server/client tool turns deliberately
// keep their original client-driven semantics instead of being partially run.
func IsOnlyLegacyCodeExecutionTool(body []byte) bool {
	tools := gjson.GetBytes(body, "tools")
	if !tools.IsArray() {
		return false
	}
	items := tools.Array()
	if len(items) != 1 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(items[0].Get("type").String()), legacyCodeExecutionToolType) &&
		strings.EqualFold(strings.TrimSpace(items[0].Get("name").String()), codeExecutionToolName)
}

// ReplaceLegacyCodeExecutionTool converts Anthropic's server-tool declaration
// into a regular Kiro tool. Kiro can then choose/write the Python program while
// Sub2API executes it in its isolated worker and closes the server-side loop.
func ReplaceLegacyCodeExecutionTool(body []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, err
	}
	rawTools, ok := payload["tools"].([]any)
	if !ok || len(rawTools) != 1 {
		return body, nil
	}
	tool, ok := rawTools[0].(map[string]any)
	if !ok || !strings.EqualFold(getInterfaceString(tool["type"]), legacyCodeExecutionToolType) ||
		!strings.EqualFold(getInterfaceString(tool["name"]), codeExecutionToolName) {
		return body, nil
	}
	payload["tools"] = []any{map[string]any{
		"name": codeExecutionToolName,
		"description": "Execute Python code in an isolated, network-disabled sandbox. " +
			"Pass the complete Python program in the code field and use the returned stdout and stderr.",
		"input_schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "Complete Python program to execute",
				},
			},
			"required":             []string{"code"},
			"additionalProperties": false,
		},
	}}
	return json.Marshal(payload)
}

// AnalyzeCodeExecutionBufferedStream extracts a complete Kiro-emitted custom
// tool call from translated Anthropic SSE chunks.
func AnalyzeCodeExecutionBufferedStream(chunks [][]byte) (CodeExecutionCall, bool) {
	call := CodeExecutionCall{Index: -1}
	currentIndex := -1
	currentID := ""
	currentName := ""
	var input strings.Builder

	for _, chunk := range chunks {
		for _, line := range strings.Split(string(chunk), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var event map[string]any
			if json.Unmarshal([]byte(payload), &event) != nil {
				continue
			}
			switch eventType, _ := event["type"].(string); eventType {
			case "content_block_start":
				block, _ := event["content_block"].(map[string]any)
				if getInterfaceString(block["type"]) != "tool_use" {
					continue
				}
				currentName = strings.ToLower(getInterfaceString(block["name"]))
				currentID = getInterfaceString(block["id"])
				currentIndex = jsonInt(event["index"], -1)
				input.Reset()
			case "content_block_delta":
				if currentName != codeExecutionToolName {
					continue
				}
				delta, _ := event["delta"].(map[string]any)
				if getInterfaceString(delta["type"]) == "input_json_delta" {
					_, _ = input.WriteString(getInterfaceString(delta["partial_json"]))
				}
			case "content_block_stop":
				if currentName != codeExecutionToolName {
					currentName, currentID, currentIndex = "", "", -1
					input.Reset()
					continue
				}
				var args struct {
					Code string `json:"code"`
				}
				if json.Unmarshal([]byte(input.String()), &args) == nil && strings.TrimSpace(args.Code) != "" {
					return CodeExecutionCall{ToolUseID: currentID, Code: args.Code, Index: currentIndex}, true
				}
				currentName, currentID, currentIndex = "", "", -1
				input.Reset()
			}
		}
	}
	return call, false
}

func ExtractCodeExecutionToolUseFromResponse(response []byte) (CodeExecutionCall, bool) {
	call, _, _, ok := ExtractCodeExecutionTurnFromResponse(response)
	return call, ok

}

// ExtractCodeExecutionTurnFromResponse retains non-tool content around the
// internal custom-tool call so non-streaming server-tool responses do not lose
// model text or thinking blocks from an intermediate turn.
func ExtractCodeExecutionTurnFromResponse(response []byte) (CodeExecutionCall, []any, []any, bool) {
	var payload map[string]any
	if json.Unmarshal(response, &payload) != nil {
		return CodeExecutionCall{Index: -1}, nil, nil, false
	}
	content, ok := payload["content"].([]any)
	if !ok {
		return CodeExecutionCall{Index: -1}, nil, nil, false
	}
	for i, rawBlock := range content {
		block, _ := rawBlock.(map[string]any)
		if getInterfaceString(block["type"]) != "tool_use" || !strings.EqualFold(getInterfaceString(block["name"]), codeExecutionToolName) {
			continue
		}
		input, _ := block["input"].(map[string]any)
		code := getInterfaceString(input["code"])
		if strings.TrimSpace(code) == "" {
			continue
		}
		before := append([]any(nil), content[:i]...)
		after := append([]any(nil), content[i+1:]...)
		return CodeExecutionCall{ToolUseID: getInterfaceString(block["id"]), Code: code, Index: i}, before, after, true
	}
	return CodeExecutionCall{Index: -1}, nil, nil, false
}

// InjectCodeExecutionResultClaude appends the client-tool-shaped exchange Kiro
// understands. This exchange stays upstream-only; client responses are rendered
// as Anthropic server_tool_use/result blocks by the gateway.
func InjectCodeExecutionResultClaude(body []byte, call CodeExecutionCall, result CodeExecutionResult) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body, fmt.Errorf("parse code execution payload: %w", err)
	}
	messages, ok := payload["messages"].([]any)
	if !ok {
		return body, fmt.Errorf("code execution payload missing messages array")
	}
	resultJSON, err := json.Marshal(map[string]any{
		"stdout":      result.Stdout,
		"stderr":      result.Stderr,
		"return_code": result.ReturnCode,
		"error_code":  result.ErrorCode,
	})
	if err != nil {
		return body, err
	}
	messages = append(messages,
		map[string]any{
			"role": "assistant",
			"content": []any{map[string]any{
				"type":  "tool_use",
				"id":    call.ToolUseID,
				"name":  codeExecutionToolName,
				"input": map[string]any{"code": call.Code},
			}},
		},
		map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":        "tool_result",
				"tool_use_id": call.ToolUseID,
				"content":     string(resultJSON),
				"is_error":    result.ErrorCode != "",
			}},
		},
	)
	payload["messages"] = messages
	return json.Marshal(payload)
}

func GenerateCodeExecutionToolUseEvents(code, serverToolUseID string, index int) [][]byte {
	inputJSON, _ := json.Marshal(map[string]string{"code": code})
	return marshalSSEEvents([]map[string]any{
		{
			"type": "content_block_start", "index": index,
			"content_block": map[string]any{
				"type": "server_tool_use", "id": serverToolUseID,
				"name": codeExecutionToolName, "input": map[string]any{},
			},
		},
		{
			"type": "content_block_delta", "index": index,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": string(inputJSON)},
		},
		{"type": "content_block_stop", "index": index},
	})
}

func GenerateCodeExecutionResultEvents(serverToolUseID string, result CodeExecutionResult, index int) [][]byte {
	content := legacyCodeExecutionResultContent(result)
	return marshalSSEEvents([]map[string]any{
		{
			"type": "content_block_start", "index": index,
			"content_block": map[string]any{
				"type": "code_execution_tool_result", "tool_use_id": serverToolUseID,
				"content": content,
			},
		},
		{"type": "content_block_stop", "index": index},
	})
}

func InjectCodeExecutionIndicatorsInResponse(response []byte, indicators []CodeExecutionIndicator) ([]byte, error) {
	if len(indicators) == 0 {
		return response, nil
	}
	var payload map[string]any
	if err := json.Unmarshal(response, &payload); err != nil {
		return response, err
	}
	content, _ := payload["content"].([]any)
	updated := make([]any, 0, len(indicators)*2+len(content))
	for _, indicator := range indicators {
		updated = append(updated, indicator.Before...)
		updated = append(updated,
			map[string]any{
				"type": "server_tool_use", "id": indicator.ServerToolUseID,
				"name": codeExecutionToolName, "input": map[string]any{"code": indicator.Code},
			},
			map[string]any{
				"type": "code_execution_tool_result", "tool_use_id": indicator.ServerToolUseID,
				"content": legacyCodeExecutionResultContent(indicator.Result),
			},
		)
		updated = append(updated, indicator.After...)
	}
	payload["content"] = append(updated, content...)
	usage, _ := payload["usage"].(map[string]any)
	if usage == nil {
		usage = map[string]any{}
		payload["usage"] = usage
	}
	serverUsage, _ := usage["server_tool_use"].(map[string]any)
	if serverUsage == nil {
		serverUsage = map[string]any{}
		usage["server_tool_use"] = serverUsage
	}
	serverUsage["code_execution_requests"] = len(indicators)
	return json.Marshal(payload)
}

func legacyCodeExecutionResultContent(result CodeExecutionResult) map[string]any {
	if strings.TrimSpace(result.ErrorCode) != "" {
		return map[string]any{
			"type":       "code_execution_tool_result_error",
			"error_code": result.ErrorCode,
		}
	}
	return map[string]any{
		"type":        "code_execution_result",
		"stdout":      result.Stdout,
		"stderr":      result.Stderr,
		"return_code": result.ReturnCode,
		"content":     []any{},
	}
}

func marshalSSEEvents(events []map[string]any) [][]byte {
	out := make([][]byte, 0, len(events))
	for _, event := range events {
		typeName := getInterfaceString(event["type"])
		payload, _ := json.Marshal(event)
		out = append(out, []byte("event: "+typeName+"\ndata: "+string(payload)+"\n\n"))
	}
	return out
}

func jsonInt(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return fallback
}
