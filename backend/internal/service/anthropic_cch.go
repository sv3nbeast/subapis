// CCH normalization and signing behavior is adapted from CLIProxyAPI.
//
// MIT License
// Copyright (c) 2025-2005.9 Luis Pater
// Copyright (c) 2025.9-present Router-For.ME
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.
package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	anthropicCCHSeed   uint64 = 0x4D659218E32A3268
	anthropicCCHLength        = 5
	anthropicCCHZero          = "00000"
)

type anthropicCCHNormalizationEdit struct {
	start int
	end   int
}

type anthropicCCHJSONMember struct {
	start       int
	end         int
	commaBefore int
	commaAfter  int
	excluded    bool
}

type anthropicCCHJSONScanner struct {
	body  []byte
	pos   int
	edits []anthropicCCHNormalizationEdit
}

// shouldFinalizeAnthropicCCH limits first-party request signing to the account
// and endpoint combination where Claude Code produces CCH. Custom relays and
// API-key passthrough retain their existing wire contract.
func shouldFinalizeAnthropicCCH(account *Account, tokenType string, endpoint *url.URL) bool {
	if account == nil || !account.IsAnthropicOAuthOrSetupToken() || tokenType != "oauth" || endpoint == nil {
		return false
	}
	if !strings.EqualFold(endpoint.Scheme, "https") || !strings.EqualFold(endpoint.Hostname(), "api.anthropic.com") {
		return false
	}
	if port := endpoint.Port(); port != "" && port != "443" {
		return false
	}
	return endpoint.Path == "/v1/messages" || endpoint.Path == "/v1/messages/count_tokens"
}

// finalizeAnthropicCCH recreates the billing attribution after account-scoped
// body rewrites, then signs the exact final serialized bytes. Nothing may mutate
// the body after this function returns.
func finalizeAnthropicCCH(body []byte, userAgent string) ([]byte, error) {
	version := ExtractCLIVersion(userAgent)
	if version == "" {
		return nil, fmt.Errorf("Claude OAuth CCH requires a claude-cli user agent")
	}

	billing := buildAnthropicCCHBillingText(body, version, claudeEntrypointFromUserAgent(userAgent))
	withPlaceholder, err := ensureAnthropicCCHBillingBlock(body, billing)
	if err != nil {
		return nil, err
	}
	return signAnthropicCCH(withPlaceholder)
}

func buildAnthropicCCHBillingText(body []byte, version, entrypoint string) string {
	if entrypoint == "" {
		entrypoint = "cli"
	}
	fingerprint := computeClaudeCodeFingerprint(body, version)
	return fmt.Sprintf(
		"x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=%s; cch=%s;",
		version,
		fingerprint,
		entrypoint,
		anthropicCCHZero,
	)
}

func claudeEntrypointFromUserAgent(userAgent string) string {
	lower := strings.ToLower(strings.TrimSpace(userAgent))
	marker := "(external,"
	start := strings.Index(lower, marker)
	if start < 0 {
		return "cli"
	}
	entrypoint := strings.TrimSpace(lower[start+len(marker):])
	if end := strings.IndexAny(entrypoint, ",)"); end >= 0 {
		entrypoint = strings.TrimSpace(entrypoint[:end])
	}
	if entrypoint == "" {
		return "cli"
	}
	for _, r := range entrypoint {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return "cli"
		}
	}
	return entrypoint
}

func ensureAnthropicCCHBillingBlock(body []byte, billingText string) ([]byte, error) {
	billing := gjson.GetBytes(body, "system.0.text")
	if billing.Type != gjson.String || !strings.HasPrefix(billing.String(), "x-anthropic-billing-header:") {
		return prependAnthropicBillingSystemBlock(body, billingText)
	}
	updated, err := sjson.SetBytes(body, "system.0.text", billingText)
	if err != nil {
		return nil, fmt.Errorf("replace Anthropic CCH billing block: %w", err)
	}
	return updated, nil
}

func prependAnthropicBillingSystemBlock(body []byte, billingText string) ([]byte, error) {
	type systemTextBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	billingBlock, err := json.Marshal(systemTextBlock{Type: "text", Text: billingText})
	if err != nil {
		return nil, fmt.Errorf("marshal Anthropic CCH billing block: %w", err)
	}

	system := gjson.GetBytes(body, "system")
	var systemArray []byte
	switch {
	case system.Type == gjson.String:
		originalBlock, marshalErr := json.Marshal(systemTextBlock{Type: "text", Text: system.String()})
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal existing Anthropic system text: %w", marshalErr)
		}
		systemArray = make([]byte, 0, len(billingBlock)+len(originalBlock)+3)
		systemArray = append(systemArray, '[')
		systemArray = append(systemArray, billingBlock...)
		systemArray = append(systemArray, ',')
		systemArray = append(systemArray, originalBlock...)
		systemArray = append(systemArray, ']')
	case system.IsArray():
		rawSystem := bytes.TrimSpace([]byte(system.Raw))
		if bytes.Equal(rawSystem, []byte("[]")) {
			systemArray = make([]byte, 0, len(billingBlock)+2)
			systemArray = append(systemArray, '[')
			systemArray = append(systemArray, billingBlock...)
			systemArray = append(systemArray, ']')
		} else {
			systemArray = make([]byte, 0, len(billingBlock)+len(rawSystem)+1)
			systemArray = append(systemArray, '[')
			systemArray = append(systemArray, billingBlock...)
			systemArray = append(systemArray, ',')
			systemArray = append(systemArray, rawSystem[1:]...)
		}
	default:
		systemArray = make([]byte, 0, len(billingBlock)+2)
		systemArray = append(systemArray, '[')
		systemArray = append(systemArray, billingBlock...)
		systemArray = append(systemArray, ']')
	}

	updated, err := sjson.SetRawBytes(body, "system", systemArray)
	if err != nil {
		return nil, fmt.Errorf("prepend Anthropic CCH billing block: %w", err)
	}
	return updated, nil
}

