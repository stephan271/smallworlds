"""Tier 1 remediation service entrypoint.

A tiny stdlib HTTP server that receives Alertmanager webhook batches and fans
each alert out to the registry. No framework — one POST route and a health
check are all this needs.

Alertmanager posts the v4 webhook shape:
    {"status": ..., "alerts": [{"status","labels","annotations","fingerprint",...}, ...]}
"""
import json
import logging
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import config
import registry

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("main")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _respond(self, code: int, body: bytes = b""):
        self.send_response(code)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if body:
            self.wfile.write(body)

    def do_GET(self):
        if self.path == "/healthz":
            self._respond(200, b"OK")
        else:
            self._respond(404)

    def do_POST(self):
        # Always drain the request body first: with HTTP/1.1 keep-alive, leaving
        # it unread desyncs the next request on the same connection.
        length = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(length) if length > 0 else b""

        if self.path != "/webhook":
            self._respond(404)
            return
        try:
            payload = json.loads(raw or b"{}")
        except Exception:
            log.exception("bad webhook body")
            self._respond(400, b"bad request")
            return

        alerts = payload.get("alerts", [])
        log.info("received %d alert(s)", len(alerts))
        for alert in alerts:
            try:
                registry.dispatch(alert)
            except Exception:
                log.exception("dispatch failed for alert %s",
                              alert.get("labels", {}).get("alertname"))
        self._respond(200, b"accepted")

    def log_message(self, *args):  # silence default per-request stderr logging
        pass


def main():
    log.info("Tier 1 remediation starting on :%d (DRY_RUN=%s, overlay=%s)",
             config.LISTEN_PORT, config.DRY_RUN, config.OVERLAY_REPO)
    ThreadingHTTPServer(("0.0.0.0", config.LISTEN_PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
