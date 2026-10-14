"""Keycloak, as the `join` service account, over the in-cluster Service.

Standard library only (urllib), like the Hermes and Remediation agents. The
account holds manage-users, view-users and query-users and nothing else
(tenants/join/join-keycloak-setup-job.yaml).
"""
import json
import logging
import threading
import time
import urllib.error
import urllib.parse
import urllib.request

import config

log = logging.getLogger("keycloak")

_token: dict = {"value": None, "expires": 0.0}
_token_lock = threading.Lock()


class KeycloakError(Exception):
    def __init__(self, status: int, detail: str):
        super().__init__(f"Keycloak answered {status}: {detail[:300]}")
        self.status = status
        self.detail = detail


class Conflict(Exception):
    """The username or the email address already belongs to an account."""

    def __init__(self, field: str):
        super().__init__(f"{field} already taken")
        self.field = field


def _request(method: str, url: str, *, body=None, form=None, token: str | None = None,
             timeout: float = 15) -> tuple[int, dict, bytes]:
    headers = {"Accept": "application/json"}
    data = None
    if form is not None:
        data = urllib.parse.urlencode(form).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    elif body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    if token:
        headers["Authorization"] = f"Bearer {token}"
    request = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, dict(response.headers), response.read()
    except urllib.error.HTTPError as error:
        with error:
            return error.code, dict(error.headers or {}), error.read()


def _service_token() -> str:
    with _token_lock:
        if _token["value"] and time.time() < _token["expires"]:
            return _token["value"]
        status, _, raw = _request(
            "POST", f"{config.KEYCLOAK_URL}/realms/{config.REALM}/protocol/openid-connect/token",
            form={"grant_type": "client_credentials", "client_id": config.CLIENT_ID,
                  "client_secret": config.CLIENT_SECRET})
        if status != 200:
            raise KeycloakError(status, raw.decode(errors="replace"))
        answer = json.loads(raw)
        _token["value"] = answer["access_token"]
        # Renew half a minute early so a request never starts with a token
        # that expires on its way.
        _token["expires"] = time.time() + max(int(answer.get("expires_in", 60)) - 30, 5)
        return _token["value"]


def _admin(method: str, path: str, *, body=None, params=None) -> tuple[int, dict, bytes]:
    url = f"{config.KEYCLOAK_URL}/admin/realms/{config.REALM}{path}"
    if params:
        url += "?" + urllib.parse.urlencode(params)
    return _request(method, url, body=body, token=_service_token())


def _json_or_raise(status: int, raw: bytes, expected: int = 200):
    if status != expected:
        raise KeycloakError(status, raw.decode(errors="replace"))
    return json.loads(raw) if raw else None


def find_user(username: str) -> dict | None:
    """Exact lookup — Keycloak's filter is a prefix match without `exact`."""
    status, _, raw = _admin("GET", "/users",
                            params={"username": username, "exact": "true", "briefRepresentation": "true"})
    for user in _json_or_raise(status, raw):
        if user.get("username", "").lower() == username.lower():
            return user
    return None


def email_taken(email: str) -> bool:
    status, _, raw = _admin("GET", "/users", params={"email": email, "exact": "true",
                                                     "briefRepresentation": "true"})
    return any((u.get("email") or "").lower() == email.lower() for u in _json_or_raise(status, raw))


def get_user(user_id: str) -> dict | None:
    status, _, raw = _admin("GET", f"/users/{urllib.parse.quote(user_id, safe='')}")
    if status == 404:
        return None
    return _json_or_raise(status, raw)


def create_user(username: str, first_name: str, last_name: str, email: str, locale: str) -> str:
    """Create the member and return the new account's id.

    `emailVerified` is set because nothing will ever verify it: onboarding is out
    of band and the realm does not mail members (ADR 0049). It is contact
    metadata, as in admin-tools/bulk-invite.py. `locale` is what lets Keycloak's
    own pages follow the language chosen on the join page.
    """
    status, headers, raw = _admin("POST", "/users", body={
        "username": username,
        "enabled": True,
        "firstName": first_name,
        "lastName": last_name,
        "email": email,
        "emailVerified": True,
        "attributes": {"locale": [locale]},
    })
    if status == 409:
        detail = raw.decode(errors="replace").lower()
        raise Conflict("email" if "email" in detail else "username")
    if status != 201:
        raise KeycloakError(status, raw.decode(errors="replace"))
    location = next((v for k, v in headers.items() if k.lower() == "location"), "")
    if location:
        return location.rstrip("/").rsplit("/", 1)[-1]
    user = find_user(username)
    if not user:
        raise KeycloakError(201, "account created but could not be read back")
    return user["id"]


def set_locale(user_id: str, locale: str) -> None:
    """Keep the account's language in step with the latest choice on the join page."""
    user = get_user(user_id)
    if user is None:
        return
    attributes = user.get("attributes") or {}
    if attributes.get("locale") == [locale]:
        return
    attributes["locale"] = [locale]
    user["attributes"] = attributes
    status, _, raw = _admin("PUT", f"/users/{urllib.parse.quote(user_id, safe='')}", body=user)
    if status != 204:
        # The link still carries kc_locale, so this costs later sign-ins their
        # language, not the passkey pages. Not worth failing the member over.
        log.warning("could not set locale of %s: %s %s", user_id, status, raw[:200])


