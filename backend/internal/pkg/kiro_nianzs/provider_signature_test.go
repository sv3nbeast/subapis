package kiro

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestValidateProviderThinkingSignatureAcceptsNativeKiroEnvelope(t *testing.T) {
	signature := providerThinkingSignatureFixture(t, true)

	metadata, err := validateProviderThinkingSignature(signature)
	require.NoError(t, err)
	require.Equal(t, "claude-quince", metadata.WireModel)
	require.Zero(t, metadata.ChannelKind)
	require.Equal(t, "015911059195", metadata.ContextID)
	require.Equal(t, 128, metadata.SignedPayloadBytes)
}

func TestValidateProviderThinkingSignatureRejectsFormerLocalFallbackShape(t *testing.T) {
	_, err := validateProviderThinkingSignature(providerThinkingSignatureFixture(t, false))
	require.ErrorContains(t, err, "provider-native marker field count is 0")
}

func TestValidateProviderThinkingSignatureRejectsMalformedBase64(t *testing.T) {
	_, err := validateProviderThinkingSignature("not a signature")
	require.ErrorContains(t, err, "decode provider thinking signature")
}

func providerThinkingSignatureFixtureWithOuterMarker(t *testing.T) string {
	t.Helper()
	wire, err := base64.StdEncoding.DecodeString(providerThinkingSignatureFixture(t, true))
	require.NoError(t, err)
	prefix := protowire.AppendTag(nil, 1, protowire.VarintType)
	prefix = protowire.AppendVarint(prefix, 2)
	wire = append(prefix, wire...)
	return base64.StdEncoding.EncodeToString(wire)
}

func providerThinkingSignatureFixture(t *testing.T, providerNative bool) string {
	t.Helper()
	appendVarint := func(dst []byte, field protowire.Number, value uint64) []byte {
		dst = protowire.AppendTag(dst, field, protowire.VarintType)
		return protowire.AppendVarint(dst, value)
	}
	appendBytes := func(dst []byte, field protowire.Number, value []byte) []byte {
		dst = protowire.AppendTag(dst, field, protowire.BytesType)
		return protowire.AppendBytes(dst, value)
	}
	repeated := func(value byte, count int) []byte {
		result := make([]byte, count)
		for i := range result {
			result[i] = value
		}
		return result
	}

	channel := appendVarint(nil, 1, 16)
	if providerNative {
		channel = appendVarint(channel, 2, 1)
	}
	channel = appendVarint(channel, 3, 2)
	channel = appendBytes(channel, 5, repeated(0x51, 64))
	channel = appendBytes(channel, 6, []byte("claude-quince"))
	channel = appendVarint(channel, 7, 0)
	channel = appendBytes(channel, 8, []byte("thinking"))
	channel = appendBytes(channel, 11, []byte("015911059195"))

	inner := appendBytes(nil, 1, channel)
	inner = appendBytes(inner, 2, repeated(0x52, 12))
	inner = appendBytes(inner, 3, repeated(0x53, 12))
	inner = appendBytes(inner, 4, repeated(0x54, 48))
	inner = appendBytes(inner, 5, repeated(0x55, 128))

	body := appendBytes(nil, 2, inner)
	body = appendVarint(body, 3, 1)
	return base64.StdEncoding.EncodeToString(body)
}
