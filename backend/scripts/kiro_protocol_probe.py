#!/usr/bin/env python3
"""Black-box Kiro/Anthropic protocol fidelity probe.

The probe intentionally uses only Python's standard library. Supply the API key
through SUB2API_API_KEY; it is never included in output.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any, Callable


STOP_REASONS = {
    "end_turn",
    "max_tokens",
    "stop_sequence",
    "tool_use",
    "pause_turn",
    "refusal",
    "model_context_window_exceeded",
}
WEB_SEARCH_ERRORS = {
    "invalid_tool_input",
    "unavailable",
    "max_uses_exceeded",
    "too_many_requests",
    "query_too_long",
    "request_too_large",
}


class ProbeError(RuntimeError):
    pass


@dataclass
class SSEEvent:
    name: str
    data: dict[str, Any]


class Client:
    def __init__(self, base_url: str, api_key: str, timeout: float, user_agent: str) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout
        self.user_agent = user_agent

    def _request(self, method: str, path: str, payload: dict[str, Any] | None = None) -> bytes:
        body = None if payload is None else json.dumps(payload, separators=(",", ":")).encode()
        request = urllib.request.Request(
            self.base_url + path,
            data=body,
            method=method,
            headers={
                "Accept": "application/json, text/event-stream",
                "Content-Type": "application/json",
                "Anthropic-Version": "2023-06-01",
                "Authorization": "Bearer " + self.api_key,
                "X-Api-Key": self.api_key,
                "User-Agent": self.user_agent,
                "X-App": "cli",
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                return response.read()
        except urllib.error.HTTPError as exc:
            detail = exc.read(4096).decode("utf-8", "replace")
            raise ProbeError(f"HTTP {exc.code}: {detail}") from exc
        except urllib.error.URLError as exc:
            raise ProbeError(f"request failed: {exc.reason}") from exc

    def models(self) -> list[str]:
        payload = decode_json(self._request("GET", "/v1/models"))
        data = payload.get("data")
        if not isinstance(data, list):
            raise ProbeError("/v1/models response has no data array")
        return [item.get("id", "") for item in data if isinstance(item, dict) and item.get("id")]

    def messages(self, payload: dict[str, Any]) -> bytes:
        return self._request("POST", "/v1/messages", payload)


def decode_json(raw: bytes) -> dict[str, Any]:
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ProbeError(f"invalid JSON response: {exc}") from exc
    if not isinstance(value, dict):
        raise ProbeError("response JSON is not an object")
    if value.get("type") == "error":
        raise ProbeError(f"API error: {value.get('error')}")
    return value


def parse_sse(raw: bytes) -> list[SSEEvent]:
    wire = raw.decode("utf-8", "strict").replace("\r\n", "\n").replace("\r", "\n")
    events: list[SSEEvent] = []
    for frame in wire.split("\n\n"):
        if not frame.strip():
            continue
        event_name = ""
        data_lines: list[str] = []
        for line in frame.split("\n"):
            if line.startswith(":"):
                continue
            if line.startswith("event:"):
                event_name = line[6:].strip()
            elif line.startswith("data:"):
                data_lines.append(line[5:].lstrip())
        if not data_lines:
            continue
        data_text = "\n".join(data_lines)
        if data_text == "[DONE]":
            continue
        try:
            data = json.loads(data_text)
        except json.JSONDecodeError as exc:
            raise ProbeError(f"invalid SSE JSON in {event_name or '<unnamed>'}: {exc}") from exc
        if not isinstance(data, dict):
            raise ProbeError("SSE data is not an object")
        event_type = data.get("type")
        event_name = event_name or str(event_type or "")
        if event_name == "ping":
            continue
        if event_name == "error" or event_type == "error":
            raise ProbeError(f"SSE error: {data.get('error')}")
        if event_name != event_type:
            raise ProbeError(f"SSE event/type mismatch: {event_name!r} != {event_type!r}")
        events.append(SSEEvent(event_name, data))
    if not events:
        raise ProbeError("empty SSE stream")
    return events


def require(condition: bool, message: str) -> None:
    if not condition:
        raise ProbeError(message)


def opaque_base64(value: Any, field: str) -> bytes:
    require(isinstance(value, str) and bool(value), f"{field} is empty")
    padded = value + "=" * ((4 - len(value) % 4) % 4)
    try:
        decoded = base64.b64decode(padded, validate=True)
    except ValueError as exc:
        raise ProbeError(f"{field} is not valid base64") from exc
    require(bool(decoded), f"{field} decodes to an empty value")
    return decoded


def read_proto_varint(wire: bytes, offset: int) -> tuple[int, int]:
    value = 0
    shift = 0
    while offset < len(wire) and shift < 70:
        current = wire[offset]
        offset += 1
        value |= (current & 0x7f) << shift
        if current < 0x80:
            return value, offset
        shift += 7
    raise ProbeError("invalid protobuf varint")


def protobuf_fields(wire: bytes, field: str) -> dict[int, tuple[int, Any]]:
    fields: dict[int, tuple[int, Any]] = {}
    offset = 0
    while offset < len(wire):
        tag, offset = read_proto_varint(wire, offset)
        number, wire_type = tag >> 3, tag & 7
        require(number > 0, f"{field} has an invalid protobuf field number")
        if wire_type == 0:
            value, offset = read_proto_varint(wire, offset)
        elif wire_type == 2:
            size, offset = read_proto_varint(wire, offset)
            require(size <= len(wire) - offset, f"{field} has a truncated protobuf field")
            value = wire[offset:offset + size]
            offset += size
        else:
            raise ProbeError(f"{field} has unsupported protobuf wire type {wire_type}")
        fields[number] = (wire_type, value)
    return fields


def validate_thinking_signature(value: Any, field: str) -> dict[str, Any]:
    wire = opaque_base64(value, field)
    require(wire[0] == 0x12, f"{field} is not a field-2 protobuf envelope")
    outer = protobuf_fields(wire, field)
    require(2 in outer and outer[2][0] == 2, f"{field} has no protobuf envelope payload")
    require(outer.get(3) == (0, 1), f"{field} has the wrong thinking envelope kind")

    inner = protobuf_fields(outer[2][1], field)
    require(1 in inner and inner[1][0] == 2, f"{field} has no channel header")
    require(2 in inner and len(inner[2][1]) == 12, f"{field} has an invalid nonce")
    require(3 in inner and len(inner[3][1]) == 12, f"{field} has an invalid session value")
    require(4 in inner and len(inner[4][1]) == 48, f"{field} has an invalid authenticator")
    require(5 in inner and bool(inner[5][1]), f"{field} has an empty signed payload")

    channel = protobuf_fields(inner[1][1], field)
    require(channel.get(1) == (0, 16), f"{field} has an invalid channel version")
    require(channel.get(3) == (0, 2), f"{field} has an invalid signature schema")
    require(5 in channel and len(channel[5][1]) == 64, f"{field} has an invalid channel signature")
    require(6 in channel and channel[6][0] == 2, f"{field} has no provider channel")
    provider_channel = channel[6][1].decode("ascii", "strict")
    require(provider_channel.startswith("claude-"), f"{field} provider channel is invalid")
    require(channel.get(7) is not None and channel[7][0] == 0, f"{field} has no channel kind")
    require(channel.get(8) == (2, b"thinking"), f"{field} has the wrong block kind")
    require(11 in channel and channel[11][0] == 2 and bool(channel[11][1]), f"{field} has no context ID")
    # Kiro provider signatures carry field 2=1 in their channel header. The
    # local compatibility fallback deliberately omits it, so this lets the
    # online probe catch regressions back to a merely shape-compatible value.
    provider_native = channel.get(2) == (0, 1)
    return {
        "marker": f"0x{wire[0]:02x}",
        "decoded_bytes": len(wire),
        "provider_channel": provider_channel,
        "provider_native": provider_native,
        "context_id_bytes": len(channel[11][1]),
        "signed_payload_bytes": len(inner[5][1]),
    }


def validate_web_opaque_envelope(value: Any, field: str, expected_kind: int) -> str:
    wire = opaque_base64(value, field)
    require(wire[0] == 0x12, f"{field} is not a field-2 protobuf envelope")
    outer = protobuf_fields(wire, field)
    require(2 in outer and outer[2][0] == 2, f"{field} has no protobuf envelope payload")
    require(3 in outer and outer[3] == (0, expected_kind), f"{field} has the wrong envelope kind")
    inner = protobuf_fields(outer[2][1], field)
    require(1 in inner and inner[1][0] == 2, f"{field} has no envelope header")
    require(2 in inner and len(inner[2][1]) == 12, f"{field} has an invalid salt")
    require(3 in inner and len(inner[3][1]) == 12, f"{field} has an invalid nonce")
    require(4 in inner and len(inner[4][1]) == 48, f"{field} has an invalid authenticator")
    require(5 in inner and bool(inner[5][1]), f"{field} has an empty ciphertext")
    header = protobuf_fields(inner[1][1], field)
    require(header.get(1) == (0, 18), f"{field} has an invalid header version")
    require(header.get(3) == (0, 2), f"{field} has an invalid header type")
    require(4 in header and header[4][0] == 2, f"{field} has no response identifier")
    response_id = header[4][1].decode("ascii", "strict")
    require(len(response_id) == 36 and response_id.count("-") == 4, f"{field} response identifier is invalid")
    return response_id


def validate_web_result_content(content: Any) -> tuple[int, str | None, set[str]]:
    if isinstance(content, list):
        response_ids: set[str] = set()
        for result in content:
            require(isinstance(result, dict), "web-search result item is not an object")
            require(result.get("type") == "web_search_result", "invalid web-search result type")
            require(bool(result.get("title")), "web-search result title is empty")
            require(bool(result.get("url")), "web-search result URL is empty")
            response_ids.add(validate_web_opaque_envelope(
                result.get("encrypted_content"), "encrypted_content", 0,
            ))
        require(len(response_ids) == 1, "web-search results do not share one opaque response ID")
        return len(content), None, response_ids
    require(isinstance(content, dict), "web-search result content has invalid union shape")
    require(content.get("type") == "web_search_tool_result_error", "invalid web-search error type")
    error_code = content.get("error_code")
    require(error_code in WEB_SEARCH_ERRORS, f"invalid web-search error code: {error_code!r}")
    return 0, str(error_code), set()


def validate_direct_caller(block: dict[str, Any]) -> None:
    caller = block.get("caller")
    require(isinstance(caller, dict), "web-search result caller is missing")
    require(caller.get("type") == "direct", "web-search result caller is not direct")


def validate_sse(events: list[SSEEvent]) -> dict[str, Any]:
    first = events[0]
    require(first.name == "message_start", "message_start is not the first event")
    message = first.data.get("message")
    require(isinstance(message, dict), "message_start.message is missing")
    require(str(message.get("id", "")).startswith("msg_"), "message ID does not start with msg_")
    require(message.get("role") == "assistant", "message role is not assistant")
    require(message.get("content") == [], "message_start content must be empty")
    require(message.get("stop_reason") is None, "message_start stop_reason must be null")

    next_index = 0
    open_index: int | None = None
    open_type = ""
    last_delta = ""
    json_fragments: list[str] = []
    server_tool_ids: set[str] = set()
    server_tool_names: dict[str, str] = {}
    result_tool_ids: set[str] = set()
    code_execution_result_ids: set[str] = set()
    code_execution_successes = 0
    code_execution_errors: list[str] = []
    code_execution_stdout: list[str] = []
    signatures: list[str] = []
    redacted_thinking_blocks = 0
    citations = 0
    result_count = 0
    result_errors: list[str] = []
    result_response_ids: set[str] = set()
    citation_response_ids: set[str] = set()
    client_web_search_tool_uses = 0
    tool_uses = 0
    tool_names: list[str] = []
    first_content_block_type = ""
    cited_text_blocks = 0
    citation_deltas_before_text = 0
    open_text_has_citations = False
    open_text_seen_delta = False
    message_delta_count = 0
    message_stop_count = 0
    stop_reason = ""

    for position, event in enumerate(events[1:], start=1):
        data = event.data
        if event.name == "content_block_start":
            require(open_index is None, "content blocks overlap")
            index = data.get("index")
            require(index == next_index, f"non-contiguous content index: got {index}, want {next_index}")
            next_index += 1
            open_index = int(index)
            block = data.get("content_block")
            require(isinstance(block, dict), "content_block_start has no block")
            open_type = str(block.get("type", ""))
            if not first_content_block_type:
                first_content_block_type = open_type
            require(open_type in {
                "text", "thinking", "redacted_thinking", "tool_use", "server_tool_use",
                "web_search_tool_result", "code_execution_tool_result",
            }, f"unknown content block type: {open_type!r}")
            last_delta = ""
            json_fragments = []
            open_text_has_citations = False
            open_text_seen_delta = False
            if open_type == "server_tool_use":
                tool_id = str(block.get("id", ""))
                require(tool_id.startswith("srvtoolu_"), "server tool ID does not start with srvtoolu_")
                server_tool_ids.add(tool_id)
                server_tool_names[tool_id] = str(block.get("name", ""))
                require("caller" not in block, "server tool block leaked client-tool caller metadata")
            elif open_type == "text" and "citations" in block:
                require(block.get("citations") == [], "cited text block must start with citations: []")
                open_text_has_citations = True
            elif open_type == "thinking":
                require(block.get("thinking") == "", "thinking block must start empty")
            elif open_type == "redacted_thinking":
                opaque_base64(block.get("data"), "redacted thinking data")
                redacted_thinking_blocks += 1
            elif open_type == "tool_use":
                validate_direct_caller(block)
                tool_uses += 1
                tool_names.append(str(block.get("name", "")))
                if str(block.get("name", "")).lower() in {
                    "web_search", "web_search_20250305", "remote_web_search", "google_search",
                }:
                    client_web_search_tool_uses += 1
            elif open_type == "web_search_tool_result":
                validate_direct_caller(block)
                tool_id = str(block.get("tool_use_id", ""))
                require(tool_id in server_tool_ids, "web-search result is not paired with a preceding server tool")
                result_tool_ids.add(tool_id)
                count, error_code, response_ids = validate_web_result_content(block.get("content"))
                result_count += count
                result_response_ids.update(response_ids)
                if error_code:
                    result_errors.append(error_code)
            elif open_type == "code_execution_tool_result":
                tool_id = str(block.get("tool_use_id", ""))
                require(tool_id in server_tool_ids, "code-execution result is not paired with a preceding server tool")
                require(server_tool_names.get(tool_id) == "code_execution", "code-execution result references the wrong server tool")
                code_execution_result_ids.add(tool_id)
                content = block.get("content")
                require(isinstance(content, dict), "code-execution result content is missing")
                content_type = str(content.get("type", ""))
                if content_type == "code_execution_result":
                    require(isinstance(content.get("stdout"), str), "code-execution stdout is missing")
                    require(isinstance(content.get("stderr"), str), "code-execution stderr is missing")
                    require(isinstance(content.get("return_code"), int), "code-execution return_code is missing")
                    code_execution_successes += 1
                    code_execution_stdout.append(content["stdout"])
                else:
                    require(content_type == "code_execution_tool_result_error", "invalid code-execution result type")
                    error_code = str(content.get("error_code", ""))
                    require(bool(error_code), "code-execution error code is missing")
                    code_execution_errors.append(error_code)
        elif event.name == "content_block_delta":
            require(open_index is not None, "content delta without an open block")
            require(data.get("index") == open_index, "content delta index mismatch")
            delta = data.get("delta")
            require(isinstance(delta, dict), "content delta payload is missing")
            delta_type = str(delta.get("type", ""))
            if open_type == "text":
                require(delta_type in {"text_delta", "citations_delta"}, f"invalid text delta: {delta_type}")
                if delta_type == "citations_delta":
                    require(open_text_has_citations, "citation delta belongs to a text block without citations: []")
                    require(not open_text_seen_delta, "citation delta arrived after cited text")
                    citation = delta.get("citation")
                    require(isinstance(citation, dict), "citation payload is missing")
                    require(citation.get("type") == "web_search_result_location", "invalid citation type")
                    require(bool(citation.get("url")), "citation URL is empty")
                    require(bool(citation.get("title")), "citation title is empty")
                    require(bool(citation.get("cited_text")), "citation text is empty")
                    citation_response_ids.add(validate_web_opaque_envelope(
                        citation.get("encrypted_index"), "encrypted_index", 4,
                    ))
                    citations += 1
                    citation_deltas_before_text += 1
                else:
                    open_text_seen_delta = True
            elif open_type == "thinking":
                require(delta_type in {"thinking_delta", "signature_delta"}, f"invalid thinking delta: {delta_type}")
                if delta_type == "signature_delta":
                    signature = delta.get("signature")
                    opaque_base64(signature, "thinking signature")
                    signatures.append(str(signature))
            elif open_type in {"tool_use", "server_tool_use"}:
                require(delta_type == "input_json_delta", f"invalid tool delta: {delta_type}")
                json_fragments.append(str(delta.get("partial_json", "")))
            else:
                raise ProbeError(f"{open_type} block must not emit deltas")
            last_delta = delta_type
        elif event.name == "content_block_stop":
            require(open_index is not None, "content stop without an open block")
            require(data.get("index") == open_index, "content stop index mismatch")
            if open_type == "thinking":
                require(last_delta == "signature_delta", "thinking signature is not the last delta")
            if open_type == "text" and open_text_has_citations:
                require(last_delta == "text_delta", "cited text block must end with text")
                cited_text_blocks += 1
            if open_type in {"tool_use", "server_tool_use"} and json_fragments:
                try:
                    parsed_input = json.loads("".join(json_fragments))
                except json.JSONDecodeError as exc:
                    raise ProbeError(f"tool input deltas do not form JSON: {exc}") from exc
                require(isinstance(parsed_input, dict), "tool input is not an object")
            open_index = None
            open_type = ""
        elif event.name == "message_delta":
            require(open_index is None, "message_delta arrived with an open block")
            message_delta_count += 1
            delta = data.get("delta")
            require(isinstance(delta, dict), "message_delta.delta is missing")
            stop_reason = str(delta.get("stop_reason", ""))
            require(stop_reason in STOP_REASONS, f"invalid stop reason: {stop_reason!r}")
            usage = data.get("usage")
            require(isinstance(usage, dict) and "output_tokens" in usage, "message_delta usage is missing output_tokens")
        elif event.name == "message_stop":
            require(open_index is None, "message_stop arrived with an open block")
            message_stop_count += 1
            require(position == len(events) - 1, "message_stop is not the final event")
        else:
            raise ProbeError(f"unexpected SSE event: {event.name}")

    require(open_index is None, "stream ended with an open content block")
    require(message_delta_count == 1, f"expected one message_delta, got {message_delta_count}")
    require(message_stop_count == 1, f"expected one message_stop, got {message_stop_count}")
    require(result_tool_ids.issubset(server_tool_ids), "unpaired server-tool results")
    if result_response_ids and citation_response_ids:
        require(citation_response_ids.issubset(result_response_ids), "citations do not reference the search-result envelope")
    return {
        "content_blocks": next_index,
        "stop_reason": stop_reason,
        "thinking_signatures": len(signatures),
        "redacted_thinking_blocks": redacted_thinking_blocks,
        "server_tool_uses": len(server_tool_ids),
        "server_tool_names": sorted(server_tool_names.values()),
        "web_search_results": result_count,
        "web_search_errors": result_errors,
        "client_web_search_tool_uses": client_web_search_tool_uses,
        "tool_uses": tool_uses,
        "tool_names": tool_names,
        "code_execution_results": len(code_execution_result_ids),
        "code_execution_successes": code_execution_successes,
        "code_execution_errors": code_execution_errors,
        "code_execution_stdout": code_execution_stdout,
        "citations": citations,
        "first_content_block_type": first_content_block_type,
        "cited_text_blocks": cited_text_blocks,
        "citation_deltas_before_text": citation_deltas_before_text,
        "signature_values": signatures,
    }


def validate_message(payload: dict[str, Any]) -> dict[str, Any]:
    require(str(payload.get("id", "")).startswith("msg_"), "message ID does not start with msg_")
    require(payload.get("type") == "message", "response type is not message")
    require(payload.get("role") == "assistant", "response role is not assistant")
    require(isinstance(payload.get("content"), list), "response content is not an array")
    require(payload.get("stop_reason") in STOP_REASONS, "response stop_reason is invalid")
    usage = payload.get("usage")
    require(isinstance(usage, dict), "response usage is missing")
    require(isinstance(usage.get("input_tokens"), int), "usage.input_tokens is missing")
    require(isinstance(usage.get("output_tokens"), int), "usage.output_tokens is missing")
    return {"content_blocks": len(payload["content"]), "stop_reason": payload["stop_reason"]}


def validate_nonstream_web_search(payload: dict[str, Any]) -> dict[str, Any]:
    facts = validate_message(payload)
    server_ids: set[str] = set()
    result_ids: set[str] = set()
    result_count = 0
    errors: list[str] = []
    client_web_search_tool_uses = 0
    citations = 0
    first_content_block_type = ""
    for block in payload["content"]:
        if not isinstance(block, dict):
            continue
        block_type = block.get("type")
        if not first_content_block_type:
            first_content_block_type = str(block_type or "")
        if block_type == "server_tool_use":
            tool_id = str(block.get("id", ""))
            require(tool_id.startswith("srvtoolu_"), "server tool ID does not start with srvtoolu_")
            server_ids.add(tool_id)
        elif block_type == "web_search_tool_result":
            validate_direct_caller(block)
            tool_id = str(block.get("tool_use_id", ""))
            require(tool_id in server_ids, "web-search result is not paired")
            result_ids.add(tool_id)
            count, error_code, _ = validate_web_result_content(block.get("content"))
            result_count += count
            if error_code:
                errors.append(error_code)
        elif block_type == "tool_use" and str(block.get("name", "")).lower() in {
            "web_search", "web_search_20250305", "remote_web_search", "google_search",
        }:
            client_web_search_tool_uses += 1
        elif block_type == "text":
            for citation in block.get("citations") or []:
                require(citation.get("type") == "web_search_result_location", "invalid non-stream citation type")
                opaque_base64(citation.get("encrypted_index"), "encrypted_index")
                citations += 1
    require(server_ids and server_ids == result_ids, "non-stream web-search blocks are not paired")
    facts.update({
        "server_tool_uses": len(server_ids),
        "web_search_results": result_count,
        "web_search_errors": errors,
        "client_web_search_tool_uses": client_web_search_tool_uses,
        "citations": citations,
        "first_content_block_type": first_content_block_type,
    })
    return facts


def validate_nonstream_code_execution(payload: dict[str, Any]) -> dict[str, Any]:
    facts = validate_message(payload)
    server_ids: set[str] = set()
    result_ids: set[str] = set()
    stdout: list[str] = []
    errors: list[str] = []
    for block in payload["content"]:
        require(isinstance(block, dict), "code-execution content block is not an object")
        block_type = block.get("type")
        if block_type == "server_tool_use":
            tool_id = str(block.get("id", ""))
            require(tool_id.startswith("srvtoolu_"), "server tool ID does not start with srvtoolu_")
            require(block.get("name") == "code_execution", "non-stream code execution exposed the wrong server tool")
            require("caller" not in block, "non-stream server tool leaked client-tool caller metadata")
            server_ids.add(tool_id)
        elif block_type == "code_execution_tool_result":
            tool_id = str(block.get("tool_use_id", ""))
            require(tool_id in server_ids, "non-stream code-execution result is not paired")
            result_ids.add(tool_id)
            content = block.get("content")
            require(isinstance(content, dict), "non-stream code-execution result content is missing")
            if content.get("type") == "code_execution_result":
                require(isinstance(content.get("stdout"), str), "non-stream code-execution stdout is missing")
                require(isinstance(content.get("stderr"), str), "non-stream code-execution stderr is missing")
                require(isinstance(content.get("return_code"), int), "non-stream code-execution return_code is missing")
                stdout.append(content["stdout"])
            else:
                require(content.get("type") == "code_execution_tool_result_error", "invalid non-stream code-execution result type")
                error_code = str(content.get("error_code", ""))
                require(bool(error_code), "non-stream code-execution error code is missing")
                errors.append(error_code)
    require(server_ids and server_ids == result_ids, "non-stream code-execution blocks are not paired")
    facts.update({
        "server_tool_uses": len(server_ids),
        "code_execution_results": len(result_ids),
        "code_execution_stdout": stdout,
        "code_execution_errors": errors,
    })
    return facts


def choose_model(models: list[str], requested: str) -> str:
    if requested:
        require(requested in models, f"requested model is not exposed: {requested}")
        return requested
    for candidate in ("claude-opus-5", "claude-opus-4-8", "claude-sonnet-4-6"):
        if candidate in models:
            return candidate
    for model in models:
        if model.startswith("claude-") and not model.endswith("-thinking"):
            return model
    raise ProbeError("no Claude model is exposed by /v1/models")


def message_body(model: str, prompt: str, stream: bool, max_tokens: int = 128) -> dict[str, Any]:
    return {
        "model": model,
        "max_tokens": max_tokens,
        "stream": stream,
        "messages": [{"role": "user", "content": prompt}],
    }


def run_probe(client: Client, model: str) -> list[dict[str, Any]]:
    checks: list[dict[str, Any]] = []

    def run(name: str, operation: Callable[[], dict[str, Any]]) -> None:
        started = time.monotonic()
        try:
            facts = operation()
            checks.append({"name": name, "status": "PASS", "elapsed_ms": round((time.monotonic() - started) * 1000), **facts})
        except Exception as exc:  # keep running independent probes
            checks.append({"name": name, "status": "FAIL", "elapsed_ms": round((time.monotonic() - started) * 1000), "error": str(exc)})

    def basic_nonstream() -> dict[str, Any]:
        response = decode_json(client.messages(message_body(model, "Reply with exactly BASIC_OK.", False, 64)))
        return validate_message(response)

    def basic_stream() -> dict[str, Any]:
        return validate_sse(parse_sse(client.messages(message_body(model, "Reply with exactly STREAM_OK.", True, 64))))

    def thinking_stream() -> dict[str, Any]:
        body = message_body(
            model,
            (
                "Solve this carefully before answering: five machines make five widgets in five minutes. "
                "At that constant per-machine rate, how many minutes do one hundred machines need to make "
                "one hundred widgets? Explain the rate calculation, then end with SIGNATURE_OK."
            ),
            True,
            4096,
        )
        body["thinking"] = {"type": "enabled", "budget_tokens": 2048}
        facts = validate_sse(parse_sse(client.messages(body)))
        require(facts["thinking_signatures"] > 0, "stream has no thinking signature")
        envelopes: list[dict[str, Any]] = []
        for signature in facts.pop("signature_values"):
            envelope = validate_thinking_signature(signature, "thinking signature")
            require(envelope["provider_native"], "thinking signature is not the provider-issued Kiro envelope")
            envelopes.append(envelope)
        facts["signature_envelopes"] = envelopes
        return facts

    def signature_replay() -> dict[str, Any]:
        prompt = (
            "Solve this carefully before answering: if three identical pumps fill three tanks in six minutes, "
            "how many minutes do twelve identical pumps need to fill twelve equal tanks? End with ROUNDTRIP_ONE."
        )
        first_body = message_body(model, prompt, False, 4096)
        first_body["thinking"] = {"type": "enabled", "budget_tokens": 2048}
        first = decode_json(client.messages(first_body))
        first_facts = validate_message(first)
        thinking_blocks = [
            block for block in first["content"]
            if isinstance(block, dict) and block.get("type") == "thinking"
        ]
        require(bool(thinking_blocks), "non-stream response has no thinking block to replay")
        envelopes: list[dict[str, Any]] = []
        for block in thinking_blocks:
            require(bool(block.get("thinking")), "replay thinking text is empty")
            envelope = validate_thinking_signature(block.get("signature"), "replay thinking signature")
            require(envelope["provider_native"], "replay signature is not the provider-issued Kiro envelope")
            envelopes.append(envelope)

        replay_body = message_body(model, "", False, 512)
        replay_body["messages"] = [
            {"role": "user", "content": prompt},
            {"role": "assistant", "content": first["content"]},
            {"role": "user", "content": "Use the prior reasoning and reply with exactly ROUNDTRIP_OK."},
        ]
        replay = decode_json(client.messages(replay_body))
        replay_facts = validate_message(replay)
        return {
            "original_content_blocks": first_facts["content_blocks"],
            "replayed_thinking_blocks": len(thinking_blocks),
            "signature_envelopes": envelopes,
            "roundtrip_content_blocks": replay_facts["content_blocks"],
            "roundtrip_stop_reason": replay_facts["stop_reason"],
        }

    def forced_tool_stream() -> dict[str, Any]:
        body = message_body(
            model,
            "Call protocol_probe exactly once with value TOOL_OK. Do not answer in text.",
            True,
            512,
        )
        body["tools"] = [{
            "name": "protocol_probe",
            "description": "Records one protocol test value.",
            "input_schema": {
                "type": "object",
                "properties": {"value": {"type": "string"}},
                "required": ["value"],
                "additionalProperties": False,
            },
        }]
        body["tool_choice"] = {"type": "tool", "name": "protocol_probe"}
        facts = validate_sse(parse_sse(client.messages(body)))
        require(facts["first_content_block_type"] == "tool_use", "forced tool stream does not start with tool_use")
        require(facts["tool_uses"] == 1, "forced tool stream did not emit exactly one tool_use")
        require(facts["tool_names"] == ["protocol_probe"], "forced tool stream changed the tool name")
        require(facts["thinking_signatures"] == 0, "forced tool stream leaked unrequested thinking")
        require(facts["stop_reason"] == "tool_use", "forced tool stream did not stop with tool_use")
        return facts

    def web_search(stream: bool) -> dict[str, Any]:
        body = message_body(
            model,
            "Perform a web search for the query: site:go.dev goroutines channels. Answer using the source URL.",
            stream,
            512,
        )
        body["tools"] = [{
            "type": "web_search_20250305",
            "name": "web_search",
            "max_uses": 1,
            "allowed_domains": ["go.dev"],
        }]
        if stream:
            facts = validate_sse(parse_sse(client.messages(body)))
        else:
            facts = validate_nonstream_web_search(decode_json(client.messages(body)))
        require(facts["server_tool_uses"] == 1, "expected exactly one web-search server tool use")
        require(not facts["web_search_errors"], f"web search returned an error: {facts['web_search_errors']}")
        require(facts["web_search_results"] > 0, "web search returned no results")
        require(facts["citations"] > 0, "web-search answer has no citations")
        require(facts["client_web_search_tool_uses"] == 0, "response leaked an internal web-search tool_use")
        require(facts["stop_reason"] == "end_turn", "max_uses=1 response did not finish with end_turn")
        require(facts["first_content_block_type"] == "server_tool_use", "web-search response does not start with server_tool_use")
        if stream:
            require(facts["cited_text_blocks"] > 0, "web-search response has no cited text block")
            require(facts["citation_deltas_before_text"] == facts["citations"], "not every citation arrived before cited text")
        return facts

    def web_search_claude_code_stream() -> dict[str, Any]:
        body = {
            "model": model,
            "messages": [{
                "role": "user",
                "content": [{"type": "text", "text": "Perform a web search for the query: AI news 2026-08-12"}],
            }],
            "system": [
                {"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
                {"type": "text", "text": "You are an assistant for performing a web search tool use"},
            ],
            "tools": [{"type": "web_search_20250305", "name": "web_search", "max_uses": 8}],
            "tool_choice": {"type": "tool", "name": "web_search"},
            "max_tokens": 64000,
            "temperature": 1,
            "stream": True,
        }
        facts = validate_sse(parse_sse(client.messages(body)))
        require(facts["server_tool_uses"] >= 1, "Claude Code shape produced no web-search server tool use")
        require(not facts["web_search_errors"], f"Claude Code shape returned an error: {facts['web_search_errors']}")
        require(facts["web_search_results"] > 0, "Claude Code shape returned no results")
        require(facts["citations"] > 0, "Claude Code shape has no citations")
        require(facts["client_web_search_tool_uses"] == 0, "Claude Code shape leaked an internal web-search tool_use")
        require(facts["first_content_block_type"] == "server_tool_use", "Claude Code shape does not start with server_tool_use")
        require(facts["cited_text_blocks"] > 0, "Claude Code shape has no cited text block")
        require(facts["citation_deltas_before_text"] == facts["citations"], "Claude Code citations did not precede cited text")
        require(facts["stop_reason"] == "end_turn", "Claude Code shape did not finish with end_turn")
        return facts

    def code_execution_claude_code(stream: bool) -> dict[str, Any]:
        body = {
            "model": model,
            "messages": [{
                "role": "user",
                "content": [{
                    "type": "text",
                    "text": "Write and execute a Python script that prints 'HELLO_CHECK'. Only use the code execution tool, nothing else.",
                }],
            }],
            "tools": [{"type": "code_execution_20250522", "name": "code_execution"}],
            "max_tokens": 64000,
            "stream": stream,
        }
        if stream:
            facts = validate_sse(parse_sse(client.messages(body)))
        else:
            facts = validate_nonstream_code_execution(decode_json(client.messages(body)))
        require(facts["server_tool_uses"] == 1, "code execution produced the wrong number of server tool calls")
        if stream:
            require(facts["server_tool_names"] == ["code_execution"], "code execution exposed the wrong server tool")
        require(facts["code_execution_results"] == 1, "code execution produced the wrong number of results")
        if stream:
            require(facts["code_execution_successes"] == 1, "code execution did not produce a successful result")
        require(not facts["code_execution_errors"], f"code execution returned an error: {facts['code_execution_errors']}")
        require(facts["code_execution_stdout"] == ["HELLO_CHECK\n"], "code execution stdout was changed")
        require(facts["stop_reason"] == "end_turn", "code execution did not finish with end_turn")
        return facts

    run("basic_nonstream", basic_nonstream)
    run("basic_stream", basic_stream)
    run("thinking_signature_stream", thinking_stream)
    run("thinking_signature_replay", signature_replay)
    run("forced_tool_stream", forced_tool_stream)
    run("web_search_nonstream", lambda: web_search(False))
    run("web_search_stream", lambda: web_search(True))
    run("web_search_claude_code_stream", web_search_claude_code_stream)
    run("code_execution_claude_code_nonstream", lambda: code_execution_claude_code(False))
    run("code_execution_claude_code_stream", lambda: code_execution_claude_code(True))
    return checks


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default=os.getenv("SUB2API_BASE_URL", "http://127.0.0.1:8080"))
    parser.add_argument("--api-key", default=os.getenv("SUB2API_API_KEY", ""))
    parser.add_argument("--model", default=os.getenv("SUB2API_MODEL", ""))
    parser.add_argument("--timeout", type=float, default=float(os.getenv("SUB2API_PROBE_TIMEOUT", "180")))
    parser.add_argument(
        "--user-agent",
        default=os.getenv("SUB2API_USER_AGENT", "sub2api-kiro-protocol-probe/1"),
    )
    args = parser.parse_args()
    if not args.api_key:
        parser.error("provide --api-key or SUB2API_API_KEY")

    try:
        client = Client(args.base_url, args.api_key, args.timeout, args.user_agent)
        models = client.models()
        model = choose_model(models, args.model)
        checks = run_probe(client, model)
        passed = sum(check["status"] == "PASS" for check in checks)
        report = {
            "status": "PASS" if passed == len(checks) else "FAIL",
            "base_url": args.base_url,
            "model": model,
            "models_exposed": len(models),
            "score": {"passed": passed, "total": len(checks)},
            "checks": checks,
        }
    except Exception as exc:
        report = {"status": "FAIL", "base_url": args.base_url, "error": str(exc)}
    print(json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True))
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
