package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeAnthropicXMLInvokeResponseBodyConvertsTextToToolUse(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","content":[{"type":"text","text":"Before <invoke name=\"Bash\"><parameter name=\"command\">pwd</parameter><parameter name=\"description\">print cwd</parameter></invoke> After"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":2}}`)

	normalized, changed := normalizeAnthropicXMLInvokeResponseBody(body)

	require.True(t, changed)
	require.NotContains(t, string(normalized), "<invoke")
	require.Equal(t, "tool_use", gjson.GetBytes(normalized, "stop_reason").String())
	content := gjson.GetBytes(normalized, "content").Array()
	require.Len(t, content, 3)
	require.Equal(t, "text", content[0].Get("type").String())
	require.Equal(t, "Before ", content[0].Get("text").String())
	require.Equal(t, "tool_use", content[1].Get("type").String())
	require.Equal(t, "Bash", content[1].Get("name").String())
	require.Equal(t, "pwd", content[1].Get("input.command").String())
	require.Equal(t, "print cwd", content[1].Get("input.description").String())
	require.Equal(t, "text", content[2].Get("type").String())
	require.Equal(t, " After", content[2].Get("text").String())
}

func TestDrainAnthropicXMLInvokeTextConvertsEscapedInvoke(t *testing.T) {
	cleaned, calls, pending := drainAnthropicXMLInvokeText(`&lt;invoke name=&quot;Bash&quot;&gt;&lt;parameter name=&quot;command&quot;&gt;echo &amp;&amp; pwd&lt;/parameter&gt;&lt;/invoke&gt;`)

	require.Empty(t, cleaned)
	require.Empty(t, pending)
	require.Len(t, calls, 1)
	require.Equal(t, "Bash", calls[0].name)
	require.Equal(t, "echo && pwd", calls[0].input["command"])
}

func TestDrainAnthropicXMLInvokeTextPreservesEscapedTextOutsideInvoke(t *testing.T) {
	cleaned, calls, pending := drainAnthropicXMLInvokeText(`show &lt;keep&gt; &lt;invoke name=&quot;Bash&quot;&gt;&lt;parameter name=&quot;command&quot;&gt;pwd&lt;/parameter&gt;&lt;/invoke&gt; tail &amp; end`)

	require.Equal(t, "show &lt;keep&gt;  tail &amp; end", cleaned)
	require.Empty(t, pending)
	require.Len(t, calls, 1)
	require.Equal(t, "Bash", calls[0].name)
	require.Equal(t, "pwd", calls[0].input["command"])
}

func TestDrainAnthropicXMLInvokeTextConvertsRealEscapedBashInvokeSample(t *testing.T) {
	sample := `&lt;invoke name="Bash"&gt;
&lt;parameter name="command"&gt;cd /Users/sven.sun/Desktop/Tools/Strategy/AutoGetCode
python3 - &lt;&lt;'PYEOF'
# A. thead add channel column
ih = "templates/index.html"
s = open(ih, encoding="utf-8").read()
old = '&lt;th&gt;邮箱&lt;/th&gt;&lt;th&gt;状态&lt;/th&gt;&lt;th&gt;兑换码&lt;/th&gt;'
new = '&lt;th&gt;邮箱&lt;/th&gt;&lt;th&gt;状态&lt;/th&gt;&lt;th&gt;渠道&lt;/th&gt;&lt;th&gt;凭证&lt;/th&gt;'
print(old, new)
PYEOF&lt;/parameter&gt;
&lt;parameter name="description"&gt;Add channel column to table header&lt;/parameter&gt;
&lt;/invoke&gt;`

	cleaned, calls, pending := drainAnthropicXMLInvokeText(sample)

	require.Empty(t, cleaned)
	require.Empty(t, pending)
	require.Len(t, calls, 1)
	require.Equal(t, "Bash", calls[0].name)
	require.Contains(t, calls[0].input["command"], "python3 - <<'PYEOF'")
	require.Contains(t, calls[0].input["command"], "<th>渠道</th>")
	require.Equal(t, "Add channel column to table header", calls[0].input["description"])
}

func TestDrainAnthropicXMLInvokeTextHoldsSplitInvokePrefix(t *testing.T) {
	parts, pending := drainAnthropicXMLInvokeParts("Before <in")

	require.Len(t, parts, 1)
	require.Equal(t, "Before ", parts[0].text)
	require.Equal(t, "<in", pending)

	parts, pending = drainAnthropicXMLInvokeParts("Before &lt;in")
	require.Len(t, parts, 1)
	require.Equal(t, "Before ", parts[0].text)
	require.Equal(t, "&lt;in", pending)
}

