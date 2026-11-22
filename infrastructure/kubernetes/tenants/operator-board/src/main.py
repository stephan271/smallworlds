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

import alerts
import config
import prom
import rings
from links import github_web
import state
import web
from kube import Kube

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("main")


def describe(error: Exception) -> str:
    """A short, safe description of why a source did not answer."""
    if isinstance(error, urllib.error.HTTPError):
        error.close()  # it holds the response open until closed
        return f"HTTP {error.code} {error.reason}"
    if isinstance(error, urllib.error.URLError):
        return f"unreachable ({error.reason})"
    if isinstance(error, TimeoutError):
        return f"no answer within {config.SOURCE_TIMEOUT} s"
    return f"{type(error).__name__}: {str(error)[:200]}"


class Collector:
    def __init__(self, kube, clock=time.time, fetch_alerts=alerts.fetch, fetch_metrics=prom.fetch,
                 fetch_ring0=rings.fetch_ring0, compare=rings.compare):
        self.kube = kube
        self.fetch_alerts = fetch_alerts
        self.fetch_metrics = fetch_metrics
        self.fetch_ring0 = fetch_ring0
        self.compare = compare
        # (running, verified) -> (when, result or None, error or None): one call
        # per pair per COMPARE_SECONDS, within GitHub's 60 an hour.
        self._compares: dict[tuple[str, str], tuple[float, dict | None, str | None]] = {}
        self.clock = clock
        self.readings = {key: state.Reading() for key in state.SOURCE_NAMES}
        self._document = None
        self._cycled_at = None
        self._lock = threading.Lock()

    def _read(self, key: str, fetch) -> None:
        reading = self.readings[key]
        now = self.clock()
        try:
            reading.data = fetch()
            reading.error = None
            reading.last_good_at = now
        except Exception as error:  # noqa: BLE001 — every failure is "did not answer"
            reading.error = describe(error)
            log.warning("%s: %s", key, reading.error)
        reading.fetched_at = now

    def cycle(self) -> None:
        self._read("alerts", lambda: self.fetch_alerts(config.ALERTMANAGER_URL, config.SOURCE_TIMEOUT))
        self._read("applications", lambda: self.kube.get(
            f"/apis/argoproj.io/v1alpha1/namespaces/{config.ARGOCD_NAMESPACE}/applications"))
        self._read("ingresses", lambda: self.kube.get("/apis/networking.k8s.io/v1/ingresses"))
        self._read("nodes", lambda: self.kube.get("/api/v1/nodes"))
        self._read("prometheus", lambda: self.fetch_metrics(config.PROMETHEUS_URL, config.SOURCE_TIMEOUT))
        self._read("backups", self._backups)
        self._read("rings", self._rings)
        now = self.clock()
        document = state.build(
            self.readings, now,
            base_repo_url=config.BASE_REPO_URL, root_app=config.ROOT_APP,
            ring0_board_url=config.RING0_BOARD_URL, refresh_seconds=config.REFRESH_SECONDS,
            slow_minutes=config.SLOW_OPERATION_MINUTES, stale_hours=config.BACKUP_STALE_HOURS)
        with self._lock:
            self._document = document
            self._cycled_at = now

    def _backups(self) -> dict:
        """Everything the Backups tile reads. A kind whose CRD is not installed
        is an empty list (Kube.get_or_empty), never a failure."""
        paths = {"clusters": "/apis/postgresql.cnpg.io/v1/clusters",
                 "objectstores": "/apis/barmancloud.cnpg.io/v1/objectstores",
                 "schedules": "/apis/velero.io/v1/schedules",
                 "velero_backups": "/apis/velero.io/v1/backups",
                 "cronjobs": "/apis/batch/v1/cronjobs"}
        return {key: self.kube.get_or_empty(path).get("items", []) for key, path in paths.items()}

    def _rings(self) -> dict:
        """ring0.json, and a compare when this cluster's commit is older than
        the 25 it lists. A failing compare is noted, not a failed reading:
        the tile still knows the commit is not among the recent ones."""
        if not config.RING_STATE_URL:
            return {"disabled": True}
        ring0 = self.fetch_ring0(config.RING_STATE_URL, config.SOURCE_TIMEOUT)
        result = {"ring0": ring0, "compare": None, "compare_error": None}
        apps = self.readings["applications"].data
        running, _ = state.base_revision((apps or {}).get("items", []) if isinstance(apps, dict) else [],
                                         config.BASE_REPO_URL)
        newest = state.newest_verified(ring0)
        listed = {c.get("commit") for c in ring0.get("commits", [])}
        if not running or not newest or running in listed or running == newest["commit"]:
            return result
        repository = ring0.get("repository") or (github_web(config.BASE_REPO_URL) or "").removeprefix(
            "https://github.com/")
        key = (running, newest["commit"])
        cached = self._compares.get(key)
        if cached is None or self.clock() - cached[0] >= config.COMPARE_SECONDS:
            try:
                cached = (self.clock(), self.compare(config.GITHUB_API_URL, repository, running,
                                                     newest["commit"], config.SOURCE_TIMEOUT), None)
            except Exception as error:  # noqa: BLE001 — rate limit, network: noted, retried later
                cached = (self.clock(), None, describe(error))
            self._compares = {key: cached}  # only the current pair is worth keeping
        result["compare"], result["compare_error"] = cached[1], cached[2]
        return result

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
