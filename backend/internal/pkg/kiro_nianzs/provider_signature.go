package kiro

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

// providerThinkingSignature describes the authenticated envelope emitted by
// Kiro. The cryptographic payload remains opaque: Sub2API validates the wire
// contract and passes the exact upstream bytes through without rewriting them.
type providerThinkingSignature struct {
	WireModel          string
	ChannelVersion     uint64
	ChannelKind        uint64
	ContextID          string
	SignedPayloadBytes int
}

type providerSignatureField struct {
	wireType protowire.Type
	varint   uint64
	bytes    []byte
}

// validateProviderThinkingSignature accepts only the provider-native Kiro
// envelope. In particular, channel field 2=1 distinguishes an upstream Kiro
// signature from the former locally generated, shape-compatible fallback.
// Cryptographic verification is intentionally left to the issuing provider;
// Sub2API never has the provider's private verification material.
func validateProviderThinkingSignature(value string) (providerThinkingSignature, error) {
	return validateThinkingSignatureEnvelope(value, true)
}

func decodeThinkingSignature(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	wire, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		wire, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, fmt.Errorf("decode provider thinking signature: %w", err)
	}
	return wire, nil
}

func validateThinkingSignatureEnvelope(value string, requireKiroMarker bool) (providerThinkingSignature, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return providerThinkingSignature{}, errors.New("empty provider thinking signature")
	}
	wire, err := decodeThinkingSignature(value)
	if err != nil {
		return providerThinkingSignature{}, err
	}
	outer, err := parseProviderSignatureFields(wire)
	if err != nil {
		return providerThinkingSignature{}, fmt.Errorf("parse provider thinking signature envelope: %w", err)
	}
	container, err := requireProviderSignatureBytes(outer, 2, "envelope payload")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	if err := requireProviderSignatureVarint(outer, 3, 1, "thinking envelope kind"); err != nil {
		return providerThinkingSignature{}, err
	}

	inner, err := parseProviderSignatureFields(container)
	if err != nil {
		return providerThinkingSignature{}, fmt.Errorf("parse provider thinking signature payload: %w", err)
	}
	channelWire, err := requireProviderSignatureBytes(inner, 1, "channel header")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	for _, requirement := range []struct {
		field  protowire.Number
		length int
		name   string
	}{
		{2, 12, "nonce"},
		{3, 12, "session"},
		{4, 48, "authenticator"},
	} {
		value, fieldErr := requireProviderSignatureBytes(inner, requirement.field, requirement.name)
		if fieldErr != nil {
			return providerThinkingSignature{}, fieldErr
		}
		if len(value) != requirement.length {
			return providerThinkingSignature{}, fmt.Errorf("provider thinking signature %s has %d bytes, want %d", requirement.name, len(value), requirement.length)
		}
	}
	signedPayload, err := requireProviderSignatureBytes(inner, 5, "signed payload")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	if len(signedPayload) == 0 {
		return providerThinkingSignature{}, errors.New("provider thinking signature has an empty signed payload")
	}

	channel, err := parseProviderSignatureFields(channelWire)
	if err != nil {
		return providerThinkingSignature{}, fmt.Errorf("parse provider thinking signature channel: %w", err)
	}
	channelVersion, err := requireProviderSignatureField(channel, 1, protowire.VarintType, "channel version")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	// Amazon Q currently emits channel version 17 while older, still valid
	// responses use version 16. Keep the allowlist explicit: the signature is
	// provider-owned opaque data, so an unknown future version must be reviewed
	// instead of being accepted as structurally equivalent by accident.
	if channelVersion.varint != 16 && channelVersion.varint != 17 {
		return providerThinkingSignature{}, fmt.Errorf("provider thinking signature channel version is %d, want 16 or 17", channelVersion.varint)
	}
	if requireKiroMarker {
		if err := requireProviderSignatureVarint(channel, 2, 1, "provider-native marker"); err != nil {
			return providerThinkingSignature{}, err
		}
	} else if len(channel[2]) != 0 {
		return providerThinkingSignature{}, errors.New("Claude thinking signature unexpectedly contains Kiro provider marker")
	}
	if err := requireProviderSignatureVarint(channel, 3, 2, "signature schema"); err != nil {
		return providerThinkingSignature{}, err
	}
	channelSignature, err := requireProviderSignatureBytes(channel, 5, "channel signature")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	if len(channelSignature) != 64 {
		return providerThinkingSignature{}, fmt.Errorf("provider thinking signature channel signature has %d bytes, want 64", len(channelSignature))
	}
	wireModelBytes, err := requireProviderSignatureBytes(channel, 6, "provider channel")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	wireModel := string(wireModelBytes)
	if !strings.HasPrefix(wireModel, "claude-") {
		return providerThinkingSignature{}, fmt.Errorf("provider thinking signature has invalid channel %q", wireModel)
	}
	channelKindField, err := requireProviderSignatureField(channel, 7, protowire.VarintType, "channel kind")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	blockKind, err := requireProviderSignatureBytes(channel, 8, "block kind")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	if string(blockKind) != "thinking" {
		return providerThinkingSignature{}, fmt.Errorf("provider thinking signature has block kind %q", string(blockKind))
	}
	contextID, err := requireProviderSignatureBytes(channel, 11, "context ID")
	if err != nil {
		return providerThinkingSignature{}, err
	}
	if len(contextID) == 0 {
		return providerThinkingSignature{}, errors.New("provider thinking signature has an empty context ID")
	}

	return providerThinkingSignature{
		WireModel:          wireModel,
		ChannelVersion:     channelVersion.varint,
		ChannelKind:        channelKindField.varint,
		ContextID:          string(contextID),
		SignedPayloadBytes: len(signedPayload),
	}, nil
}

