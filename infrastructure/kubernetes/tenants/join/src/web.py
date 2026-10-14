"""What both listeners share: security headers, responses, static files, JSON bodies."""
import json
import logging
import mimetypes
import os
import re
from http.server import BaseHTTPRequestHandler

import config

log = logging.getLogger("web")

_STATIC_NAME = re.compile(r"^[a-z0-9-]+\.(html|js|css|svg)$")

SECURITY_HEADERS = {
    "Content-Security-Policy": (
        "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; "
        "connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"),
    "Referrer-Policy": "no-referrer",
    "X-Content-Type-Options": "nosniff",
    "X-Frame-Options": "DENY",
    "Permissions-Policy": "camera=(), microphone=(), geolocation=()",
}


class BaseHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "join"
    sys_version = ""

    def log_message(self, fmt, *args):
        if not self.path.startswith("/healthz"):
            log.info(fmt, *args)

    def source(self) -> str:
        # Traefik appends the visitor's address last; anything before it came
        # from the visitor and proves nothing.
        forwarded = self.headers.get("X-Forwarded-For", "")
        return forwarded.split(",")[-1].strip() if forwarded else self.client_address[0]

    def send(self, status: int, body: bytes, content_type: str, cache: str = "no-store",
             headers: list[tuple[str, str]] | None = None):
        self.send_response(status)
        for name, value in SECURITY_HEADERS.items():
            self.send_header(name, value)
        for name, value in headers or []:
            self.send_header(name, value)
        self.send_header("Cache-Control", cache)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def send_json(self, status: int, payload, headers=None):
        self.send(status, json.dumps(payload).encode(), "application/json; charset=utf-8",
                  headers=headers)

    def redirect(self, location: str, headers=None):
        self.send(302, b"", "text/plain", headers=[("Location", location), *(headers or [])])

    def send_file(self, name: str, cache: str):
        path = os.path.join(config.STATIC_DIR, name)
        if not _STATIC_NAME.match(name) or not os.path.isfile(path):
            self.send_json(404, {"error": "not_found"})
            return
        with open(path, "rb") as handle:
            body = handle.read()
        content_type = mimetypes.guess_type(name)[0] or "application/octet-stream"
        if content_type.startswith("text/") or content_type.endswith("javascript"):
            content_type += "; charset=utf-8"
        self.send(200, body, content_type, cache)

    def read_json(self, max_body: int = 4096) -> tuple[dict | None, tuple[int, dict] | None]:
        """The request's JSON object, or the response refusing it."""
        # JSON only: a cross-site form cannot send it without a preflight.
        if not self.headers.get("Content-Type", "").startswith("application/json"):
            return None, (415, {"error": "json_only"})
        try:
            length = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            length = -1
        if not 0 < length <= max_body:
            return None, (400, {"error": "bad_request"})
        try:
            body = json.loads(self.rfile.read(length))
        except ValueError:
            body = None
        if not isinstance(body, dict):
            return None, (400, {"error": "bad_request"})
        return body, None
