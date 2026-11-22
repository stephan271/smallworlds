"""Alertmanager's alerts (design §4.1): the only source that can make the Board red.

Read through Alertmanager's own API, like the Canary Follower's quiet check
(tenants/canary-follower/src/checks.py), with silenced and inhibited alerts
included so the page can show them greyed instead of hiding them.
"""
import json
import urllib.request


def fetch_receivers(base_url: str, timeout: int) -> list[str]:
    """The receivers Alertmanager loaded, by name. The API returns names and
    labels only, never a URL: the ntfy topic and the heartbeat's ping URL are
    credentials (doc/alerting.md) and the Board never sees them."""
    request = urllib.request.Request(f"{base_url.rstrip('/')}/api/v2/receivers",
                                     headers={"Accept": "application/json"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        answer = json.load(response)
    if not isinstance(answer, list):
        raise ValueError("Alertmanager answered with something that is not a list of receivers")
    return sorted(r.get("name", "") for r in answer if isinstance(r, dict))


def fetch(base_url: str, timeout: int) -> list[dict]:
    url = f"{base_url.rstrip('/')}/api/v2/alerts?active=true&silenced=true&inhibited=true"
    request = urllib.request.Request(url, headers={"Accept": "application/json"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        answer = json.load(response)
    if not isinstance(answer, list):
        raise ValueError("Alertmanager answered with something that is not a list of alerts")
    return answer