func parseProviderSignatureFields(payload []byte) (map[protowire.Number][]providerSignatureField, error) {
	fields := make(map[protowire.Number][]providerSignatureField)
	for len(payload) > 0 {
		fieldNumber, wireType, tagLength := protowire.ConsumeTag(payload)
		if tagLength < 0 {
			return nil, protowire.ParseError(tagLength)
		}
		if fieldNumber <= 0 {
			return nil, errors.New("invalid protobuf field number")
		}
		payload = payload[tagLength:]
		field := providerSignatureField{wireType: wireType}
		switch wireType {
		case protowire.VarintType:
			value, length := protowire.ConsumeVarint(payload)
			if length < 0 {
				return nil, protowire.ParseError(length)
			}
			field.varint = value
			payload = payload[length:]
		case protowire.BytesType:
			value, length := protowire.ConsumeBytes(payload)
			if length < 0 {
				return nil, protowire.ParseError(length)
			}
			field.bytes = value
			payload = payload[length:]
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", wireType)
		}
		fields[fieldNumber] = append(fields[fieldNumber], field)
	}
	return fields, nil
}

func requireProviderSignatureField(fields map[protowire.Number][]providerSignatureField, fieldNumber protowire.Number, wireType protowire.Type, name string) (providerSignatureField, error) {
	values := fields[fieldNumber]
	if len(values) != 1 {
		return providerSignatureField{}, fmt.Errorf("provider thinking signature %s field count is %d, want 1", name, len(values))
	}
	if values[0].wireType != wireType {
		return providerSignatureField{}, fmt.Errorf("provider thinking signature %s has wire type %d, want %d", name, values[0].wireType, wireType)
	}
	return values[0], nil
}

func requireProviderSignatureBytes(fields map[protowire.Number][]providerSignatureField, fieldNumber protowire.Number, name string) ([]byte, error) {
	field, err := requireProviderSignatureField(fields, fieldNumber, protowire.BytesType, name)
	if err != nil {
		return nil, err
	}
	return field.bytes, nil
}

func requireProviderSignatureVarint(fields map[protowire.Number][]providerSignatureField, fieldNumber protowire.Number, expected uint64, name string) error {
	field, err := requireProviderSignatureField(fields, fieldNumber, protowire.VarintType, name)
	if err != nil {
		return err
	}
	if field.varint != expected {
		return fmt.Errorf("provider thinking signature %s is %d, want %d", name, field.varint, expected)
	}
	return nil
}
