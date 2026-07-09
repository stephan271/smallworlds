"""Thin Prometheus HTTP query client (instant queries only).

Uses urllib so the container needs no third-party HTTP dependency. Two jobs:
confirm an OOMKilled termination deterministically, and read the P95 working-set
that drives the new memory limit.
"""
import json
import logging
import urllib.parse
import urllib.request

import config

log = logging.getLogger("prometheus")


def _query(expr: str):
    """Run an instant query; return the raw `data.result` list (may be empty)."""
    url = f"{config.PROMETHEUS_URL}/api/v1/query?" + urllib.parse.urlencode({"query": expr})
    with urllib.request.urlopen(url, timeout=15) as resp:
        payload = json.load(resp)
    if payload.get("status") != "success":
        raise RuntimeError(f"prometheus query failed: {payload.get('error')}")
    return payload["data"]["result"]


def confirm_oomkilled(namespace: str, pod: str, container: str) -> bool:
    """True if kube-state-metrics reports this container's last termination as
    OOMKilled. This is what makes the OOMKill handler deterministic: KubePod-
    CrashLooping alone does not distinguish OOM from other crash causes.
    """
    expr = (
        'kube_pod_container_status_last_terminated_reason'
        f'{{namespace="{namespace}",pod="{pod}",container="{container}",reason="OOMKilled"}} == 1'
    )
    try:
        return len(_query(expr)) > 0
    except Exception:
        log.exception("OOMKilled confirmation query failed")
        return False


def current_memory_limit_bytes(namespace: str, container: str, pod_regex: str) -> float | None:
    """The memory limit currently in effect for the container, read from
    kube-state-metrics. Returns bytes, or None if no limit is set / unknown.

    pod_regex is anchored (Prometheus =~ matches the whole string), so it must
    match this workload's pods exactly and not a sibling that shares a prefix."""
    expr = (
        "max(kube_pod_container_resource_limits"
        f'{{namespace="{namespace}",container="{container}",resource="memory",pod=~"{pod_regex}"}})'
    )
    try:
        result = _query(expr)
    except Exception:
        log.exception("current-limit query failed")
        return None
    if not result:
        return None
    return float(result[0]["value"][1])


def p95_working_set_bytes(namespace: str, container: str, pod_regex: str) -> float | None:
    """P95 of container_memory_working_set_bytes over config.P95_WINDOW, across
    the workload's pods (matched by the anchored pod_regex), max over pods.

    Returns bytes, or None if there is no data (e.g. brand-new workload).
    """
    expr = (
        "max(quantile_over_time(0.95, "
        "container_memory_working_set_bytes"
        f'{{namespace="{namespace}",container="{container}",pod=~"{pod_regex}"}}'
        f"[{config.P95_WINDOW}]))"
    )
    try:
        result = _query(expr)
    except Exception:
        log.exception("P95 query failed")
        return None
    if not result:
        return None
    return float(result[0]["value"][1])
