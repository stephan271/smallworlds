"""A restore point before Ring 0 takes a commit that may migrate a database (design §3.6).

When a commit touches a tenant whose database an update may migrate, every CNPG
cluster in that tenant's namespace gets an on-demand Backup before anything is
synced. Reverting such a commit does not bring the application back (Gallery
v5.10 -> v5.8 on Ring 0, 2026-11-11): the backup is the way back, and it is
named after the commit so the alert and the runbook can point at it.

The tenants concerned are synced through the overlay's remote base, so Argo CD
does not take the new commit until the follower hard-refreshes them, and the
backup is taken while the old release still runs.

The follower's Backups are labelled and pruned to the newest `keep` per
namespace; the scheduled nightly backups are left alone.
"""
import logging
import time

log = logging.getLogger("backups")

GROUP = "/apis/postgresql.cnpg.io/v1"
LABEL = "smallworlds.network/ring0-before"


def backup_name(sha: str) -> str:
    return f"ring0-before-{sha[:12]}"


class BackupFailed(Exception):
    pass


class Backups:
    def __init__(self, kube, namespaces: dict[str, str], timeout: int, keep: int = 3,
                 clock=time.monotonic, sleep=time.sleep):
        self.kube = kube
        self.namespaces = namespaces  # tenant -> namespace, where they differ
        self.timeout = timeout
        self.keep = keep
        self.clock = clock
        self.sleep = sleep

    def namespace_of(self, tenant: str) -> str:
        return self.namespaces.get(tenant, tenant)

    def before(self, sha: str, tenants: list[str]) -> list[str]:
        """Back up every CNPG cluster of these tenants and wait until each backup
        has completed. Returns 'namespace/cluster' for each one taken; raises
        BackupFailed when one fails or does not finish in time."""
        name = backup_name(sha)
        started = []
        for namespace in sorted({self.namespace_of(t) for t in tenants}):
            clusters = self.kube.get(f"{GROUP}/namespaces/{namespace}/clusters").get("items", [])
            for cluster in clusters:
                cluster_name = cluster["metadata"]["name"]
                # One Backup per cluster: two clusters in one namespace get their own.
                backup = name if len(clusters) == 1 else f"{name}-{cluster_name}"[:63]
                path = f"{GROUP}/namespaces/{namespace}/backups"
                existing = {b["metadata"]["name"] for b in self.kube.get(path).get("items", [])}
                if backup not in existing:
                    self.kube.create(path, {
                        "apiVersion": "postgresql.cnpg.io/v1",
                        "kind": "Backup",
                        "metadata": {"name": backup, "namespace": namespace,
                                     "labels": {LABEL: sha[:12]}},
                        "spec": {"cluster": {"name": cluster_name},
                                 "method": "plugin",
                                 "pluginConfiguration": {"name": "barman-cloud.cloudnative-pg.io"}},
                    })
                    log.info("backup %s/%s started for %s", namespace, backup, cluster_name)
                started.append((namespace, backup, cluster_name))
        deadline = self.clock() + self.timeout
        pending = list(started)
        while pending:
            still = []
            for namespace, backup, cluster_name in pending:
                status = self.kube.get(f"{GROUP}/namespaces/{namespace}/backups/{backup}").get("status", {})
                phase = status.get("phase")
                if phase == "completed":
                    continue
                if phase == "failed":
                    raise BackupFailed(f"{namespace}/{cluster_name}: {status.get('error', 'failed')}"[:200])
                still.append((namespace, backup, cluster_name))
            pending = still
            if not pending:
                break
            if self.clock() >= deadline:
                raise BackupFailed("did not complete in time: "
                                   + ", ".join(f"{n}/{c}" for n, _, c in pending))
            self.sleep(15)
        for namespace in sorted({n for n, _, _ in started}):
            self._prune(namespace)
        return [f"{n}/{c}" for n, _, c in started]

    def _prune(self, namespace: str) -> None:
        path = f"{GROUP}/namespaces/{namespace}/backups"
        ours = [b for b in self.kube.get(f"{path}?labelSelector={LABEL}").get("items", [])]
        ours.sort(key=lambda b: b["metadata"].get("creationTimestamp", ""), reverse=True)
        for old in ours[self.keep:]:
            try:
                self.kube.delete(f"{path}/{old['metadata']['name']}")
            except Exception:
                log.exception("could not prune %s/%s", namespace, old["metadata"]["name"])
