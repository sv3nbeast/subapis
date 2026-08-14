package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

const kiroCodexCustomToolInputField = "input"

type kiroCodexResponsesToolMetadata struct {
	DeclaredToolCount         int
	ForwardedToolCount        int
	RecoveredHistoryToolCount int
	CustomToolNames           map[string]struct{}
}

// normalizeKiroCodexResponsesTools adapts Codex Responses Lite tool
// declarations for Kiro's Anthropic-shaped tool bridge. Newer Codex clients
// place tools inside input[type=additional_tools] and may group them under a
// namespace. The generic Responses converter intentionally ignores unknown
// input items, so this normalization is scoped to Kiro native GPT requests.
func normalizeKiroCodexResponsesTools(body []byte) ([]byte, kiroCodexResponsesToolMetadata, error) {
	metadata := kiroCodexResponsesToolMetadata{CustomToolNames: make(map[string]struct{})}
	changed := false
	codexLiteAdditionalToolsDetected := false
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, metadata, fmt.Errorf("parse Kiro Codex Responses tools: %w", err)
	}

	declared := make([]any, 0)
	if tools, ok := request["tools"].([]any); ok {
		declared = append(declared, tools...)
	}

	// Responses Lite embeds dynamic tools in the input history. Remove only
	// the declaration carrier after extracting it; all actual conversation
	// items retain their original order. History normalization runs after the
	// declarations are flattened so pre-fix bare names can be repaired only
	// when they resolve to one unambiguous namespace child.
	var filteredInput []any
	inputIsArray := false
	if input, ok := request["input"].([]any); ok {
		inputIsArray = true
		filteredInput = make([]any, 0, len(input))
		for _, rawItem := range input {
			item, isObject := rawItem.(map[string]any)
			if !isObject {
				filteredInput = append(filteredInput, rawItem)
				continue
			}
			typ := strings.TrimSpace(stringValue(item["type"]))
			if typ != "additional_tools" {
				filteredInput = append(filteredInput, rawItem)
				continue
			}
			changed = true
			codexLiteAdditionalToolsDetected = true
			if tools, ok := item["tools"].([]any); ok {
				declared = append(declared, tools...)
			}
		}
		request["input"] = filteredInput
	}

	flattened := make([]any, 0, len(declared))
	for _, rawTool := range declared {
		flattened = append(flattened, flattenKiroCodexResponsesTool(rawTool, "", &metadata)...)
	}
	metadata.ForwardedToolCount = len(flattened)
	if metadata.DeclaredToolCount > 0 && metadata.ForwardedToolCount == 0 {
		return nil, metadata, fmt.Errorf("Kiro Codex Responses tool conversion dropped all %d declared tools", metadata.DeclaredToolCount)
	}
	if inputIsArray {
		historyAliases := buildKiroCodexToolHistoryAliases(flattened)
		for index, rawItem := range filteredInput {
			item, isObject := rawItem.(map[string]any)
			if !isObject {
				continue
			}
			normalizedItem, itemChanged, recovered := normalizeKiroCodexToolHistoryItem(item, historyAliases)
			changed = changed || itemChanged
			if recovered {
				metadata.RecoveredHistoryToolCount++
			}
			filteredInput[index] = normalizedItem
		}
		request["input"] = filteredInput
	}
	if metadata.DeclaredToolCount == 0 && !changed {
		return body, metadata, nil
	}
	if metadata.DeclaredToolCount > 0 {
		request["tools"] = flattened
	}
	if codexLiteAdditionalToolsDetected {
		changed = normalizeKiroCodexResponsesToolChoice(request) || changed
	}
	normalized, err := json.Marshal(request)
	if err != nil {
		return nil, metadata, fmt.Errorf("marshal Kiro Codex Responses tools: %w", err)
	}
	return normalized, metadata, nil
}

func normalizeKiroCodexToolHistoryItem(item map[string]any, historyAliases map[string]string) (map[string]any, bool, bool) {
	typ := strings.TrimSpace(stringValue(item["type"]))
	switch typ {
	case "function_call":
		normalized := cloneStringAnyMap(item)
		changed, recovered := qualifyKiroCodexToolHistoryName(normalized, historyAliases)
		if !changed {
			return item, false, false
		}
		return normalized, true, recovered
	case "custom_tool_call":
		normalized := cloneStringAnyMap(item)
		normalized["type"] = "function_call"
		_, recovered := qualifyKiroCodexToolHistoryName(normalized, historyAliases)
		if input, ok := item["input"].(string); ok {
			normalized["arguments"] = mustJSONText(map[string]any{kiroCodexCustomToolInputField: input})
		}
		delete(normalized, "input")
		return normalized, true, recovered
	case "custom_tool_call_output":
		normalized := cloneStringAnyMap(item)
		normalized["type"] = "function_call_output"
		return normalized, true, false
	default:
		return item, false, false
	}
}

func qualifyKiroCodexToolHistoryName(item map[string]any, historyAliases map[string]string) (bool, bool) {
	// Codex 0.147+ exposes namespace children to the client as separate
	// namespace/name fields. Kiro history and declarations must use the same
	// flattened identity or the translator will inject a second placeholder
	// tool for the client-visible short name on continuation turns.
	namespace := strings.TrimSpace(stringValue(item["namespace"]))
	name := strings.TrimSpace(stringValue(item["name"]))
	if namespace != "" && name != "" {
		item["name"] = joinKiroCodexToolNamespace(namespace, name)
		delete(item, "namespace")
		return true, false
	}
	if namespace != "" || name == "" {
		return false, false
	}
	qualified := strings.TrimSpace(historyAliases[strings.ToLower(name)])
	if qualified == "" || strings.EqualFold(qualified, name) {
		return false, false
	}
	item["name"] = qualified
	delete(item, "namespace")
	return true, true
}

