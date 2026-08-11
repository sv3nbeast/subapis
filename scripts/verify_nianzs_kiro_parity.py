#!/usr/bin/env python3
"""Verify the vendored Kiro engine against the pinned nianzs/sub2api source.

The Kiro packages are byte-for-byte copies.  Service files have to live beside
the rollback engine, so their Go identifiers are mechanically namespaced.  A
small, explicit adapter list is then applied for state isolation and dependency
injection.  This script recreates that expected source and fails on any other
drift.
"""

from __future__ import annotations

import argparse
import difflib
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path


SCRIPT_DIR = Path(__file__).resolve().parent
REPO_ROOT = SCRIPT_DIR.parent
MANIFEST_PATH = SCRIPT_DIR / "nianzs_kiro" / "upstream_manifest.json"
MAP_FILES = (
    SCRIPT_DIR / "nianzs_kiro" / "core_symbols.json",
    SCRIPT_DIR / "nianzs_kiro" / "scheduling_symbols.json",
    SCRIPT_DIR / "nianzs_kiro" / "control_symbols.json",
)

SERVICE_STEMS = (
    "gateway_kiro_helpers",
    "gateway_scheduling",
    "kiro_cache_emulation",
    "kiro_error_classifier",
    "kiro_http_helpers",
    "kiro_oauth_service",
    "kiro_profile_resolver",
    "kiro_runtime",
    "kiro_runtime_state",
    "kiro_token_provider",
    "kiro_token_refresher",
    "kiro_usage_fetcher",
    "kiro_websearch",
)

SERVICE_TEST_FILES = (
    (
        "kiro_cache_emulation_test.go",
        "kiro_cache_emulation_nianzs_parity_test.go",
    ),
)

SERVICE_TEST_SYMBOLS = {
    "resetKiroCacheTracker": "resetNianzsKiroCacheTracker",
    "kiroCacheGroup": "nianzsTestKiroCacheGroup",
    "kiroCacheAccount": "nianzsTestKiroCacheAccount",
    "kiroCacheImageRequestBody": "nianzsTestKiroCacheImageRequestBody",
    "kiroCacheMultiMessageBody": "nianzsTestKiroCacheMultiMessageBody",
    "kiroCacheRequestBody": "nianzsTestKiroCacheRequestBody",
    "kiroChatCompletionsConversationBody": "nianzsTestKiroChatCompletionsConversationBody",
    "kiroPNGDataURL": "nianzsTestKiroPNGDataURL",
    "kiroResponsesCacheRequestBody": "nianzsTestKiroResponsesCacheRequestBody",
    "kiroResponsesCacheRequestBodyWithOptions": "nianzsTestKiroResponsesCacheRequestBodyWithOptions",
    "kiroResponsesConversationRequestBody": "nianzsTestKiroResponsesConversationRequestBody",
    "kiroResponsesImageCacheRequestBody": "nianzsTestKiroResponsesImageCacheRequestBody",
}

EXACT_FILE_PAIRS = (
    (
        "backend/migrations/192_add_group_kiro_cache_emulation_modes.sql",
        "backend/migrations/196_add_group_kiro_cache_emulation_modes.sql",
    ),
    (
        "frontend/src/components/admin/group/KiroCacheRatioField.vue",
        "frontend/src/components/admin/group/KiroCacheRatioField.vue",
    ),
    (
        "frontend/src/components/admin/group/__tests__/KiroCacheRatioField.spec.ts",
        "frontend/src/components/admin/group/__tests__/KiroCacheRatioField.spec.ts",
    ),
    (
        "frontend/src/views/admin/__tests__/groupsKiroCacheMode.spec.ts",
        "frontend/src/views/admin/__tests__/groupsKiroCacheMode.spec.ts",
    ),
)


class ParityError(RuntimeError):
    pass


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def unified_diff(expected: str, actual: str, expected_name: str, actual_name: str) -> str:
    return "".join(
        difflib.unified_diff(
            expected.splitlines(keepends=True),
            actual.splitlines(keepends=True),
            fromfile=expected_name,
            tofile=actual_name,
            n=3,
        )
    )


def require_replace(text: str, old: str, new: str, count: int, label: str) -> str:
    actual_count = text.count(old)
    if actual_count != count:
        raise ParityError(
            f"{label}: adapter source occurrence changed: expected {count}, got {actual_count}: {old!r}"
        )
    return text.replace(old, new)


