"""The public half: what an invited member's browser may ask (design §3, §9).

Every request carries the invitation code, and every action is on the account
the invitation itself records. Nothing here accepts an account id from the
browser, so a valid code can create one account (`new`) or reach exactly one
existing account (`recovery`), and nothing else.

Each function returns (HTTP status, JSON body). Errors are message keys the page
translates, never prose.
"""
import collections
import logging
import re
import threading
import time

import codes
import config
import keycloak
import store
import usernames

log = logging.getLogger("member")

# Keycloak's default person-name-prohibited-characters validator, mirrored so a
# name it would refuse is refused here with a message, not by a 400 from it.
_PROHIBITED_IN_NAMES = re.compile(r"[<>&\"\v$%!#?§;*~/\\|^=\[\]{}()\x00-\x1f\x7f]")
_EMAIL = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")


class Limiter:
    """At most `limit` events per key within `window` seconds."""

    def __init__(self, limit: int, window: float):
        self.limit, self.window = limit, window
        self._events: dict[str, collections.deque] = {}
        self._lock = threading.Lock()

    def _recent(self, key: str, now: float) -> collections.deque:
        events = self._events.setdefault(key, collections.deque())
        while events and events[0] <= now - self.window:
            events.popleft()
        return events

    def exhausted(self, key: str) -> bool:
        with self._lock:
            return len(self._recent(key, time.time())) >= self.limit

    def hit(self, key: str) -> None:
        with self._lock:
            now = time.time()
            self._recent(key, now).append(now)
            if len(self._events) > 10_000:  # forget idle keys
                for stale in [k for k, v in self._events.items() if not v]:
                    del self._events[stale]


failed_lookups = Limiter(config.FAILED_LOOKUPS_PER_HOUR, 3600)
username_checks = Limiter(config.USERNAME_CHECKS_PER_10_MIN, 600)


def refresh(invitation: store.Invitation) -> store.Invitation:
    """Notice a passkey registered since the invitation was issued (design §5).

    Observed rather than reported, so an invitation is recorded as done even if
    the member never comes back to /done.
    """
    if invitation.state() not in ("open", "started") or not invitation.keycloak_user_id:
        return invitation
    try:
        if keycloak.has_passkey_since(invitation.keycloak_user_id, invitation.created_at):
            store.complete(invitation.id)
            return store.by_id(invitation.id)
    except keycloak.KeycloakError as error:
        log.warning("could not check invitation %s for a passkey: %s", invitation.id, error)
    return invitation


def _resolve(code_text, source: str) -> tuple[store.Invitation | None, tuple[int, dict] | None]:
    """The live invitation for `code_text`, or the response explaining why not."""
    if failed_lookups.exhausted(source):
        return None, (429, {"error": "too_many"})
    code = codes.normalize(code_text)
    invitation = store.by_code(code) if code else None
    if invitation is None:
        failed_lookups.hit(source)
        return None, (404, {"error": "unknown"})
    invitation = refresh(invitation)
    state = invitation.state()
    if state in ("done", "expired", "revoked"):
        return None, (410, {"error": "used" if state == "done" else state})
    return invitation, None


def _describe(invitation: store.Invitation) -> dict:
    answer = {"state": invitation.state(), "kind": invitation.kind, "locale": invitation.locale}
    if invitation.username:
        answer["username"] = invitation.username
    return answer


def lookup(code_text, source: str) -> tuple[int, dict]:
    invitation, refusal = _resolve(code_text, source)
    return refusal or (200, _describe(invitation))


def check_username(code_text, username_text, source: str) -> tuple[int, dict]:
    """Live availability, for the holder of an unclaimed new-member invitation only."""
    invitation, refusal = _resolve(code_text, source)
    if refusal:
        return refusal
    if invitation.kind != "new" or invitation.state() != "open":
        return 409, {"error": "no_choice"}
    key = str(invitation.id)
    if username_checks.exhausted(key):
        return 429, {"error": "too_many"}
    username_checks.hit(key)
    username = usernames.normalize(username_text)
    reason = usernames.problem(username)
    if reason:
        return 200, {"username": username, "available": False, "reason": reason}
    try:
        taken = keycloak.find_user(username) is not None
    except keycloak.KeycloakError as error:
        log.warning("username check failed: %s", error)
        return 502, {"error": "unavailable"}
    if taken:
        return 200, {"username": username, "available": False, "reason": "username_taken"}
    return 200, {"username": username, "available": True}


