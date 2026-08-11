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
	channel := requireKiroProtoBytesField(t, containerValue, 1)
	channelID := requireKiroProtoVarintField(t, channel, 1)
	require.Equal(t, uint64(16), channelID)
	require.Equal(t, uint64(2), requireKiroProtoVarintField(t, channel, 3))
	require.Len(t, requireKiroProtoBytesField(t, channel, 5), 64)
	require.Equal(t, "claude-opus-5", string(requireKiroProtoBytesField(t, channel, 6)))
	require.Equal(t, "thinking", string(requireKiroProtoBytesField(t, channel, 8)))
	require.Len(t, requireKiroProtoBytesField(t, channel, 11), 36)
	require.Equal(t, uint64(1), requireKiroProtoVarintField(t, wire, 3))
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
