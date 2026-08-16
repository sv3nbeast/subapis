package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

const nianzsStructuredOutputMaxPendingBytes = 8 << 20

// nianzsKiroStructuredOutputSchema returns the JSON schema requested through
// Anthropic's structured-output fields. Kiro does not expose constrained
// decoding, so the adapter must validate the completed JSON before returning
// it to the client instead of treating a prompt-only JSON answer as strict.
func nianzsKiroStructuredOutputSchema(body []byte) (any, bool) {
	if len(body) == 0 {
		return nil, false
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, false
	}
	for _, candidate := range []any{
		nianzsNestedJSONValue(payload, "output_config", "format"),
		payload["output_format"],
		payload["response_format"],
	} {
		format, ok := candidate.(map[string]any)
		if !ok || !strings.EqualFold(strings.TrimSpace(nianzsStringValue(format["type"])), "json_schema") {
			continue
		}
		schema := format["schema"]
		if schema == nil {
			if wrapped, ok := format["json_schema"].(map[string]any); ok {
				schema = wrapped["schema"]
			}
		}
		if schema != nil {
			return schema, true
		}
	}
	return nil, false
}

func nianzsNestedJSONValue(root map[string]any, path ...string) any {
	var current any = root
	for _, key := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = obj[key]
	}
	return current
}

func nianzsStringValue(value any) string {
	text, _ := value.(string)
	return text
}

// nianzsNormalizeStructuredOutputJSON applies the subset of JSON Schema used
// by Anthropic structured outputs. It is deliberately fail-closed: if required
// fields or value types do not match, the original model response is retained
// for observability rather than inventing data. additionalProperties=false is
// enforced recursively, which removes Kiro's common explanatory extra fields.
func nianzsNormalizeStructuredOutputJSON(raw string, schema any) (string, bool) {
	raw = nianzsTrimJSONEnvelope(raw)
	if raw == "" || schema == nil {
		return "", false
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", false
	}
	projected, ok := nianzsProjectStructuredValue(value, schema)
	if !ok {
		return "", false
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func nianzsTrimJSONEnvelope(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "```") {
		return raw
	}
	lines := strings.Split(raw, "\n")
	if len(lines) < 3 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "```") || strings.TrimSpace(lines[len(lines)-1]) != "```" {
		return raw
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

func nianzsProjectStructuredValue(value, schema any) (any, bool) {
	schemaMap, ok := schema.(map[string]any)
	if !ok || schemaMap == nil {
		return value, true
	}
	if constValue, exists := schemaMap["const"]; exists && !nianzsJSONValuesEqual(value, constValue) {
		return nil, false
	}
	if enumValues, ok := schemaMap["enum"].([]any); ok && len(enumValues) > 0 {
		matched := false
		for _, enumValue := range enumValues {
			if nianzsJSONValuesEqual(value, enumValue) {
				matched = true
				break
			}
		}
		if !matched {
			return nil, false
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf"} {
		if alternatives, ok := schemaMap[keyword].([]any); ok && len(alternatives) > 0 {
			for _, alternative := range alternatives {
				if projected, valid := nianzsProjectStructuredValue(value, alternative); valid {
					return projected, true
				}
			}
			return nil, false
		}
	}

	typeName := strings.ToLower(strings.TrimSpace(nianzsStringValue(schemaMap["type"])))
	if typeName == "" {
		switch {
		case schemaMap["properties"] != nil:
			typeName = "object"
		case schemaMap["items"] != nil:
			typeName = "array"
		default:
			return value, true
		}
	}
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		properties, _ := schemaMap["properties"].(map[string]any)
		required := make(map[string]struct{})
		if requiredValues, ok := schemaMap["required"].([]any); ok {
			for _, item := range requiredValues {
				if name, ok := item.(string); ok {
					required[name] = struct{}{}
				}
			}
		}
		for name := range required {
			if _, exists := object[name]; !exists {
				return nil, false
			}
		}
		result := make(map[string]any, len(object))
		for name, item := range object {
			propertySchema, declared := properties[name]
			if !declared {
				if additional, exists := schemaMap["additionalProperties"]; exists {
					switch typed := additional.(type) {
					case bool:
						if !typed {
							continue
						}
					case map[string]any:
						projected, valid := nianzsProjectStructuredValue(item, typed)
						if !valid {
							return nil, false
						}
						result[name] = projected
						continue
					}
				}
				result[name] = item
				continue
			}
			projected, valid := nianzsProjectStructuredValue(item, propertySchema)
			if !valid {
				return nil, false
			}
			result[name] = projected
		}
		return result, true
	case "array":
		array, ok := value.([]any)
		if !ok {
			return nil, false
		}
		if minItems, ok := nianzsSchemaInteger(schemaMap["minItems"]); ok && len(array) < minItems {
			return nil, false
		}
		if maxItems, ok := nianzsSchemaInteger(schemaMap["maxItems"]); ok && len(array) > maxItems {
			return nil, false
		}
		itemSchema := schemaMap["items"]
		if itemSchema == nil {
			return array, true
		}
		result := make([]any, 0, len(array))
		for _, item := range array {
			projected, valid := nianzsProjectStructuredValue(item, itemSchema)
			if !valid {
				return nil, false
			}
			result = append(result, projected)
		}
		return result, true
	case "string":
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		if minLength, ok := nianzsSchemaInteger(schemaMap["minLength"]); ok && len([]rune(text)) < minLength {
			return nil, false
		}
		if maxLength, ok := nianzsSchemaInteger(schemaMap["maxLength"]); ok && len([]rune(text)) > maxLength {
			return nil, false
		}
		return text, true
	case "integer":
		number, ok := value.(json.Number)
		if !ok || strings.ContainsAny(number.String(), ".eE") {
			return nil, false
		}
		if _, err := number.Int64(); err != nil {
			return nil, false
		}
		return number, true
	case "number":
		number, ok := value.(json.Number)
		if !ok {
			return nil, false
		}
		if parsed, err := number.Float64(); err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, false
		}
		return number, true
	case "boolean":
		_, ok := value.(bool)
		return value, ok
	case "null":
		return nil, value == nil
	default:
		return value, true
	}
}

func nianzsSchemaInteger(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil && parsed >= 0
	case float64:
		return int(typed), typed >= 0 && typed == math.Trunc(typed)
	default:
		return 0, false
	}
}

func nianzsJSONValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func nianzsNormalizeStructuredOutputResponseJSON(response []byte, schema any) []byte {
	normalized, _ := nianzsNormalizeStructuredOutputResponseJSONWithStatus(response, schema)
	return normalized
}

func nianzsNormalizeStructuredOutputResponseJSONWithStatus(response []byte, schema any) ([]byte, bool) {
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(response))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return response, false
	}
	content, ok := payload["content"].([]any)
	if !ok {
		return response, false
	}
	var text strings.Builder
	textIndexes := make([]int, 0, 1)
	for index, rawBlock := range content {
		block, ok := rawBlock.(map[string]any)
		if !ok || block["type"] != "text" {
			continue
		}
		part, _ := block["text"].(string)
		text.WriteString(part)
		textIndexes = append(textIndexes, index)
	}
	if len(textIndexes) == 0 {
		return response, false
	}
	normalized, ok := nianzsNormalizeStructuredOutputJSON(text.String(), schema)
	if !ok {
		return response, false
	}
	first := textIndexes[0]
	content[first].(map[string]any)["text"] = normalized
	for index := len(textIndexes) - 1; index >= 1; index-- {
		removeAt := textIndexes[index]
		content = append(content[:removeAt], content[removeAt+1:]...)
	}
	payload["content"] = content
	encoded, err := json.Marshal(payload)
	if err != nil {
		return response, false
	}
	return encoded, true
}

func nianzsNormalizeStructuredOutputSSE(wire []byte, schema any) []byte {
	normalized, _ := nianzsNormalizeStructuredOutputSSEWithStatus(wire, schema)
	return normalized
}

func nianzsNormalizeStructuredOutputSSEWithStatus(wire []byte, schema any) ([]byte, bool) {
	var out bytes.Buffer
	writer, ok := newNianzsStructuredOutputSSEWriter(&out, schema)
	if !ok {
		return wire, false
	}
	if _, err := writer.Write(wire); err != nil {
		return wire, false
	}
	if err := writer.Finish(); err != nil {
		return wire, false
	}
	return out.Bytes(), writer.enforced
}

// nianzsStructuredOutputSSEWriter projects completed top-level object members
// as soon as each member closes. It never waits for the full response: ordinary
// SSE frames pass through immediately and only the current incomplete JSON
// member remains buffered.
type nianzsStructuredOutputSSEWriter struct {
	dst        io.Writer
	pendingSSE []byte
	projector  *nianzsStructuredObjectProjector
	textBlocks map[int]struct{}
	enforced   bool
}