def _profile(payload: dict) -> tuple[dict, dict]:
    """The submitted profile, normalized, and the problems with it by field."""
    profile = {
        "username": usernames.normalize(payload.get("username")),
        "firstName": (payload.get("firstName") or "").strip() if isinstance(payload.get("firstName"), str) else "",
        "lastName": (payload.get("lastName") or "").strip() if isinstance(payload.get("lastName"), str) else "",
        "email": (payload.get("email") or "").strip() if isinstance(payload.get("email"), str) else "",
    }
    problems = {}
    reason = usernames.problem(profile["username"])
    if reason:
        problems["username"] = reason
    for field in ("firstName", "lastName"):
        if not profile[field]:
            problems[field] = "required"
        elif len(profile[field]) > 255 or _PROHIBITED_IN_NAMES.search(profile[field]):
            problems[field] = "name_characters"
    if not profile["email"]:
        problems["email"] = "required"
    elif len(profile["email"]) > 254 or not _EMAIL.match(profile["email"]):
        problems["email"] = "email_format"
    return profile, problems


def _create_account(invitation: store.Invitation, payload: dict,
                    locale: str) -> tuple[str | None, tuple[int, dict] | None]:
    profile, problems = _profile(payload)
    if problems:
        return None, (400, {"error": "invalid", "fields": problems})
    # Checked up front for a precise message; the create below still settles
    # any race, because Keycloak refuses a duplicate either way.
    if keycloak.find_user(profile["username"]) is not None:
        return None, (409, {"error": "invalid", "fields": {"username": "username_taken"}})
    if keycloak.email_taken(profile["email"]):
        return None, (409, {"error": "invalid", "fields": {"email": "email_taken"}})
    try:
        user_id = keycloak.create_user(profile["username"], profile["firstName"],
                                       profile["lastName"], profile["email"], locale)
    except keycloak.Conflict as conflict:
        return None, (409, {"error": "invalid",
                            "fields": {conflict.field: f"{conflict.field}_taken"}})
    if not store.claim(invitation.id, user_id, profile["username"]):
        # Cannot happen under store.lock short of a database failure; an account
        # no invitation records would be invisible to the Desk, so undo it.
        log.error("invitation %s could not be claimed; removing account %s", invitation.id, user_id)
        try:
            keycloak.delete_user(user_id)
        except keycloak.KeycloakError as error:
            log.error("and the account could not be removed: %s", error)
        return None, (409, {"error": "conflict"})
    log.info("invitation %s: created account %s", invitation.id, profile["username"])
    return user_id, None


def redeem(code_text, payload: dict, source: str) -> tuple[int, dict]:
    """Create the account if needed and hand back a fresh link into Keycloak.

    Opening a started invitation again mints a new link for the same account
    until a passkey exists, so an abandoned tab or the 12-hour token lifespan
    never strands a member (design §3).
    """
    locale = payload.get("locale")
    with store.lock:
        invitation, refusal = _resolve(code_text, source)
        if refusal:
            return refusal
        if locale not in config.LOCALES:
            locale = invitation.locale
        try:
            if invitation.kind == "new" and invitation.state() == "open":
                user_id, refusal = _create_account(invitation, payload, locale)
                if refusal:
                    return refusal
            else:
                user_id = invitation.keycloak_user_id
                if keycloak.get_user(user_id) is None:
                    return 410, {"error": "account_gone"}
                keycloak.set_locale(user_id, locale)
                if invitation.kind == "recovery":
                    store.mark_started(invitation.id)
            link = keycloak.mint_link(user_id, locale)
        except keycloak.KeycloakError as error:
            log.error("invitation %s: %s", invitation.id, error)
            return 502, {"error": "unavailable"}
    return 200, {"redirect": link}
