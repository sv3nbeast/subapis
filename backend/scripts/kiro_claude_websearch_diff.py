#!/usr/bin/env python3
"""Capture and summarize the same WebSearch request through Claude and Kiro groups."""

from __future__ import annotations

import json
import os
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


REQUEST_BODY: dict[str, Any] = {
    "model": "claude-opus-4-8",
    "messages": [
        {
            "role": "user",
            "content": [
                {
                    "type": "text",
                    "text": "Perform a web search for the query: AI news 2026-08-12",
                }
            ],
        }
    ],
    "system": [
        {
            "type": "text",
            "text": "You are Claude Code, Anthropic's official CLI for Claude.",
        },
        {
            "type": "text",
            "text": "You are an assistant for performing a web search tool use",
        },
    ],
    "tools": [
        {"type": "web_search_20250305", "name": "web_search", "max_uses": 8}
    ],
    "tool_choice": {"type": "tool", "name": "web_search"},
    "max_tokens": 64000,
    "temperature": 1,
    "stream": True,
}


def parse_sse(raw: bytes) -> list[dict[str, Any]]:
    wire = raw.decode("utf-8", "strict").replace("\r\n", "\n").replace("\r", "\n")
    events: list[dict[str, Any]] = []
    for frame in wire.split("\n\n"):
        if not frame.strip():
            continue
        event_name = ""
        data_lines: list[str] = []
        for line in frame.split("\n"):
            if line.startswith("event:"):
                event_name = line[6:].strip()
            elif line.startswith("data:"):
                data_lines.append(line[5:].lstrip())
        if not data_lines or data_lines == ["[DONE]"]:
            continue
        data = json.loads("\n".join(data_lines))
        events.append({"event": event_name, "data": data})
    return events


def summarize(events: list[dict[str, Any]]) -> dict[str, Any]:
    summary: dict[str, Any] = {
        "event_sequence": [],
        "blocks": [],
        "message_start": {},
        "message_delta": {},
        "message_stop_count": 0,
    }
    blocks: dict[int, dict[str, Any]] = {}
    for envelope in events:
        name = envelope["event"]
        data = envelope["data"]
        summary["event_sequence"].append(name)
        if name == "message_start":
            message = data.get("message", {})
            summary["message_start"] = {
                "keys": sorted(message),
                "model": message.get("model"),
                "role": message.get("role"),
                "stop_reason": message.get("stop_reason"),
                "usage": message.get("usage"),
                "id_prefix": str(message.get("id", "")).split("_")[0],
            }
        elif name == "content_block_start":
            index = int(data["index"])
            block = data.get("content_block", {})
            entry = {
                "index": index,
                "type": block.get("type"),
                "keys": sorted(block),
                "name": block.get("name"),
                "caller_type": (block.get("caller") or {}).get("type"),
                "id_prefix": str(block.get("id", "")).split("_")[0],
                "tool_use_id_prefix": str(block.get("tool_use_id", "")).split("_")[0],
                "input": block.get("input"),
                "result_count": len(block.get("content", []))
                if isinstance(block.get("content"), list)
                else None,
                "result_item_keys": sorted(block["content"][0])
                if isinstance(block.get("content"), list) and block["content"]
                else [],
                "text_chars": len(block.get("text", "")),
                "text_deltas": 0,
                "text_delta_chars": 0,
                "citation_deltas": 0,
                "delta_types": [],
            }
            blocks[index] = entry
            summary["blocks"].append(entry)
        elif name == "content_block_delta":
            entry = blocks[int(data["index"])]
            delta = data.get("delta", {})
            delta_type = delta.get("type")
            entry["delta_types"].append(delta_type)
            if delta_type == "text_delta":
                entry["text_deltas"] += 1
                entry["text_delta_chars"] += len(delta.get("text", ""))
            elif delta_type == "citations_delta":
                entry["citation_deltas"] += 1
                citation = delta.get("citation", {})
                entry.setdefault("citation_keys", sorted(citation))
        elif name == "message_delta":
            summary["message_delta"] = {
                "keys": sorted(data),
                "delta": data.get("delta"),
                "usage": data.get("usage"),
            }
        elif name == "message_stop":
            summary["message_stop_count"] += 1
    return summary


