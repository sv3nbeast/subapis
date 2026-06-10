"""
mitmproxy addon for redacted Claude/Anthropic traffic summaries.

This addon intentionally avoids writing raw request/response bodies and always
redacts authentication and cookie headers. It is meant to feed
scripts/audit_claude_cli_mimicry.py --flow-summary.
"""

from __future__ import annotations

import json
import os
import re
import time
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

from mitmproxy import ctx, http


SENSITIVE_HEADERS = {"authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key"}
ANTHROPIC_HOST_RE = re.compile(r"(^|\.)anthropic\.com$|(^|\.)claude\.(ai|com)$", re.I)
MAX_TEXT_PREVIEW = 120


def redact_header(name: str, value: str) -> str:
    if name.lower() in SENSITIVE_HEADERS:
        if value.lower().startswith("bearer "):
            return "Bearer [redacted]"
        if value.lower().startswith("basic "):
            return "Basic [redacted]"
        return "[redacted]"
    if len(value) > 500:
        return value[:497] + "..."
    return value


def header_dict(headers: http.Headers) -> dict[str, str]:
    out: dict[str, str] = {}
    for name, value in headers.items(multi=True):
        key = name.lower()
        if key in out:
            out[key] += ", " + redact_header(name, value)
        else:
            out[key] = redact_header(name, value)
    return out


def json_path(obj: Any, path: str) -> Any:
    cur = obj
    for part in path.split("."):
        if isinstance(cur, dict):
            cur = cur.get(part)
        else:
            return None
    return cur


def text_preview(value: Any) -> str:
    if value is None:
        return ""
    text = str(value).replace("\n", "\\n")
    if len(text) > MAX_TEXT_PREVIEW:
        return text[:MAX_TEXT_PREVIEW] + "..."
    return text


def parse_json_body(content: bytes) -> Any:
    if not content:
        return None
    try:
        return json.loads(content.decode("utf-8", errors="replace"))
    except Exception:
        return None


def collect_cache_controls(obj: Any) -> list[dict[str, Any]]:
    controls: list[dict[str, Any]] = []

    def walk(value: Any, path: str) -> None:
        if isinstance(value, dict):
            cc = value.get("cache_control")
            if isinstance(cc, dict):
                controls.append({"path": path, "type": cc.get("type"), "ttl": cc.get("ttl")})
            for key, child in value.items():
                walk(child, f"{path}.{key}" if path else str(key))
        elif isinstance(value, list):
            for index, child in enumerate(value):
                walk(child, f"{path}.{index}" if path else str(index))

    walk(obj, "")
    return controls


def summarize_system_entries(body: Any) -> list[dict[str, Any]]:
    system = body.get("system") if isinstance(body, dict) else None
    entries: list[dict[str, Any]] = []
    if isinstance(system, str):
        return [{"type": "text", "text_preview": text_preview(system)}]
    if not isinstance(system, list):
        return entries
    for entry in system:
        if not isinstance(entry, dict):
            continue
        text = str(entry.get("text") or "")
        item: dict[str, Any] = {
            "type": entry.get("type"),
            "text_preview": text_preview(text),
            "has_cache_control": isinstance(entry.get("cache_control"), dict),
        }
        if "x-anthropic-billing-header:" in text:
            item["billing"] = text_preview(text)
        entries.append(item)
    return entries


def summarize_body(content: bytes) -> dict[str, Any]:
    body = parse_json_body(content)
    if not isinstance(body, dict):
        return {"body_parse": "non_json_or_empty", "body_bytes": len(content or b"")}

    messages = body.get("messages")
    tools = body.get("tools")
    metadata_user_id = json_path(body, "metadata.user_id")
    metadata_summary: Any = metadata_user_id
    if isinstance(metadata_user_id, str):
        try:
            parsed_metadata = json.loads(metadata_user_id)
            if isinstance(parsed_metadata, dict):
                metadata_summary = {
                    "session_id": parsed_metadata.get("session_id"),
                    "has_device_id": bool(parsed_metadata.get("device_id")),
                    "has_account_uuid": bool(parsed_metadata.get("account_uuid")),
                }
        except json.JSONDecodeError:
            metadata_summary = {"raw_preview": text_preview(metadata_user_id)}

    return {
        "body_parse": "json",
        "body_bytes": len(content or b""),
        "model": body.get("model"),
        "stream": body.get("stream"),
        "max_tokens": body.get("max_tokens"),
        "thinking": body.get("thinking"),
        "output_config": body.get("output_config"),
        "metadata_user_id": metadata_summary,
        "message_count": len(messages) if isinstance(messages, list) else None,
        "tools": [tool.get("name") for tool in tools if isinstance(tool, dict)] if isinstance(tools, list) else [],
        "system_entries": summarize_system_entries(body),
        "cache_controls": collect_cache_controls(body),
    }


