"""Operator Board (docs/adr/0068): one read-only page that says whether the
cluster is well, shows why not, and links to every operator tool.

A collector thread reads every source once per REFRESH_SECONDS and builds one
state document (state.py); the HTTP server only hands out the last one. No
source can stop the refresh: each is read on its own, with its own timeout,
and a failure is recorded as that source not answering.
"""
import logging
import threading
import time
import urllib.error
from http.server import ThreadingHTTPServer

import config
import state
import web
from kube import Kube

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("main")


def describe(error: Exception) -> str:
    """A short, safe description of why a source did not answer."""
    if isinstance(error, urllib.error.HTTPError):
        return f"HTTP {error.code} {error.reason}"
    if isinstance(error, urllib.error.URLError):
        return f"unreachable ({error.reason})"
    if isinstance(error, TimeoutError):
        return f"no answer within {config.SOURCE_TIMEOUT} s"
    return f"{type(error).__name__}: {str(error)[:200]}"


class Collector:
    def __init__(self, kube, clock=time.time):
        self.kube = kube
        self.clock = clock
        self.readings = {"applications": state.Reading(), "ingresses": state.Reading()}
        self._document = None
        self._cycled_at = None
        self._lock = threading.Lock()

    def _read(self, key: str, path: str) -> None:
        reading = self.readings[key]
        now = self.clock()
        try:
            reading.data = self.kube.get(path)
            reading.error = None
            reading.last_good_at = now
        except Exception as error:  # noqa: BLE001 — every failure is "did not answer"
            reading.error = describe(error)
            log.warning("%s: %s", key, reading.error)
        reading.fetched_at = now

    def cycle(self) -> None:
        self._read("applications",
                   f"/apis/argoproj.io/v1alpha1/namespaces/{config.ARGOCD_NAMESPACE}/applications")
        self._read("ingresses", "/apis/networking.k8s.io/v1/ingresses")
        now = self.clock()
        document = state.build(
            self.readings["applications"], self.readings["ingresses"], now,
            base_repo_url=config.BASE_REPO_URL, root_app=config.ROOT_APP,
            ring0_board_url=config.RING0_BOARD_URL, refresh_seconds=config.REFRESH_SECONDS,
            slow_minutes=config.SLOW_OPERATION_MINUTES)
        with self._lock:
            self._document = document
            self._cycled_at = now

    def document(self) -> dict | None:
        with self._lock:
            return self._document

    def healthy(self) -> bool:
        """Ready once a document exists; unhealthy when the refresh has stopped."""
        with self._lock:
            cycled = self._cycled_at
        return cycled is not None and self.clock() - cycled < 3 * config.REFRESH_SECONDS + 30

    def run(self) -> None:
        while True:
            try:
                self.cycle()
            except Exception:  # noqa: BLE001 — a bug in one cycle must not end the Board
                log.exception("refresh failed")
            time.sleep(config.REFRESH_SECONDS)


def main() -> None:
    collector = Collector(Kube(timeout=config.SOURCE_TIMEOUT))
    threading.Thread(target=collector.run, name="collector", daemon=True).start()
    server = ThreadingHTTPServer((config.BIND, config.PORT), web.make_handler(collector))
    log.info("Operator Board on %s:%d, refreshing every %d s", config.BIND, config.PORT,
             config.REFRESH_SECONDS)
    server.serve_forever()


if __name__ == "__main__":
    main()