func newNianzsStructuredOutputSSEWriter(dst io.Writer, schema any) (*nianzsStructuredOutputSSEWriter, bool) {
	projector, ok := newNianzsStructuredObjectProjector(schema)
	if !ok || dst == nil {
		return nil, false
	}
	return &nianzsStructuredOutputSSEWriter{
		dst:        dst,
		projector:  projector,
		textBlocks: make(map[int]struct{}),
	}, true
}

func (w *nianzsStructuredOutputSSEWriter) Write(p []byte) (int, error) {
	if w == nil || w.dst == nil || w.projector == nil {
		return 0, fmt.Errorf("upstream structured output writer is not initialized")
	}
	w.pendingSSE = append(w.pendingSSE, p...)
	for {
		end := bytes.Index(w.pendingSSE, []byte("\n\n"))
		if end < 0 {
			break
		}
		frame := append([]byte(nil), w.pendingSSE[:end+2]...)
		w.pendingSSE = w.pendingSSE[end+2:]
		projected, err := w.projectFrame(frame)
		if err != nil {
			return 0, err
		}
		if len(projected) > 0 {
			if err := nianzsWriteAll(w.dst, projected); err != nil {
				return 0, err
			}
		}
	}
	if len(w.pendingSSE) > nianzsStructuredOutputMaxPendingBytes {
		return 0, fmt.Errorf("upstream structured output SSE frame exceeded %d bytes", nianzsStructuredOutputMaxPendingBytes)
	}
	return len(p), nil
}

func (w *nianzsStructuredOutputSSEWriter) Finish() error {
	if len(w.pendingSSE) > 0 {
		if err := nianzsWriteAll(w.dst, w.pendingSSE); err != nil {
			return err
		}
		w.pendingSSE = nil
	}
	if w.projector.started && !w.projector.done {
		return fmt.Errorf("upstream structured output ended before the JSON object closed")
	}
	return nil
}

func (w *nianzsStructuredOutputSSEWriter) projectFrame(frame []byte) ([]byte, error) {
	event, ok := nianzsDecodeSSEDataObject(frame)
	if !ok {
		return frame, nil
	}
	index, _ := nianzsAnyInt(event["index"])
	switch event["type"] {
	case "content_block_start":
		block, _ := event["content_block"].(map[string]any)
		if block["type"] == "text" {
			w.textBlocks[index] = struct{}{}
		}
		return frame, nil
	case "content_block_delta":
		delta, _ := event["delta"].(map[string]any)
		if delta["type"] != "text_delta" {
			return frame, nil
		}
		w.textBlocks[index] = struct{}{}
		text, _ := delta["text"].(string)
		projected, err := w.projector.Feed(text)
		if err != nil {
			return nil, err
		}
		w.enforced = true
		if projected == "" {
			return nil, nil
		}
		delta["text"] = projected
		encoded, err := json.Marshal(event)
		if err != nil {
			return nil, err
		}
		return nianzsReplaceSSEDataLine(frame, encoded), nil
	case "content_block_stop":
		if _, textBlock := w.textBlocks[index]; textBlock {
			delete(w.textBlocks, index)
			if !w.projector.done {
				return nil, fmt.Errorf("upstream structured output text block ended before a valid JSON object")
			}
		}
	}
	return frame, nil
}

func nianzsDecodeSSEDataObject(frame []byte) (map[string]any, bool) {
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		var event map[string]any
		decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(line, []byte("data: "))))
		decoder.UseNumber()
		if decoder.Decode(&event) == nil {
			return event, true
		}
	}
	return nil, false
}

