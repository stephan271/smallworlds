"""The Invitation Desk — the Operator's half of join (design §4, §9).

Served on its own port, behind the Private Network allow-list, and only to a
session signed in as a master-realm account holding `admin` — in practice the
`operator` account, password + TOTP. That is the same protection as the Keycloak
admin console, because the Desk can hand any member a new passkey.

Sign-in is the authorization code flow with PKCE against the public `join-desk`
client. Sessions live in memory: a restart signs the Operator out, nothing more.

Codes are shown once, when issued. Only their hash is stored, so "show again"
does not exist; a new code (renew) replaces the old one.
"""
import base64
import hashlib
import json
import logging
import re
import secrets
import threading
import time
import urllib.parse
from http.cookies import SimpleCookie

import codes
import config
import keycloak
import member
import store
import web

log = logging.getLogger("desk")

SESSION_COOKIE = "join_desk"
STATE_COOKIE = "join_desk_login"
LOGIN_SECONDS = 600

_sessions: dict[str, dict] = {}
_pending: dict[str, dict] = {}
_lock = threading.Lock()


class Refused(Exception):
    """Sign-in did not produce an Operator session. The message is a reason key."""


# ---- sign-in ----------------------------------------------------------------

def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def start_login(lang: str) -> tuple[str, str]:
    """Where to send the browser, and the state to remember in a cookie."""
    state = secrets.token_urlsafe(24)
    verifier = secrets.token_urlsafe(48)
    now = time.time()
    with _lock:
        for stale in [k for k, v in _pending.items() if v["expires"] < now]:
            del _pending[stale]
        if len(_pending) > 200:  # a flood of abandoned logins; the oldest go
            for oldest in sorted(_pending, key=lambda k: _pending[k]["expires"])[:100]:
                del _pending[oldest]
        _pending[state] = {"verifier": verifier, "expires": now + LOGIN_SECONDS}
    query = urllib.parse.urlencode({
        "client_id": config.DESK_CLIENT_ID,
        "response_type": "code",
        "scope": "openid",
        "redirect_uri": config.desk_redirect_uri(),
        "state": state,
        "code_challenge": _b64url(hashlib.sha256(verifier.encode()).digest()),
        "code_challenge_method": "S256",
        "ui_locales": lang if lang in config.LOCALES else "en",
    })
    auth = f"{config.PUBLIC_IDENTITY_URL}/realms/{config.DESK_REALM}/protocol/openid-connect/auth"
    return f"{auth}?{query}", state


def _claims(token: str) -> dict:
    try:
        payload = token.split(".")[1]
        return json.loads(base64.urlsafe_b64decode(payload + "=" * (-len(payload) % 4)))
    except (IndexError, ValueError):
        raise Refused("bad_token")


def finish_login(state: str, cookie_state: str, code: str) -> str:
    """A new session id for a successful sign-in. Raises Refused otherwise."""
    if not state or not code or not secrets.compare_digest(state, cookie_state or ""):
        raise Refused("state")
    with _lock:
        pending = _pending.pop(state, None)
    if not pending or pending["expires"] < time.time():
        raise Refused("state")
    try:
        tokens = keycloak.exchange_desk_code(code, pending["verifier"])
    except keycloak.KeycloakError as error:
        log.warning("desk sign-in: code exchange failed: %s", error)
        raise Refused("exchange")
    claims = _claims(tokens.get("access_token", ""))
    issuer = f"{config.PUBLIC_IDENTITY_URL}/realms/{config.DESK_REALM}"
    if claims.get("iss") != issuer or claims.get("azp") != config.DESK_CLIENT_ID:
        raise Refused("bad_token")
    if claims.get("exp", 0) < time.time():
        raise Refused("bad_token")
    if config.DESK_REQUIRED_ROLE not in (claims.get("realm_access") or {}).get("roles", []):
        log.warning("desk sign-in refused for %s: no %s role",
                    claims.get("preferred_username"), config.DESK_REQUIRED_ROLE)
        raise Refused("not_operator")
    sid = secrets.token_urlsafe(32)
    with _lock:
        now = time.time()
        for stale in [k for k, v in _sessions.items() if v["expires"] < now]:
            del _sessions[stale]
        _sessions[sid] = {"username": claims.get("preferred_username", "?"),
                          "expires": now + config.DESK_SESSION_SECONDS}
    log.info("desk: %s signed in", _sessions[sid]["username"])
    return sid


def session(sid: str | None) -> dict | None:
    if not sid:
        return None
    with _lock:
        found = _sessions.get(sid)
        if found and found["expires"] < time.time():
            del _sessions[sid]
            found = None
        return found


