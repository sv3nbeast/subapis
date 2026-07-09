package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareSharedAnthropicThinkingHistoryInfersStrictModel(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"thinking":{"type":"adaptive"},
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"prior private reasoning","signature":"signature_from_another_context"},
				{"type":"text","text":"visible answer"}
			]},
			{"role":"user","content":"continue"}
		]
	}`)

	out := PrepareSharedAnthropicThinkingHistory(input, &Account{
		Platform: PlatformAnthropic,
		Type:     AccountTypeOAuth,
	})

	require.False(t, gjson.GetBytes(out, "thinking").Exists())
	require.False(t, gjson.GetBytes(out, "messages.0.content.0.signature").Exists())
	require.Equal(t, "text", gjson.GetBytes(out, "messages.0.content.0.type").String())
	require.Equal(t, "prior private reasoning", gjson.GetBytes(out, "messages.0.content.0.text").String())
	require.Equal(t, "visible answer", gjson.GetBytes(out, "messages.0.content.1.text").String())

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out, &decoded))
}

func TestPrepareSharedAnthropicThinkingHistoryUsesExplicitMappedModel(t *testing.T) {
	input := []byte(`{
		"model":"claude-opus-4-8",
		"thinking":{"type":"adaptive"},
		"messages":[
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"must pass back","signature":"vendor_signature"}
			]}
		]
	}`)

	out := PrepareSharedAnthropicThinkingHistory(input, &Account{
		Platform: PlatformAnthropic,
		Type:     AccountTypeOAuth,
	}, "deepseek-v4-pro")

	require.Equal(t, string(input), string(out))
}