def strip_provenance(text: str) -> str:
    marker = "package service"
    index = text.find(marker)
    if index < 0:
        raise ParityError("generated/local service source has no package service declaration")
    return text[index:]


def gofmt(source: str, label: str) -> str:
    process = subprocess.run(
        ["gofmt"],
        input=source,
        text=True,
        capture_output=True,
        check=False,
    )
    if process.returncode != 0:
        raise ParityError(f"{label}: gofmt failed: {process.stderr.strip()}")
    return process.stdout


def load_symbol_map() -> dict[str, str]:
    merged: dict[str, str] = {}
    for path in MAP_FILES:
        values = json.loads(path.read_text())
        overlap = set(merged).intersection(values)
        if overlap:
            raise ParityError(f"duplicate symbol mappings in {path}: {sorted(overlap)}")
        merged.update(values)
    return merged


def rename_identifiers(source: str, symbols: dict[str, str]) -> str:
    pattern = re.compile(
        r"\b(" + "|".join(re.escape(name) for name in sorted(symbols, key=len, reverse=True)) + r")\b"
    )
    return pattern.sub(lambda match: symbols[match.group(0)], source)


def apply_import_rewrites(source: str) -> str:
    source = source.replace(
        'kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"',
        'nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"',
    )
    source = source.replace(
        '"github.com/Wei-Shaw/sub2api/internal/pkg/kiro"',
        'nianzskiro "github.com/Wei-Shaw/sub2api/internal/pkg/kiro_nianzs"',
    )
    source = source.replace("kiropkg.", "nianzskiro.")
    source = source.replace(
        '"github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown"',
        'nianzscooldown "github.com/Wei-Shaw/sub2api/internal/pkg/kirocooldown_nianzs"',
    )
    return source.replace("kirocooldown.", "nianzscooldown.")


def apply_integration_adapters(stem: str, source: str) -> str:
    """Apply only the integration seams required to keep both engines isolated."""

    if stem == "gateway_scheduling":
        source = require_replace(
            source,
            "kiroCooldownRecoveryAttemptedKey",
            "nianzsKiroCooldownRecoveryAttemptedKey",
            3,
            stem,
        )
        source = require_replace(
            source,
            "s.kiroCooldownStore",
            "s.nianzsKiroCooldownStore",
            3,
            stem,
        )
        upstream_body = """func (s *GatewayService) isAccountBlockedBySchedulingThresholdNianzs(ctx context.Context, account *Account) bool {
\tif s == nil || s.rateLimitService == nil || account == nil {
\t\treturn false
\t}
\treturn s.rateLimitService.ApplyAccountSchedulingThreshold(ctx, account)
}"""
        baseline_body = """func (s *GatewayService) isAccountBlockedBySchedulingThresholdNianzs(ctx context.Context, account *Account) bool {
\t// The production baseline does not expose nianzs' later admin-configurable
\t// scheduling-threshold setting. Its observable default is therefore always
\t// fail-open, which is exactly what ApplyAccountSchedulingThreshold returns
\t// when no threshold setting exists.
\treturn false
}"""
        source = require_replace(source, upstream_body, baseline_body, 1, stem)

    if stem == "kiro_runtime":
        source = require_replace(
            source,
            "s.GetAccessToken(ctx, account)",
            "s.getNianzsKiroAccessToken(ctx, account)",
            2,
            stem,
        )
        source = require_replace(
            source,
            "s.kiroTokenProvider",
            "s.nianzsKiroTokenProvider",
            2,
            stem,
        )
        upstream_error_block = '''\t\tif streamErr != nil {
\t\t\t_, _ = io.WriteString(pw, "event: error\\ndata: {\\"type\\":\\"error\\",\\"error\\":{\\"type\\":\\"api_error\\",\\"message\\":\\"stream interrupted\\"}}\\n\\n")
\t\t\t_ = pw.CloseWithError(streamErr)
\t\t\treturn
\t\t}'''
        production_error_block = '''\t\tif streamErr != nil {
\t\t\t// Do not inject an upstream SSE error frame here. The production
\t\t\t// downstream handler intentionally parses provider error events as
\t\t\t// internal errors and would suppress that frame. Propagating the pipe
\t\t\t// error lets it preserve the correct boundary: fail over before any
\t\t\t// client write, or emit exactly one client-visible error after output.
\t\t\t_ = pw.CloseWithError(streamErr)
\t\t\treturn
\t\t}'''
        source = require_replace(
            source,
            upstream_error_block,
            production_error_block,
            1,
            stem,
        )

    if stem == "kiro_runtime_state":
        source = require_replace(
            source,
            "s.kiroCooldownStore",
            "s.nianzsKiroCooldownStore",
            10,
            stem,
        )

    if stem == "kiro_usage_fetcher":
        source = require_replace(
            source,
            "s.kiroTokenProvider",
            "s.nianzsKiroTokenProvider",
            4,
            stem,
        )
        source = require_replace(
            source,
            "s.kiroCooldownStore",
            "s.nianzsKiroCooldownStore",
            4,
            stem,
        )

    if stem == "kiro_websearch":
        source = require_replace(
            source,
            "s.kiroTokenProvider",
            "s.nianzsKiroTokenProvider",
            2,
            stem,
        )

    return source


