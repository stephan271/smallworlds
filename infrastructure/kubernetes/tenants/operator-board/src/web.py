"""HTTP: the page, its state document, and a health check. GET and HEAD only:
the Board changes nothing (docs/adr/0068), so every other method is refused."""
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
        "connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"),
    "Referrer-Policy": "no-referrer",
    "X-Content-Type-Options": "nosniff",
    "X-Frame-Options": "DENY",
    "Permissions-Policy": "camera=(), microphone=(), geolocation=()",
}


def make_handler(collector):
    class Handler(BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"
        server_version = "operator-board"
        sys_version = ""

        def log_message(self, fmt, *args):
            if not self.path.startswith("/healthz"):
                log.info(fmt, *args)

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

        def send_json(self, status: int, payload):
            self.send(status, json.dumps(payload).encode(), "application/json; charset=utf-8")

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

        def do_GET(self):
            path = self.path.split("?", 1)[0]
            if path == "/healthz":
                healthy = collector.healthy()
                self.send(200 if healthy else 503, b"OK" if healthy else b"not refreshing",
                          "text/plain")
            elif path == "/":
                self.send_file("board.html", "no-store")
            elif path.startswith("/static/"):
                self.send_file(path[len("/static/"):], "public, max-age=300")
            elif path == "/api/state":
                document = collector.document()
                if document is None:
                    self.send_json(503, {"error": "starting"})
                else:
                    self.send_json(200, document)
            else:
                self.send_json(404, {"error": "not_found"})

        do_HEAD = do_GET

        def _refuse(self):
            # The body is never read, so the connection cannot carry another request.
            self.close_connection = True
            self.send(405, b"", "text/plain", headers=[("Allow", "GET, HEAD"), ("Connection", "close")])

        do_POST = do_PUT = do_PATCH = do_DELETE = do_OPTIONS = _refuse

    return Handler