// signAnthropicCCH reproduces Claude Code 2.1.222's final-body signature. It
// changes only the five CCH digits after normalizing dispatch-only fields.
func signAnthropicCCH(body []byte) ([]byte, error) {
	cchOffset, ok := anthropicCCHDigitsOffset(body)
	if !ok {
		return nil, fmt.Errorf("Anthropic CCH billing placeholder not found")
	}

	unsignedBody := bytes.Clone(body)
	copy(unsignedBody[cchOffset:cchOffset+anthropicCCHLength], anthropicCCHZero)
	normalizedBody, err := normalizeAnthropicCCHInput(unsignedBody)
	if err != nil {
		return nil, fmt.Errorf("normalize Anthropic CCH input: %w", err)
	}

	hasher := xxhash.NewWithSeed(anthropicCCHSeed)
	if _, err = hasher.Write(normalizedBody); err != nil {
		return nil, fmt.Errorf("hash Anthropic CCH input: %w", err)
	}
	cch := fmt.Sprintf("%05x", hasher.Sum64()&0xFFFFF)
	copy(unsignedBody[cchOffset:cchOffset+anthropicCCHLength], cch)
	return unsignedBody, nil
}

func anthropicCCHDigitsOffset(body []byte) (int, bool) {
	billing := gjson.GetBytes(body, "system.0.text")
	if billing.Type != gjson.String || !strings.HasPrefix(billing.String(), "x-anthropic-billing-header:") {
		return 0, false
	}

	raw := []byte(billing.Raw)
	for searchFrom := 0; searchFrom < len(raw); {
		relative := bytes.Index(raw[searchFrom:], []byte("cch="))
		if relative < 0 {
			return 0, false
		}
		prefix := searchFrom + relative
		digits := prefix + len("cch=")
		end := digits + anthropicCCHLength
		if end < len(raw) && raw[end] == ';' && isAnthropicCCHLowerHex(raw[digits:end]) {
			return billing.Index + digits, true
		}
		searchFrom = prefix + len("cch=")
	}
	return 0, false
}

