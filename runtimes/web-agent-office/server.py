"""Private, bounded renderer HTTP API. Never mount the gateway data directory here."""
import base64
import hashlib
import json
import os
import secrets
import subprocess
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

from renderers import render
from schema import InvalidSpec, MAX_REQUEST_BYTES


class Handler(BaseHTTPRequestHandler):
    def setup(self):
        super().setup()
        self.connection.settimeout(120)

    def log_message(self, *_args):
        pass  # Do not log prompts, request headers or document contents.

    def respond(self, status, value):
        body = json.dumps(value, ensure_ascii=False, allow_nan=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if not secrets.compare_digest(self.headers.get("Authorization", "").encode(), ("Bearer " + self.server.token).encode()):
            self.respond(401, {"code": "unauthorized"})
            return
        if self.path != "/health":
            self.respond(404, {"code": "not_found"})
            return
        self.respond(200, {"status": "ok", "protocol_version": 1, "kinds": ["slides", "spreadsheet", "document"]})

    def do_POST(self):
        expected = "Bearer " + self.server.token
        if not secrets.compare_digest(self.headers.get("Authorization", "").encode(), expected.encode()):
            self.respond(401, {"code": "unauthorized"})
            return
        if self.path != "/render":
            self.respond(404, {"code": "not_found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            length = 0
        if not 0 < length <= MAX_REQUEST_BYTES or self.headers.get("Transfer-Encoding"):
            self.respond(413, {"code": "invalid_body_size"})
            return
        try:
            raw = self.rfile.read(length)
            if len(raw) != length:
                raise InvalidSpec("Incomplete request")
            spec = json.loads(raw, parse_constant=lambda _v: (_ for _ in ()).throw(InvalidSpec("Non-finite JSON value")))
            result = render(spec)
            self.respond(200, {
                "protocol_version": 1,
                "extension": result["extension"], "mime": result["mime"],
                "file_base64": base64.b64encode(result["file"]).decode(),
                "file_sha256": hashlib.sha256(result["file"]).hexdigest(),
                "size_bytes": len(result["file"]),
                "preview_pdf_base64": base64.b64encode(result["preview"]).decode(),
            })
        except (InvalidSpec, ValueError, TypeError, KeyError) as error:
            message = str(error) if isinstance(error, InvalidSpec) else "Invalid artifact specification"
            self.respond(422, {"code": "invalid_spec", "message": message})
        except (BrokenPipeError, ConnectionResetError):
            return
        except Exception:
            self.respond(500, {"code": "render_failed", "message": "Artifact rendering failed"})


def main():
    token = os.environ.get("WEB_AGENT_RENDERER_TOKEN", "")
    if len(token) < 32:
        raise SystemExit("WEB_AGENT_RENDERER_TOKEN must contain at least 32 characters")
    binary = os.environ.get("OFFICE_BINARY", "/usr/bin/libreoffice")
    if not Path(binary).is_file() or not os.access(binary, os.X_OK):
        raise SystemExit("A working OFFICE_BINARY is required")
    # A PDF with empty CJK glyphs is not a successful render. Fail startup
    # instead of advertising a broken worker when its required font is missing.
    try:
        font = subprocess.run(["fc-match", "-f", "%{family}", "Noto Sans CJK SC"],
                              capture_output=True, text=True, timeout=5, check=True).stdout
    except (OSError, subprocess.SubprocessError):
        raise SystemExit("Font validation is unavailable")
    if "Noto Sans CJK SC" not in font:
        raise SystemExit("Noto Sans CJK SC must be installed")
    # Single rendering slot; bound container memory/CPU and use short gateway
    # connection timeouts. Each Office child has its own 90s deadline/profile.
    server = HTTPServer((os.environ.get("HOST", "127.0.0.1"), int(os.environ.get("PORT", "58082"))), Handler)
    server.token = token
    server.serve_forever()


if __name__ == "__main__":
    main()
