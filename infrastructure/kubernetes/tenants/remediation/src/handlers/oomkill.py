"""OOMKill right-sizing handler — the first Tier 1 handler.

Trigger: KubePodCrashLooping on a container whose last termination was
OOMKilled. Action: size a new memory limit from the workload's P95 working-set
and open an overlay PR bumping it. Everything is deterministic; if anything is
ambiguous (can't confirm OOM, no metrics, unknown app) it declines so the
registry escalates to Hermes instead of guessing.
"""
import logging
import re

import config
import overlay_pr
import prometheus
import units
from handlers.base import Handler, Outcome, Result

log = logging.getLogger("oomkill")

# ReplicaSet pod name shape: "<deployment>-<rs-hash>-<pod-hash>".
_HASH_SUFFIX = "[a-z0-9]{6,10}-[a-z0-9]{5}"
_POD_SUFFIX = re.compile(f"-{_HASH_SUFFIX}$")


def _deployment_of(pod: str) -> str:
    """Best-effort Deployment name from a pod name by stripping the ReplicaSet
    and pod hash suffixes. TODO: use ownerReferences via the k8s API for
    workloads that aren't Deployments (StatefulSet/DaemonSet), which this
    Deployment-shaped pattern does not match."""
    return _POD_SUFFIX.sub("", pod)


def _pod_regex(deployment: str) -> str:
    """Anchored (Prometheus =~ matches the whole string) regex matching only
    this Deployment's pods — not a sibling workload sharing the name prefix
    (e.g. 'web' must not match 'web-admin-<hash>-<hash>')."""
    return f"{re.escape(deployment)}-{_HASH_SUFFIX}"


class OOMKillHandler(Handler):
    name = "oomkill"

    def matches(self, alert: dict) -> bool:
        return alert.get("labels", {}).get("alertname") == "KubePodCrashLooping"

    def handle(self, alert: dict) -> Result:
        labels = alert.get("labels", {})
        namespace = labels.get("namespace")
        pod = labels.get("pod")
        container = labels.get("container")
        if not (namespace and pod and container):
            return Result(Outcome.NOT_APPLICABLE, "alert lacks namespace/pod/container labels")

        # Deterministic OOM confirmation — CrashLooping alone isn't enough.
        if not prometheus.confirm_oomkilled(namespace, pod, container):
            return Result(Outcome.NOT_APPLICABLE, "last termination is not OOMKilled")

        app = config.NAMESPACE_TO_OVERLAY_APP.get(namespace, namespace)
        deployment = _deployment_of(pod)
        pod_regex = _pod_regex(deployment)

        p95 = prometheus.p95_working_set_bytes(namespace, container, pod_regex)
        if p95 is None:
            return Result(Outcome.NOT_APPLICABLE, "no P95 working-set data to size from")

        current = prometheus.current_memory_limit_bytes(namespace, container, pod_regex)
        proposed = p95 * config.HEADROOM_FACTOR

        # Never propose a limit we can't prove is an increase. If the current
        # limit is unknown (no metric during the crashloop, or a query error) we
        # cannot rule out a *decrease* — which would make the OOMs worse — so
        # decline and let Hermes inspect live state instead.
        if current is None:
            return Result(Outcome.NOT_APPLICABLE, "could not read current memory limit; escalating")

        # Churn guard: only act if the proposal meaningfully exceeds the current
        # limit. If the limit is already generous, an OOM here is not a sizing
        # problem — decline so it escalates (leak, spike, node pressure...). This
        # also blocks any decrease, since proposed <= current fails the check.
        if proposed <= current * (1 + config.MIN_BUMP_FRACTION):
            return Result(
                Outcome.NOT_APPLICABLE,
                f"limit {units.to_mi(current)} already covers P95 {units.to_mi(p95)}; "
                "OOM is not a right-sizing case",
            )

        new_limit = units.to_mi(proposed)
        old_limit = units.to_mi(current)
        context = (
            f"Sizing: P95 working-set over {config.P95_WINDOW} = {units.to_mi(p95)}, "
            f"× {config.HEADROOM_FACTOR} headroom → **{new_limit}** "
            f"(current limit: {old_limit})."
        )
        log.info("OOMKill %s/%s (%s): %s -> %s | %s",
                 namespace, deployment, container, old_limit, new_limit, context)

        try:
            pr_url = overlay_pr.open_limit_bump_pr(
                app=app, deployment=deployment, container=container,
                new_limit=new_limit, old_limit=old_limit, context=context,
            )
        except Exception as e:
            log.exception("failed to open overlay PR")
            return Result(Outcome.ERROR, f"overlay PR failed: {e}")

        return Result(Outcome.ACTED, f"opened memory-bump PR ({new_limit}): {pr_url}")
