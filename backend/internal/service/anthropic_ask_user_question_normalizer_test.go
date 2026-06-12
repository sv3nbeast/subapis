package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeAnthropicAskUserQuestionResponseBodyFillsQuestion(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","content":[{"type":"tool_use","id":"toolu_1","name":"AskUserQuestion","input":{"questions":[{"header":"第一项","options":[{"label":"继续"}]},{"title":"第二项","options":[{"label":"停止"}]}]}}],"usage":{"input_tokens":1,"output_tokens":2}}`)

	normalized, changed := normalizeAnthropicAskUserQuestionResponseBody(body)

	require.True(t, changed)
	require.Equal(t, "第一项", gjson.GetBytes(normalized, "content.0.input.questions.0.question").String())
	require.Equal(t, "第二项", gjson.GetBytes(normalized, "content.0.input.questions.1.question").String())
	require.Equal(t, "继续", gjson.GetBytes(normalized, "content.0.input.questions.0.options.0.label").String())
}

func TestNormalizeAnthropicAskUserQuestionResponseBodyIgnoresOtherTools(t *testing.T) {
	body := []byte(`{"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"questions":[{"header":"不应修改"}]}}]}`)

	normalized, changed := normalizeAnthropicAskUserQuestionResponseBody(body)

	require.False(t, changed)
	require.Equal(t, string(body), string(normalized))
}

func TestNormalizeAnthropicAskUserQuestionResponseBodyUsesToolRewrite(t *testing.T) {
	body := []byte(`{"content":[{"type":"tool_use","id":"toolu_1","name":"ask_fake","input":{"questions":[{"header":"第一项"}]}}]}`)
	rw := &ToolNameRewrite{Reverse: map[string]string{"ask_fake": "AskUserQuestion"}}

	normalized, changed := normalizeAnthropicAskUserQuestionResponseBodyWithRewrite(body, rw)

	require.True(t, changed)
	require.Equal(t, "第一项", gjson.GetBytes(normalized, "content.0.input.questions.0.question").String())
}

func TestAnthropicAskUserQuestionStreamNormalizerBuffersPartialJSON(t *testing.T) {
	normalizer := newAnthropicAskUserQuestionStreamNormalizer(nil)

	generated, handled, changed := normalizer.handleEvent(map[string]any{
		"type":  "content_block_start",
		"index": float64(0),
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    "toolu_1",
			"name":  "AskUserQuestion",
			"input": map[string]any{},
		},
	})
	require.False(t, handled)
	require.False(t, changed)
	require.Empty(t, generated)

	generated, handled, changed = normalizer.handleEvent(map[string]any{
		"type":  "content_block_delta",
		"index": float64(0),
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": `{"questions":[{"header":"第一项"},{"text":"第二项"}]}`,
		},
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Empty(t, generated)

	generated, handled, changed = normalizer.handleEvent(map[string]any{
		"type":  "content_block_stop",
		"index": float64(0),
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Len(t, generated, 2)

	delta := generated[0]["delta"].(map[string]any)
	partialJSON := delta["partial_json"].(string)
	require.Equal(t, "第一项", gjson.Get(partialJSON, "questions.0.question").String())
	require.Equal(t, "第二项", gjson.Get(partialJSON, "questions.1.question").String())
	require.Equal(t, "content_block_stop", generated[1]["type"])
}
