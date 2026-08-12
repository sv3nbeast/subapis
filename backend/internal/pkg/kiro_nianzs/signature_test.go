package kiro

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenerateClaudeSignatureMatchesLegacyCompatibilityEnvelopeShape(t *testing.T) {
	signature := generateClaudeSignature("reasoning text", "claude-opus-5", "msg_shape")
	require.Len(t, signature, 312)
	require.True(t, strings.HasPrefix(signature, "EuYBCkQYAiJA"), signature[:16])

	wire, err := base64.StdEncoding.DecodeString(signature)
	require.NoError(t, err)
	require.Len(t, wire, 233)
	require.Equal(t, []byte{0x12, 0xe6, 0x01}, wire[:3])
	require.Equal(t, []byte{0x0a, 0x44, 0x18, 0x02, 0x22, 0x40}, wire[3:9])

	inner := requireKiroProtoBytesField(t, wire, 2)
	require.Len(t, inner, 230)
	channel := requireKiroProtoBytesField(t, inner, 1)
	require.Equal(t, uint64(2), requireKiroProtoVarintField(t, channel, 3))
	require.Len(t, requireKiroProtoBytesField(t, channel, 4), 64)
	require.Len(t, requireKiroProtoBytesField(t, inner, 2), 12)
	require.Len(t, requireKiroProtoBytesField(t, inner, 3), 12)
	require.Len(t, requireKiroProtoBytesField(t, inner, 4), 48)
	require.Len(t, requireKiroProtoBytesField(t, inner, 5), 80)
}

func TestThinkingSignatureConcurrentReplayUsesOneValue(t *testing.T) {
	const workers = 64
	start := make(chan struct{})
	results := make(chan string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- thinkingSignature("concurrent replay content", "claude-opus-4-8", "msg_concurrent_replay")
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var first string
	for signature := range results {
		require.NotEmpty(t, signature)
		if first == "" {
			first = signature
			continue
		}
		require.Equal(t, first, signature)
	}
}

func TestSignatureCacheKeyUsesUnambiguousTupleEncoding(t *testing.T) {
	require.NotEqual(t,
		signatureCacheKey("a:b", "c", "d"),
		signatureCacheKey("a", "b:c", "d"),
	)
}

func TestThinkingSignatureIsStableForReplayAndFreshAcrossMessages(t *testing.T) {
	content := "same thinking content"
	first := thinkingSignature(content, "claude-opus-5", "msg_replay_a")
	replayed := thinkingSignature(content, "claude-opus-5", "msg_replay_a")
	secondMessage := thinkingSignature(content, "claude-opus-5", "msg_replay_b")

	require.Equal(t, first, replayed)
	require.NotEqual(t, first, secondMessage)
	require.NotContains(t, first, content)
}

func TestDeterministicSignatureOpaqueFallbackHasRequestedLengthAndVaries(t *testing.T) {
	key := []byte("test-key")
	first := deterministicSignatureOpaque(key, "one", 216)
	second := deterministicSignatureOpaque(key, "two", 216)
	require.Len(t, first, 216)
	require.Len(t, second, 216)
	require.NotEqual(t, first, second)
}