func nianzsWriteAll(dst io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := dst.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

type nianzsStructuredObjectProjector struct {
	properties map[string]any
	required   map[string]struct{}
	seen       map[string]struct{}
	pending    string
	started    bool
	done       bool
	emitted    int
}

func newNianzsStructuredObjectProjector(schema any) (*nianzsStructuredObjectProjector, bool) {
	schemaMap, ok := schema.(map[string]any)
	if !ok {
		return nil, false
	}
	typeName := strings.ToLower(strings.TrimSpace(nianzsStringValue(schemaMap["type"])))
	if typeName == "" && schemaMap["properties"] != nil {
		typeName = "object"
	}
	additional, hasAdditional := schemaMap["additionalProperties"].(bool)
	if typeName != "object" || !hasAdditional || additional {
		return nil, false
	}
	properties, ok := schemaMap["properties"].(map[string]any)
	if !ok {
		properties = map[string]any{}
	}
	required := make(map[string]struct{})
	if values, ok := schemaMap["required"].([]any); ok {
		for _, value := range values {
			if name, ok := value.(string); ok {
				required[name] = struct{}{}
			}
		}
	}
	return &nianzsStructuredObjectProjector{
		properties: properties,
		required:   required,
		seen:       make(map[string]struct{}),
	}, true
}

func (p *nianzsStructuredObjectProjector) Feed(chunk string) (string, error) {
	if p.done {
		if strings.TrimSpace(chunk) != "" {
			return "", fmt.Errorf("upstream structured output contained trailing content")
		}
		return "", nil
	}
	p.pending += chunk
	if len(p.pending) > nianzsStructuredOutputMaxPendingBytes {
		return "", fmt.Errorf("upstream structured output JSON member exceeded %d bytes", nianzsStructuredOutputMaxPendingBytes)
	}
	var out strings.Builder
	if !p.started {
		trimmed := strings.TrimLeft(p.pending, " \t\r\n")
		if trimmed == "" {
			p.pending = trimmed
			return "", nil
		}
		if trimmed[0] != '{' {
			return "", fmt.Errorf("upstream structured output did not start with a JSON object")
		}
		p.pending = trimmed[1:]
		p.started = true
		out.WriteByte('{')
	}

	for !p.done {
		p.pending = strings.TrimLeft(p.pending, " \t\r\n")
		if p.pending == "" {
			break
		}
		if p.pending[0] == '}' {
			p.pending = p.pending[1:]
			if err := p.complete(); err != nil {
				return "", err
			}
			out.WriteByte('}')
			break
		}
		boundary, closing := nianzsFindStructuredMemberBoundary(p.pending)
		if boundary < 0 {
			break
		}
		member := strings.TrimSpace(p.pending[:boundary])
		if member == "" {
			return "", fmt.Errorf("upstream structured output contained an empty JSON member")
		}
		encoded, keep, err := p.projectMember(member)
		if err != nil {
			return "", err
		}
		if keep {
			if p.emitted > 0 {
				out.WriteByte(',')
			}
			out.Write(encoded)
			p.emitted++
		}
		p.pending = p.pending[boundary+1:]
		if closing {
			if err := p.complete(); err != nil {
				return "", err
			}
			out.WriteByte('}')
		}
	}
	if p.done && strings.TrimSpace(p.pending) != "" {
		return "", fmt.Errorf("upstream structured output contained trailing content")
	}
	return out.String(), nil
}

func (p *nianzsStructuredObjectProjector) projectMember(member string) ([]byte, bool, error) {
	var wrapper map[string]any
	decoder := json.NewDecoder(strings.NewReader("{" + member + "}"))
	decoder.UseNumber()
	if err := decoder.Decode(&wrapper); err != nil || len(wrapper) != 1 {
		return nil, false, fmt.Errorf("upstream structured output contained an invalid JSON member")
	}
	for name, value := range wrapper {
		schema, declared := p.properties[name]
		if !declared {
			return nil, false, nil
		}
		projected, valid := nianzsProjectStructuredValue(value, schema)
		if !valid {
			return nil, false, fmt.Errorf("upstream structured output field %q did not match its schema", name)
		}
		nameJSON, _ := json.Marshal(name)
		valueJSON, err := json.Marshal(projected)
		if err != nil {
			return nil, false, err
		}
		p.seen[name] = struct{}{}
		encoded := make([]byte, 0, len(nameJSON)+1+len(valueJSON))
		encoded = append(encoded, nameJSON...)
		encoded = append(encoded, ':')
		encoded = append(encoded, valueJSON...)
		return encoded, true, nil
	}
	return nil, false, fmt.Errorf("upstream structured output contained an invalid JSON member")
}

func (p *nianzsStructuredObjectProjector) complete() error {
	for name := range p.required {
		if _, exists := p.seen[name]; !exists {
			return fmt.Errorf("upstream structured output omitted required field %q", name)
		}
	}
	p.done = true
	return nil
}

func nianzsFindStructuredMemberBoundary(value string) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for index := 0; index < len(value); index++ {
		char := value[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '}':
			if depth == 0 {
				return index, true
			}
			depth--
		case ',':
			if depth == 0 {
				return index, false
			}
		}
	}
	return -1, false
}

func nianzsAnyInt(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	case float64:
		return int(typed), typed == math.Trunc(typed)
	default:
		return 0, false
	}
}

func nianzsReplaceSSEDataLine(block, payload []byte) []byte {
	lines := bytes.Split(block, []byte("\n"))
	for index, line := range lines {
		if bytes.HasPrefix(line, []byte("data: ")) {
			lines[index] = append([]byte("data: "), payload...)
			break
		}
	}
	return bytes.Join(lines, []byte("\n"))
}
