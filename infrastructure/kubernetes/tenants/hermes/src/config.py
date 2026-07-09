"""Runtime configuration for the Hermes Tier 2 agent.

All values come from the environment (the `hermes-agent-config` ConfigMap and
the `hermes-anthropic` Secret). Kept in one place so the tools and the agent
loop never read os.environ directly.
"""
import os


def _bool(name: str, default: bool) -> bool:
    return os.environ.get(name, str(default)).strip().lower() in ("1", "true", "yes", "on")


# --- Network ------------------------------------------------------------
LISTEN_PORT = int(os.environ.get("LISTEN_PORT", "8080"))

# Diagnostic data sources (in-cluster).
PROMETHEUS_URL = os.environ.get(
    "PROMETHEUS_URL",
    "http://kube-prometheus-stack-prometheus.monitoring.svc.cluster.local:9090",
)
LOKI_URL = os.environ.get(
    "LOKI_URL", "http://loki-stack.monitoring.svc.cluster.local:3100"
)

# --- Claude (Anthropic Messages API over raw HTTP) ----------------------
ANTHROPIC_API = os.environ.get("ANTHROPIC_API", "https://api.anthropic.com")
ANTHROPIC_VERSION = os.environ.get("ANTHROPIC_VERSION", "2023-06-01")
# Provisioned as the `hermes-anthropic` Secret when arming; empty in DRY_RUN.
ANTHROPIC_API_KEY = os.environ.get("ANTHROPIC_API_KEY", "")
MODEL = os.environ.get("HERMES_MODEL", "claude-opus-4-8")
# effort: low | medium | high | max. high is the sweet spot for this workload.
EFFORT = os.environ.get("HERMES_EFFORT", "high")
# Total output budget (shared with adaptive extended thinking at effort=high),
# so keep it generous. Stays under the 120s non-streaming timeout comfortably.
MAX_TOKENS = int(os.environ.get("HERMES_MAX_TOKENS", "16000"))
# Retries for transient Anthropic API errors (429/529/5xx/timeout).
API_RETRIES = int(os.environ.get("HERMES_API_RETRIES", "3"))
# Hard cap on the agentic loop so a confused model can't spend unboundedly.
MAX_ITERATIONS = int(os.environ.get("HERMES_MAX_ITERATIONS", "8"))
# Max investigations running at once. Each is a multi-minute paid Opus loop, so
# an alert storm must NOT spawn one thread per alert — extras queue.
MAX_CONCURRENT = int(os.environ.get("HERMES_MAX_CONCURRENT", "2"))
SYSTEM_PROMPT_PATH = os.environ.get("SYSTEM_PROMPT_PATH", "/etc/hermes/system-prompt.txt")

# --- Email (Stalwart internal relay) ------------------------------------
# Same relay Alertmanager uses: unauthenticated from cluster subnets, plain
# port 25, DKIM-signs local-domain senders. EHLO must be a valid FQDN.
SMTP_HOST = os.environ.get("SMTP_HOST", "stalwart-mail.stalwart.svc.cluster.local")
SMTP_PORT = int(os.environ.get("SMTP_PORT", "25"))
SMTP_HELO = os.environ.get("SMTP_HELO", "smallworlds.network")
MAIL_FROM = os.environ.get("MAIL_FROM", "hermes@smallworlds.network")
# Placeholder recipient; each operator overrides ADMIN_EMAIL in their overlay.
ADMIN_EMAIL = os.environ.get("ADMIN_EMAIL", "admin@smallworlds.network")

# --- Safety -------------------------------------------------------------
# When true (default for the scaffold) Hermes logs the escalation and the
# intended investigation but makes NO Claude API calls and sends NO email.
# Flip to false (and provide ANTHROPIC_API_KEY) to arm.
DRY_RUN = _bool("DRY_RUN", True)