def end_session(sid: str | None) -> None:
    with _lock:
        _sessions.pop(sid or "", None)


# ---- what the Desk does -----------------------------------------------------

def link_for(code: str) -> str:
    return f"{config.JOIN_URL}/#{codes.display(code)}"


def describe(invitation: store.Invitation) -> dict:
    return {
        "id": invitation.id,
        "kind": invitation.kind,
        "label": invitation.label,
        "note": invitation.note,
        "locale": invitation.locale,
        "state": invitation.state(),
        "createdAt": invitation.created_at,
        "expiresAt": invitation.expires_at,
        "completedAt": invitation.completed_at,
        "revokedAt": invitation.revoked_at,
        "username": invitation.username,
    }


def _issued(invitation: store.Invitation, code: str) -> dict:
    return {"invitation": describe(invitation), "code": codes.display(code), "link": link_for(code)}


def _text(body: dict, key: str, limit: int) -> str | None:
    value = body.get(key, "")
    if not isinstance(value, str) or len(value.strip()) > limit:
        return None
    return value.strip()


def _settings(body: dict) -> tuple[dict | None, tuple[int, dict] | None]:
    locale = body.get("locale", "de")
    days = body.get("days", config.DEFAULT_DAYS)
    label = _text(body, "label", 80)
    note = _text(body, "note", 500)
    problems = {}
    if locale not in config.LOCALES:
        problems["locale"] = "invalid"
    if not isinstance(days, int) or isinstance(days, bool) or not 1 <= days <= config.MAX_DAYS:
        problems["days"] = "invalid"
    if label is None:
        problems["label"] = "too_long"
    if note is None:
        problems["note"] = "too_long"
    if problems:
        return None, (400, {"error": "invalid", "fields": problems})
    return {"locale": locale, "days": days, "label": label, "note": note}, None


def list_invitations() -> tuple[int, dict]:
    return 200, {"invitations": [describe(member.refresh(i)) for i in store.all_invitations()]}


def create(body: dict) -> tuple[int, dict]:
    settings, refusal = _settings(body)
    if refusal:
        return refusal
    invitation, code = store.create("new", settings["locale"], settings["days"],
                                    settings["label"], settings["note"])
    return 200, _issued(invitation, code)


def create_bulk(body: dict) -> tuple[int, dict]:
    labels = body.get("labels")
    if not isinstance(labels, list) or not all(isinstance(x, str) for x in labels):
        return 400, {"error": "invalid", "fields": {"labels": "invalid"}}
    labels = [x.strip() for x in labels if x.strip()]
    if not labels or len(labels) > config.MAX_BULK or any(len(x) > 80 for x in labels):
        return 400, {"error": "invalid", "fields": {"labels": "invalid"}}
    settings, refusal = _settings({**body, "label": ""})
    if refusal:
        return refusal
    issued = []
    for label in labels:
        invitation, code = store.create("new", settings["locale"], settings["days"], label,
                                        settings["note"])
        issued.append(_issued(invitation, code))
    return 200, {"issued": issued}


def _live(invitation_id) -> tuple[store.Invitation | None, tuple[int, dict] | None]:
    invitation = store.by_id(invitation_id)
    if invitation is None:
        return None, (404, {"error": "not_found"})
    invitation = member.refresh(invitation)
    state = invitation.state()
    if state in ("done", "revoked"):
        return None, (409, {"error": state})
    return invitation, None


def revoke(invitation_id: int, body: dict) -> tuple[int, dict]:
    """Withdraw an invitation; optionally delete the account it started.

    An account is deleted only if this invitation created it and it holds no
    passkey at all — an account someone can sign in to is never removed here.
    """
    with store.lock:
        invitation, refusal = _live(invitation_id)
        if refusal:
            return refusal
        if body.get("deleteAccount") is True and invitation.kind == "new" \
                and invitation.keycloak_user_id:
            try:
                if keycloak.has_passkey_since(invitation.keycloak_user_id, 0):
                    store.complete(invitation.id)
                    return 409, {"error": "has_passkey"}
                keycloak.delete_user(invitation.keycloak_user_id)
            except keycloak.KeycloakError as error:
                log.error("revoke %s: %s", invitation.id, error)
                return 502, {"error": "unavailable"}
            log.info("desk: deleted account %s of invitation %s", invitation.username, invitation.id)
        store.revoke(invitation.id)
        return 200, {"invitation": describe(store.by_id(invitation.id))}


