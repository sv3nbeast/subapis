#!/usr/bin/env python3
"""Unit tests for the Kiro protocol probe's provider-envelope parser."""

from __future__ import annotations

import base64
import unittest

from kiro_protocol_probe import (
    ProbeError,
    validate_adaptive_thinking_progress,
    validate_thinking_signature,
)


def proto_varint(value: int) -> bytes:
    result = bytearray()
    while value >= 0x80:
        result.append((value & 0x7F) | 0x80)
        value >>= 7
    result.append(value)
    return bytes(result)


def varint_field(number: int, value: int) -> bytes:
    return proto_varint(number << 3) + proto_varint(value)


def bytes_field(number: int, value: bytes) -> bytes:
    return proto_varint((number << 3) | 2) + proto_varint(len(value)) + value


def provider_signature(*, outer_prefix: bool, provider_marker: bool = True) -> str:
    channel = varint_field(1, 16)
    if provider_marker:
        channel += varint_field(2, 1)
    channel += varint_field(3, 2)
    channel += bytes_field(5, b"S" * 64)
    channel += bytes_field(6, b"claude-quince")
    channel += varint_field(7, 0)
    channel += bytes_field(8, b"thinking")
    channel += bytes_field(11, b"015911059195")

    inner = bytes_field(1, channel)
    inner += bytes_field(2, b"N" * 12)
    inner += bytes_field(3, b"I" * 12)
    inner += bytes_field(4, b"A" * 48)
    inner += bytes_field(5, b"P" * 128)

    outer = varint_field(1, 2) if outer_prefix else b""
    outer += bytes_field(2, inner) + varint_field(3, 1)
    return base64.b64encode(outer).decode()


class ThinkingSignatureEnvelopeTest(unittest.TestCase):
    def test_accepts_provider_envelope_with_outer_field_one_prefix(self) -> None:
        facts = validate_thinking_signature(
            provider_signature(outer_prefix=True),
            "thinking signature",
        )
        self.assertEqual(facts["first_tag"], "0x08")
        self.assertEqual(facts["provider_channel"], "claude-quince")
        self.assertTrue(facts["provider_native"])
        self.assertEqual(facts["signed_payload_bytes"], 128)

    def test_accepts_provider_envelope_starting_with_field_two(self) -> None:
        facts = validate_thinking_signature(
            provider_signature(outer_prefix=False),
            "thinking signature",
        )
        self.assertEqual(facts["first_tag"], "0x12")

    def test_rejects_former_local_fallback_without_provider_marker(self) -> None:
        with self.assertRaisesRegex(ProbeError, "provider-issued Kiro envelope"):
            validate_thinking_signature(
                provider_signature(outer_prefix=False, provider_marker=False),
                "thinking signature",
            )

    def test_accepts_native_claude_envelope_without_kiro_marker(self) -> None:
        facts = validate_thinking_signature(
            provider_signature(outer_prefix=False, provider_marker=False),
            "thinking signature",
            "claude",
        )
        self.assertEqual(facts["issuer"], "claude")
        self.assertFalse(facts["kiro_marker_present"])

    def test_rejects_kiro_envelope_when_native_claude_is_expected(self) -> None:
        with self.assertRaisesRegex(ProbeError, "native Claude envelope"):
            validate_thinking_signature(
                provider_signature(outer_prefix=False, provider_marker=True),
                "thinking signature",
                "claude",
            )


class AdaptiveThinkingProgressTest(unittest.TestCase):
    def test_accepts_minimal_compatibility_sequence(self) -> None:
        validate_adaptive_thinking_progress({
            "thinking_delta_count": 3,
            "signature_delta_count": 1,
            "thinking_progress": [50, 100, None],
        })

    def test_accepts_dynamic_native_claude_sequence(self) -> None:
        validate_adaptive_thinking_progress({
            "thinking_delta_count": 9,
            "signature_delta_count": 1,
            "thinking_progress": [50, 100, 150, 150, 100, 150, 150, 150, None],
        })

    def test_rejects_missing_final_null(self) -> None:
        with self.assertRaisesRegex(ProbeError, "final progress is not null"):
            validate_adaptive_thinking_progress({
                "thinking_delta_count": 2,
                "signature_delta_count": 1,
                "thinking_progress": [50, 100],
            })


if __name__ == "__main__":
    unittest.main()
