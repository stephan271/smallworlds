"""Make Argo CD take main now, and tell when it has.

Two Argo CD behaviours make a plain wait insufficient (doc/tenant-join.md):

- A remote Kustomize base is served from cache until a *hard* refresh, so an
  overlay referencing the base at ?ref=HEAD does not notice a new commit.
- Self-heal syncs only drifted objects; Sync hooks (keycloak-client-job,
  garage-init, realm-config-job...) run only on a full sync.

So every Application is hard-refreshed and then given a sync operation, which
is what `argocd app sync` does. Everything goes through the Kubernetes API on
the Application objects; no Argo CD API token is needed.
"""
import logging
import time

log = logging.getLogger("argocd")

REFRESH = "argocd.argoproj.io/refresh"


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


def _sources(app: dict) -> list[dict]:
    spec = app.get("spec", {})
    return spec.get("sources") or ([spec["source"]] if spec.get("source") else [])


def _revisions(app: dict) -> list[str]:
    sync = app.get("status", {}).get("sync", {})
    return sync.get("revisions") or ([sync["revision"]] if sync.get("revision") else [])


class ArgoCD:
    def __init__(self, kube, namespace: str, root_app: str,
                 clock=time.monotonic, sleep=time.sleep, error_resyncs: int = 3):
        self.kube = kube
        self.error_resyncs = error_resyncs
        self.path = f"/apis/argoproj.io/v1alpha1/namespaces/{namespace}/applications"
        self.root_app = root_app
        self.clock = clock
        self.sleep = sleep

    def list(self) -> list[dict]:
        return self.kube.get(self.path).get("items", [])

    def get(self, name: str) -> dict:
        return self.kube.get(f"{self.path}/{name}")

    def _refresh(self, names: list[str], deadline: float) -> None:
        for name in names:
            self.kube.merge_patch(f"{self.path}/{name}",
                                  {"metadata": {"annotations": {REFRESH: "hard"}}})
        # The controller removes the annotation once it has refreshed.
        pending = set(names)
        while pending:
            pending = {n for n in pending
                       if REFRESH in self.get(n).get("metadata", {}).get("annotations", {})}
            if not pending:
                return
            if self.clock() >= deadline:
                raise TimeoutError("Argo CD did not refresh " + ", ".join(sorted(pending)))
            self.sleep(5)

    def _sync(self, app: dict) -> None:
        # The controller reads sync options from the operation, not from the
        # Application's syncPolicy (the UI and CLI copy them over). Without
        # them ServerSideApply was lost and cloudnative-pg's CRDs failed
        # client-side apply on Ring 0: "metadata.annotations: Too long".
        sync = {"prune": True}
        options = app.get("spec", {}).get("syncPolicy", {}).get("syncOptions")
        if options:
            sync["syncOptions"] = list(options)
        self.kube.merge_patch(f"{self.path}/{app['metadata']['name']}", {"operation": {
            "initiatedBy": {"username": "canary-follower"},
            "sync": sync,
        }})

    def follow(self, refresh_timeout: int) -> tuple[list[str], set[str]]:
        """Hard-refresh and fully sync the root first (it owns the other
        Applications, and a new one may appear), then every other one.

        A sync written over a running operation is lost, and an application
        can sit in a retrying sync for hours (Stalwart did, on Ring 0). Such
        an application is not waited for here: it is returned as deferred and
        synced by converge() once its operation ends. Returns (synced, deferred).
        """
        deadline = self.clock() + refresh_timeout
        self._refresh([self.root_app], deadline)
        while _operation_running(root := self.get(self.root_app)):
            if self.clock() >= deadline:
                raise TimeoutError(f"{self.root_app} kept running an operation")
            self.sleep(5)
        self._sync(root)
        while _operation_running(self.get(self.root_app)):
            if self.clock() >= deadline:
                raise TimeoutError(f"{self.root_app} did not finish syncing")
            self.sleep(5)
        others = sorted(a["metadata"]["name"] for a in self.list()
                        if a["metadata"]["name"] != self.root_app)
        self._refresh(others, deadline)
        synced, deferred = [self.root_app], set()
        for name in others:
            if _operation_running(app := self.get(name)):
                deferred.add(name)
            else:
                self._sync(app)
                synced.append(name)
        return synced, deferred

    def converge(self, timeout: int, interval: int,
                 deferred: set[str] = frozenset()) -> tuple[bool, list[str]]:
        """Wait until every Application is Synced and Healthy, syncing each
        deferred one as soon as its running operation ends. Returns
        (converged, the ones that are not, as 'name: sync/health' or
        'name: sync Failed: <what failed>')."""
        deferred = set(deferred)
        resynced: dict[str, int] = {}
        deadline = self.clock() + timeout
        while True:
            apps = self.list()
            for app in apps:
                name = app["metadata"]["name"]
                if name in deferred and not _operation_running(app):
                    self._sync(app)
                    deferred.discard(name)
                elif _errored(app) and resynced.get(name, 0) < self.error_resyncs:
                    # A transient error is no verdict on the commit: try again,
                    # a bounded number of times, before reporting it.
                    resynced[name] = resynced.get(name, 0) + 1
                    log.info("%s: last operation ended in Error; syncing again (%d/%d)",
                             name, resynced[name], self.error_resyncs)
                    self._sync(app)
            lagging = []
            for a in self.list():
                name = a["metadata"]["name"]
                if name in deferred:
                    lagging.append(f"{name}: still running an earlier sync")
                elif _operation_running(a) or _sync_status(a) != "Synced" or _health(a) != "Healthy":
                    lagging.append(f"{name}: {_sync_status(a)}/{_health(a)}")
                elif failure := _sync_failure(a):
                    lagging.append(f"{name}: {failure}")
            if not lagging:
                return True, []
            if self.clock() >= deadline:
                return False, sorted(lagging)
            self.sleep(interval)

    def not_at(self, sha: str, base_repo_url: str) -> list[str]:
        """Applications sourced straight from the base that run another
        revision than sha: main moved on while this commit was verified."""
        stale = []
        for app in self.list():
            for source, revision in zip(_sources(app), _revisions(app)):
                if source.get("repoURL", "").rstrip("/") == base_repo_url and revision != sha:
                    stale.append(f"{app['metadata']['name']}@{revision[:12]}")
        return sorted(stale)
