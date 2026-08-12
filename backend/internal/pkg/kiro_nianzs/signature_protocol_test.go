package kiro

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestKiroThinkingSignatureOpus5UsesCAISEnvelope(t *testing.T) {
	signature := kiroThinkingSignature("reasoning text", "claude-opus-5", "msg_01shape")
	require.NotEmpty(t, signature)
	require.Equal(t, byte('C'), signature[0])

	wire, err := base64.StdEncoding.DecodeString(signature)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(wire), 3)
	require.Equal(t, byte(0x08), wire[0])

	require.Equal(t, uint64(2), requireKiroProtoVarintField(t, wire, 1))

	containerValue := requireKiroProtoBytesField(t, wire, 2)
	require.Len(t, containerValue, 390)
	channel := requireKiroProtoBytesField(t, containerValue, 1)
	channelID := requireKiroProtoVarintField(t, channel, 1)
	require.Equal(t, uint64(16), channelID)
	require.Equal(t, uint64(2), requireKiroProtoVarintField(t, channel, 3))
	require.Len(t, requireKiroProtoBytesField(t, channel, 5), 64)
	require.Equal(t, "claude-opus-5", string(requireKiroProtoBytesField(t, channel, 6)))
	require.Equal(t, "thinking", string(requireKiroProtoBytesField(t, channel, 8)))
	require.Len(t, requireKiroProtoBytesField(t, channel, 11), 36)
	require.Len(t, requireKiroProtoBytesField(t, containerValue, 5), 171)
	require.Equal(t, uint64(1), requireKiroProtoVarintField(t, wire, 3))
	require.Len(t, wire, 397)
	require.Len(t, signature, 532)
}

func TestKiroThinkingSignatureMatchesObservedClaudeEnvelopeProfiles(t *testing.T) {
	cases := []struct {
		model          string
		wireModel      string
		encodedChars   int
		decodedBytes   int
		containerBytes int
		tailBytes      int
		channelKind    uint64
		versioned      bool
	}{
		{
			model: "claude-opus-4-8", wireModel: "claude-opus-4-8",
			encodedChars: 596, decodedBytes: 447, containerBytes: 442, tailBytes: 221,
		},
		{
			model: "claude-sonnet-4-6", wireModel: "claude-sonnet-4-6",
			encodedChars: 752, decodedBytes: 562, containerBytes: 557, tailBytes: 334,
		},
		{
			model: "claude-opus-5", wireModel: "claude-opus-5",
			encodedChars: 532, decodedBytes: 397, containerBytes: 390, tailBytes: 171,
			channelKind: 1, versioned: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			signature := kiroThinkingSignature("reasoning text", tc.model, "msg_observed_shape")
			require.Len(t, signature, tc.encodedChars)
			wire, err := base64.StdEncoding.DecodeString(signature)
			require.NoError(t, err)
			require.Len(t, wire, tc.decodedBytes)
			if tc.versioned {
				require.Equal(t, uint64(2), requireKiroProtoVarintField(t, wire, 1))
			} else {
				require.Equal(t, byte(0x12), wire[0])
			}
			container := requireKiroProtoBytesField(t, wire, 2)
			require.Len(t, container, tc.containerBytes)
			channel := requireKiroProtoBytesField(t, container, 1)
			require.Equal(t, uint64(16), requireKiroProtoVarintField(t, channel, 1))
			require.Equal(t, uint64(2), requireKiroProtoVarintField(t, channel, 3))
			require.Equal(t, tc.wireModel, string(requireKiroProtoBytesField(t, channel, 6)))
			require.Equal(t, tc.channelKind, requireKiroProtoVarintField(t, channel, 7))
			require.Len(t, requireKiroProtoBytesField(t, container, 5), tc.tailBytes)
			require.Equal(t, uint64(1), requireKiroProtoVarintField(t, wire, 3))
		})
	}
}

func TestKiroThinkingSignatureIsStablePerMessageAndContent(t *testing.T) {
	first := kiroThinkingSignature("same reasoning", "claude-opus-5", "msg_a")
	replayed := kiroThinkingSignature("same reasoning", "claude-opus-5-thinking", "msg_a")
	otherMessage := kiroThinkingSignature("same reasoning", "claude-opus-5", "msg_b")
	otherContent := kiroThinkingSignature("different reasoning", "claude-opus-5", "msg_a")

	require.Equal(t, first, replayed)
	require.NotEqual(t, first, otherMessage)
	require.NotEqual(t, first, otherContent)
}

func TestKiroThinkingSignatureFallsBackForUnverifiedModel(t *testing.T) {
	legacy := thinkingSignature("reasoning", "claude-opus-4-6", "msg_legacy")
	require.Equal(t, legacy, kiroThinkingSignature("reasoning", "claude-opus-4-6", "msg_legacy"))
}

func requireKiroProtoTreeField(t *testing.T, payload []byte, wantField protowire.Number, wantType protowire.Type) (uint64, []byte) {
	t.Helper()
	var varintValue uint64
	var bytesValue []byte
	for len(payload) > 0 {
		field, typ, tagLen := protowire.ConsumeTag(payload)
		require.Greater(t, tagLen, 0)
		payload = payload[tagLen:]
		valueLen := protowire.ConsumeFieldValue(field, typ, payload)
		require.Greater(t, valueLen, 0)
		raw := payload[:valueLen]
		payload = payload[valueLen:]
		if field != wantField {
			continue
		}
		require.Equal(t, wantType, typ)
		switch typ {
		case protowire.VarintType:
			varintValue, _ = protowire.ConsumeVarint(raw)
		case protowire.BytesType:
			bytesValue, _ = protowire.ConsumeBytes(raw)
		}
		return varintValue, bytesValue
	}
	t.Fatalf("protobuf field %d not found", wantField)
	return 0, nil
}

func requireKiroProtoVarintField(t *testing.T, payload []byte, field protowire.Number) uint64 {
	t.Helper()
	value, _ := requireKiroProtoTreeField(t, payload, field, protowire.VarintType)
	return value
}

func requireKiroProtoBytesField(t *testing.T, payload []byte, field protowire.Number) []byte {
	t.Helper()
	_, value := requireKiroProtoTreeField(t, payload, field, protowire.BytesType)
	return value
}
