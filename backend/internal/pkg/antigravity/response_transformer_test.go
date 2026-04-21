//go:build unit

package antigravity

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTransformGeminiToClaude_MapsOfficialToolNameBackToClientAlias(t *testing.T) {
	body := []byte(`{
		"response": {
			"responseId":"resp_1",
			"candidates": [
				{
					"content": {
						"parts": [
							{
								"functionCall": {
									"name": "view_file",
									"id": "tool_1",
									"args": {
										"AbsolutePath": "/tmp/demo.txt",
										"StartLine": 3,
										"EndLine": 9
									}
								},
								"thoughtSignature": "sig_1"
							}
						]
					},
					"finishReason": "STOP"
				}
			],
			"usageMetadata": {
				"promptTokenCount": 10,
				"candidatesTokenCount": 3
			}
		}
	}`)

	claudeRespBody, usage, err := TransformGeminiToClaude(body, "claude-opus-4-6", map[string]string{
		"view_file": "read_file",
	})
	require.NoError(t, err)
	require.NotNil(t, usage)

	var resp ClaudeResponse
	require.NoError(t, json.Unmarshal(claudeRespBody, &resp))
	require.Equal(t, "resp_1", resp.ID)
	require.Equal(t, "tool_use", resp.StopReason)
	require.Len(t, resp.Content, 1)
	require.Equal(t, "tool_use", resp.Content[0].Type)
	require.Equal(t, "read_file", resp.Content[0].Name)
	require.Equal(t, "sig_1", resp.Content[0].Signature)
	input, ok := resp.Content[0].Input.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "/tmp/demo.txt", input["path"])
	require.Equal(t, float64(3), input["start_line"])
	require.Equal(t, float64(9), input["end_line"])
}
