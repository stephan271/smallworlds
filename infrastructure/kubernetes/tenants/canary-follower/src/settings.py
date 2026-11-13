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
    quiet_seconds: int = 1200
    quiet_interval_seconds: int = 60
    alertmanager_url: str = "http://kube-prometheus-stack-alertmanager.monitoring.svc:9093"
    # Before smoke and checks, wait until the 1-minute load per CPU stays below
    # this percentage (checks.settle). Ring 0 idles at about 100.
    prometheus_url: str = "http://kube-prometheus-stack-prometheus.monitoring.svc:9090"
    settle_load_percent: int = 150
    settle_timeout_seconds: int = 900
    settle_interval_seconds: int = 30
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

    # The Ring 0 Steward (incidents.py; design-per-app-updates.md §5). Incidents
    # are issues in the private Ring 0 overlay repository; the follower's token
    # needs Issues (read and write) there. STEWARD off: incidents are still
    # opened and closed, the Steward is never asked.
    overlay_repo: str = "stephan271/my-canary-installation"
    incidents: str = "false"
    steward: str = "false"
    steward_login: str = "smallworlds-steward[bot]"
    owner_login: str = "stephan271"
    protected_paths: str = ("e2e-tests/,admin-tools/canary/,infrastructure/kubernetes/tenants/canary-follower/,"
                            "renovate.json,.github/")
    steward_attempts: int = 3
    steward_runs_per_day: int = 6
    steward_request_timeout_seconds: int = 3600
    steward_working_timeout_seconds: int = 10800
    steward_retry_seconds: int = 10800
    stuck_queue_seconds: int = 86400
    unmergeable_seconds: int = 43200
    # Restoring a migrating tenant on Ring 0 by itself (design §5.2) is not built;
    # until it is, an S1 that needs a restore tells the Operator at once.
    auto_restore: str = "false"

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
    def protected(self) -> tuple[str, ...]:
        return tuple(p.strip() for p in self.protected_paths.split(",") if p.strip())

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
