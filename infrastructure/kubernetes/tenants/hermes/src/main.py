"""Hermes Tier 2 entrypoint — a stdlib HTTP server.

Receives escalations from the Tier 1 remediation service (and, later, directly
from Alertmanager) and hands each one to the agentic loop. One POST route and a
health check.

Tier 1 posts: {"source": "...", "reason": "...", "alert": {alertmanager-alert}}.
A bare Alertmanager webhook batch ({"alerts": [...]}) is also accepted.
"""
import json
import logging
import threading
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import agent
import config

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("main")

# A bounded pool so an alert storm queues instead of spawning one paid Opus loop
# per alert. `_inflight` dedups so the same incident isn't investigated twice
# concurrently; the lock guards it against ThreadingHTTPServer request threads.
_pool = ThreadPoolExecutor(max_workers=config.MAX_CONCURRENT)
_inflight: set[str] = set()
_lock = threading.Lock()


def _fingerprint(alert: dict) -> str:
    fp = alert.get("fingerprint")
    if fp:
        return fp
    labels = alert.get("labels", {})
    return "|".join(labels.get(k, "") for k in ("alertname", "namespace", "pod", "container"))


def _investigate(alert: dict, reason: str, fp: str) -> None:
    try:
        agent.run(alert, reason)
    except Exception:
        log.exception("investigation crashed")
    finally:
        with _lock:
            _inflight.discard(fp)


def _submit(alert: dict, reason: str) -> bool:
    """Queue an investigation unless the same fingerprint is already in flight."""
    fp = _fingerprint(alert)
    with _lock:
        if fp in _inflight:
            log.info("already investigating %s; skipping duplicate", fp)
            return False
        _inflight.add(fp)
    _pool.submit(_investigate, alert, reason, fp)
    return True


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _respond(self, code: int, body: bytes = b""):
        self.send_response(code)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if body:
            self.wfile.write(body)

    def do_GET(self):
        self._respond(200, b"OK") if self.path == "/healthz" else self._respond(404)

    def do_POST(self):
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

        # Normalize to (alert, reason) pairs, skipping resolved alerts (an
        # Alertmanager batch carries both firing and resolved).
        jobs = []
        if "alert" in payload:  # Tier 1 escalation
            a = payload["alert"]
            if a.get("status") != "resolved":
                jobs.append((a, payload.get("reason", "escalated")))
        for a in payload.get("alerts", []):  # raw Alertmanager batch
            if a.get("status") == "firing":
                jobs.append((a, "alertmanager"))

        queued = sum(_submit(alert, reason) for alert, reason in jobs)
        log.info("received %d actionable alert(s); queued %d", len(jobs), queued)
        self._respond(202, b"accepted")

    def log_message(self, *args):
        pass


def main():
    if not config.DRY_RUN and not config.ANTHROPIC_API_KEY:
        # Armed but no key: every investigation would 401. Fail loudly rather
        # than silently dropping escalations behind a healthy-looking pod.
        log.critical("DRY_RUN is off but ANTHROPIC_API_KEY is empty — investigations "
                     "will fail. Provision the hermes-anthropic Secret.")
    log.info("Hermes (Tier 2) starting on :%d (DRY_RUN=%s, model=%s, max_concurrent=%d)",
             config.LISTEN_PORT, config.DRY_RUN, config.MODEL, config.MAX_CONCURRENT)
    ThreadingHTTPServer(("0.0.0.0", config.LISTEN_PORT), Handler).serve_forever()


if __name__ == "__main__":
    main()
