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
import urllib.request

log = logging.getLogger("checks")

ACME_SOLVER_LABEL = "acme.cert-manager.io/http01-solver"


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