def renew(invitation_id: int, body: dict) -> tuple[int, dict]:
    days = body.get("days", config.DEFAULT_DAYS)
    if not isinstance(days, int) or isinstance(days, bool) or not 1 <= days <= config.MAX_DAYS:
        return 400, {"error": "invalid", "fields": {"days": "invalid"}}
    with store.lock:
        _, refusal = _live(invitation_id)
        if refusal:
            return refusal
        renewed = store.renew(invitation_id, days)
    if renewed is None:
        return 409, {"error": "revoked"}
    return 200, _issued(*renewed)


def search_members(query: str) -> tuple[int, dict]:
    query = (query or "").strip()
    if len(query) < 2:
        return 200, {"members": []}
    try:
        found = keycloak.search_users(query)
        members = []
        for user in found:
            passkeys = sum(1 for c in keycloak.credentials(user["id"])
                           if c.get("type") == config.PASSKEY_CREDENTIAL_TYPE)
            members.append({"id": user["id"], "username": user.get("username", ""),
                            "firstName": user.get("firstName", ""),
                            "lastName": user.get("lastName", ""), "passkeys": passkeys})
    except keycloak.KeycloakError as error:
        log.error("member search: %s", error)
        return 502, {"error": "unavailable"}
    return 200, {"members": members}


def create_recovery(body: dict) -> tuple[int, dict]:
    """A new passkey for an existing member (design §4).

    Older live recovery invitations for the same member are withdrawn, so only
    one code can reach that account at a time.
    """
    settings, refusal = _settings(body)
    if refusal:
        return refusal
    user_id = body.get("userId")
    if not isinstance(user_id, str) or not user_id:
        return 400, {"error": "invalid", "fields": {"userId": "invalid"}}
    try:
        user = keycloak.get_user(user_id)
        if user is None or user.get("username", "").startswith("service-account-"):
            return 404, {"error": "not_found"}
        removed = keycloak.remove_passkeys(user_id) if body.get("removePasskeys") is True else 0
    except keycloak.KeycloakError as error:
        log.error("recovery for %s: %s", user_id, error)
        return 502, {"error": "unavailable"}
    with store.lock:
        for older in store.all_invitations():
            if older.kind == "recovery" and older.keycloak_user_id == user_id \
                    and older.state() in ("open", "started"):
                store.revoke(older.id)
        invitation, code = store.create("recovery", settings["locale"], settings["days"],
                                        settings["label"] or user["username"], settings["note"],
                                        user_id=user_id, username=user["username"])
    log.info("desk: recovery invitation %s for %s (%d passkeys removed)",
             invitation.id, user["username"], removed)
    return 200, {**_issued(invitation, code), "removedPasskeys": removed}


# ---- HTTP -------------------------------------------------------------------

_SIGNED_OUT_PAGE = """<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>{title}</title>
<link rel="stylesheet" href="/desk/static/style.css"></head><body><main class="card">
<h1>{title}</h1><p>{text}</p><a class="button primary" href="/desk/login">Anmelden · Sign in</a>
</main></body></html>"""

_REFUSALS = {
    "state": ("Die Anmeldung ist abgelaufen. · The sign-in expired."),
    "exchange": ("Keycloak hat die Anmeldung nicht bestätigt. · Keycloak did not confirm the sign-in."),
    "bad_token": ("Die Anmeldung war ungültig. · The sign-in was not valid."),
    "not_operator": ("Dieses Konto darf keine Einladungen ausstellen. · "
                     "This account may not issue invitations."),
}

_API_POST = re.compile(r"^/desk/api/invitations/(\d+)/(revoke|renew)$")