func buildKiroCodexToolHistoryAliases(flattened []any) map[string]string {
	exactNames := make(map[string]struct{}, len(flattened))
	candidates := make(map[string]string)
	ambiguous := make(map[string]struct{})
	for _, rawTool := range flattened {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringValue(tool["name"]))
		if name == "" {
			continue
		}
		exactNames[strings.ToLower(name)] = struct{}{}
		separator := strings.LastIndex(name, "__")
		if separator <= 0 || separator+2 >= len(name) {
			continue
		}
		shortName := strings.TrimSpace(name[separator+2:])
		shortKey := strings.ToLower(shortName)
		if shortKey == "" {
			continue
		}
		if existing, exists := candidates[shortKey]; exists && !strings.EqualFold(existing, name) {
			delete(candidates, shortKey)
			ambiguous[shortKey] = struct{}{}
			continue
		}
		if _, isAmbiguous := ambiguous[shortKey]; !isAmbiguous {
			candidates[shortKey] = name
		}
	}
	for shortKey := range candidates {
		if _, hasExactDeclaration := exactNames[shortKey]; hasExactDeclaration {
			delete(candidates, shortKey)
		}
	}
	return candidates
}

func mustJSONText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func flattenKiroCodexResponsesTool(rawTool any, namespace string, metadata *kiroCodexResponsesToolMetadata) []any {
	tool, ok := rawTool.(map[string]any)
	if !ok {
		return nil
	}
	typ := strings.TrimSpace(stringValue(tool["type"]))
	if typ == "namespace" {
		name := firstNonEmptyStringValue(tool["name"], tool["namespace"])
		prefix := joinKiroCodexToolNamespace(namespace, name)
		children, _ := tool["tools"].([]any)
		var flattened []any
		for _, child := range children {
			flattened = append(flattened, flattenKiroCodexResponsesTool(child, prefix, metadata)...)
		}
		return flattened
	}

	metadata.DeclaredToolCount++
	copyTool := cloneStringAnyMap(tool)
	if function, ok := copyTool["function"].(map[string]any); ok {
		for _, key := range []string{"name", "description", "parameters", "input_schema", "inputSchema"} {
			if _, exists := copyTool[key]; !exists {
				copyTool[key] = function[key]
			}
		}
	}

	name := strings.TrimSpace(firstNonEmptyStringValue(copyTool["name"], copyTool["type"]))
	if name == "" {
		return nil
	}
	name = joinKiroCodexToolNamespace(namespace, name)
	copyTool["name"] = name

	schema := firstNonNil(copyTool["parameters"], copyTool["input_schema"], copyTool["inputSchema"])
	if typ == "custom" {
		metadata.CustomToolNames[name] = struct{}{}
		if !isJSONObjectSchema(schema) {
			schema = map[string]any{
				"type": "object",
				"properties": map[string]any{
					kiroCodexCustomToolInputField: map[string]any{
						"type":        "string",
						"description": "Exact freeform input for the custom tool.",
					},
				},
				"required":             []any{kiroCodexCustomToolInputField},
				"additionalProperties": false,
			}
			copyTool["description"] = appendKiroCodexCustomToolHint(stringValue(copyTool["description"]))
		}
		copyTool["parameters"] = schema
		return []any{copyTool}
	}

	// Kiro accepts client tools as ordinary function specifications. Preserve
	// server-side web_search, but turn other Codex declarations into function
	// tools with a valid schema rather than forwarding unsupported type tags.
	if typ != "function" && typ != "web_search" && typ != "google_search" && typ != "web_search_20250305" {
		copyTool["type"] = "function"
	}
	if schema != nil {
		copyTool["parameters"] = schema
	}
	return []any{copyTool}
}

func normalizeKiroCodexResponsesToolChoice(request map[string]any) bool {
	rawChoice, exists := request["tool_choice"]
	if !exists || rawChoice == nil {
		return false
	}
	if choice, ok := rawChoice.(string); ok {
		if strings.EqualFold(strings.TrimSpace(choice), "none") {
			request["tool_choice"] = "auto"
			return true
		}
		return false
	}
	return normalizeCodexToolChoice(request)
}

func appendKiroCodexCustomToolHint(description string) string {
	const hint = "For this bridge, invoke the tool by placing its exact freeform input in the `input` string field."
	description = strings.TrimSpace(description)
	if description == "" {
		return hint
	}
	if strings.Contains(description, hint) {
		return description
	}
	return description + "\n\n" + hint
}

func joinKiroCodexToolNamespace(namespace, name string) string {
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if namespace == "" {
		return name
	}
	if name == "" || strings.HasPrefix(name, namespace+"__") {
		return name
	}
	return namespace + "__" + name
}

func cloneStringAnyMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonEmptyStringValue(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringValue(value)); text != "" {
			return text
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func isJSONObjectSchema(value any) bool {
	schema, ok := value.(map[string]any)
	if !ok || schema == nil {
		return false
	}
	typ := strings.TrimSpace(stringValue(schema["type"]))
	return typ == "" || typ == "object"
}
