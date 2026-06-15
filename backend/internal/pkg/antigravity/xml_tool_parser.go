package antigravity

import (
	"encoding/json"
	"strings"
	"unicode"
)

type embeddedXMLToolCall struct {
	name  string
	input map[string]any
	start int
	end   int
}

func drainEmbeddedXMLToolText(text string) (string, []embeddedXMLToolCall, string) {
	text = normalizeAntigravityXMLToolText(text)
	var builder strings.Builder
	var calls []embeddedXMLToolCall
	index := 0
	for index < len(text) {
		start, ok := nextEmbeddedMCPXMLStart(text, index)
		if !ok {
			visible, pending := splitEmbeddedMCPXMLStartPrefix(text[index:])
			_, _ = builder.WriteString(visible)
			return builder.String(), calls, pending
		}
		call, ok := parseEmbeddedMCPXMLToolAt(text, start)
		if !ok {
			_, _ = builder.WriteString(text[index:start])
			return builder.String(), calls, text[start:]
		}
		_, _ = builder.WriteString(text[index:start])
		calls = append(calls, call)
		index = call.end
	}
	return builder.String(), calls, ""
}

func splitEmbeddedMCPXMLStartPrefix(text string) (string, string) {
	if text == "" {
		return "", ""
	}
	pendingLen := longestEmbeddedMCPXMLStartPrefixSuffix(text)
	if pendingLen == 0 {
		return text, ""
	}
	return text[:len(text)-pendingLen], text[len(text)-pendingLen:]
}

func longestEmbeddedMCPXMLStartPrefixSuffix(text string) int {
	longest := 0
	for _, marker := range []string{"<mcp__", "&lt;mcp__"} {
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

func normalizeAntigravityXMLToolText(text string) string {
	if !strings.Contains(text, "&lt;mcp__") {
		return text
	}
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

func nextEmbeddedMCPXMLStart(text string, from int) (int, bool) {
	if from < 0 {
		from = 0
	}
	if from >= len(text) {
		return 0, false
	}
	idx := strings.Index(text[from:], "<mcp__")
	if idx == -1 {
		return 0, false
	}
	return from + idx, true
}

func parseEmbeddedMCPXMLToolAt(text string, start int) (embeddedXMLToolCall, bool) {
	if start < 0 || start >= len(text) || !strings.HasPrefix(text[start:], "<mcp__") {
		return embeddedXMLToolCall{}, false
	}
	nameEnd := start + 1
	for nameEnd < len(text) && isMCPXMLToolNameChar(rune(text[nameEnd])) {
		nameEnd++
	}
	if nameEnd <= start+1 || nameEnd >= len(text) || text[nameEnd] != '>' {
		return embeddedXMLToolCall{}, false
	}
	name := text[start+1 : nameEnd]
	closeTag := "</" + name + ">"
	bodyStart := nameEnd + 1
	closeRel := strings.Index(text[bodyStart:], closeTag)
	if closeRel == -1 {
		return embeddedXMLToolCall{}, false
	}
	bodyEnd := bodyStart + closeRel
	body := strings.TrimSpace(text[bodyStart:bodyEnd])
	input := map[string]any{}
	if body != "" {
		if err := json.Unmarshal([]byte(body), &input); err != nil {
			input = map[string]any{"value": body}
		}
	}
	return embeddedXMLToolCall{
		name:  name,
		input: input,
		start: start,
		end:   bodyEnd + len(closeTag),
	}, true
}

func isMCPXMLToolNameChar(r rune) bool {
	return r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