class DeskHandler(web.BaseHandler):
    def _cookie(self, name: str) -> str | None:
        jar = SimpleCookie()
        try:
            jar.load(self.headers.get("Cookie", ""))
        except Exception:
            return None
        return jar[name].value if name in jar else None

    def _lang(self) -> str:
        accept = self.headers.get("Accept-Language", "").lower()
        return "de" if accept.startswith("de") else "en"

    def _page(self, status: int, title: str, text: str, headers=None):
        body = _SIGNED_OUT_PAGE.format(title=title, text=text).encode()
        self.send(status, body, "text/html; charset=utf-8", headers=headers)

    def _session(self) -> dict | None:
        return session(self._cookie(SESSION_COOKIE))

    def do_GET(self):
        url = urllib.parse.urlsplit(self.path)
        path = url.path
        if path == "/desk":
            self.redirect("/desk/")
        elif path == "/desk/":
            if self._session():
                self.send_file("desk.html", "no-store")
            else:
                self.redirect("/desk/login")
        elif path == "/desk/login":
            query = dict(urllib.parse.parse_qsl(url.query))
            location, state = start_login(query.get("lang") or self._lang())
            self.redirect(location, [("Set-Cookie", f"{STATE_COOKIE}={state}; Path=/desk/callback; "
                                                    f"Max-Age={LOGIN_SECONDS}; HttpOnly; Secure; SameSite=Lax")])
        elif path == "/desk/callback":
            query = dict(urllib.parse.parse_qsl(url.query))
            clear_state = ("Set-Cookie", f"{STATE_COOKIE}=; Path=/desk/callback; Max-Age=0; "
                                         "HttpOnly; Secure; SameSite=Lax")
            try:
                sid = finish_login(query.get("state", ""), self._cookie(STATE_COOKIE),
                                   query.get("code", ""))
            except Refused as refusal:
                self._page(403, "Anmeldung fehlgeschlagen · Sign-in failed",
                           _REFUSALS.get(str(refusal), str(refusal)), [clear_state])
                return
            # A page that moves on by itself rather than a redirect: the session
            # cookie is SameSite=Strict, and a redirect chain that began at
            # Keycloak would not carry it on the next request.
            body = (b'<!doctype html><meta charset="utf-8">'
                    b'<meta http-equiv="refresh" content="0;url=/desk/"><a href="/desk/">/desk/</a>')
            self.send(200, body, "text/html; charset=utf-8", headers=[
                clear_state,
                ("Set-Cookie", f"{SESSION_COOKIE}={sid}; Path=/desk; "
                               f"Max-Age={config.DESK_SESSION_SECONDS}; HttpOnly; Secure; SameSite=Strict")])
        elif path == "/desk/logout":
            end_session(self._cookie(SESSION_COOKIE))
            logout = (f"{config.PUBLIC_IDENTITY_URL}/realms/{config.DESK_REALM}/protocol/openid-connect/logout?"
                      + urllib.parse.urlencode({"client_id": config.DESK_CLIENT_ID,
                                                "post_logout_redirect_uri": f"{config.JOIN_URL}/desk/signed-out"}))
            self.redirect(logout, [("Set-Cookie", f"{SESSION_COOKIE}=; Path=/desk; Max-Age=0; "
                                                  "HttpOnly; Secure; SameSite=Strict")])
        elif path == "/desk/signed-out":
            self._page(200, "Abgemeldet · Signed out",
                       "Du bist abgemeldet. · You are signed out.")
        elif path.startswith("/desk/static/"):
            self.send_file(path[len("/desk/static/"):], "public, max-age=300")
        elif path.startswith("/desk/api/"):
            current = self._session()
            if not current:
                self.send_json(401, {"error": "signed_out"})
            elif path == "/desk/api/me":
                self.send_json(200, {"username": current["username"], "joinUrl": config.JOIN_URL,
                                     "defaultDays": config.DEFAULT_DAYS, "maxDays": config.MAX_DAYS})
            elif path == "/desk/api/invitations":
                self._answer(list_invitations)
            elif path == "/desk/api/members":
                query = dict(urllib.parse.parse_qsl(url.query))
                self._answer(lambda: search_members(query.get("q", "")))
            else:
                self.send_json(404, {"error": "not_found"})
        else:
            self.send_json(404, {"error": "not_found"})

    do_HEAD = do_GET

    def do_POST(self):
        path = urllib.parse.urlsplit(self.path).path
        if not path.startswith("/desk/api/"):
            self.send_json(404, {"error": "not_found"})
            return
        if not self._session():
            self.send_json(401, {"error": "signed_out"})
            return
        # A header a cross-site form cannot set, on top of SameSite=Strict.
        if self.headers.get("X-Join-Desk") != "1":
            self.send_json(403, {"error": "csrf"})
            return
        body, refusal = self.read_json(max_body=32768)
        if refusal:
            self.send_json(*refusal)
            return
        match = _API_POST.match(path)
        if path == "/desk/api/invitations":
            self._answer(lambda: create(body))
        elif path == "/desk/api/invitations/bulk":
            self._answer(lambda: create_bulk(body))
        elif path == "/desk/api/recovery":
            self._answer(lambda: create_recovery(body))
        elif match:
            action = revoke if match.group(2) == "revoke" else renew
            self._answer(lambda: action(int(match.group(1)), body))
        else:
            self.send_json(404, {"error": "not_found"})

    def _answer(self, handler):
        try:
            status, payload = handler()
        except Exception:
            log.exception("%s failed", self.path)
            status, payload = 500, {"error": "unavailable"}
        self.send_json(status, payload)
