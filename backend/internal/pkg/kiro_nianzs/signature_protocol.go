package kiro

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protowire"
)

// kiroThinkingSignature is the boundary used by the Kiro Anthropic adapter
// when it has to materialize a thinking block.  Claude Code's Opus 5 and
// Fable 5 clients identify their thinking signatures with the CAIS envelope
// (decoded protobuf marker 0x08).  Older models keep the historical adapter
// shape until their provider-specific envelope is verified separately.
//
// The bytes inside the envelope are intentionally opaque to Sub2API.  They
// are deterministic for a (thinking, model, message) tuple so a replay of the
// same response does not create a different client-visible signature.  This
// is protocol-shape compatibility; it is not a claim that Sub2API can mint an
// Anthropic-issued cryptographic signature.
func kiroThinkingSignature(content, model, messageID string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}

	normalizedModel := normalizeKiroSignatureModel(model)
	if isKiroCAISModel(normalizedModel) {
		return buildKiroCAISThinkingSignature(content, normalizedModel, messageID)
	}
	return thinkingSignature(content, model, messageID)
}

func normalizeKiroSignatureModel(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	for strings.HasSuffix(normalized, "-thinking") {
		normalized = strings.TrimSuffix(normalized, "-thinking")
	}
	if mapped := MapModel(normalized); mapped != "" {
		return mapped
	}
	return normalized
}

func isKiroCAISModel(model string) bool {
	switch normalizeKiroSignatureModel(model) {
	case "claude-opus-5", "claude-fable-5":
		return true
	default:
		return false
	}
}

func buildKiroCAISThinkingSignature(content, model, messageID string) string {
	key := kiroSignatureKey(content, model, messageID)

	// CAIS channel block. These fields are the stable discriminator used by
	// current Claude Code clients: channel 16, schema version 2, an opaque
	// 64-byte signature, the Claude model name, block kind, and a UUID context.
	channel := make([]byte, 0, 160)
	channel = appendKiroProtoVarint(channel, 1, 16)
	channel = appendKiroProtoVarint(channel, 3, 2)
	channel = appendKiroProtoBytes(channel, 5, kiroSignatureBytes(key, "channel-signature", 64))
	channel = appendKiroProtoBytes(channel, 6, []byte(model))
	channel = appendKiroProtoVarint(channel, 7, 1)
	channel = appendKiroProtoBytes(channel, 8, []byte("thinking"))
	contextID := uuid.NewSHA1(uuid.Nil, []byte("kiro-claude-context\x00"+model+"\x00"+messageID)).String()
	channel = appendKiroProtoBytes(channel, 11, []byte(contextID))

	container := make([]byte, 0, len(channel)+90)
	container = appendKiroProtoBytes(container, 1, channel)
	container = appendKiroProtoBytes(container, 2, kiroSignatureBytes(key, "nonce", 12))
	container = appendKiroProtoBytes(container, 3, kiroSignatureBytes(key, "session", 12))
	container = appendKiroProtoBytes(container, 4, kiroSignatureBytes(key, "digest", 48))

	// CAIS top-level envelope: field 1 is the version marker, field 2 carries
	// the shared container, and field 3 is the observed trailer value.
	body := make([]byte, 0, len(container)+12)
	body = appendKiroProtoVarint(body, 1, 2)
	body = appendKiroProtoBytes(body, 2, container)
	body = appendKiroProtoVarint(body, 3, 1)
	return base64.StdEncoding.EncodeToString(body)
}

func kiroSignatureKey(content, model, messageID string) []byte {
	mac := hmac.New(sha256.New, []byte("sub2api-kiro-claude-signature-v1"))
	_, _ = mac.Write([]byte(model))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(messageID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(content))
	return mac.Sum(nil)
}

func kiroSignatureBytes(key []byte, label string, size int) []byte {
	result := make([]byte, 0, size)
	for counter := byte(0); len(result) < size; counter++ {
		mac := hmac.New(sha512.New, key)
		_, _ = mac.Write([]byte(label))
		_, _ = mac.Write([]byte{0})
		_, _ = mac.Write([]byte{counter})
		result = append(result, mac.Sum(nil)...)
	}
	return result[:size]
}

func appendKiroProtoVarint(dst []byte, field protowire.Number, value uint64) []byte {
	dst = protowire.AppendTag(dst, field, protowire.VarintType)
	return protowire.AppendVarint(dst, value)
}

func appendKiroProtoBytes(dst []byte, field protowire.Number, value []byte) []byte {
	dst = protowire.AppendTag(dst, field, protowire.BytesType)
	return protowire.AppendBytes(dst, value)
}
