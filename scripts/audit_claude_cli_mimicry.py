#!/usr/bin/env python3
"""
Compare a real Claude CLI request with the request sub2api forwards upstream.

Supported inputs:
- Raw HTTP request text exported from a proxy.
- JSON / HAR request export.
- sub2api gateway debug snapshots written by SUB2API_DEBUG_GATEWAY_BODY.

Examples:
  python3 scripts/audit_claude_cli_mimicry.py \
    --real /tmp/real-claude-request.txt \
    --sub2api /tmp/sub2api-gateway-debug.log

  python3 scripts/audit_claude_cli_mimicry.py \
    --real real.har \
    --sub2api gateway_debug.log \
    --sub2api-tag UPSTREAM_FORWARD \
    --strict
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any


SNAPSHOT_RE = re.compile(r"^========== \[(?P<ts>[^\]]+)\] (?P<tag>[^=]+?) ==========$", re.M)
UUID_RE = re.compile(r"^[a-fA-F0-9-]{36}$")
LEGACY_METADATA_RE = re.compile(
    r"^user_([a-fA-F0-9]{64})_account_([a-fA-F0-9-]*)_session_([a-fA-F0-9-]{36})$"
)

CORE_HEADERS = [
    "accept",
    "x-stainless-retry-count",
    "x-stainless-timeout",
    "x-stainless-lang",
    "x-stainless-package-version",
    "x-stainless-os",
    "x-stainless-arch",
    "x-stainless-runtime",
    "x-stainless-runtime-version",
    "anthropic-dangerous-direct-browser-access",
    "anthropic-version",
    "authorization",
    "x-api-key",
    "x-app",
    "user-agent",
    "x-claude-code-session-id",
    "content-type",
    "anthropic-beta",
    "x-client-request-id",
    "accept-language",
    "sec-fetch-mode",
    "accept-encoding",
    "x-stainless-helper-method",
]

STAINLESS_HEADERS = {
    "x-stainless-retry-count",
    "x-stainless-timeout",
    "x-stainless-lang",
    "x-stainless-package-version",
    "x-stainless-os",
    "x-stainless-arch",
    "x-stainless-runtime",
    "x-stainless-runtime-version",
}

HIGH_RISK_HEADERS = {
    "user-agent",
    "x-app",
    "anthropic-dangerous-direct-browser-access",
    "anthropic-version",
    "anthropic-beta",
}

EXPECTED_OAUTH_UPSTREAM_BETA_TOKENS = {
    "oauth-2025-04-20",
    "claude-code-20250219",
    "interleaved-thinking-2025-05-14",
}
EXPECTED_HAIKU_BETA_TOKENS = {
    "oauth-2025-04-20",
    "interleaved-thinking-2025-05-14",
}

EXPECTED_COMPANION_ENDPOINTS = [
    "GET /v1/mcp_servers",
    "GET /api/claude_cli/bootstrap",
    "GET /api/claude_code_penguin_mode",
    "GET /api/claude_code_grove",
    "GET /api/oauth/profile",
    "GET /v1/mcp_servers",
    "GET /mcp-registry/v0/servers",
]

EXPECTED_COMPANION_FINGERPRINTS = {
    "GET /api/claude_cli/bootstrap": {
        "user-agent": "claude-code/2.1.165",
        "anthropic-beta": "oauth-2025-04-20",
        "query_contains": ["entrypoint=sdk-cli", "model=awsclaude4.5"],
    },
    "GET /api/claude_code_penguin_mode": {
        "user-agent": "axios/1.15.2",
        "anthropic-beta": "oauth-2025-04-20",
    },
    "GET /api/claude_code_grove": {
        "user-agent": "claude-cli/2.1.165 (external, sdk-cli)",
        "anthropic-beta": "oauth-2025-04-20",
    },
    "GET /api/oauth/profile": {
        "user-agent": "axios/1.15.2",
        "anthropic-beta": "",
    },
    "GET /v1/mcp_servers": {
        "user-agent": "axios/1.15.2",
        "anthropic-beta": "mcp-servers-2025-12-04",
        "query_contains": ["limit=1000"],
    },
    "GET /mcp-registry/v0/servers": {
        "user-agent": "claude-cli/2.1.165 (external, sdk-cli)",
        "query_contains": ["version=latest", "limit=100", "visibility=commercial%2Cgsuite%2Centerprise%2Chealth"],
    },
}

SEVERITY_WEIGHT = {"high": 25, "medium": 8, "low": 2}
SEVERITY_ORDER = {"high": 0, "medium": 1, "low": 2}
SCRIPT_PATH = Path(__file__).resolve()
REPO_ROOT = SCRIPT_PATH.parent.parent
CLAUDE_CONSTANTS_PATH = REPO_ROOT / "backend/internal/pkg/claude/constants.go"


@dataclass
class RequestSample:
    source: str
    label: str
    method: str = ""
    url: str = ""
    path: str = ""
    headers: dict[str, list[str]] = field(default_factory=dict)
    header_order: list[str] = field(default_factory=list)
    body_raw: str = ""
    body: Any = None
    context: dict[str, str] = field(default_factory=dict)

    def normalized_headers(self) -> dict[str, tuple[str, list[str]]]:
        out: dict[str, tuple[str, list[str]]] = {}
        for key, values in self.headers.items():
            out[key.lower()] = (key, values)
        return out

    def header(self, key: str) -> str:
        item = self.normalized_headers().get(key.lower())
        if not item:
            return ""
        values = item[1]
        return str(values[0]).strip() if values else ""


@dataclass
class Finding:
    severity: str
    area: str
    message: str
    real: str = ""
    sub2api: str = ""


def read_text(path: str) -> str:
    return Path(path).read_bytes().decode("utf-8", errors="replace")


def parse_json_body(raw: str) -> Any:
    raw = raw.strip()
    if not raw or raw == "(empty)":
        return None
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return None


def normalize_header_values(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, list):
        return [str(v) for v in value]
    return [str(value)]


def add_header(headers: dict[str, list[str]], order: list[str], key: str, value: str) -> None:
    key = key.strip()
    if not key:
        return
    existing_key = None
    for candidate in headers:
        if candidate.lower() == key.lower():
            existing_key = candidate
            break
    if existing_key is None:
        headers[key] = [value.strip()]
        order.append(key)
    else:
        headers[existing_key].append(value.strip())


def parse_raw_http(text: str, source: str, label: str) -> RequestSample | None:
    normalized = text.replace("\r\n", "\n")
    if "\n\n" in normalized:
        head, body_raw = normalized.split("\n\n", 1)
    else:
        head, body_raw = normalized, ""

    lines = [line for line in head.splitlines() if line.strip()]
    if not lines:
        return None

    first = lines[0].strip()
    match = re.match(r"^(?P<method>[A-Z]+)\s+(?P<target>\S+)(?:\s+HTTP/\d(?:\.\d)?)?$", first)
    if not match:
        return None

    headers: dict[str, list[str]] = {}
    order: list[str] = []
    for line in lines[1:]:
        if ":" not in line:
            continue
        key, value = line.split(":", 1)
        add_header(headers, order, key, value)

    target = match.group("target")
    host = ""
    for key, values in headers.items():
        if key.lower() == "host" and values:
            host = values[0]
            break
    if target.startswith("http://") or target.startswith("https://"):
        url = target
        path = re.sub(r"^https?://[^/]+", "", target) or "/"
    else:
        url = f"https://{host}{target}" if host else target
        path = target

    return RequestSample(
        source=source,
        label=label,
        method=match.group("method"),
        url=url,
        path=path,
        headers=headers,
        header_order=order,
        body_raw=body_raw.strip(),
        body=parse_json_body(body_raw),
    )


def parse_headers_object(raw_headers: Any) -> tuple[dict[str, list[str]], list[str]]:
    headers: dict[str, list[str]] = {}
    order: list[str] = []
    if isinstance(raw_headers, dict):
        for key, value in raw_headers.items():
            for item in normalize_header_values(value):
                add_header(headers, order, str(key), item)
    elif isinstance(raw_headers, list):
        for item in raw_headers:
            if isinstance(item, dict):
                key = item.get("name") or item.get("key")
                value = item.get("value")
                if key is not None and value is not None:
                    add_header(headers, order, str(key), str(value))
    return headers, order


def request_from_json_object(obj: Any, source: str, label: str) -> RequestSample | None:
    if isinstance(obj, list):
        for index, item in enumerate(obj):
            parsed = request_from_json_object(item, source, f"{label}[{index}]")
            if parsed:
                return parsed
        return None

    if not isinstance(obj, dict):
        return None

    if isinstance(obj.get("log"), dict) and isinstance(obj["log"].get("entries"), list):
        for index, entry in enumerate(obj["log"]["entries"]):
            req = entry.get("request") if isinstance(entry, dict) else None
            if not isinstance(req, dict):
                continue
            parsed = request_from_json_object(req, source, f"{label}:har[{index}]")
            if parsed and ("anthropic" in parsed.url or "/v1/messages" in parsed.path):
                return parsed
        return None

    if isinstance(obj.get("request"), dict):
        nested = request_from_json_object(obj["request"], source, f"{label}:request")
        if nested:
            return nested

    method = str(obj.get("method") or obj.get("httpMethod") or "")
    url = str(obj.get("url") or obj.get("requestUrl") or "")
    path = str(obj.get("path") or obj.get("pathname") or "")

    raw_headers = obj.get("headers") or obj.get("requestHeaders") or {}
    headers, order = parse_headers_object(raw_headers)

    body_value = None
    if isinstance(obj.get("postData"), dict):
        body_value = obj["postData"].get("text")
    for key in ("body", "rawBody", "requestBody", "data", "text"):
        if body_value is None and key in obj:
            body_value = obj[key]

    if isinstance(body_value, (dict, list)):
        body = body_value
        body_raw = json.dumps(body_value, ensure_ascii=False)
    elif isinstance(body_value, str):
        body_raw = body_value
        body = parse_json_body(body_value)
    else:
        body_raw = ""
        body = None

    if not headers and not body_raw and not url and not path:
        return None

    if not path and url:
        path = re.sub(r"^https?://[^/]+", "", url) or "/"

    return RequestSample(
        source=source,
        label=label,
        method=method,
        url=url,
        path=path,
        headers=headers,
        header_order=order,
        body_raw=body_raw.strip(),
        body=body,
    )


def parse_json_request(text: str, source: str, label: str) -> RequestSample | None:
    try:
        obj = json.loads(text)
    except json.JSONDecodeError:
        return None
    return request_from_json_object(obj, source, label)


def strip_debug_body_indent(lines: list[str]) -> str:
    if not lines:
        return ""
    while lines and not lines[0].strip():
        lines.pop(0)
    while lines and not lines[-1].strip():
        lines.pop()
    if not lines:
        return ""
    if all((not line) or line.startswith("  ") for line in lines):
        lines = [line[2:] if line.startswith("  ") else line for line in lines]
    return "\n".join(lines).strip()


def parse_snapshot_block(block: str, source: str, tag: str, ts: str) -> RequestSample:
    sections: dict[str, list[str]] = {"context": [], "headers": [], "body": []}
    current: str | None = None
    for line in block.splitlines()[1:]:
        marker = line.strip()
        if marker == "--- context ---":
            current = "context"
            continue
        if marker == "--- headers ---":
            current = "headers"
            continue
        if marker == "--- body ---":
            current = "body"
            continue
        if current:
            sections[current].append(line)

    context: dict[str, str] = {}
    for line in sections["context"]:
        stripped = line.strip()
        if not stripped or ":" not in stripped:
            continue
        key, value = stripped.split(":", 1)
        context[key.strip()] = value.strip()

    headers: dict[str, list[str]] = {}
    order: list[str] = []
    for line in sections["headers"]:
        stripped = line.strip()
        if not stripped or ":" not in stripped:
            continue
        key, value = stripped.split(":", 1)
        add_header(headers, order, key, value)

    body_raw = strip_debug_body_indent(sections["body"])
    if body_raw == "(empty)":
        body_raw = ""

    url = context.get("url", "")
    path = re.sub(r"^https?://[^/]+", "", url) or ""

    return RequestSample(
        source=source,
        label=f"{tag.strip()} @ {ts}",
        method="POST",
        url=url,
        path=path,
        headers=headers,
        header_order=order,
        body_raw=body_raw,
        body=parse_json_body(body_raw),
        context=context,
    )


def parse_snapshots(text: str, source: str) -> list[RequestSample]:
    matches = list(SNAPSHOT_RE.finditer(text))
    samples: list[RequestSample] = []
    for index, match in enumerate(matches):
        start = match.start()
        end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
        block = text[start:end]
        samples.append(parse_snapshot_block(block, source, match.group("tag"), match.group("ts")))
    return samples


def load_request(path: str, preferred_tag: str | None, label: str) -> RequestSample:
    text = read_text(path)

    snapshots = parse_snapshots(text, path)
    if snapshots:
        if preferred_tag:
            preferred = [
                sample
                for sample in snapshots
                if sample.label.split("@", 1)[0].strip().lower() == preferred_tag.lower()
                or preferred_tag.lower() in sample.label.lower()
            ]
            if preferred:
                return preferred[-1]
        return snapshots[-1]

    parsed = parse_json_request(text, path, label)
    if parsed:
        return parsed

    parsed = parse_raw_http(text, path, label)
    if parsed:
        return parsed

    raise ValueError(f"cannot parse request input: {path}")


def is_sensitive_header_key(key: str) -> bool:
    lower = key.lower().removeprefix("header.")
    return lower in {"authorization", "x-api-key", "cookie", "set-cookie", "proxy-authorization"}


def redact_header_value(key: str, value: str, show_values: bool) -> str:
    if is_sensitive_header_key(key):
        if value.lower().startswith("bearer "):
            return "Bearer [redacted]"
        if value.lower().startswith("basic "):
            return "Basic [redacted]"
        return "[redacted]"
    if show_values:
        return value
    if len(value) > 180:
        return value[:177] + "..."
    return value


def split_beta(value: str) -> list[str]:
    return [item.strip() for item in value.split(",") if item.strip()]


def auth_scheme(value: str) -> str:
    if not value:
        return ""
    parts = value.strip().split(None, 1)
    return parts[0].lower() if parts else ""


def json_get(obj: Any, path: str) -> Any:
    current = obj
    for part in path.split("."):
        if isinstance(current, dict):
            current = current.get(part)
        elif isinstance(current, list) and part.isdigit():
            idx = int(part)
            current = current[idx] if 0 <= idx < len(current) else None
        else:
            return None
    return current


def parse_metadata_user_id(raw: Any) -> dict[str, Any] | None:
    if not isinstance(raw, str):
        return None
    raw = raw.strip()
    if not raw:
        return None
    if raw.startswith("{"):
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError:
            return None
        if not isinstance(parsed, dict):
            return None
        device_id = str(parsed.get("device_id") or "")
        account_uuid = str(parsed.get("account_uuid") or "")
        session_id = str(parsed.get("session_id") or "")
        if not device_id or not UUID_RE.match(session_id):
            return None
        return {
            "format": "json",
            "device_id": device_id,
            "account_uuid": account_uuid,
            "session_id": session_id,
        }
    match = LEGACY_METADATA_RE.match(raw)
    if not match:
        return None
    return {
        "format": "legacy",
        "device_id": match.group(1),
        "account_uuid": match.group(2),
        "session_id": match.group(3),
    }


def system_texts(body: Any) -> list[str]:
    system = json_get(body, "system")
    if isinstance(system, str):
        return [system]
    if isinstance(system, list):
        out: list[str] = []
        for item in system:
            if isinstance(item, dict) and isinstance(item.get("text"), str):
                out.append(item["text"])
            elif isinstance(item, str):
                out.append(item)
        return out
    return []


def looks_like_claude_code_prompt(texts: list[str]) -> bool:
    haystack = "\n".join(texts).lower()
    return "claude code" in haystack and ("official cli" in haystack or "anthropic" in haystack)


def preview(value: Any, limit: int = 120) -> str:
    if value is None:
        return ""
    text = str(value).replace("\n", "\\n").replace("\r", "\\r")
    if len(text) > limit:
        return text[: limit - 3] + "..."
    return text


def collect_tool_names(body: Any) -> list[str]:
    tools = json_get(body, "tools")
    if not isinstance(tools, list):
        return []
    names: list[str] = []
    for item in tools:
        if isinstance(item, dict) and isinstance(item.get("name"), str):
            names.append(item["name"])
    return names


def collect_roles(body: Any) -> list[str]:
    messages = json_get(body, "messages")
    if not isinstance(messages, list):
        return []
    roles: list[str] = []
    for item in messages:
        if isinstance(item, dict):
            roles.append(str(item.get("role") or ""))
    return roles


def collect_cache_controls(value: Any, path: str = "$") -> list[str]:
    found: list[str] = []
    if isinstance(value, dict):
        if isinstance(value.get("cache_control"), dict):
            cc = value["cache_control"]
            cc_type = str(cc.get("type") or "")
            ttl = str(cc.get("ttl") or "")
            found.append(f"{path}.cache_control:type={cc_type},ttl={ttl}")
        for key, child in value.items():
            found.extend(collect_cache_controls(child, f"{path}.{key}"))
    elif isinstance(value, list):
        for index, child in enumerate(value):
            found.extend(collect_cache_controls(child, f"{path}[{index}]"))
    return found


def normalized_order(sample: RequestSample) -> list[str]:
    return [key.lower() for key in sample.header_order if key.lower() in CORE_HEADERS]


def compare_headers(real: RequestSample, sub: RequestSample, show_values: bool) -> list[Finding]:
    findings: list[Finding] = []

    for key in CORE_HEADERS:
        real_value = real.header(key)
        sub_value = sub.header(key)

        if not real_value and not sub_value:
            continue
        if real_value and not sub_value:
            severity = "high" if key in HIGH_RISK_HEADERS or key in STAINLESS_HEADERS else "medium"
            findings.append(
                Finding(
                    severity,
                    f"header.{key}",
                    "sub2api upstream request is missing a header present in real Claude CLI traffic",
                    redact_header_value(key, real_value, show_values),
                    "",
                )
            )
            continue
        if sub_value and not real_value:
            severity = "medium" if key in HIGH_RISK_HEADERS else "low"
            findings.append(
                Finding(
                    severity,
                    f"header.{key}",
                    "sub2api upstream request has an extra header not present in the real sample",
                    "",
                    redact_header_value(key, sub_value, show_values),
                )
            )
            continue

        if key in {"authorization", "x-api-key"}:
            if auth_scheme(real_value) != auth_scheme(sub_value):
                findings.append(
                    Finding(
                        "medium",
                        f"header.{key}",
                        "authentication header scheme differs",
                        redact_header_value(key, real_value, show_values),
                        redact_header_value(key, sub_value, show_values),
                    )
                )
            continue

        if key == "anthropic-beta":
            continue

        if key == "content-length":
            continue

        if key == "x-claude-code-session-id":
            # Different captures can legitimately use different sessions. The
            # important invariant is checked in compare_metadata: sub2api's
            # header must match sub2api's own metadata.user_id session_id.
            continue

        if real_value != sub_value:
            severity = "medium" if key in HIGH_RISK_HEADERS or key in STAINLESS_HEADERS else "low"
            findings.append(
                Finding(
                    severity,
                    f"header.{key}",
                    "header value differs from real Claude CLI traffic",
                    redact_header_value(key, real_value, show_values),
                    redact_header_value(key, sub_value, show_values),
                )
            )

    sub_ua = sub.header("user-agent")
    if sub_ua and not re.match(r"^claude-cli/\d+\.\d+\.\d+", sub_ua, re.I):
        findings.append(Finding("high", "header.user-agent", "sub2api User-Agent is not a Claude CLI UA", "", sub_ua))

    if sub.header("x-app") and sub.header("x-app") != "cli":
        findings.append(Finding("high", "header.x-app", "x-app should be cli for Claude CLI traffic", "", sub.header("x-app")))

    if sub.header("anthropic-dangerous-direct-browser-access") and sub.header("anthropic-dangerous-direct-browser-access") != "true":
        findings.append(
            Finding(
                "high",
                "header.anthropic-dangerous-direct-browser-access",
                "Claude CLI traffic carries this header with true",
                "",
                sub.header("anthropic-dangerous-direct-browser-access"),
            )
        )

    real_order = normalized_order(real)
    sub_order = normalized_order(sub)
    if len(real_order) >= 3 and len(sub_order) >= 3 and real_order != sub_order:
        findings.append(
            Finding(
                "low",
                "headers.order",
                "interesting header order differs; confirm whether this came from real wire capture or sorted debug output",
                " > ".join(real_order),
                " > ".join(sub_order),
            )
        )

    return findings


def compare_beta(real: RequestSample, sub: RequestSample) -> list[Finding]:
    real_tokens = split_beta(real.header("anthropic-beta"))
    sub_tokens = split_beta(sub.header("anthropic-beta"))
    findings: list[Finding] = []
    if not real_tokens and not sub_tokens:
        return findings
    if real_tokens:
        missing = [token for token in real_tokens if token not in sub_tokens]
        extra = [token for token in sub_tokens if token not in real_tokens]
    else:
        missing = []
        extra = sub_tokens

    if missing:
        high = [token for token in missing if token in EXPECTED_OAUTH_UPSTREAM_BETA_TOKENS]
        findings.append(
            Finding(
                "high" if high else "medium",
                "header.anthropic-beta",
                "sub2api is missing beta token(s) from the real Claude CLI sample",
                ",".join(missing),
                ",".join(sub_tokens),
            )
        )
    if extra:
        findings.append(
            Finding(
                "low",
                "header.anthropic-beta",
                "sub2api sends beta token(s) that are absent from this real sample",
                ",".join(real_tokens),
                ",".join(extra),
            )
        )

    required_oauth_tokens = required_oauth_upstream_beta_tokens(sub)
    for token in required_oauth_tokens:
        if token not in sub_tokens:
            findings.append(
                Finding(
                    "high",
                    "header.anthropic-beta",
                    f"sub2api upstream request is missing expected Claude CLI beta token {token}",
                    "",
                    ",".join(sub_tokens),
                )
            )
    return findings


def required_oauth_upstream_beta_tokens(sample: RequestSample) -> set[str]:
    token_type = sample.context.get("token_type", "").strip().lower()
    mimic = sample.context.get("mimic_claude_code", "").strip().lower()
    if token_type == "oauth" or mimic == "true":
        model = str(json_get(sample.body, "model") or "").lower()
        if "haiku" in model:
            return official_beta_tokens(haiku=True)
        return official_beta_tokens(haiku=False)

    target = sample.url or sample.path
    if "api.anthropic.com" in target and sample.header("authorization") and not sample.header("x-api-key"):
        model = str(json_get(sample.body, "model") or "").lower()
        if "haiku" in model:
            return official_beta_tokens(haiku=True)
        return official_beta_tokens(haiku=False)

    return set()


def compare_metadata(real: RequestSample, sub: RequestSample) -> list[Finding]:
    findings: list[Finding] = []
    real_uid = json_get(real.body, "metadata.user_id")
    sub_uid = json_get(sub.body, "metadata.user_id")

    real_parsed = parse_metadata_user_id(real_uid)
    sub_parsed = parse_metadata_user_id(sub_uid)

    if real_uid and not sub_uid:
        findings.append(Finding("high", "body.metadata.user_id", "sub2api request is missing metadata.user_id", preview(real_uid), ""))
        return findings
    if sub_uid and not sub_parsed:
        findings.append(Finding("high", "body.metadata.user_id", "sub2api metadata.user_id is not a valid Claude CLI format", "", preview(sub_uid)))
        return findings

    if real_parsed and sub_parsed:
        if real_parsed["format"] != sub_parsed["format"]:
            findings.append(
                Finding(
                    "medium",
                    "body.metadata.user_id",
                    "metadata.user_id format differs from the real sample",
                    real_parsed["format"],
                    sub_parsed["format"],
                )
            )
        if real_parsed.get("account_uuid") and not sub_parsed.get("account_uuid"):
            findings.append(
                Finding(
                    "medium",
                    "body.metadata.user_id",
                    "real sample has account_uuid but sub2api sent an empty account_uuid",
                    real_parsed.get("account_uuid", ""),
                    sub_parsed.get("account_uuid", ""),
                )
            )

    sub_session_header = sub.header("x-claude-code-session-id")
    if sub_session_header and sub_parsed and sub_session_header != sub_parsed["session_id"]:
        findings.append(
            Finding(
                "high",
                "header.x-claude-code-session-id",
                "session header does not match metadata.user_id session_id",
                sub_parsed["session_id"],
                sub_session_header,
            )
        )

    return findings


def compare_body(real: RequestSample, sub: RequestSample) -> list[Finding]:
    findings: list[Finding] = []

    if real.body is None and sub.body is None:
        return findings
    if real.body is not None and sub.body is None:
        return [Finding("high", "body", "sub2api body is missing or not valid JSON", "valid JSON", preview(sub.body_raw))]
    if real.body is None and sub.body is not None:
        findings.append(Finding("low", "body", "real sample body is missing or not valid JSON; body comparison is partial", preview(real.body_raw), "valid JSON"))
        return findings

    for key in ("model", "stream", "max_tokens"):
        real_value = json_get(real.body, key)
        sub_value = json_get(sub.body, key)
        if real_value != sub_value:
            severity = "medium" if key in {"model", "stream"} else "low"
            findings.append(Finding(severity, f"body.{key}", "top-level body field differs", preview(real_value), preview(sub_value)))

    for key in ("thinking.type", "thinking.budget_tokens"):
        real_value = json_get(real.body, key)
        sub_value = json_get(sub.body, key)
        if real_value != sub_value:
            findings.append(Finding("medium", f"body.{key}", "thinking field differs", preview(real_value), preview(sub_value)))

    real_system = system_texts(real.body)
    sub_system = system_texts(sub.body)
    if looks_like_claude_code_prompt(real_system) and not looks_like_claude_code_prompt(sub_system):
        findings.append(
            Finding(
                "high",
                "body.system",
                "real sample contains Claude Code system prompt but sub2api upstream body does not",
                preview(real_system[0] if real_system else ""),
                preview(sub_system[0] if sub_system else ""),
            )
        )
    elif real_system and sub_system and preview(real_system[0]) != preview(sub_system[0]):
        findings.append(
            Finding(
                "medium",
                "body.system",
                "first system prompt text differs",
                preview(real_system[0]),
                preview(sub_system[0]),
            )
        )
    if len(real_system) != len(sub_system):
        findings.append(
            Finding("low", "body.system", "system entry count differs", str(len(real_system)), str(len(sub_system)))
        )

    real_roles = collect_roles(real.body)
    sub_roles = collect_roles(sub.body)
    if real_roles != sub_roles:
        findings.append(
            Finding("medium", "body.messages", "message role sequence differs", ",".join(real_roles), ",".join(sub_roles))
        )

    real_tools = collect_tool_names(real.body)
    sub_tools = collect_tool_names(sub.body)
    if real_tools != sub_tools:
        findings.append(
            Finding("medium", "body.tools", "tool name list differs", ",".join(real_tools), ",".join(sub_tools))
        )

    real_cache = collect_cache_controls(real.body)
    sub_cache = collect_cache_controls(sub.body)
    if real_cache != sub_cache:
        findings.append(
            Finding(
                "low",
                "body.cache_control",
                "cache_control placement or ttl differs; this can be policy-driven but should be intentional",
                " | ".join(real_cache),
                " | ".join(sub_cache),
            )
        )

    findings.extend(compare_metadata(real, sub))
    return findings


def score_findings(findings: list[Finding]) -> int:
    penalty = sum(SEVERITY_WEIGHT[f.severity] for f in findings)
    return max(0, 100 - penalty)


def finding_counts(findings: list[Finding]) -> dict[str, int]:
    return {
        "high": sum(1 for f in findings if f.severity == "high"),
        "medium": sum(1 for f in findings if f.severity == "medium"),
        "low": sum(1 for f in findings if f.severity == "low"),
    }


def sort_findings(findings: list[Finding]) -> list[Finding]:
    return sorted(findings, key=lambda f: (SEVERITY_ORDER.get(f.severity, 9), f.area, f.message))


def build_report(real: RequestSample, sub: RequestSample, findings: list[Finding], show_values: bool) -> str:
    counts = finding_counts(findings)
    lines: list[str] = []
    lines.append("Claude CLI mimicry audit")
    lines.append(f"real:   {real.source} ({real.label})")
    lines.append(f"sub2api:{sub.source} ({sub.label})")
    lines.append(f"score:  {score_findings(findings)}/100")
    lines.append(f"issues: high={counts['high']} medium={counts['medium']} low={counts['low']}")
    lines.append("")

    if findings:
        lines.append("Findings")
        for finding in sort_findings(findings):
            lines.append(f"- [{finding.severity.upper()}] {finding.area}: {finding.message}")
            if finding.real or finding.sub2api:
                real_value = redact_header_value(finding.area, finding.real, show_values)
                sub_value = redact_header_value(finding.area, finding.sub2api, show_values)
                lines.append(f"  real={preview(real_value, 240)}")
                lines.append(f"  sub2api={preview(sub_value, 240)}")
    else:
        lines.append("Findings")
        lines.append("- No protocol-level differences detected by this offline audit.")

    lines.append("")
    lines.append("Key fields")
    rows = [
        ("url", real.url or real.path, sub.url or sub.path),
        ("user-agent", real.header("user-agent"), sub.header("user-agent")),
        ("anthropic-beta", real.header("anthropic-beta"), sub.header("anthropic-beta")),
        ("x-stainless-package-version", real.header("x-stainless-package-version"), sub.header("x-stainless-package-version")),
        ("x-stainless-runtime-version", real.header("x-stainless-runtime-version"), sub.header("x-stainless-runtime-version")),
        ("x-claude-code-session-id", real.header("x-claude-code-session-id"), sub.header("x-claude-code-session-id")),
        ("model", preview(json_get(real.body, "model")), preview(json_get(sub.body, "model"))),
        ("stream", preview(json_get(real.body, "stream")), preview(json_get(sub.body, "stream"))),
        ("system_entries", str(len(system_texts(real.body))), str(len(system_texts(sub.body)))),
        ("tools", ",".join(collect_tool_names(real.body)), ",".join(collect_tool_names(sub.body))),
    ]
    width = max(len(row[0]) for row in rows)
    for key, real_value, sub_value in rows:
        real_out = redact_header_value(key, str(real_value or ""), show_values)
        sub_out = redact_header_value(key, str(sub_value or ""), show_values)
        lines.append(f"- {key.ljust(width)} real={preview(real_out, 160)}")
        lines.append(f"  {' '.ljust(width)} sub2api={preview(sub_out, 160)}")

    lines.append("")
    lines.append("Recommended next checks")
    if counts["high"]:
        lines.append("- Fix HIGH items first; these are protocol-shape differences likely to affect Claude Code OAuth compatibility.")
    if counts["medium"]:
        lines.append("- Review MEDIUM items against a fresh real CLI capture before changing production behavior.")
    lines.append("- Re-run this audit after every Claude CLI upgrade or upstream mimicry change.")
    return "\n".join(lines)


def build_json_report(real: RequestSample, sub: RequestSample, findings: list[Finding]) -> str:
    payload = {
        "real": {
            "source": real.source,
            "label": real.label,
            "url": real.url,
            "path": real.path,
        },
        "sub2api": {
            "source": sub.source,
            "label": sub.label,
            "url": sub.url,
            "path": sub.path,
        },
        "score": score_findings(findings),
        "counts": finding_counts(findings),
        "findings": [finding.__dict__ for finding in sort_findings(findings)],
    }
    return json.dumps(payload, ensure_ascii=False, indent=2)


def list_snapshots(path: str) -> int:
    text = read_text(path)
    snapshots = parse_snapshots(text, path)
    if not snapshots:
        print(f"No sub2api gateway debug snapshots found in {path}")
        return 1
    for index, sample in enumerate(snapshots):
        print(f"{index}: {sample.label} url={sample.url}")
    return 0


def load_flow_summary(path: str) -> dict[str, Any]:
    try:
        payload = json.loads(read_text(path))
    except json.JSONDecodeError as exc:
        raise ValueError(f"cannot parse flow summary JSON: {path}: {exc}") from exc
    if not isinstance(payload, dict) or not isinstance(payload.get("items"), list):
        raise ValueError(f"flow summary must contain an items array: {path}")
    return payload


def endpoint_key(item: dict[str, Any]) -> str:
    method = str(item.get("method") or "").upper()
    path = str(item.get("path") or "")
    path_only = path.split("?", 1)[0] or "/"
    return f"{method} {path_only}".strip()


def body_summary_value(item: dict[str, Any], key: str) -> Any:
    body_summary = item.get("body_summary")
    if not isinstance(body_summary, dict):
        return None
    return body_summary.get(key)


def first_core_flow(items: list[dict[str, Any]]) -> dict[str, Any] | None:
    candidates = [item for item in items if endpoint_key(item) == "POST /v1/messages"]
    for item in candidates:
        body_summary = item.get("body_summary") if isinstance(item.get("body_summary"), dict) else {}
        model = str(body_summary.get("model") or "").lower()
        tools = body_summary.get("tools")
        if "haiku" not in model and isinstance(tools, list) and tools:
            return item
    for item in candidates:
        body_summary = item.get("body_summary") if isinstance(item.get("body_summary"), dict) else {}
        model = str(body_summary.get("model") or "").lower()
        if "haiku" not in model:
            return item
    for item in items:
        if endpoint_key(item) == "POST /v1/messages":
            return item
    return None


def companion_coverage(items: list[dict[str, Any]]) -> dict[str, Any]:
    observed = [endpoint_key(item) for item in items]
    expected = list(EXPECTED_COMPANION_ENDPOINTS)
    remaining = list(observed)
    present: list[str] = []
    missing: list[str] = []
    for endpoint in expected:
        try:
            index = remaining.index(endpoint)
        except ValueError:
            missing.append(endpoint)
            continue
        present.append(endpoint)
        del remaining[index]
    return {
        "expected": expected,
        "present": present,
        "missing": missing,
        "observed_sequence": observed,
    }


def companion_sequence_findings(items: list[dict[str, Any]]) -> list[Finding]:
    expected = list(EXPECTED_COMPANION_ENDPOINTS)
    observed = [endpoint_key(item) for item in items if endpoint_key(item) in EXPECTED_COMPANION_FINGERPRINTS]
    if len(observed) < len(expected):
        return []
    for start in range(0, len(observed)-len(expected)+1):
        if observed[start : start + len(expected)] == expected:
            return []
    return [
        Finding(
            "medium",
            "flow.companion.order",
            "companion request order differs from official Claude Code capture",
            " > ".join(expected),
            " > ".join(observed),
        )
    ]


def check_companion_fingerprints(items: list[dict[str, Any]]) -> list[Finding]:
    findings: list[Finding] = []
    grouped: dict[str, list[dict[str, Any]]] = {}
    for item in items:
        grouped.setdefault(endpoint_key(item), []).append(item)

    for endpoint, expected in EXPECTED_COMPANION_FINGERPRINTS.items():
        candidates = grouped.get(endpoint) or []
        if not candidates:
            continue
        item = candidates[0]
        headers = item.get("headers") if isinstance(item.get("headers"), dict) else {}
        path = str(item.get("path") or "")

        for header_key in ("user-agent", "anthropic-beta"):
            want = expected.get(header_key)
            if want is None:
                continue
            got = str(headers.get(header_key) or "")
            if got != want:
                findings.append(
                    Finding(
                        "medium",
                        f"flow.companion.{endpoint}.{header_key}",
                        "companion request fingerprint differs from official Claude Code capture",
                        str(want),
                        got,
                    )
                )

        for token in expected.get("query_contains", []):
            if token not in path:
                findings.append(
                    Finding(
                        "medium",
                        f"flow.companion.{endpoint}.query",
                        "companion request query differs from official Claude Code capture",
                        token,
                        path,
                    )
                )
    return findings


def parse_go_string_const(text: str, name: str) -> str:
    match = re.search(rf'\bconst\s+{re.escape(name)}\s*=\s*"([^"]*)"', text)
    return match.group(1) if match else ""


def parse_go_default_headers(text: str) -> dict[str, str]:
    match = re.search(r"var\s+DefaultHeaders\s*=\s*map\[string\]string\s*{(?P<body>.*?)}", text, re.S)
    if not match:
        return {}
    headers: dict[str, str] = {}
    for key, value in re.findall(r'"([^"]+)"\s*:\s*"([^"]*)"', match.group("body")):
        headers[key.lower()] = value
    return headers


def parse_go_beta_consts(text: str) -> dict[str, str]:
    return {name: value for name, value in re.findall(r'\b(Beta[A-Za-z0-9_]+)\s*=\s*"([^"]*)"', text)}


def parse_go_concat_const(text: str, name: str, values: dict[str, str]) -> str:
    match = re.search(rf'\bconst\s+{re.escape(name)}\s*=\s*(?P<expr>[^\n]+)', text)
    if not match:
        return ""
    tokens: list[str] = []
    for part in match.group("expr").split("+"):
        part = part.strip()
        if not part:
            continue
        if part.startswith('"') and part.endswith('"'):
            tokens.append(part[1:-1])
            continue
        tokens.append(values.get(part, ""))
    return "".join(tokens)


def load_official_mimic_fingerprint_from_repo() -> dict[str, Any]:
    try:
        text = CLAUDE_CONSTANTS_PATH.read_text(encoding="utf-8")
    except OSError:
        return {}

    headers = parse_go_default_headers(text)
    cli_version = parse_go_string_const(text, "CLICurrentVersion")
    ua = headers.get("user-agent", "")
    if cli_version and ua and f"claude-cli/{cli_version}" not in ua:
        raise ValueError(f"{CLAUDE_CONSTANTS_PATH}: CLICurrentVersion does not match DefaultHeaders User-Agent")
    if not headers:
        return {}

    return {
        "user-agent": headers.get("user-agent", ""),
        "x-stainless-package-version": headers.get("x-stainless-package-version", ""),
        "x-stainless-os": headers.get("x-stainless-os", ""),
        "x-stainless-arch": headers.get("x-stainless-arch", ""),
        "x-stainless-runtime": headers.get("x-stainless-runtime", ""),
        "x-stainless-runtime-version": headers.get("x-stainless-runtime-version", ""),
        "x-stainless-timeout": headers.get("x-stainless-timeout", ""),
        "x-app": headers.get("x-app", ""),
        "anthropic-dangerous-direct-browser-access": headers.get("anthropic-dangerous-direct-browser-access", ""),
        "x-anthropic-billing-header.cc_entrypoint": "sdk-cli",
    }


def fallback_official_mimic_fingerprint() -> dict[str, Any]:
    return {
        "user-agent": "claude-cli/2.1.165 (external, sdk-cli)",
        "x-stainless-package-version": "0.94.0",
        "x-stainless-os": "MacOS",
        "x-stainless-arch": "arm64",
        "x-stainless-runtime": "node",
        "x-stainless-runtime-version": "v24.3.0",
        "x-stainless-timeout": "600",
        "x-app": "cli",
        "anthropic-dangerous-direct-browser-access": "true",
        "x-anthropic-billing-header.cc_entrypoint": "sdk-cli",
    }


def official_mimic_fingerprint() -> dict[str, Any]:
    return load_official_mimic_fingerprint_from_repo() or fallback_official_mimic_fingerprint()


def fallback_official_beta_tokens(haiku: bool) -> set[str]:
    if haiku:
        return {
            "oauth-2025-04-20",
            "interleaved-thinking-2025-05-14",
            "context-management-2025-06-27",
            "prompt-caching-scope-2026-01-05",
            "mid-conversation-system-2026-04-07",
            "effort-2025-11-24",
            "structured-outputs-2025-12-15",
        }
    return {
        "claude-code-20250219",
        "oauth-2025-04-20",
        "interleaved-thinking-2025-05-14",
        "context-management-2025-06-27",
        "prompt-caching-scope-2026-01-05",
        "mid-conversation-system-2026-04-07",
        "advanced-tool-use-2025-11-20",
        "effort-2025-11-24",
        "extended-cache-ttl-2025-04-11",
    }


def official_beta_tokens(haiku: bool) -> set[str]:
    try:
        text = CLAUDE_CONSTANTS_PATH.read_text(encoding="utf-8")
    except OSError:
        return fallback_official_beta_tokens(haiku)
    beta_consts = parse_go_beta_consts(text)
    value = parse_go_concat_const(text, "HaikuBetaHeader" if haiku else "DefaultBetaHeader", beta_consts)
    tokens = set(split_beta(value))
    return tokens or fallback_official_beta_tokens(haiku)


def extract_cache_ttls_from_summary(body_summary: dict[str, Any]) -> list[str]:
    ttl_values: list[str] = []

    def add(value: Any) -> None:
        if value is None:
            return
        text = str(value).strip()
        if text:
            ttl_values.append(text)

    for key in ("cache_control_ttls", "cache_ttls"):
        value = body_summary.get(key)
        if isinstance(value, list):
            for item in value:
                add(item)
        else:
            add(value)

    controls = body_summary.get("cache_controls")
    if isinstance(controls, list):
        for item in controls:
            if isinstance(item, dict):
                add(item.get("ttl"))
            elif isinstance(item, str):
                match = re.search(r"(?:ttl=|ttl:)([^,\s}]+)", item)
                add(match.group(1) if match else item)

    raw_body = body_summary.get("body")
    if raw_body is not None:
        if isinstance(raw_body, str):
            raw_body = parse_json_body(raw_body)
        for item in collect_cache_controls(raw_body):
            match = re.search(r"ttl=([^,\s]+)", item)
            if match:
                add(match.group(1))

    return ttl_values


def usage_int(value: Any, *paths: str) -> int | None:
    for path in paths:
        found = json_get(value, path) if path else value
        if found is None:
            continue
        try:
            return int(found)
        except (TypeError, ValueError):
            continue
    return None


def extract_usage_cache_breakdown(item: dict[str, Any]) -> dict[str, int | None]:
    candidates: list[Any] = []
    for key in ("usage", "usage_summary", "response_usage"):
        if isinstance(item.get(key), dict):
            candidates.append(item[key])
    response_summary = item.get("response_body_summary")
    if isinstance(response_summary, dict):
        if isinstance(response_summary.get("usage"), dict):
            candidates.append(response_summary["usage"])
        candidates.append(response_summary)

    for usage in candidates:
        five_min = usage_int(
            usage,
            "cache_creation.ephemeral_5m_input_tokens",
            "cache_creation_5m_tokens",
            "cache_creation5m_tokens",
            "ephemeral_5m_input_tokens",
        )
        one_hour = usage_int(
            usage,
            "cache_creation.ephemeral_1h_input_tokens",
            "cache_creation_1h_tokens",
            "cache_creation1h_tokens",
            "ephemeral_1h_input_tokens",
        )
        total = usage_int(usage, "cache_creation_input_tokens", "cache_creation_tokens")
        if five_min is not None or one_hour is not None or total is not None:
            return {"5m": five_min, "1h": one_hour, "total": total}
    return {"5m": None, "1h": None, "total": None}


def ttl_usage_consistency(core: dict[str, Any] | None) -> dict[str, Any]:
    if not core:
        return {"status": "unknown", "reason": "no core request"}
    body_summary = core.get("body_summary") if isinstance(core.get("body_summary"), dict) else {}
    ttls = extract_cache_ttls_from_summary(body_summary)
    breakdown = extract_usage_cache_breakdown(core)
    has_1h_request = "1h" in ttls
    has_usage = any(value is not None for value in breakdown.values())
    if not ttls:
        return {"status": "unknown", "reason": "no cache_control ttl summary", "ttls": ttls, "usage": breakdown}
    if not has_usage:
        return {"status": "unknown", "reason": "no response usage cache breakdown", "ttls": ttls, "usage": breakdown}
    if has_1h_request and (breakdown["1h"] or 0) == 0 and (breakdown["5m"] or 0) > 0:
        return {"status": "mismatch", "reason": "request sent 1h cache_control but usage was recorded as 5m", "ttls": ttls, "usage": breakdown}
    return {"status": "ok", "reason": "", "ttls": ttls, "usage": breakdown}


def flow_summary_findings(items: list[dict[str, Any]]) -> list[Finding]:
    core = first_core_flow(items)
    if not core:
        findings = [Finding("high", "flow.core", "flow summary has no POST /v1/messages core request")]
    else:
        findings = []
    if core:
        headers = core.get("headers") if isinstance(core.get("headers"), dict) else {}
        expected = official_mimic_fingerprint()
        for key, value in expected.items():
            if key.startswith("x-anthropic-billing-header."):
                system_entries = body_summary_value(core, "system_entries")
                billing = ""
                if isinstance(system_entries, list):
                    for entry in system_entries:
                        if isinstance(entry, dict) and isinstance(entry.get("billing"), str):
                            billing = entry["billing"]
                            break
                if "cc_entrypoint=sdk-cli" not in billing:
                    findings.append(Finding("high", key, "core billing header does not use sdk-cli entrypoint", "cc_entrypoint=sdk-cli", billing))
                continue
            got = str(headers.get(key) or headers.get(key.lower()) or "")
            if got != value:
                severity = "high" if key in HIGH_RISK_HEADERS or key in STAINLESS_HEADERS else "medium"
                findings.append(Finding(severity, f"header.{key}", "captured core request differs from expected official fingerprint", str(value), got))

        beta = str(headers.get("anthropic-beta") or "")
        body_summary = core.get("body_summary") if isinstance(core.get("body_summary"), dict) else {}
        model = str(body_summary.get("model") or "").lower()
        required_tokens = official_beta_tokens(haiku="haiku" in model)
        for token in required_tokens:
            if token not in split_beta(beta):
                findings.append(Finding("high", "header.anthropic-beta", f"captured core request is missing {token}", token, beta))

        metadata = body_summary.get("metadata_user_id") if isinstance(body_summary.get("metadata_user_id"), dict) else {}
        session_id = str(metadata.get("session_id") or "")
        header_session = str(headers.get("x-claude-code-session-id") or "")
        if session_id and header_session and session_id != header_session:
            findings.append(Finding("high", "header.x-claude-code-session-id", "captured core session header does not match metadata.user_id", session_id, header_session))

    coverage = companion_coverage(items)
    for endpoint in coverage["missing"]:
        findings.append(Finding("medium", "flow.companion", "expected Claude Code companion endpoint was not observed", endpoint, "missing"))
    findings.extend(companion_sequence_findings(items))
    findings.extend(check_companion_fingerprints(items))

    consistency = ttl_usage_consistency(core)
    if consistency["status"] == "mismatch":
        findings.append(
            Finding(
                "high",
                "body.cache_control.usage",
                "cache_control ttl and usage accounting are inconsistent",
                ",".join(consistency.get("ttls") or []),
                str(consistency.get("usage")),
            )
        )
    return findings


def build_flow_summary_report(payload: dict[str, Any], show_values: bool) -> str:
    items = [item for item in payload.get("items", []) if isinstance(item, dict)]
    groups: dict[str, dict[str, Any]] = {}
    for item in items:
        key = endpoint_key(item)
        group = groups.setdefault(
            key,
            {
                "count": 0,
                "hosts": set(),
                "statuses": {},
                "content_types": {},
                "sse": False,
                "sample": item,
            },
        )
        group["count"] += 1
        if item.get("host"):
            group["hosts"].add(str(item["host"]))
        status = str(item.get("status"))
        group["statuses"][status] = group["statuses"].get(status, 0) + 1
        ct = str(item.get("response_content_type") or "")
        group["content_types"][ct] = group["content_types"].get(ct, 0) + 1
        if "text/event-stream" in ct:
            group["sse"] = True

    core = first_core_flow(items)
    findings = flow_summary_findings(items)
    coverage = companion_coverage(items)
    consistency = ttl_usage_consistency(core)
    counts = finding_counts(findings)
    lines: list[str] = []
    lines.append("Claude CLI flow summary audit")
    lines.append(f"source: {payload.get('source') or 'flow summary'}")
    lines.append(f"flows:  {payload.get('count', len(items))}")
    lines.append(f"score:  {score_findings(findings)}/100")
    lines.append(f"issues: high={counts['high']} medium={counts['medium']} low={counts['low']}")
    lines.append("")
    lines.append("Endpoint inventory")
    for key in sorted(groups):
        group = groups[key]
        hosts = ",".join(sorted(group["hosts"]))
        statuses = ",".join(f"{k}:{v}" for k, v in sorted(group["statuses"].items()))
        content_types = ",".join(f"{k or '(none)'}:{v}" for k, v in sorted(group["content_types"].items()))
        lines.append(f"- {key} count={group['count']} hosts={hosts} status={statuses} content_type={content_types} sse={str(group['sse']).lower()}")

    lines.append("")
    lines.append("Companion coverage")
    lines.append(f"- present: {len(coverage['present'])}/{len(coverage['expected'])}")
    if coverage["present"]:
        lines.append(f"- observed: {', '.join(coverage['present'])}")
    if coverage["missing"]:
        lines.append(f"- missing: {', '.join(coverage['missing'])}")

    lines.append("")
    lines.append("Core request fingerprint")
    if core:
        headers = core.get("headers") if isinstance(core.get("headers"), dict) else {}
        body_summary = core.get("body_summary") if isinstance(core.get("body_summary"), dict) else {}
        for key in [
            "user-agent",
            "x-stainless-package-version",
            "x-stainless-os",
            "x-stainless-arch",
            "x-stainless-runtime",
            "x-stainless-runtime-version",
            "x-stainless-timeout",
            "x-app",
            "anthropic-dangerous-direct-browser-access",
            "anthropic-version",
            "anthropic-beta",
            "x-claude-code-session-id",
        ]:
            value = str(headers.get(key) or "")
            lines.append(f"- {key}: {redact_header_value(key, value, show_values)}")
        lines.append(f"- model: {preview(body_summary.get('model'))}")
        lines.append(f"- stream: {preview(body_summary.get('stream'))}")
        lines.append(f"- max_tokens: {preview(body_summary.get('max_tokens'))}")
        lines.append(f"- thinking: {preview(body_summary.get('thinking'))}")
        lines.append(f"- tools: {','.join(body_summary.get('tools') or []) if isinstance(body_summary.get('tools'), list) else ''}")
        system_entries = body_summary.get("system_entries")
        if isinstance(system_entries, list):
            billing = next((entry.get("billing") for entry in system_entries if isinstance(entry, dict) and entry.get("billing")), "")
            lines.append(f"- billing: {preview(billing, 220)}")
    else:
        lines.append("- No POST /v1/messages request found.")

    lines.append("")
    lines.append("TTL and usage consistency")
    lines.append(f"- status: {consistency['status']}")
    if consistency.get("reason"):
        lines.append(f"- reason: {consistency['reason']}")
    lines.append(f"- request_ttls: {','.join(consistency.get('ttls') or [])}")
    lines.append(f"- usage: {consistency.get('usage')}")

    lines.append("")
    lines.append("Findings")
    if findings:
        for finding in sort_findings(findings):
            lines.append(f"- [{finding.severity.upper()}] {finding.area}: {finding.message}")
            if finding.real or finding.sub2api:
                lines.append(f"  expected={preview(redact_header_value(finding.area, finding.real, show_values), 240)}")
                lines.append(f"  actual={preview(redact_header_value(finding.area, finding.sub2api, show_values), 240)}")
    else:
        lines.append("- No flow-summary fingerprint issues detected.")
    return "\n".join(lines)


def build_flow_summary_json_report(payload: dict[str, Any]) -> str:
    items = [item for item in payload.get("items", []) if isinstance(item, dict)]
    core = first_core_flow(items)
    findings = flow_summary_findings(items)
    endpoints: dict[str, dict[str, Any]] = {}
    for item in items:
        key = endpoint_key(item)
        endpoint = endpoints.setdefault(key, {"count": 0, "statuses": {}, "content_types": {}, "sse": False})
        endpoint["count"] += 1
        status = str(item.get("status"))
        endpoint["statuses"][status] = endpoint["statuses"].get(status, 0) + 1
        ct = str(item.get("response_content_type") or "")
        endpoint["content_types"][ct] = endpoint["content_types"].get(ct, 0) + 1
        if "text/event-stream" in ct:
            endpoint["sse"] = True

    return json.dumps(
        {
            "count": payload.get("count", len(items)),
            "score": score_findings(findings),
            "counts": finding_counts(findings),
            "endpoints": endpoints,
            "companion_coverage": companion_coverage(items),
            "ttl_usage_consistency": ttl_usage_consistency(core),
            "findings": [finding.__dict__ for finding in sort_findings(findings)],
        },
        ensure_ascii=False,
        indent=2,
    )


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Audit sub2api Claude CLI mimicry against a real request sample.")
    parser.add_argument("--real", help="real Claude CLI request sample: raw HTTP, JSON/HAR, or debug snapshot")
    parser.add_argument("--sub2api", help="sub2api upstream request sample: raw HTTP, JSON/HAR, or gateway debug snapshot")
    parser.add_argument("--flow-summary", help="mitm JSON summary generated from a Claude CLI capture")
    parser.add_argument("--real-tag", default=None, help="preferred snapshot tag for --real when input is a debug log")
    parser.add_argument("--sub2api-tag", default="UPSTREAM_FORWARD", help="preferred snapshot tag for --sub2api debug logs")
    parser.add_argument("--json", action="store_true", help="emit machine-readable JSON")
    parser.add_argument("--show-values", action="store_true", help="show full non-sensitive header values; auth and cookie values are always redacted")
    parser.add_argument("--strict", action="store_true", help="exit with code 2 when HIGH findings are present")
    parser.add_argument("--list-snapshots", metavar="FILE", help="list gateway debug snapshots in a file and exit")
    args = parser.parse_args(argv)
    if not args.list_snapshots and not args.flow_summary and (not args.real or not args.sub2api):
        parser.error("--real and --sub2api are required unless --list-snapshots is used")
    return args


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    if args.list_snapshots:
        return list_snapshots(args.list_snapshots)
    if args.flow_summary:
        try:
            payload = load_flow_summary(args.flow_summary)
        except Exception as exc:
            print(f"error: {exc}", file=sys.stderr)
            return 1
        if args.json:
            print(build_flow_summary_json_report(payload))
        else:
            print(build_flow_summary_report(payload, args.show_values))
        return 0

    try:
        real = load_request(args.real, args.real_tag, "real")
        sub = load_request(args.sub2api, args.sub2api_tag, "sub2api")
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    findings = []
    findings.extend(compare_headers(real, sub, args.show_values))
    findings.extend(compare_beta(real, sub))
    findings.extend(compare_body(real, sub))

    if args.json:
        print(build_json_report(real, sub, findings))
    else:
        print(build_report(real, sub, findings, args.show_values))

    counts = finding_counts(findings)
    if args.strict and counts["high"] > 0:
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
