#!/usr/bin/env python3
"""Capture equivalent Claude/Kiro protocol streams without printing API keys."""

from __future__ import annotations

import base64
import hashlib
import json
import os
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


MODEL = os.getenv("SUB2API_MODEL", "claude-opus-4-8")
SCENARIOS: dict[str, dict[str, Any]] = {
    "basic_stream": {
        "model": MODEL,
        "max_tokens": 64,
        "stream": True,
        "messages": [{"role": "user", "content": "Reply with exactly STREAM_OK."}],
    },
    "thinking_stream": {
        "model": MODEL,
        "max_tokens": 4096,
        "stream": True,
        "thinking": {"type": "enabled", "budget_tokens": 2048},
        "messages": [{
            "role": "user",
            "content": (
                "Solve this carefully before answering: five machines make five widgets in five minutes. "
                "At that constant per-machine rate, how many minutes do one hundred machines need to make "
                "one hundred widgets? Explain the rate calculation, then end with SIGNATURE_OK."
            ),
        }],
    },
    "forced_tool_stream": {
        "model": MODEL,
        "max_tokens": 512,
        "stream": True,
        "messages": [{
            "role": "user",
            "content": "Call protocol_probe exactly once with value TOOL_OK. Do not answer in text.",
        }],
        "tools": [{
            "name": "protocol_probe",
            "description": "Records one protocol test value.",
            "input_schema": {
                "type": "object",
                "properties": {"value": {"type": "string"}},
                "required": ["value"],
                "additionalProperties": False,
            },
        }],
        "tool_choice": {"type": "tool", "name": "protocol_probe"},
    },
}


def parse_sse(raw: bytes) -> list[dict[str, Any]]:
    wire = raw.decode("utf-8", "strict").replace("\r\n", "\n").replace("\r", "\n")
    events: list[dict[str, Any]] = []
    for frame in wire.split("\n\n"):
        if not frame.strip():
            continue
        name = ""
        data_lines: list[str] = []
        for line in frame.split("\n"):
            if line.startswith("event:"):
                name = line[6:].strip()
            elif line.startswith("data:"):
                data_lines.append(line[5:].lstrip())
        if not data_lines or data_lines == ["[DONE]"]:
            continue
        data = json.loads("\n".join(data_lines))
        events.append({"event": name, "data": data})
    return events


def read_varint(wire: bytes, offset: int) -> tuple[int, int]:
    value = 0
    shift = 0
    while offset < len(wire) and shift < 70:
        byte = wire[offset]
        offset += 1
        value |= (byte & 0x7F) << shift
        if byte < 0x80:
            return value, offset
        shift += 7
    raise ValueError("invalid protobuf varint")


def protobuf_shape(wire: bytes) -> list[dict[str, Any]]:
    fields: list[dict[str, Any]] = []
    offset = 0
    while offset < len(wire):
        tag, offset = read_varint(wire, offset)
        field = tag >> 3
        kind = tag & 7
        item: dict[str, Any] = {"field": field, "wire_type": kind}
        if kind == 0:
            value, offset = read_varint(wire, offset)
            item["value"] = value
        elif kind == 1:
            offset += 8
            item["bytes"] = 8
        elif kind == 2:
            size, offset = read_varint(wire, offset)
            item["bytes"] = size
            offset += size
        elif kind == 5:
            offset += 4
            item["bytes"] = 4
        else:
            item["invalid"] = True
            fields.append(item)
            break
        fields.append(item)
        if offset > len(wire):
            item["truncated"] = True
            break
    return fields


def signature_shape(value: Any) -> dict[str, Any]:
    if not isinstance(value, str) or not value:
        return {"present": False}
    padded = value + "=" * ((4 - len(value) % 4) % 4)
    try:
        wire = base64.b64decode(padded, validate=True)
    except ValueError:
        return {"present": True, "base64": False, "encoded_chars": len(value)}
    return {
        "present": True,
        "base64": True,
        "encoded_chars": len(value),
        "decoded_bytes": len(wire),
        "first_bytes_hex": wire[:12].hex(),
        "sha256_prefix": hashlib.sha256(wire).hexdigest()[:16],
        "top_level_protobuf": protobuf_shape(wire),
    }