def merge_usage(into: dict[str, Any], extra: Any) -> None:
    if not isinstance(extra, dict):
        return
    for key, value in extra.items():
        if value is None:
            continue
        if isinstance(value, dict):
            existing = into.get(key)
            if not isinstance(existing, dict):
                into[key] = {}
            merge_usage(into[key], value)
        else:
            into[key] = value


def usage_summary(content: bytes, content_type: str = "") -> dict[str, Any]:
    if "text/event-stream" in (content_type or "").lower():
        text = (content or b"").decode("utf-8", errors="replace")
        combined: dict[str, Any] = {}
        for raw in text.splitlines():
            if not raw.startswith("data:"):
                continue
            payload = raw[5:].strip()
            if not payload or payload == "[DONE]":
                continue
            try:
                event = json.loads(payload)
            except json.JSONDecodeError:
                continue
            event_type = event.get("type") if isinstance(event, dict) else None
            if event_type == "message_start":
                inner = event.get("message")
                if isinstance(inner, dict):
                    merge_usage(combined, inner.get("usage"))
            elif event_type == "message_delta":
                merge_usage(combined, event.get("usage"))
        return combined
    body = parse_json_body(content)
    if isinstance(body, dict):
        usage = body.get("usage")
        if isinstance(usage, dict):
            return usage
    return {}


class ClaudeFlowSummary:
    def __init__(self) -> None:
        self.items: list[dict[str, Any]] = []
        self.pending: dict[str, dict[str, Any]] = {}
        self.output = Path(os.environ.get("CLAUDE_FLOW_SUMMARY_OUT", "/tmp/claude-flow-summary.json"))
        self.source = os.environ.get("CLAUDE_FLOW_SUMMARY_SOURCE", "mitmproxy")

    def load(self, loader) -> None:
        loader.add_option(
            name="claude_flow_summary_out",
            typespec=str,
            default=str(self.output),
            help="Path to write redacted Claude flow summary JSON.",
        )

    def configure(self, updates) -> None:
        self.output = Path(ctx.options.claude_flow_summary_out)

    def build_item(self, flow: http.HTTPFlow) -> dict[str, Any]:
        request = flow.request
        split = urlsplit(request.pretty_url)
        return {
            "ts": time.time(),
            "method": request.method,
            "scheme": request.scheme,
            "host": request.host or "",
            "path": split.path + (("?" + split.query) if split.query else ""),
            "headers": header_dict(request.headers),
            "body_summary": summarize_body(request.raw_content or b""),
            "status": None,
            "response_content_type": "",
            "response_headers": {},
            "usage_summary": {},
        }

    def requestheaders(self, flow: http.HTTPFlow) -> None:
        request = flow.request
        host = request.host or ""
        if not ANTHROPIC_HOST_RE.search(host):
            return
        self.pending[flow.id] = self.build_item(flow)

    def response(self, flow: http.HTTPFlow) -> None:
        request = flow.request
        host = request.host or ""
        if not ANTHROPIC_HOST_RE.search(host):
            return
        item = self.pending.pop(flow.id, None) or self.build_item(flow)
        item["headers"] = header_dict(request.headers)
        item["body_summary"] = summarize_body(request.raw_content or b"")
        item["status"] = flow.response.status_code if flow.response else None
        item["response_content_type"] = flow.response.headers.get("content-type", "") if flow.response else ""
        item["response_headers"] = header_dict(flow.response.headers) if flow.response else {}
        if flow.response:
            response_ct = flow.response.headers.get("content-type", "")
            item["usage_summary"] = usage_summary(flow.response.raw_content or b"", response_ct)
        else:
            item["usage_summary"] = {}
        self.items.append(item)
        self.write()

    def error(self, flow: http.HTTPFlow) -> None:
        request = flow.request
        host = request.host or ""
        if not ANTHROPIC_HOST_RE.search(host):
            return
        item = self.pending.pop(flow.id, None) or self.build_item(flow)
        item["headers"] = header_dict(request.headers)
        item["body_summary"] = summarize_body(request.raw_content or b"")
        item["error"] = str(flow.error) if flow.error else "request_error"
        self.items.append(item)
        self.write()

    def done(self) -> None:
        self.write()

    def write(self) -> None:
        self.output.parent.mkdir(parents=True, exist_ok=True)
        payload = {
            "source": self.source,
            "generated_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "count": len(self.items),
            "items": self.items,
        }
        self.output.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")


addons = [ClaudeFlowSummary()]