def delete_user(user_id: str) -> None:
    status, _, raw = _admin("DELETE", f"/users/{urllib.parse.quote(user_id, safe='')}")
    if status not in (204, 404):
        raise KeycloakError(status, raw.decode(errors="replace"))


def search_users(query: str, limit: int = 20) -> list[dict]:
    """Members whose username, name or address contains `query`."""
    status, _, raw = _admin("GET", "/users", params={"search": query, "max": str(limit),
                                                     "briefRepresentation": "true"})
    return [u for u in _json_or_raise(status, raw)
            if not u.get("username", "").startswith("service-account-")]


def credentials(user_id: str) -> list[dict]:
    status, _, raw = _admin("GET", f"/users/{urllib.parse.quote(user_id, safe='')}/credentials")
    if status == 404:
        return []
    return _json_or_raise(status, raw)


def remove_passkeys(user_id: str) -> int:
    """Delete every passkey of the account, and end its sessions.

    For a member whose device is lost — and may be in someone else's hands, so
    a session that device still holds must not outlive the passkey. Returns how
    many passkeys were removed.
    """
    quoted = urllib.parse.quote(user_id, safe="")
    removed = 0
    for credential in credentials(user_id):
        if credential.get("type") != config.PASSKEY_CREDENTIAL_TYPE:
            continue
        status, _, raw = _admin("DELETE", f"/users/{quoted}/credentials/"
                                          f"{urllib.parse.quote(credential['id'], safe='')}")
        if status not in (204, 404):
            raise KeycloakError(status, raw.decode(errors="replace"))
        removed += 1
    status, _, raw = _admin("POST", f"/users/{quoted}/logout")
    if status not in (204, 404):
        raise KeycloakError(status, raw.decode(errors="replace"))
    return removed


def has_passkey_since(user_id: str, since: int) -> bool:
    """Whether the account holds a passkey registered at or after `since` (epoch s).

    The cut-off is what lets a recovery invitation count as done only once a
    *new* passkey exists, while an old one has not been removed yet.
    """
    for credential in credentials(user_id):
        if credential.get("type") == config.PASSKEY_CREDENTIAL_TYPE \
                and int(credential.get("createdDate", 0)) >= since * 1000:
            return True
    return False


def mint_link(user_id: str, locale: str) -> str:
    """The onboarding link for `user_id`, in `locale`.

    Minted by the action-token-link SPI (infrastructure/keycloak-spi/), with the
    realm's admin lifespan of 12 hours — which no longer matters, because a
    fresh one is minted whenever the member opens their invitation. The token
    is single-use: Keycloak invalidates it once the actions are done.
    """
    status, _, raw = _request(
        "POST", f"{config.KEYCLOAK_URL}/realms/{config.REALM}/action-token-link/generate-link",
        body={
            "userId": user_id,
            "actions": config.REQUIRED_ACTIONS,
            "clientId": config.CLIENT_ID,
            # Must match a redirect registered on the `join` client, or Keycloak
            # rejects it — but only when the member clicks. The language is a
            # path segment rather than a query so the match against
            # https://join.<domain>/* never depends on query handling.
            "redirectUri": f"{config.JOIN_URL}/done/{locale}",
            # join's own token type (docs/adr/0059): no confirmation page, and
            # the member is signed in at the end. An older extension ignores
            # the field and mints Keycloak's execute-actions token instead.
            "tokenType": "smallworlds-join",
        },
        token=_service_token())
    link = _json_or_raise(status, raw).get("link", "")
    # The issuer is fixed to KC_HOSTNAME today (observed 2026-10-14, design §6).
    # A link on any other base would be rejected at click time, far from here.
    expected = f"{config.PUBLIC_IDENTITY_URL}/realms/{config.REALM}/"
    if not link.startswith(expected):
        raise KeycloakError(status, f"minted link is not under {expected}: {link[:80]}")
    # kc_locale wins over every other source in Keycloak's locale selector, so
    # the passkey pages are right on the very first visit (design §8).
    return f"{link}{'&' if '?' in link else '?'}kc_locale={locale}"


def exchange_desk_code(code: str, verifier: str) -> dict:
    """Trade the Desk's authorization code for the Operator's tokens (design §9).

    Straight from the master realm's token endpoint over the Service, so the
    answer is trusted without a signature check — the same trust every init job
    places in that channel. The caller still checks issuer, client and role.
    """
    status, _, raw = _request(
        "POST", f"{config.KEYCLOAK_URL}/realms/{config.DESK_REALM}/protocol/openid-connect/token",
        form={"grant_type": "authorization_code", "code": code, "code_verifier": verifier,
              "client_id": config.DESK_CLIENT_ID, "redirect_uri": config.desk_redirect_uri()})
    return _json_or_raise(status, raw)
