// Package anthropictokenizer provides a local port of Anthropic's
// @anthropic-ai/tokenizer package for rough Claude token estimation.
package anthropictokenizer

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
	embeddedtokenizer "github.com/tiktoken-go/tokenizer"
	"golang.org/x/text/unicode/norm"
)

// claudeJSON is copied from Anthropic's anthropic-tokenizer-typescript package.
//
//go:embed claude.json
var claudeJSON []byte

type claudeEncoding struct {
	ExplicitNVocab int            `json:"explicit_n_vocab"`
	PatStr         string         `json:"pat_str"`
	SpecialTokens  map[string]int `json:"special_tokens"`
	BPERanks       string         `json:"bpe_ranks"`
}

var (
	tokenizerOnce sync.Once
	tokenizer     *tiktoken.Tiktoken
	tokenizerErr  error
	modernOnce    sync.Once
	modernCodec   embeddedtokenizer.Codec
	modernErr     error
)

// CountTokens returns the Anthropic reference tokenizer count for text.
// It follows @anthropic-ai/tokenizer: NFKC normalization and all special
// tokens allowed.
func CountTokens(text string) int {
	if text == "" {
		return 0
	}
	tok, err := getTokenizer()
	if err != nil {
		return fallbackTokenCount(text)
	}
	return len(tok.Encode(norm.NFKC.String(text), []string{"all"}, nil))
}

// EstimateModernClaudeTextTokens returns a conservative text-token estimate
// for Claude 4.7 and later. Anthropic does not publish the newer tokenizer and
// recommends its count_tokens endpoint for exact input counts. For translated
// Kiro output, where that endpoint cannot count generated text, cl100k provides
// a substantially closer multilingual/numeric lower bound than the legacy
// public Claude vocabulary. The legacy count remains a second lower bound.
func EstimateModernClaudeTextTokens(text string) int {
	if text == "" {
		return 0
	}
	estimated := CountTokens(text)
	modernOnce.Do(func() {
		modernCodec, modernErr = embeddedtokenizer.Get(embeddedtokenizer.Cl100kBase)
	})
	if modernErr == nil && modernCodec != nil {
		if ids, _, err := modernCodec.Encode(norm.NFKC.String(text)); err == nil && len(ids) > estimated {
			estimated = len(ids)
		}
	}
	// Newer Claude tokenization splits short high-entropy uppercase literals
	// more aggressively than cl100k. This shape is also common in nonce/tag
	// round-trips, so retain it as an additional conservative lower bound.
	if literalEstimate := shortUppercaseLiteralTokenEstimate(text); literalEstimate > estimated {
		estimated = literalEstimate
	}
	return estimated
}

func shortUppercaseLiteralTokenEstimate(text string) int {
	if len(text) < 6 || len(text) > 12 {
		return 0
	}
	hasDigit := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
			hasDigit = true
		default:
			return 0
		}
	}
	if hasDigit {
		return len(text)
	}
	// A single common pair may merge in an all-letter literal.
	return len(text) - 1
}

func getTokenizer() (*tiktoken.Tiktoken, error) {
	tokenizerOnce.Do(func() {
		tokenizer, tokenizerErr = newTokenizer()
	})
	return tokenizer, tokenizerErr
}

func newTokenizer() (*tiktoken.Tiktoken, error) {
	var enc claudeEncoding
	if err := json.Unmarshal(claudeJSON, &enc); err != nil {
		return nil, fmt.Errorf("parse claude tokenizer: %w", err)
	}
	ranks, err := parseBPERanks(enc.BPERanks, len(enc.SpecialTokens))
	if err != nil {
		return nil, err
	}
	core, err := tiktoken.NewCoreBPE(ranks, enc.SpecialTokens, enc.PatStr)
	if err != nil {
		return nil, err
	}
	specialSet := make(map[string]any, len(enc.SpecialTokens))
	for token := range enc.SpecialTokens {
		specialSet[token] = true
	}
	return tiktoken.NewTiktoken(core, &tiktoken.Encoding{
		Name:           "claude",
		PatStr:         enc.PatStr,
		MergeableRanks: ranks,
		SpecialTokens:  enc.SpecialTokens,
		ExplicitNVocab: enc.ExplicitNVocab,
	}, specialSet), nil
}

func parseBPERanks(raw string, rankOffset int) (map[string]int, error) {
	parts := strings.Fields(raw)
	ranks := make(map[string]int, len(parts))
	for i, part := range parts {
		token, err := base64.StdEncoding.DecodeString(part)
		if err != nil {
			return nil, fmt.Errorf("decode claude bpe rank %d: %w", i, err)
		}
		ranks[string(token)] = i + rankOffset
	}
	if len(ranks) != len(parts) {
		return nil, fmt.Errorf("claude tokenizer has duplicate bpe ranks")
	}
	return ranks, nil
}

func fallbackTokenCount(text string) int {
	count := len([]rune(strings.TrimSpace(text))) / 4
	if count == 0 {
		return 1
	}
	return count
}
