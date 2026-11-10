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
    refresh_timeout_seconds: int = 300
    converge_timeout_seconds: int = 2700
    converge_interval_seconds: int = 20
    smoke_attempts: int = 3
    smoke_retry_seconds: int = 20
    quiet_seconds: int = 1800
    quiet_interval_seconds: int = 60
    alertmanager_url: str = "http://kube-prometheus-stack-alertmanager.monitoring.svc:9093"
    http_port: int = 8080

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