func isAnthropicCCHLowerHex(value []byte) bool {
	if len(value) != anthropicCCHLength {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// normalizeAnthropicCCHInput preserves the final JSON byte representation.
// Every model string is emptied, while dispatch-only members are omitted.
func normalizeAnthropicCCHInput(body []byte) ([]byte, error) {
	if !json.Valid(body) {
		return nil, fmt.Errorf("invalid JSON body")
	}

	scanner := anthropicCCHJSONScanner{
		body:  body,
		edits: make([]anthropicCCHNormalizationEdit, 0),
	}
	if err := scanner.parseValue(true); err != nil {
		return nil, err
	}
	scanner.skipWhitespace()
	if scanner.pos != len(body) {
		return nil, fmt.Errorf("unexpected JSON data at byte %d", scanner.pos)
	}

	sort.Slice(scanner.edits, func(i, j int) bool {
		return scanner.edits[i].start < scanner.edits[j].start
	})
	normalized := make([]byte, 0, len(body))
	last := 0
	for _, edit := range scanner.edits {
		if edit.start < last || edit.end > len(body) {
			return nil, fmt.Errorf("overlapping CCH normalization edit at byte %d", edit.start)
		}
		normalized = append(normalized, body[last:edit.start]...)
		last = edit.end
	}
	normalized = append(normalized, body[last:]...)
	return normalized, nil
}

func (scanner *anthropicCCHJSONScanner) parseValue(collect bool) error {
	scanner.skipWhitespace()
	if scanner.pos >= len(scanner.body) {
		return fmt.Errorf("missing JSON value at byte %d", scanner.pos)
	}

	switch scanner.body[scanner.pos] {
	case '{':
		return scanner.parseObject(collect)
	case '[':
		return scanner.parseArray(collect)
	case '"':
		_, _, err := scanner.parseString()
		return err
	default:
		start := scanner.pos
		for scanner.pos < len(scanner.body) {
			switch scanner.body[scanner.pos] {
			case ',', '}', ']', ' ', '\t', '\r', '\n':
				if scanner.pos == start {
					return fmt.Errorf("missing JSON value at byte %d", start)
				}
				return nil
			default:
				scanner.pos++
			}
		}
		if scanner.pos == start {
			return fmt.Errorf("missing JSON value at byte %d", start)
		}
		return nil
	}
}

func (scanner *anthropicCCHJSONScanner) parseObject(collect bool) error {
	scanner.pos++
	scanner.skipWhitespace()
	if scanner.consume('}') {
		return nil
	}

	members := make([]anthropicCCHJSONMember, 0)
	commaBefore := -1
	for {
		scanner.skipWhitespace()
		memberStart := scanner.pos
		keyStart, keyEnd, err := scanner.parseString()
		if err != nil {
			return err
		}
		scanner.skipWhitespace()
		if !scanner.consume(':') {
			return fmt.Errorf("missing object colon at byte %d", scanner.pos)
		}
		scanner.skipWhitespace()

		key := scanner.body[keyStart:keyEnd]
		excluded := collect && isAnthropicCCHExcludedKey(key)
		if collect && bytes.Equal(key, []byte(`"model"`)) && scanner.pos < len(scanner.body) && scanner.body[scanner.pos] == '"' {
			valueStart, valueEnd, stringErr := scanner.parseString()
			if stringErr != nil {
				return stringErr
			}
			scanner.addEdit(valueStart+1, valueEnd-1)
		} else if err = scanner.parseValue(collect && !excluded); err != nil {
			return err
		}
		memberEnd := scanner.pos
		scanner.skipWhitespace()

		commaAfter := -1
		if scanner.consume(',') {
			commaAfter = scanner.pos - 1
		}
		members = append(members, anthropicCCHJSONMember{
			start:       memberStart,
			end:         memberEnd,
			commaBefore: commaBefore,
			commaAfter:  commaAfter,
			excluded:    excluded,
		})
		if commaAfter >= 0 {
			commaBefore = commaAfter
			continue
		}
		if !scanner.consume('}') {
			return fmt.Errorf("missing object end at byte %d", scanner.pos)
		}
		break
	}

	if collect {
		scanner.addExcludedMemberEdits(members)
	}
	return nil
}

func (scanner *anthropicCCHJSONScanner) parseArray(collect bool) error {
	scanner.pos++
	scanner.skipWhitespace()
	if scanner.consume(']') {
		return nil
	}

	for {
		if err := scanner.parseValue(collect); err != nil {
			return err
		}
		scanner.skipWhitespace()
		if scanner.consume(',') {
			continue
		}
		if !scanner.consume(']') {
			return fmt.Errorf("missing array end at byte %d", scanner.pos)
		}
		return nil
	}
}

func (scanner *anthropicCCHJSONScanner) parseString() (start, end int, err error) {
	if scanner.pos >= len(scanner.body) || scanner.body[scanner.pos] != '"' {
		return 0, 0, fmt.Errorf("missing JSON string at byte %d", scanner.pos)
	}

	start = scanner.pos
	scanner.pos++
	for scanner.pos < len(scanner.body) {
		switch scanner.body[scanner.pos] {
		case '\\':
			scanner.pos += 2
		case '"':
			scanner.pos++
			return start, scanner.pos, nil
		default:
			scanner.pos++
		}
	}
	return 0, 0, fmt.Errorf("unterminated JSON string at byte %d", start)
}

func (scanner *anthropicCCHJSONScanner) addExcludedMemberEdits(members []anthropicCCHJSONMember) {
	for start := 0; start < len(members); {
		if !members[start].excluded {
			start++
			continue
		}

		end := start
		for end+1 < len(members) && members[end+1].excluded {
			end++
		}
		switch {
		case end+1 < len(members):
			scanner.addEdit(members[start].start, members[end].commaAfter+1)
		case start > 0 && end > start:
			// Claude Code retains the preceding comma in this unusual hash view.
			scanner.addEdit(members[start].start, members[end].end)
		case start > 0:
			scanner.addEdit(members[start].commaBefore, members[end].end)
		default:
			scanner.addEdit(members[start].start, members[end].end)
		}
		start = end + 1
	}
}

func (scanner *anthropicCCHJSONScanner) addEdit(start, end int) {
	if start < end {
		scanner.edits = append(scanner.edits, anthropicCCHNormalizationEdit{start: start, end: end})
	}
}

func (scanner *anthropicCCHJSONScanner) skipWhitespace() {
	for scanner.pos < len(scanner.body) {
		switch scanner.body[scanner.pos] {
		case ' ', '\t', '\r', '\n':
			scanner.pos++
		default:
			return
		}
	}
}

func (scanner *anthropicCCHJSONScanner) consume(character byte) bool {
	if scanner.pos >= len(scanner.body) || scanner.body[scanner.pos] != character {
		return false
	}
	scanner.pos++
	return true
}

func isAnthropicCCHExcludedKey(key []byte) bool {
	switch string(key) {
	case `"max_tokens"`, `"fallbacks"`, `"fallback_credit_token"`:
		return true
	default:
		return false
	}
}
