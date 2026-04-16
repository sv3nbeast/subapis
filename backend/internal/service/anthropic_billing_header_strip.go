package service

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

const anthropicBillingHeaderPrefix = "x-anthropic-billing-header:"

// StripAnthropicBillingHeaderBlocks removes Claude Code attribution/billing
// system blocks from Anthropic-style request bodies.
//
// The removal is intentionally narrow:
//   - only top-level system array entries are considered
//   - only text blocks whose trimmed text starts with
//     "x-anthropic-billing-header:" are removed
//
// This keeps user/system instructions intact while making requests from clients
// with and without CLAUDE_CODE_ATTRIBUTION_HEADER=0 converge to the same
// canonical body.
func StripAnthropicBillingHeaderBlocks(body []byte) []byte {
	if len(body) == 0 || !bytes.Contains(body, []byte(anthropicBillingHeaderPrefix)) {
		return body
	}

	sys := gjson.GetBytes(body, "system")
	if !sys.Exists() || !sys.IsArray() {
		return body
	}

	kept := make([][]byte, 0)
	modified := false
	sys.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "text" {
			text := strings.TrimSpace(item.Get("text").String())
			if strings.HasPrefix(text, anthropicBillingHeaderPrefix) {
				modified = true
				return true
			}
		}
		kept = append(kept, sliceRawFromBody(body, item))
		return true
	})

	if !modified {
		return body
	}

	if next, ok := setJSONRawBytes(body, "system", buildJSONArrayRaw(kept)); ok {
		return next
	}
	return body
}
