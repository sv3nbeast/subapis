package kiro

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"sync"
)

const signatureCacheMaxSize = 1000

type sigCacheEntry struct {
	key   string
	value string
}

var (
	signatureLRU      = list.New()
	signatureCacheMap = make(map[string]*list.Element)
	signatureCacheMu  sync.Mutex
)

func thinkingSignature(content, model, messageID string) string {
	if content == "" {
		return ""
	}

	cacheKey := signatureCacheKey(content, model, messageID)

	signatureCacheMu.Lock()
	if elem, ok := signatureCacheMap[cacheKey]; ok {
		signatureLRU.MoveToFront(elem)
		signatureCacheMu.Unlock()
		if entry, ok := elem.Value.(*sigCacheEntry); ok && entry != nil {
			return entry.value
		}
		return ""
	}
	signatureCacheMu.Unlock()

	sig := generateClaudeSignature(content, model, messageID)

	signatureCacheMu.Lock()
	// Another goroutine may have generated this signature while rand.Reader was
	// running. Reuse the winner so one logical response never exposes two
	// different signatures merely because its stream was replayed concurrently.
	if elem, ok := signatureCacheMap[cacheKey]; ok {
		signatureLRU.MoveToFront(elem)
		signatureCacheMu.Unlock()
		if entry, ok := elem.Value.(*sigCacheEntry); ok && entry != nil {
			return entry.value
		}
		return ""
	}
	for signatureLRU.Len() >= signatureCacheMaxSize {
		if oldest := signatureLRU.Back(); oldest != nil {
			if entry, ok := oldest.Value.(*sigCacheEntry); ok && entry != nil {
				delete(signatureCacheMap, entry.key)
			}
			signatureLRU.Remove(oldest)
		}
	}
	entry := &sigCacheEntry{key: cacheKey, value: sig}
	elem := signatureLRU.PushFront(entry)
	signatureCacheMap[cacheKey] = elem
	signatureCacheMu.Unlock()

	return sig
}

func generateClaudeSignature(thinkingContent, model, messageID string) string {
	keyMaterial := deriveSignatureKey(model, messageID)
	// Thinking signatures are opaque to clients. The adapter-issued legacy
	// signature uses the protobuf envelope observed by compatibility probes:
	//
	//   field 2 (230 bytes) {
	//     field 1 (68 bytes) { field 3 = 2; field 4 = 64 opaque bytes }
	//     field 2 = 12 opaque bytes
	//     field 3 = 12 opaque bytes
	//     field 4 = 48 opaque bytes
	//     field 5 = 80 opaque bytes
	//   }
	//
	// These bytes are not presented as an Anthropic cryptographic signature;
	// they provide protocol-shape compatibility for a provider that does not
	// expose Anthropic's upstream signature. Keep every payload field opaque,
	// bind the first digest to this response, and preserve fresh entropy per
	// logical message.
	opaque := make([]byte, 64+12+12+48+80)
	if _, err := io.ReadFull(rand.Reader, opaque); err != nil {
		opaque = deterministicSignatureOpaque(keyMaterial, thinkingContent, len(opaque))
	}
	mac := hmac.New(sha256.New, keyMaterial)
	_, _ = mac.Write([]byte(thinkingContent))
	contentMAC := mac.Sum(nil)
	for i := range contentMAC {
		opaque[i] ^= contentMAC[i]
	}

	channel := make([]byte, 0, 68)
	channel = appendKiroProtoVarint(channel, 3, 2)
	channel = appendKiroProtoBytes(channel, 4, opaque[:64])

	inner := make([]byte, 0, 230)
	inner = appendKiroProtoBytes(inner, 1, channel)
	inner = appendKiroProtoBytes(inner, 2, opaque[64:76])
	inner = appendKiroProtoBytes(inner, 3, opaque[76:88])
	inner = appendKiroProtoBytes(inner, 4, opaque[88:136])
	inner = appendKiroProtoBytes(inner, 5, opaque[136:216])

	body := make([]byte, 0, 233)
	body = appendKiroProtoBytes(body, 2, inner)
	return base64.StdEncoding.EncodeToString(body)
}

func deterministicSignatureOpaque(keyMaterial []byte, content string, size int) []byte {
	result := make([]byte, 0, size)
	var previous []byte
	for counter := byte(1); len(result) < size; counter++ {
		mac := hmac.New(sha256.New, keyMaterial)
		_, _ = mac.Write(previous)
		_, _ = mac.Write([]byte(content))
		_, _ = mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		result = append(result, previous...)
	}
	return result[:size]
}

func deriveSignatureKey(model, messageID string) []byte {
	mac := hmac.New(sha256.New, []byte("anthropic-thinking-signature-v2"))
	_, _ = mac.Write([]byte(model))
	_, _ = mac.Write([]byte(":"))
	_, _ = mac.Write([]byte(messageID))
	return mac.Sum(nil)
}

func signatureCacheKey(content, model, messageID string) string {
	h := sha256.New()
	for _, value := range []string{content, model, messageID} {
		var size [8]byte
		for i := uint(0); i < 8; i++ {
			size[7-i] = byte(uint64(len(value)) >> (i * 8))
		}
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:16])
}
