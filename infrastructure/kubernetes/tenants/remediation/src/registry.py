"""Dispatch: route one Alertmanager alert through the escalation gate and the
handler registry.

Decision flow per alert:
  resolved              -> forget its fingerprint, done
  already escalated     -> wait (handed to Tier 2, awaiting resolution)
  already acted:
    deadline passed     -> escalate (Tier 1's fix didn't hold), mark escalated
    still within grace  -> wait
  first sighting:
    a handler acts       -> record it, start the resolve-by clock
    a handler errors     -> escalate, mark escalated
    no handler applies   -> escalate, mark escalated

The whole sequence runs under a lock: ThreadingHTTPServer serves each webhook
POST on its own thread, and the escalation gate does check-then-act on shared
state, so concurrent deliveries of the same fingerprint must be serialized.
"""
import logging
import threading

import escalation
from handlers import REGISTRY
from handlers.base import Outcome

log = logging.getLogger("registry")

_lock = threading.Lock()


def dispatch(alert: dict) -> None:
    with _lock:
        _dispatch_locked(alert)


def _dispatch_locked(alert: dict) -> None:
    labels = alert.get("labels", {})
    fingerprint = alert.get("fingerprint") or _synthetic_fingerprint(labels)
    alertname = labels.get("alertname", "?")

    if alert.get("status") == "resolved":
        escalation.forget(fingerprint)
        log.info("resolved: %s (%s)", alertname, fingerprint)
        return

    if escalation.is_escalated(fingerprint):
        log.info("already handed to Tier 2, waiting: %s (%s)", alertname, fingerprint)
        return

    if not escalation.first_sighting(fingerprint):
        if escalation.deadline_passed(fingerprint):
            if escalation.escalate(alert, "tier1 fix did not resolve within deadline"):
                escalation.mark_escalated(fingerprint)
        else:
            log.info("within grace period, waiting: %s (%s)", alertname, fingerprint)
        return

    for handler in REGISTRY:
        if not handler.matches(alert):
            continue
        result = handler.handle(alert)
        log.info("handler=%s outcome=%s detail=%s", handler.name, result.outcome.value, result.detail)
        if result.handled:
            escalation.record_action(fingerprint, f"{handler.name}:{result.detail}")
            return
        if result.outcome == Outcome.ERROR:
            if escalation.escalate(alert, f"handler {handler.name} errored: {result.detail}"):
                escalation.mark_escalated(fingerprint)
            return
        # NOT_APPLICABLE: fall through to the next handler.

    if escalation.escalate(alert, "no Tier 1 handler applied"):
        escalation.mark_escalated(fingerprint)


def _synthetic_fingerprint(labels: dict) -> str:
    """Alertmanager always sends a fingerprint; fall back to a stable key from
    the identifying labels only if it's missing (e.g. hand-crafted test posts)."""
    keys = ("alertname", "namespace", "pod", "container")
    return "|".join(f"{k}={labels.get(k, '')}" for k in keys)