def apply_service_test_rewrites(source: str) -> str:
    source = rename_identifiers(source, SERVICE_TEST_SYMBOLS)
    return re.sub(r"\bTest(Kiro|ScaleKiro)", r"TestNianzs\1", source)


def verify_upstream_manifest(upstream: Path, manifest: dict) -> None:
    failures: list[str] = []
    for relative, expected_hash in manifest["sha256"].items():
        path = upstream / relative
        if not path.is_file():
            failures.append(f"missing {relative}")
            continue
        actual_hash = sha256(path)
        if actual_hash != expected_hash:
            failures.append(f"hash mismatch {relative}: {actual_hash}")
    if failures:
        raise ParityError("upstream is not the pinned snapshot:\n  " + "\n  ".join(failures))

    git_dir = upstream / ".git"
    if git_dir.exists():
        process = subprocess.run(
            ["git", "-C", str(upstream), "rev-parse", "HEAD"],
            text=True,
            capture_output=True,
            check=False,
        )
        actual_commit = process.stdout.strip()
        if process.returncode != 0 or actual_commit != manifest["commit"]:
            raise ParityError(
                f"upstream git HEAD mismatch: expected {manifest['commit']}, got {actual_commit or process.stderr.strip()}"
            )


def verify_exact_package(upstream: Path, upstream_relative: str, local_relative: str) -> int:
    upstream_dir = upstream / upstream_relative
    local_dir = REPO_ROOT / local_relative
    upstream_names = sorted(path.name for path in upstream_dir.glob("*.go"))
    local_names = sorted(path.name for path in local_dir.glob("*.go"))
    if upstream_names != local_names:
        raise ParityError(
            f"package file set mismatch for {local_relative}: expected {upstream_names}, got {local_names}"
        )
    for name in upstream_names:
        expected = (upstream_dir / name).read_bytes()
        actual = (local_dir / name).read_bytes()
        if expected != actual:
            raise ParityError(f"byte drift: {local_relative}/{name}")
    return len(upstream_names)


def verify_service_sources(upstream: Path, symbols: dict[str, str]) -> int:
    for stem in SERVICE_STEMS:
        upstream_path = upstream / "backend/internal/service" / f"{stem}.go"
        local_path = REPO_ROOT / "backend/internal/service" / f"{stem}_nianzs.go"
        expected = upstream_path.read_text()
        expected = rename_identifiers(expected, symbols)
        expected = apply_import_rewrites(expected)
        expected = apply_integration_adapters(stem, expected)
        expected = gofmt(strip_provenance(expected), f"generated {stem}")
        actual = gofmt(strip_provenance(local_path.read_text()), f"local {stem}")
        if expected != actual:
            diff = unified_diff(
                expected,
                actual,
                f"pinned/{stem}.go (mechanically namespaced)",
                str(local_path),
            )
            raise ParityError(f"service logic drift in {stem}:\n{diff}")
    return len(SERVICE_STEMS)