func TestAnthropicXMLInvokeStreamNormalizerConvertsSplitInvokeToToolUse(t *testing.T) {
	normalizer := newAnthropicXMLInvokeStreamNormalizer()

	generated, handled, changed := normalizer.handleEvent(map[string]any{
		"type":  "content_block_start",
		"index": float64(0),
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})
	require.False(t, handled)
	require.False(t, changed)
	require.Empty(t, generated)

	generated, handled, changed = normalizer.handleEvent(map[string]any{
		"type":  "content_block_delta",
		"index": float64(0),
		"delta": map[string]any{
			"type": "text_delta",
			"text": "Before <invoke name=\"Bash\"><parameter name=\"command\">pw",
		},
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Len(t, generated, 1)
	require.Equal(t, "Before ", generated[0]["delta"].(map[string]any)["text"])

	generated, handled, changed = normalizer.handleEvent(map[string]any{
		"type":  "content_block_delta",
		"index": float64(0),
		"delta": map[string]any{
			"type": "text_delta",
			"text": "d</parameter></invoke> After",
		},
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Len(t, generated, 7)
	require.Equal(t, "content_block_stop", generated[0]["type"])
	require.Equal(t, 0, generated[0]["index"])
	require.Equal(t, "content_block_start", generated[1]["type"])
	require.Equal(t, 1, generated[1]["index"])
	require.Equal(t, "tool_use", generated[1]["content_block"].(map[string]any)["type"])
	require.Equal(t, "Bash", generated[1]["content_block"].(map[string]any)["name"])
	require.Equal(t, "content_block_delta", generated[2]["type"])
	require.Equal(t, 1, generated[2]["index"])
	require.Contains(t, generated[2]["delta"].(map[string]any)["partial_json"], `"command":"pwd"`)
	require.Equal(t, "content_block_stop", generated[3]["type"])
	require.Equal(t, 1, generated[3]["index"])
	require.Equal(t, "content_block_start", generated[4]["type"])
	require.Equal(t, 2, generated[4]["index"])
	require.Equal(t, "text", generated[4]["content_block"].(map[string]any)["type"])
	require.Equal(t, "content_block_delta", generated[5]["type"])
	require.Equal(t, 2, generated[5]["index"])
	require.Equal(t, " After", generated[5]["delta"].(map[string]any)["text"])
	require.Equal(t, "content_block_stop", generated[6]["type"])
	require.Equal(t, 2, generated[6]["index"])

	generated, handled, changed = normalizer.handleEvent(map[string]any{
		"type":  "content_block_stop",
		"index": float64(0),
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Empty(t, generated)
}

func TestAnthropicXMLInvokeStreamNormalizerConvertsSplitEscapedInvokeToToolUse(t *testing.T) {
	normalizer := newAnthropicXMLInvokeStreamNormalizer()

	_, _, _ = normalizer.handleEvent(map[string]any{
		"type":  "content_block_start",
		"index": float64(0),
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})

	generated, handled, changed := normalizer.handleEvent(map[string]any{
		"type":  "content_block_delta",
		"index": float64(0),
		"delta": map[string]any{
			"type": "text_delta",
			"text": "Before &lt;in",
		},
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Len(t, generated, 1)
	require.Equal(t, "Before ", generated[0]["delta"].(map[string]any)["text"])

	generated, handled, changed = normalizer.handleEvent(map[string]any{
		"type":  "content_block_delta",
		"index": float64(0),
		"delta": map[string]any{
			"type": "text_delta",
			"text": "voke name=&quot;Bash&quot;&gt;&lt;parameter name=&quot;command&quot;&gt;pwd&lt;/parameter&gt;&lt;/invoke&gt;",
		},
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Len(t, generated, 4)
	require.Equal(t, "content_block_stop", generated[0]["type"])
	require.Equal(t, "content_block_start", generated[1]["type"])
	require.Equal(t, "tool_use", generated[1]["content_block"].(map[string]any)["type"])
	require.Equal(t, "Bash", generated[1]["content_block"].(map[string]any)["name"])
	require.Contains(t, generated[2]["delta"].(map[string]any)["partial_json"], `"command":"pwd"`)
	require.Equal(t, "content_block_stop", generated[3]["type"])

	generated = normalizer.flushPendingEvents()
	require.Empty(t, generated)
}

func TestAnthropicXMLInvokeStreamNormalizerKeepsIncompleteInvokeAsText(t *testing.T) {
	normalizer := newAnthropicXMLInvokeStreamNormalizer()
	_, _, _ = normalizer.handleEvent(map[string]any{
		"type":  "content_block_start",
		"index": float64(0),
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})
	generated, handled, changed := normalizer.handleEvent(map[string]any{
		"type":  "content_block_delta",
		"index": float64(0),
		"delta": map[string]any{
			"type": "text_delta",
			"text": "Before <invoke name=\"Bash\"><parameter name=\"command\">",
		},
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Len(t, generated, 1)
	require.Equal(t, "Before ", generated[0]["delta"].(map[string]any)["text"])

	generated, handled, changed = normalizer.handleEvent(map[string]any{
		"type":  "content_block_stop",
		"index": float64(0),
	})
	require.True(t, handled)
	require.True(t, changed)
	require.Len(t, generated, 2)
	require.Equal(t, "content_block_delta", generated[0]["type"])
	require.Equal(t, `<invoke name="Bash"><parameter name="command">`, generated[0]["delta"].(map[string]any)["text"])
	require.Equal(t, "content_block_stop", generated[1]["type"])
}