def summarize(events: list[dict[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {
        "event_sequence": [],
        "event_type_mismatches": [],
        "blocks": [],
        "message_start": {},
        "message_delta": {},
        "message_stop_count": 0,
    }
    blocks: dict[int, dict[str, Any]] = {}
    for envelope in events:
        name = envelope["event"]
        data = envelope["data"]
        result["event_sequence"].append(name)
        if data.get("type") != name:
            result["event_type_mismatches"].append({"event": name, "type": data.get("type")})
        if name == "message_start":
            message = data.get("message") or {}
            result["message_start"] = {
                "keys": sorted(message),
                "id_prefix": str(message.get("id", "")).split("_")[0],
                "model": message.get("model"),
                "role": message.get("role"),
                "content": message.get("content"),
                "stop_reason": message.get("stop_reason"),
                "stop_sequence": message.get("stop_sequence"),
                "usage_keys": sorted((message.get("usage") or {})),
            }
        elif name == "content_block_start":
            index = int(data["index"])
            block = data.get("content_block") or {}
            entry = {
                "index": index,
                "type": block.get("type"),
                "keys": sorted(block),
                "id_prefix": str(block.get("id", "")).split("_")[0],
                "name": block.get("name"),
                "delta_types": [],
                "partial_json_fragments": 0,
                "partial_json_chars": 0,
                "text_delta_chars": 0,
                "thinking_delta_chars": 0,
                "signature": signature_shape(block.get("signature")),
            }
            blocks[index] = entry
            result["blocks"].append(entry)
        elif name == "content_block_delta":
            entry = blocks[int(data["index"])]
            delta = data.get("delta") or {}
            delta_type = delta.get("type")
            entry["delta_types"].append(delta_type)
            if delta_type == "input_json_delta":
                entry["partial_json_fragments"] += 1
                entry["partial_json_chars"] += len(delta.get("partial_json", ""))
            elif delta_type == "text_delta":
                entry["text_delta_chars"] += len(delta.get("text", ""))
            elif delta_type == "thinking_delta":
                entry["thinking_delta_chars"] += len(delta.get("thinking", ""))
            elif delta_type == "signature_delta":
                entry["signature"] = signature_shape(delta.get("signature"))
        elif name == "message_delta":
            result["message_delta"] = {
                "keys": sorted(data),
                "delta": data.get("delta"),
                "usage_keys": sorted((data.get("usage") or {})),
            }
        elif name == "message_stop":
            result["message_stop_count"] += 1
    return result


def request(label: str, scenario: str, body: dict[str, Any], key: str, output_dir: Path) -> dict[str, Any]:
    req = urllib.request.Request(
        os.environ.get("SUB2API_BASE_URL", "http://127.0.0.1:8080").rstrip("/") + "/v1/messages",
        data=json.dumps(body, separators=(",", ":")).encode(),
        method="POST",
        headers={
            "Accept": "application/json, text/event-stream",
            "Content-Type": "application/json",
            "Anthropic-Version": "2023-06-01",
            "Anthropic-Beta": "interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13",
            "Authorization": "Bearer " + key,
            "X-Api-Key": key,
            "User-Agent": "claude-cli/2.1.153 (external, cli)",
            "X-App": "cli",
        },
    )
    started = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=240) as response:
            headers_ms = round((time.monotonic() - started) * 1000)
            raw = response.read()
            status = response.status
            headers = dict(response.headers.items())
    except urllib.error.HTTPError as exc:
        headers_ms = round((time.monotonic() - started) * 1000)
        raw = exc.read()
        status = exc.code
        headers = dict(exc.headers.items())
    elapsed_ms = round((time.monotonic() - started) * 1000)
    stem = f"{scenario}.{label}"
    (output_dir / f"{stem}.response").write_bytes(raw)
    safe_headers = {
        name: value for name, value in headers.items()
        if name.lower() not in {"authorization", "x-api-key", "set-cookie"}
    }
    (output_dir / f"{stem}.headers.json").write_text(
        json.dumps(safe_headers, indent=2, sort_keys=True), encoding="utf-8"
    )
    item: dict[str, Any] = {
        "label": label,
        "scenario": scenario,
        "status": status,
        "headers_ms": headers_ms,
        "elapsed_ms": elapsed_ms,
        "bytes": len(raw),
    }
    if status == 200:
        item["summary"] = summarize(parse_sse(raw))
    else:
        item["error_preview"] = raw[:1000].decode("utf-8", "replace")
    return item


def main() -> int:
    output_dir = Path(os.getenv("DIFF_OUTPUT_DIR", "/tmp/kiro-claude-protocol-diff"))
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    keys = {"claude": os.getenv("CLAUDE_API_KEY", ""), "kiro": os.getenv("KIRO_API_KEY", "")}
    if not all(keys.values()):
        raise SystemExit("CLAUDE_API_KEY and KIRO_API_KEY are required")
    selected = {
        name.strip() for name in os.getenv("DIFF_SCENARIOS", "").split(",") if name.strip()
    }
    scenarios = {
        name: body for name, body in SCENARIOS.items() if not selected or name in selected
    }
    if not scenarios:
        raise SystemExit("DIFF_SCENARIOS did not match a known scenario")
    results = [
        request(label, scenario, body, key, output_dir)
        for scenario, body in scenarios.items()
        for label, key in keys.items()
    ]
    report = {"model": MODEL, "scenarios": scenarios, "results": results}
    (output_dir / "report.json").write_text(
        json.dumps(report, indent=2, sort_keys=True), encoding="utf-8"
    )
    compact = []
    for item in results:
        summary = item.get("summary") or {}
        compact.append({
            "scenario": item["scenario"],
            "label": item["label"],
            "status": item["status"],
            "headers_ms": item["headers_ms"],
            "elapsed_ms": item["elapsed_ms"],
            "event_sequence": summary.get("event_sequence"),
            "blocks": [
                {
                    "index": block["index"],
                    "type": block["type"],
                    "delta_types": block["delta_types"],
                    "signature": block["signature"],
                }
                for block in summary.get("blocks", [])
            ],
            "message_stop_count": summary.get("message_stop_count"),
        })
    print(json.dumps(compact, indent=2, sort_keys=True))
    return 0 if all(item["status"] == 200 for item in results) else 1


if __name__ == "__main__":
    raise SystemExit(main())
