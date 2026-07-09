"""Escalation gate — the piece Alertmanager cannot do itself.

Alertmanager has no memory of "Tier 1 already tried a fix for this". This gate
holds that state: keyed by alert fingerprint, it decides whether an incoming
alert is a first sighting (→ run the handler), a re-fire still within the fix's
grace period (→ wait), or a re-fire past the deadline / already handed off
(→ Hermes / Tier 2, exactly once).

State is in-process only, so it resets on pod restart — acceptable because the
handlers are idempotent (re-running finds the branch/PR already there and
returns the existing one). Persisting to a ConfigMap is the obvious hardening.

Serialization: registry.dispatch() holds a lock around the whole check-then-act
sequence, so these functions assume single-threaded access and take no locks of
their own.
"""
import json
import logging
import time
import urllib.request

import config

log = logging.getLogger("escalation")

# fingerprint -> {"first_seen": epoch, "deadline": epoch, "action": str}
_state: dict[str, dict] = {}
# fingerprints already handed to Tier 2; suppressed until the alert resolves.
_escalated: set[str] = set()


def first_sighting(fingerprint: str) -> bool:
    """True if no handler has acted on this fingerprint yet."""
    return fingerprint not in _state


def is_escalated(fingerprint: str) -> bool:
    """True if this incident has already been handed to Tier 2 and is awaiting
    resolution — do not act on or re-escalate it."""
    return fingerprint in _escalated


def record_action(fingerprint: str, action: str) -> None:
    """Remember that a Tier 1 handler acted, and by when the alert should clear."""
    now = time.time()
    _state[fingerprint] = {
        "first_seen": now,
        "deadline": now + config.RESOLVE_DEADLINE_SECONDS,
        "action": action,
    }


def deadline_passed(fingerprint: str) -> bool:
    rec = _state.get(fingerprint)
    return bool(rec) and time.time() > rec["deadline"]


def mark_escalated(fingerprint: str) -> None:
    _escalated.add(fingerprint)


def forget(fingerprint: str) -> None:
    """Called when Alertmanager reports the alert resolved — clears all state so
    a future recurrence is treated as a fresh first sighting."""
    _state.pop(fingerprint, None)
    _escalated.discard(fingerprint)


def escalate(alert: dict, reason: str) -> bool:
    """Hand an alert off to Tier 2 (Hermes). Returns True only if the hand-off
    was accepted (or dry-run), so the caller can decide whether to mark it
    escalated — a failed POST must NOT be treated as handed off."""
    payload = {"source": "tier1-remediation", "reason": reason, "alert": alert}
    alertname = alert.get("labels", {}).get("alertname")
    if config.DRY_RUN:
        log.info("[DRY_RUN] would escalate to Hermes (%s): %s", reason, alertname)
        return True
    try:
        req = urllib.request.Request(
            config.HERMES_WEBHOOK_URL,
            data=json.dumps(payload).encode(),
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        urllib.request.urlopen(req, timeout=15).read()
        log.info("escalated to Hermes (%s): %s", reason, alertname)
        return True
    except Exception:
        log.exception("escalation to Hermes failed; will retry on next delivery")
        return False
