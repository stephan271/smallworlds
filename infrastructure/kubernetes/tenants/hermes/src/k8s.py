"""Minimal in-cluster Kubernetes read client (stdlib urllib).

Authenticates with the pod's mounted ServiceAccount token against the in-cluster
API. The `hermes-agent` SA is granted read-only (get/list/watch) on the handful
of resources these helpers touch, so there is no way for the agent to mutate the
cluster through this path even if it tried.
"""
import json
import ssl
import urllib.request

_API = "https://kubernetes.default.svc"
_TOKEN_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/token"
_CA_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"


def _get(path: str) -> dict:
    try:
        with open(_TOKEN_PATH) as f:
            token = f.read().strip()
        ctx = ssl.create_default_context(cafile=_CA_PATH)
        req = urllib.request.Request(
            f"{_API}{path}", headers={"Authorization": f"Bearer {token}"}
        )
        with urllib.request.urlopen(req, context=ctx, timeout=15) as resp:
            return json.load(resp)
    except Exception as e:
        return {"status": "error", "error": f"k8s request failed: {e}"}


def pod_status(namespace: str, pod: str) -> dict:
    """Phase + per-container state, restart counts, and last-termination reason
    (the OOMKilled/Error signal) for one pod."""
    raw = _get(f"/api/v1/namespaces/{namespace}/pods/{pod}")
    if raw.get("status") == "error":
        return raw
    st = raw.get("status", {})
    containers = []
    for cs in st.get("containerStatuses", []):
        last = cs.get("lastState", {}).get("terminated", {})
        containers.append({
            "name": cs.get("name"),
            "ready": cs.get("ready"),
            "restarts": cs.get("restartCount"),
            "state": list(cs.get("state", {}).keys()),
            "last_terminated_reason": last.get("reason"),
            "last_exit_code": last.get("exitCode"),
        })
    return {"phase": st.get("phase"), "containers": containers}


def recent_events(namespace: str, limit: int = 30) -> dict:
    """Recent Kubernetes events in a namespace (scheduling, image pulls, OOM,
    probe failures) — the fastest way to see why a pod is unhealthy. The core
    events API returns items unsorted, so we sort newest-first client-side and
    return the most recent `limit`."""
    raw = _get(f"/api/v1/namespaces/{namespace}/events")
    if raw.get("status") == "error":
        return raw

    def _ts(e: dict) -> str:
        # lastTimestamp is empty for events using the newer eventTime field.
        return (e.get("lastTimestamp") or e.get("eventTime")
                or e.get("metadata", {}).get("creationTimestamp") or "")

    items = sorted(raw.get("items", []), key=_ts, reverse=True)[:limit]
    events = [
        {
            "type": e.get("type"),
            "reason": e.get("reason"),
            "object": f"{e.get('involvedObject', {}).get('kind')}/{e.get('involvedObject', {}).get('name')}",
            "message": e.get("message"),
            "count": e.get("count"),
            "last": _ts(e),
        }
        for e in items
    ]
    return {"events": events}