def verify_service_tests(upstream: Path, symbols: dict[str, str]) -> int:
    for upstream_name, local_name in SERVICE_TEST_FILES:
        upstream_path = upstream / "backend/internal/service" / upstream_name
        local_path = REPO_ROOT / "backend/internal/service" / local_name
        expected = rename_identifiers(upstream_path.read_text(), symbols)
        expected = apply_import_rewrites(expected)
        expected = apply_service_test_rewrites(expected)
        expected = gofmt(strip_provenance(expected), f"generated {upstream_name}")
        actual = gofmt(strip_provenance(local_path.read_text()), f"local {local_name}")
        if expected != actual:
            diff = unified_diff(
                expected,
                actual,
                f"pinned/{upstream_name} (mechanically namespaced)",
                str(local_path),
            )
            raise ParityError(f"service test drift in {upstream_name}:\n{diff}")
    return len(SERVICE_TEST_FILES)


def verify_account_test_source(upstream: Path, symbols: dict[str, str]) -> int:
    """Verify the three Kiro account-test functions copied from a mixed file."""

    upstream_path = upstream / "backend/internal/service/account_test_service.go"
    local_path = REPO_ROOT / "backend/internal/service/account_test_kiro_nianzs.go"
    upstream_source = upstream_path.read_text()
    upstream_start_marker = "func (s *AccountTestService) testKiroAccountConnection("
    upstream_end_marker = "// testBedrockAccountConnection"
    start = upstream_source.find(upstream_start_marker)
    end = upstream_source.find(upstream_end_marker, start)
    if start < 0 or end < 0:
        raise ParityError("account_test_service.go: Kiro function region changed")

    expected = upstream_source[start:end].rstrip() + "\n"
    expected = rename_identifiers(expected, symbols)
    expected = apply_import_rewrites(expected)
    expected = require_replace(
        expected,
        "s.kiroTokenProvider",
        "s.nianzsKiroTokenProvider",
        4,
        "account_test_service",
    )
    expected = gofmt("package service\n\n" + expected, "generated account test")

    local_source = local_path.read_text()
    local_start_marker = "func (s *AccountTestService) testKiroAccountConnectionNianzs("
    local_start = local_source.find(local_start_marker)
    if local_start < 0:
        raise ParityError(f"{local_path}: Kiro account-test region missing")
    actual = gofmt(
        "package service\n\n" + local_source[local_start:].strip() + "\n",
        "local account test",
    )
    if expected != actual:
        diff = unified_diff(
            expected,
            actual,
            "pinned/account_test_service.go (Kiro region, mechanically namespaced)",
            str(local_path),
        )
        raise ParityError("account-test source drift:\n" + diff)
    return 3


def verify_exact_integration_files(upstream: Path) -> int:
    for upstream_relative, local_relative in EXACT_FILE_PAIRS:
        expected = (upstream / upstream_relative).read_bytes()
        actual = (REPO_ROOT / local_relative).read_bytes()
        if expected != actual:
            raise ParityError(f"exact integration file drift: {local_relative}")
    return len(EXACT_FILE_PAIRS)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--upstream",
        type=Path,
        required=True,
        help="path to nianzs/sub2api source at the pinned commit",
    )
    args = parser.parse_args()

    manifest = json.loads(MANIFEST_PATH.read_text())
    upstream = args.upstream.expanduser().resolve()
    verify_upstream_manifest(upstream, manifest)
    kiro_files = verify_exact_package(
        upstream, "backend/internal/pkg/kiro", "backend/internal/pkg/kiro_nianzs"
    )
    cooldown_files = verify_exact_package(
        upstream,
        "backend/internal/pkg/kirocooldown",
        "backend/internal/pkg/kirocooldown_nianzs",
    )
    symbols = load_symbol_map()
    service_files = verify_service_sources(upstream, symbols)
    service_test_files = verify_service_tests(upstream, symbols)
    account_test_functions = verify_account_test_source(upstream, symbols)
    exact_integration_files = verify_exact_integration_files(upstream)

    print(f"PASS pinned source: {manifest['repository']}@{manifest['commit']}")
    print(f"PASS exact kiro package: {kiro_files} Go files")
    print(f"PASS exact kirocooldown package: {cooldown_files} Go files")
    print(f"PASS mechanically namespaced service logic: {service_files} Go files")
    print(f"PASS mechanically namespaced service tests: {service_test_files} Go files")
    print(f"PASS mechanically namespaced account-test logic: {account_test_functions} functions")
    print(f"PASS exact migration/frontend fixtures: {exact_integration_files} files")
    print("PASS only documented dual-engine integration adapters differ")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except ParityError as error:
        print(f"FAIL: {error}", file=sys.stderr)
        raise SystemExit(1)
