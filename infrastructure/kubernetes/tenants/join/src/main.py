"""join — Member Invitations (docs/adr/0058). A stdlib HTTP server.

The public listener serves the join page and its three API calls. The
Invitation Desk will get a listener of its own on a port the public Ingress does
not route to (design §9), so `/desk` does not exist here at all.

The code never appears in a URL path: the page reads it from the fragment and
POSTs it, so it never reaches Traefik's access log, Loki or a Referer.
"""
import json
import logging
import mimetypes
import os
import re
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import config
import member
import store

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("main")

MAX_BODY = 4096
_STATIC_NAME = re.compile(r"^[a-z0-9-]+\.(html|js|css|svg)$")
_PAGES = {"/": "index.html", "/done/de": "done.html", "/done/en": "done.html"}

SECURITY_HEADERS = {
    "Content-Security-Policy": (
        "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; "
        "connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"),
    "Referrer-Policy": "no-referrer",
    "X-Content-Type-Options": "nosniff",
    "X-Frame-Options": "DENY",
    "Permissions-Policy": "camera=(), microphone=(), geolocation=()",
}


class PublicHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "join"
    sys_version = ""

    def log_message(self, fmt, *args):
        if not self.path.startswith("/healthz"):
            log.info(fmt, *args)

    def _source(self) -> str:
        # Traefik appends the visitor's address last; anything before it came
        # from the visitor and proves nothing.
        forwarded = self.headers.get("X-Forwarded-For", "")
        return forwarded.split(",")[-1].strip() if forwarded else self.client_address[0]

    def _send(self, status: int, body: bytes, content_type: str, cache: str = "no-store"):
        self.send_response(status)
        for name, value in SECURITY_HEADERS.items():
            self.send_header(name, value)
        self.send_header("Cache-Control", cache)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def _json(self, status: int, payload: dict):
        self._send(status, json.dumps(payload).encode(), "application/json; charset=utf-8")

    def _file(self, name: str, cache: str):
        path = os.path.join(config.STATIC_DIR, name)
        if not _STATIC_NAME.match(name) or not os.path.isfile(path):
            self._json(404, {"error": "not_found"})
            return
        with open(path, "rb") as handle:
            body = handle.read()
        content_type = mimetypes.guess_type(name)[0] or "application/octet-stream"
        if content_type.startswith("text/") or content_type.endswith("javascript"):
            content_type += "; charset=utf-8"
        self._send(200, body, content_type, cache)

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        if path == "/healthz":
            self._send(200, b"OK", "text/plain")
        elif path in _PAGES:
            self._file(_PAGES[path], "no-store")
        elif path.startswith("/static/"):
            self._file(path[len("/static/"):], "public, max-age=300")
        elif path == "/api/config":
            self._json(200, {"dashboardUrl": config.DASHBOARD_URL})
        else:
            self._json(404, {"error": "not_found"})

    do_HEAD = do_GET

    def do_POST(self):
        path = self.path.split("?", 1)[0]
        routes = {
            "/api/invitation": lambda body, source: member.lookup(body.get("code"), source),
            "/api/username": lambda body, source: member.check_username(
                body.get("code"), body.get("username"), source),
            "/api/redeem": lambda body, source: member.redeem(body.get("code"), body, source),
        }
        if path not in routes:
            self._json(404, {"error": "not_found"})
            return
        # JSON only: a cross-site form cannot send it without a preflight.
        if not self.headers.get("Content-Type", "").startswith("application/json"):
            self._json(415, {"error": "json_only"})
            return
        try:
            length = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            length = -1
        if not 0 < length <= MAX_BODY:
            self._json(400, {"error": "bad_request"})
            return
        try:
            body = json.loads(self.rfile.read(length))
        except ValueError:
            body = None
        if not isinstance(body, dict):
            self._json(400, {"error": "bad_request"})
            return
        try:
            status, payload = routes[path](body, self._source())
        except Exception:
            log.exception("%s failed", path)
            status, payload = 500, {"error": "unavailable"}
        self._json(status, payload)


def serve_public(port: int = config.PUBLIC_PORT) -> ThreadingHTTPServer:
    return ThreadingHTTPServer((config.BIND, port), PublicHandler)


def main():
    if not config.CLIENT_SECRET:
        log.warning("KEYCLOAK_CLIENT_SECRET is empty — every invitation will fail at Keycloak")
    store.init()
    server = serve_public()
    log.info("join page listening on %s:%s", config.BIND, server.server_port)
    server.serve_forever()


if __name__ == "__main__":
    main()
