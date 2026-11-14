"""Verification after convergence: smoke over every Ingress, then a quiet period.

Smoke asks each public host for its front page over HTTPS, from inside the
cluster, with certificate verification on. Anything below 500 passes: a 302 to
Keycloak, a 401 or the tailnet allow-list's 403 all prove TLS, routing and a
live backend. A 5xx or a TLS failure does not.
"""
import json
import logging
import time
import urllib.error
import urllib.parse
import urllib.request

log = logging.getLogger("checks")

ACME_SOLVER_LABEL = "acme.cert-manager.io/http01-solver"

# Critical alerts that say Alertmanager cannot hand alerts on (ntfy refusing a
# message, an unreachable receiver). They are about the notification path, so
# the follower reports them as no verdict rather than rejecting the commit.
DELIVERY_ALERTS = frozenset({"AlertmanagerClusterFailedToSendAlerts",
                             "AlertmanagerFailedToSendAlerts"})


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def _probe(url: str, timeout: int = 15) -> str | None:
    """None when the host answers below 500, else what went wrong."""
    opener = urllib.request.build_opener(_NoRedirect)
    try:
        with opener.open(url, timeout=timeout) as response:
            code = response.status
    except urllib.error.HTTPError as error:
        code = error.code
    except Exception as error:  # TLS, DNS, connection: all a failed smoke
        return f"{type(error).__name__}: {error}"[:120]
    return None if code < 500 else f"HTTP {code}"


def ingress_hosts(kube) -> list[str]:
    hosts = set()
    for ingress in kube.get("/apis/networking.k8s.io/v1/ingresses").get("items", []):
        if ACME_SOLVER_LABEL in ingress.get("metadata", {}).get("labels", {}):
            continue
        for rule in ingress.get("spec", {}).get("rules", []):
            if rule.get("host"):
                hosts.add(rule["host"])
    return sorted(hosts)


def smoke(hosts: list[str], attempts: int, retry_seconds: int,
          probe=_probe, sleep=time.sleep) -> list[str]:
    """Failed hosts, as 'host: reason'. Each host gets several tries, because
    a pod that just restarted may still be warming up."""
    failed = {}
    remaining = list(hosts)
    for attempt in range(attempts):
        failed = {}
        for host in remaining:
            problem = probe(f"https://{host}/")
            if problem:
                failed[host] = problem
        if not failed:
            return []
        remaining = list(failed)
        if attempt < attempts - 1:
            sleep(retry_seconds)
    return [f"{host}: {reason}" for host, reason in sorted(failed.items())]


# 1-minute load average per CPU, of the busiest node.
LOAD_PER_CPU = ('max(node_load1 / on(instance) '
                'count by (instance) (node_cpu_seconds_total{mode="idle"}))')


def _load_per_cpu(prometheus_url: str) -> float:
    url = f"{prometheus_url}/api/v1/query?" + urllib.parse.urlencode({"query": LOAD_PER_CPU})
    with urllib.request.urlopen(url, timeout=15) as response:
        result = json.load(response)["data"]["result"]
    return float(result[0]["value"][1])


def settle(prometheus_url: str, max_load_per_cpu: float, timeout: int, interval: int,
           fetch=_load_per_cpu, clock=time.monotonic, sleep=time.sleep) -> str | None:
    """Wait until the node has calmed down after a sync, before the checks run.
    Ring 0, 2026-11-13: syncing 25 Applications and a pre-update backup drove
    the load to 38 on 8 CPUs; every probe on the node timed out, and an Immich
    e2e test failed against a server that was merely slow. Settled means two
    readings in a row below the bound. Returns None when it settled at once,
    else a note for the status. A node that does not calm down, or a Prometheus
    that cannot be read, is no verdict on the commit: the checks run anyway."""
    start = clock()
    calm = 0
    load = None
    while True:
        try:
            load = fetch(prometheus_url)
        except Exception as error:
            return f"load unreadable ({type(error).__name__})"
        calm = calm + 1 if load < max_load_per_cpu else 0
        waited = clock() - start
        if calm >= 2:
            return None if waited <= interval else f"load settled after {round(waited / 60)} min"
        if waited >= timeout:
            return f"load still {load:.1f}/CPU after {round(waited / 60)} min"
        sleep(interval)


def _critical_alerts(alertmanager_url: str) -> list[str]:
    url = f"{alertmanager_url}/api/v2/alerts?active=true&silenced=false&inhibited=false"
    with urllib.request.urlopen(url, timeout=15) as response:
        alerts = json.load(response)
    return sorted({a["labels"].get("alertname", "?") for a in alerts
                   if a.get("labels", {}).get("severity") == "critical"})


def quiet(alertmanager_url: str, seconds: int, interval: int,
          fetch=_critical_alerts, clock=time.monotonic, sleep=time.sleep) -> list[str]:
    """Critical alerts seen during the quiet period; empty means it was quiet.
    An Alertmanager that cannot be read is not quiet: nothing was verified."""
    deadline = clock() + seconds
    while True:
        try:
            firing = fetch(alertmanager_url)
        except Exception as error:
            return [f"Alertmanager unreadable ({type(error).__name__})"]
        if firing:
            return firing
        if clock() >= deadline:
            return []
        sleep(min(interval, deadline - clock()))
