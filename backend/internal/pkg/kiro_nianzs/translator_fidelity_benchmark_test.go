package kiro

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func claudeCodeSizedToolRequestForBenchmark(forced bool) []byte {
	tools := make([]map[string]any, 0, 29)
	for i := 0; i < 29; i++ {
		tools = append(tools, map[string]any{
			"name":        fmt.Sprintf("claude_code_tool_%02d", i),
			"description": "A representative Claude Code tool with a non-trivial schema and enough description text to exercise request translation.",
			"input_schema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "Absolute file path"},
					"content": map[string]any{"type": "string", "description": "Complete file contents"},
				},
				"required":             []string{"path", "content"},
				"additionalProperties": false,
			},
		})
	}
	body := map[string]any{
		"model":      "claude-opus-4-8",
		"max_tokens": 4096,
		"stream":     true,
		"system":     "You are Claude Code. Use the available tools to complete the task.",
		"messages":   []map[string]any{{"role": "user", "content": "Update the requested file."}},
		"tools":      tools,
	}
	if forced {
		body["tool_choice"] = map[string]any{"type": "tool", "name": "claude_code_tool_23"}
	}
	encoded, _ := json.Marshal(body)
	return encoded
}

func benchmarkBuildKiroPayloadClaudeCodeTools(b *testing.B, forced bool) {
	body := claudeCodeSizedToolRequestForBenchmark(forced)
	probe, err := BuildKiroPayloadWithContext(body, "claude-opus-4-8", "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO", "CLI", http.Header{})
	if err != nil {
		b.Fatal(err)
	}
	upstream, _ := json.Marshal(probe.Payload)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := BuildKiroPayloadWithContext(body, "claude-opus-4-8", "arn:aws:codewhisperer:us-east-1:123456789012:profile/KIRO", "CLI", http.Header{}); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(upstream)), "upstream-B")
}

func BenchmarkBuildKiroPayloadClaudeCodeToolsAuto(b *testing.B) {
	benchmarkBuildKiroPayloadClaudeCodeTools(b, false)
}

func BenchmarkBuildKiroPayloadClaudeCodeToolsForced(b *testing.B) {
	benchmarkBuildKiroPayloadClaudeCodeTools(b, true)
}