def request(label: str, key: str, output_dir: Path) -> dict[str, Any]:
    payload = json.dumps(REQUEST_BODY, separators=(",", ":")).encode()
    req = urllib.request.Request(
        os.environ.get("SUB2API_BASE_URL", "http://127.0.0.1:8080").rstrip("/")
        + "/v1/messages",
        data=payload,
        method="POST",
        headers={
            "Accept": "application/json",
            "Content-Type": "application/json",
            "Anthropic-Version": "2023-06-01",
            "Anthropic-Beta": (
                "claude-code-20250219,context-1m-2025-08-07,"
                "interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,"
                "context-management-2025-06-27,prompt-caching-scope-2026-01-05,"
                "advisor-tool-2026-03-01,effort-2025-11-24"
            ),
            "Authorization": "Bearer " + key,
            "X-Api-Key": key,
            "User-Agent": "claude-cli/2.1.153 (external, cli)",
            "X-App": "cli",
        },
    )
    started = time.monotonic()
    headers_ms = 0
    first_content_block_ms = 0
    try:
        with urllib.request.urlopen(req, timeout=240) as response:
            headers_ms = round((time.monotonic() - started) * 1000)
            chunks: list[bytes] = []
            while line := response.readline():
                chunks.append(line)
                if not first_content_block_ms and line.startswith(b"event: content_block_start"):
                    first_content_block_ms = round((time.monotonic() - started) * 1000)
            raw = b"".join(chunks)
            status = response.status
            headers = dict(response.headers.items())
    except urllib.error.HTTPError as exc:
        raw = exc.read()
        status = exc.code
        headers = dict(exc.headers.items())
    elapsed_ms = round((time.monotonic() - started) * 1000)
    (output_dir / f"{label}.sse").write_bytes(raw)
    safe_headers = {
        key: value
        for key, value in headers.items()
        if key.lower() not in {"authorization", "x-api-key", "set-cookie"}
    }
    (output_dir / f"{label}.headers.json").write_text(
        json.dumps(safe_headers, indent=2, sort_keys=True), encoding="utf-8"
    )
    result: dict[str, Any] = {
        "label": label,
        "status": status,
        "elapsed_ms": elapsed_ms,
        "headers_ms": headers_ms,
        "first_content_block_ms": first_content_block_ms,
        "content_type": headers.get("Content-Type", ""),
        "bytes": len(raw),
    }
    if status == 200:
        result["summary"] = summarize(parse_sse(raw))
    else:
        result["error_preview"] = raw[:1000].decode("utf-8", "replace")
    return result


def main() -> int:
    output_dir = Path(os.environ.get("DIFF_OUTPUT_DIR", "/tmp/kiro-claude-websearch-diff"))
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    keys = {
        "claude": os.environ.get("CLAUDE_API_KEY", ""),
        "kiro": os.environ.get("KIRO_API_KEY", ""),
    }
    if not all(keys.values()):
        raise SystemExit("CLAUDE_API_KEY and KIRO_API_KEY are required")
    results = [request(label, key, output_dir) for label, key in keys.items()]
    report = {"request": REQUEST_BODY, "results": results}
    (output_dir / "report.json").write_text(
        json.dumps(report, indent=2, sort_keys=True), encoding="utf-8"
    )
    compact = []
    for result in results:
        blocks = result.get("summary", {}).get("blocks", [])
        compact.append({
            "label": result["label"],
            "status": result["status"],
            "elapsed_ms": result["elapsed_ms"],
            "headers_ms": result["headers_ms"],
            "first_content_block_ms": result["first_content_block_ms"],
            "bytes": result["bytes"],
            "first_block_type": blocks[0].get("type") if blocks else None,
            "block_types": [block.get("type") for block in blocks],
            "result_callers": [
                block.get("caller_type") for block in blocks
                if block.get("type") == "web_search_tool_result"
            ],
            "cited_text_blocks": sum(
                block.get("citation_deltas", 0) > 0 and "citations" in block.get("keys", [])
                for block in blocks
            ),
            "message_stop_count": result.get("summary", {}).get("message_stop_count", 0),
        })
    print(json.dumps(compact, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
