"""Runtime configuration for the Tier 1 remediation service.

All values come from the environment (populated by the `remediation-config`
ConfigMap and the `repo-git-creds` Secret). Kept in one place so handlers and
clients never read os.environ directly.
"""
import os


def _bool(name: str, default: bool) -> bool:
    return os.environ.get(name, str(default)).strip().lower() in ("1", "true", "yes", "on")


# --- Network -------------------------------------------------------------
LISTEN_PORT = int(os.environ.get("LISTEN_PORT", "8080"))

# In-cluster Prometheus (kube-prometheus-stack). Used for P95 sizing and for
# confirming the OOMKilled termination reason deterministically.
PROMETHEUS_URL = os.environ.get(
    "PROMETHEUS_URL",
    "http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090",
)

# Tier 2 (Hermes) escalation endpoint. Unmatched alerts, and alerts that
# re-fire after a Tier 1 fix should have resolved, get POSTed here with the
# full trace so the AI layer can take over.
HERMES_WEBHOOK_URL = os.environ.get(
    "HERMES_WEBHOOK_URL",
    "http://hermes-agent.hermes.svc.cluster.local:8080/webhook",
)

# --- GitOps overlay (PR target) -----------------------------------------
# Right-sizing PRs are opened against the private overlay repo (the only ref
# that deploys on merge), not the smallworlds base. See overlay_pr.py.
OVERLAY_REPO = os.environ.get("OVERLAY_REPO", "stephan271/my-community-config")
OVERLAY_BASE_BRANCH = os.environ.get("OVERLAY_BASE_BRANCH", "main")
GITHUB_API = os.environ.get("GITHUB_API", "https://api.github.com")
# GitHub PAT with `repo` scope, shared with Renovate (Secret repo-git-creds).
GITHUB_TOKEN = os.environ.get("GITHUB_TOKEN", "")

# Map a Kubernetes namespace to its overlay app directory. Identity for every
# current tenant; override here if a namespace ever diverges from its dir name.
NAMESPACE_TO_OVERLAY_APP = {
    # "somens": "somedir",
}

# --- Sizing policy -------------------------------------------------------
# New limit = ceil(P95 over the window * headroom), rounded up to a whole Mi.
P95_WINDOW = os.environ.get("P95_WINDOW", "7d")
HEADROOM_FACTOR = float(os.environ.get("HEADROOM_FACTOR", "1.5"))
# Never propose a decrease or a no-op bump smaller than this fraction — churn
# guard so a marginally-higher P95 doesn't open a PR every time.
MIN_BUMP_FRACTION = float(os.environ.get("MIN_BUMP_FRACTION", "0.15"))

# --- Escalation gate -----------------------------------------------------
# How long a Tier 1 fix is given to take effect. If the same alert re-fires
# after this deadline, Tier 1 stops retrying and escalates to Hermes.
RESOLVE_DEADLINE_SECONDS = int(os.environ.get("RESOLVE_DEADLINE_SECONDS", str(30 * 60)))

# --- Safety --------------------------------------------------------------
# When true (default for the scaffold), handlers compute everything and log the
# intended action but do NOT open PRs or escalate. Flip to false to arm.
DRY_RUN = _bool("DRY_RUN", True)
