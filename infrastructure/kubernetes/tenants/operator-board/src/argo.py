"""How Argo CD's fields read as words: a copy of the Canary Follower's rules.

Kustomize cannot load a file from another tenant's folder, so the functions
below are copied from tenants/canary-follower/src/argocd.py unchanged, and
tests/test_board.py fails when the two differ (design §4.2). Change them there
first, then copy them here.
"""
import re


def _sync_status(app: dict) -> str:
    return app.get("status", {}).get("sync", {}).get("status", "Unknown")


def _health(app: dict) -> str:
    return app.get("status", {}).get("health", {}).get("status", "Unknown")


def _operation_running(app: dict) -> bool:
    if app.get("operation"):
        return True
    return app.get("status", {}).get("operationState", {}).get("phase") == "Running"


def _sync_failure(app: dict) -> str | None:
    """Why the last sync failed, or None. A failed sync hook (an init Job at
    its backoff limit) leaves the app Synced and Healthy: only the operation
    says so (Stalwart's stalwart-init on Ring 0, 2026-11-10)."""
    state = app.get("status", {}).get("operationState", {})
    if state.get("phase") not in ("Failed", "Error"):
        return None
    failed = [f"{r.get('kind')} {r.get('name')}: {r.get('message', '')}".rstrip(": ")
              for r in state.get("syncResult", {}).get("resources", [])
              if r.get("hookPhase") in ("Failed", "Error") or r.get("status") == "SyncFailed"]
    return f"sync {state['phase']}: " + ("; ".join(failed) or state.get("message", ""))


def _errored(app: dict) -> bool:
    """The last operation ended in Error, not Failed: Argo CD could not carry it
    out (on Ring 0: "ComparisonError: … failed to get resource health for
    CustomResourceDefinition …" while the API server was busy), as opposed to a
    sync whose resources or hooks failed. Nothing re-syncs such an app by itself."""
    return (not _operation_running(app)
            and app.get("status", {}).get("operationState", {}).get("phase") == "Error")


def _degraded_while_rolling(app: dict) -> bool:
    """The last sync ended Failed only because a plain resource, not a hook, read
    Degraded while the sync waited on it, and the app is Synced and Healthy now.
    Argo CD's built-in health check calls a CNPG cluster in "Switchover in
    progress" Degraded, so every Postgres minor update failed its sync although
    CNPG finished a moment later (immich on Ring 0, 2026-11-12). Such a sync
    stopped at that wave, so later waves and PostSync hooks may not have run:
    the answer is to sync again, not to look away."""
    state = app.get("status", {}).get("operationState", {})
    if _operation_running(app) or state.get("phase") != "Failed":
        return False
    if _sync_status(app) != "Synced" or _health(app) != "Healthy":
        return False
    failed = [r for r in state.get("syncResult", {}).get("resources", [])
              if r.get("hookPhase") in ("Failed", "Error") or r.get("status") == "SyncFailed"]
    return bool(failed) and all(not r.get("hookType") and r.get("status") != "SyncFailed"
                                for r in failed)


# Argo CD could not render the target because an upstream registry or chart
# repository did not answer (Ring 0, 2026-11-12: "Codeberg is under load", 403 on
# the Forgejo chart). That is no verdict on the commit; the follower reports an
# error and tries again later. A version that does not exist answers 404, which
# stays a failure.
UNREACHABLE = "upstream unreachable:"
_RENDER_FAILED = ("Manifest generation error", "failed to generate manifest")
_UPSTREAM_DOWN = re.compile(
    r"response status code (?:403|408|429|5\d\d)|under load|i/o timeout|dial tcp"
    r"|connection (?:reset|refused)|TLS handshake timeout|context deadline exceeded"
    r"|no such host|Service Unavailable|Too Many Requests")


def _upstream_unreachable(app: dict) -> str | None:
    """'<host>: <what it answered>' when the target could not be rendered because
    something outside the repo did not answer, else None."""
    status = app.get("status", {})
    messages = [c.get("message", "") for c in status.get("conditions", [])
                if c.get("type") == "ComparisonError"]
    messages.append(status.get("operationState", {}).get("message", ""))
    for message in messages:
        if not any(m in message for m in _RENDER_FAILED):
            continue
        if found := _UPSTREAM_DOWN.search(message):
            hosts = re.findall(r"https?://([^/\s\"']+)", message[:found.start()])
            host = next((h for h in reversed(hosts) if "github.com" not in h), None)
            return f"{host}: {found.group(0)}" if host else found.group(0)
    return None


def _sources(app: dict) -> list[dict]:
    spec = app.get("spec", {})
    return spec.get("sources") or ([spec["source"]] if spec.get("source") else [])


def _revisions(app: dict) -> list[str]:
    sync = app.get("status", {}).get("sync", {})
    return sync.get("revisions") or ([sync["revision"]] if sync.get("revision") else [])
