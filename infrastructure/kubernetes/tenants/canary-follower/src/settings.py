"""Settings, read once from the environment (ConfigMap canary-follower-config)."""
import os
from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    github_api: str = "https://api.github.com"
    github_repo: str = "stephan271/smallworlds"
    branch: str = "main"
    github_token: str = ""
    status_context: str = "ring0/verified"
    argocd_namespace: str = "argocd"
    root_app: str = "smallworlds-root"
    poll_seconds: int = 300
    # A commit whose verification ended in error is verified again this often.
    error_retries: int = 3
    error_retry_seconds: int = 3600
    refresh_timeout_seconds: int = 300
    converge_timeout_seconds: int = 2700
    converge_interval_seconds: int = 20
    smoke_attempts: int = 3
    smoke_retry_seconds: int = 20
    quiet_seconds: int = 1800
    quiet_interval_seconds: int = 60
    alertmanager_url: str = "http://kube-prometheus-stack-alertmanager.monitoring.svc:9093"
    http_port: int = 8080
    # Tenants whose database an image update may migrate. A failing commit that
    # touches one cannot be undone by reverting it: the old image may refuse the
    # migrated database (Gallery v5.10 -> v5.8 on Ring 0, 2026-11-11), so the way
    # back is the pre-update backup.
    migrating_tenants: str = "immich,gallery,nextcloud,forgejo,stalwart,keycloak"
    # tenant:namespace where they differ (Gallery is a variant of the immich tenant).
    tenant_namespaces: str = "gallery:immich"

    # Pre-update backups (backups.py): one on-demand CNPG Backup per cluster of
    # every migrating tenant a commit touches, before anything is synced.
    pre_update_backups: str = "false"
    backup_timeout_seconds: int = 1800
    backups_keep: int = 3

    # E2E and data checks (checksjob.py): the suspended CronJob the Job is cut
    # from. Empty: not run, and the status says so.
    checks_cronjob: str = ""
    checks_namespace: str = "canary-follower"
    checks_timeout_seconds: int = 3600

    # Merge queue (mergequeue.py).
    merge_queue: str = "false"
    auto_revert: str = "false"
    queue_label: str = "ring0-queue"
    queue_author: str = "renovate[bot]"

    @staticmethod
    def on(value: str) -> bool:
        return value.strip().lower() in ("1", "true", "yes", "on")

    @property
    def namespaces(self) -> dict[str, str]:
        pairs = (p.split(":", 1) for p in self.tenant_namespaces.split(",") if ":" in p)
        return {t.strip(): n.strip() for t, n in pairs}

    @property
    def migrating(self) -> list[str]:
        return [t.strip() for t in self.migrating_tenants.split(",") if t.strip()]

    @property
    def base_repo_url(self) -> str:
        return f"https://github.com/{self.github_repo}.git"


def from_environment(env=os.environ) -> Settings:
    values = {}
    for name, field in Settings.__dataclass_fields__.items():
        raw = env.get(name.upper())
        if raw is None or raw == "":
            continue
        values[name] = int(raw) if field.type in (int, "int") else raw
    return Settings(**values)
