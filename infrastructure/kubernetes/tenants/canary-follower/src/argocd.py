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


def _sources(app: dict) -> list[dict]:
    spec = app.get("spec", {})
    return spec.get("sources") or ([spec["source"]] if spec.get("source") else [])


def _revisions(app: dict) -> list[str]:
    sync = app.get("status", {}).get("sync", {})
    return sync.get("revisions") or ([sync["revision"]] if sync.get("revision") else [])


class ArgoCD:
    def __init__(self, kube, namespace: str, root_app: str,
                 clock=time.monotonic, sleep=time.sleep):
        self.kube = kube
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

    def _sync(self, name: str, deadline: float) -> None:
        # A sync operation written over a running one is lost, so wait it out.
        while _operation_running(self.get(name)):
            if self.clock() >= deadline:
                raise TimeoutError(f"{name} kept running an operation")
            self.sleep(5)
        self.kube.merge_patch(f"{self.path}/{name}", {"operation": {
            "initiatedBy": {"username": "canary-follower"},
            "sync": {"prune": True},
        }})

    def follow(self, refresh_timeout: int) -> list[str]:
        """Hard-refresh and fully sync the root first (it owns the other
        Applications, and a new one may appear), then every other one.
        Returns the names synced."""
        deadline = self.clock() + refresh_timeout
        self._refresh([self.root_app], deadline)
        self._sync(self.root_app, deadline)
        while _operation_running(self.get(self.root_app)):
            if self.clock() >= deadline:
                raise TimeoutError(f"{self.root_app} did not finish syncing")
            self.sleep(5)
        others = sorted(a["metadata"]["name"] for a in self.list()
                        if a["metadata"]["name"] != self.root_app)
        self._refresh(others, deadline)
        for name in others:
            self._sync(name, deadline)
        return [self.root_app, *others]

    def converge(self, timeout: int, interval: int) -> tuple[bool, list[str]]:
        """Wait until every Application is Synced and Healthy. Returns
        (converged, the ones that are not, as 'name: sync/health')."""
        deadline = self.clock() + timeout
        while True:
            lagging = [f"{a['metadata']['name']}: {_sync_status(a)}/{_health(a)}"
                       for a in self.list()
                       if _operation_running(a) or _sync_status(a) != "Synced"
                       or _health(a) != "Healthy"]
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
