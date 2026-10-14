"""Tests for join's public half, against a fake Keycloak (design §11).

    python3 infrastructure/kubernetes/tenants/join/tests/test_join.py

Standard library only. Covers the rules that make the join page safe to expose:
codes and usernames, the invitation lifecycle, that the browser can never name
an account, that a link on the wrong issuer is refused, and that nothing but the
member-facing routes exists on the public port.
"""
import json
import os
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
TENANT = os.path.dirname(HERE)
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(TENANT, "src"))

from fake_keycloak import FakeKeycloak  # noqa: E402

FAKE = FakeKeycloak()
SCRATCH = tempfile.mkdtemp(prefix="join-test-")
os.environ.update({
    "KEYCLOAK_URL": FAKE.url,
    "KEYCLOAK_CLIENT_SECRET": "test-secret",
    "PUBLIC_IDENTITY_URL": "https://identity.example.test",
    "JOIN_URL": "https://join.example.test",
    "DASHBOARD_URL": "https://dashboard.example.test",
    "DB_PATH": os.path.join(SCRATCH, "invitations.db"),
    "STATIC_DIR": os.path.join(TENANT, "static"),
    "BIND": "127.0.0.1",
})

import logging  # noqa: E402

import codes  # noqa: E402
import config  # noqa: E402
import main  # noqa: E402
import member  # noqa: E402
import store  # noqa: E402
import usernames  # noqa: E402

logging.disable(logging.CRITICAL)  # the server's request log is noise here
store.init()
SERVER = main.serve_public(0)
threading.Thread(target=SERVER.serve_forever, daemon=True).start()
BASE = f"http://127.0.0.1:{SERVER.server_port}"


def call(method, path, body=None, headers=None, raw=None):
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    request = urllib.request.Request(BASE + path, data=data, method=method)
    if body is not None or raw is not None:
        request.add_header("Content-Type", "application/json")
    for name, value in (headers or {}).items():
        request.add_header(name, value)
    try:
        with urllib.request.urlopen(request) as response:
            return response.status, dict(response.headers), response.read()
    except urllib.error.HTTPError as error:
        with error:
            return error.code, dict(error.headers), error.read()


def api(path, body, headers=None):
    status, _, raw = call("POST", path, body, headers)
    return status, json.loads(raw or b"{}")


PROFILE = {"firstName": "Anna", "lastName": "Muster", "email": "anna@example.org"}


class Fresh(unittest.TestCase):
    """Each test gets an empty Keycloak, an empty database and fresh limits."""

    def setUp(self):
        with FAKE.lock:
            FAKE.users.clear()
            FAKE.credentials.clear()
            FAKE.minted.clear()
            FAKE.issuer = "https://identity.example.test"
            FAKE.down = False
        if os.path.exists(config.DB_PATH):
            os.remove(config.DB_PATH)
        for suffix in ("-wal", "-shm"):
            if os.path.exists(config.DB_PATH + suffix):
                os.remove(config.DB_PATH + suffix)
        store.init()
        member.failed_lookups = member.Limiter(5, 3600)
        member.username_checks = member.Limiter(120, 600)

    def invite(self, **kwargs):
        kwargs.setdefault("kind", "new")
        kwargs.setdefault("locale", "de")
        return store.create(**kwargs)


class Codes(unittest.TestCase):
    def test_new_codes_use_the_alphabet_and_length(self):
        for _ in range(200):
            code = codes.new_code()
            self.assertEqual(len(code), 10)
            self.assertTrue(set(code) <= set(codes.ALPHABET))

    def test_typing_variants_normalize_to_one_code(self):
        self.assertEqual(codes.normalize("k7qfm-2pdx9"), "K7QFM2PDX9")
        self.assertEqual(codes.normalize(" K7QFM 2PDX9 "), "K7QFM2PDX9")
        self.assertEqual(codes.normalize("O1LIA-BCDEF"), "0111ABCDEF")

    def test_non_codes_are_rejected(self):
        for text in ("", "SHORT", "K7QFM-2PDX9X", "K7QFM-2PDXU", None, 42):
            self.assertIsNone(codes.normalize(text), text)

    def test_display_groups_in_fives(self):
        self.assertEqual(codes.display("K7QFM2PDX9"), "K7QFM-2PDX9")


class Usernames(unittest.TestCase):
    def test_allowed(self):
        for name in ("anna", "ben.k", "c_d-e", "a1b", "x" * 32):
            self.assertIsNone(usernames.problem(name), name)

    def test_refused(self):
        cases = {
            "ab": "username_length", "x" * 33: "username_length",
            "anna@home": "username_characters", ".anna": "username_characters",
            "anna.": "username_characters", "an..na": "username_characters",
            "an-_na": "username_characters", "Anna": "username_characters",
            "jürg": "username_characters", "an na": "username_characters",
            "admin": "username_reserved", "postmaster": "username_reserved",
            "operator": "username_reserved",
        }
        for name, reason in cases.items():
            self.assertEqual(usernames.problem(name), reason, name)

    def test_normalize_lowercases_and_trims(self):
        self.assertEqual(usernames.normalize("  Anna.B "), "anna.b")


class Lifecycle(Fresh):
    def test_states(self):
        now = time.time()
        invitation, _ = self.invite(now=now)
        self.assertEqual(invitation.state(now), "open")
        self.assertEqual(invitation.state(now + 14 * 86400), "expired")
        self.assertTrue(store.claim(invitation.id, "u1", "anna", now=now))
        self.assertEqual(store.by_id(invitation.id).state(now), "started")
        store.complete(invitation.id, now=now)
        self.assertEqual(store.by_id(invitation.id).state(now + 30 * 86400), "done")

    def test_an_invitation_binds_one_account_only(self):
        invitation, _ = self.invite()
        self.assertTrue(store.claim(invitation.id, "u1", "anna"))
        self.assertFalse(store.claim(invitation.id, "u2", "ben"))
        self.assertEqual(store.by_id(invitation.id).keycloak_user_id, "u1")

    def test_revoked_and_done_cannot_be_revoked_again(self):
        invitation, _ = self.invite()
        self.assertTrue(store.revoke(invitation.id))
        self.assertFalse(store.revoke(invitation.id))
        other, _ = self.invite()
        store.complete(other.id)
        self.assertFalse(store.revoke(other.id))

    def test_the_code_is_not_stored(self):
        _, code = self.invite()
        with open(config.DB_PATH, "rb") as handle:
            self.assertNotIn(code.encode(), handle.read())

    def test_invalid_creation(self):
        with self.assertRaises(ValueError):
            store.create("new", "fr")
        with self.assertRaises(ValueError):
            store.create("new", "de", days=31)
        with self.assertRaises(ValueError):
            store.create("recovery", "de")


class PublicSurface(Fresh):
    def test_pages_carry_the_security_headers(self):
        for path in ("/", "/done/de", "/done/en"):
            status, headers, body = call("GET", path)
            self.assertEqual(status, 200, path)
            self.assertIn("default-src 'none'", headers["Content-Security-Policy"])
            self.assertEqual(headers["Referrer-Policy"], "no-referrer")
            self.assertEqual(headers["Cache-Control"], "no-store")

    def test_static_files_are_served(self):
        for name in ("join.js", "i18n.js", "done.js", "style.css"):
            status, headers, _ = call("GET", f"/static/{name}")
            self.assertEqual(status, 200, name)

    def test_nothing_else_exists(self):
        for path in ("/desk", "/desk/", "/desk/callback", "/K7QFM-2PDX9", "/static/../src/main.py",
                     "/static/%2e%2e/src/main.py", "/done/fr", "/api/invitation"):
            status, _, _ = call("GET", path)
            self.assertEqual(status, 404, path)
        status, _ = api("/desk/api/invitations", {"label": "x"})
        self.assertEqual(status, 404)

    def test_posts_must_be_json_and_small(self):
        status, _, _ = call("POST", "/api/invitation", raw=b"code=x",
                            headers={"Content-Type": "application/x-www-form-urlencoded"})
        self.assertEqual(status, 415)
        status, _, _ = call("POST", "/api/invitation", raw=b"{" + b" " * 5000 + b"}")
        self.assertEqual(status, 400)
        status, _, _ = call("POST", "/api/invitation", raw=b"[1]")
        self.assertEqual(status, 400)

    def test_config_names_the_dashboard(self):
        status, _, raw = call("GET", "/api/config")
        self.assertEqual(json.loads(raw), {"dashboardUrl": "https://dashboard.example.test"})


class Lookup(Fresh):
    def test_open_invitation(self):
        _, code = self.invite(locale="en")
        status, answer = api("/api/invitation", {"code": codes.display(code).lower()})
        self.assertEqual(status, 200)
        self.assertEqual(answer, {"state": "open", "kind": "new", "locale": "en"})

    def test_unknown_codes_are_limited_per_source(self):
        for _ in range(5):
            self.assertEqual(api("/api/invitation", {"code": "AAAAA-AAAAA"})[0], 404)
        self.assertEqual(api("/api/invitation", {"code": "AAAAA-AAAAA"}), (429, {"error": "too_many"}))
        # Even a valid code waits out the limit, or the limit would be no limit.
        _, code = self.invite()
        self.assertEqual(api("/api/invitation", {"code": code})[0], 429)
        # Another visitor, as Traefik reports it (last X-Forwarded-For entry), is unaffected —
        # and a forged first entry does not buy a fresh allowance.
        self.assertEqual(api("/api/invitation", {"code": code},
                             {"X-Forwarded-For": "127.0.0.1, 203.0.113.9"})[0], 200)
        self.assertEqual(api("/api/invitation", {"code": code},
                             {"X-Forwarded-For": "203.0.113.10, 127.0.0.1"})[0], 429)

    def test_ended_invitations(self):
        expired, expired_code = self.invite(now=time.time() - 15 * 86400)
        revoked, revoked_code = self.invite()
        store.revoke(revoked.id)
        self.assertEqual(api("/api/invitation", {"code": expired_code}), (410, {"error": "expired"}))
        self.assertEqual(api("/api/invitation", {"code": revoked_code}), (410, {"error": "revoked"}))


class UsernameCheck(Fresh):
    def test_needs_a_valid_code(self):
        status, answer = api("/api/username", {"code": "AAAAA-AAAAA", "username": "anna"})
        self.assertEqual(status, 404)

    def test_answers(self):
        FAKE.add_user("taken")
        _, code = self.invite()
        check = lambda name: api("/api/username", {"code": code, "username": name})[1]
        self.assertEqual(check("Anna"), {"username": "anna", "available": True})
        self.assertEqual(check("taken")["reason"], "username_taken")
        self.assertEqual(check("admin")["reason"], "username_reserved")
        self.assertEqual(check("a@b")["reason"], "username_characters")

    def test_not_for_a_started_invitation(self):
        invitation, code = self.invite()
        store.claim(invitation.id, FAKE.add_user("anna"), "anna")
        self.assertEqual(api("/api/username", {"code": code, "username": "ben"}),
                         (409, {"error": "no_choice"}))


class Redeem(Fresh):
    def redeem(self, code, **fields):
        return api("/api/redeem", {"code": code, **fields})

    def test_creates_the_account_and_hands_back_a_link(self):
        invitation, code = self.invite(locale="de")
        status, answer = self.redeem(code, username=" Anna ", locale="en", **PROFILE)
        self.assertEqual(status, 200, answer)
        self.assertTrue(answer["redirect"].startswith(
            "https://identity.example.test/realms/smallworlds/login-actions/action-token?key="))
        self.assertTrue(answer["redirect"].endswith("&kc_locale=en"))

        user = FAKE.user_by_name("anna")
        self.assertEqual(user["firstName"], "Anna")
        self.assertEqual(user["email"], "anna@example.org")
        self.assertTrue(user["emailVerified"])
        self.assertEqual(user["attributes"], {"locale": ["en"]})

        minted = FAKE.minted[-1]
        self.assertEqual(minted["userId"], user["id"])
        self.assertEqual(minted["clientId"], "join")
        self.assertEqual(minted["redirectUri"], "https://join.example.test/done/en")
        self.assertEqual(minted["actions"], ["webauthn-register-passwordless",
                                             "CONFIGURE_RECOVERY_AUTHN_CODES"])

        stored = store.by_id(invitation.id)
        self.assertEqual(stored.state(), "started")
        self.assertEqual((stored.keycloak_user_id, stored.username), (user["id"], "anna"))

    def test_opening_again_reuses_the_account(self):
        _, code = self.invite()
        self.redeem(code, username="anna", **PROFILE)
        self.assertEqual(api("/api/invitation", {"code": code})[1],
                         {"state": "started", "kind": "new", "locale": "de", "username": "anna"})
        status, answer = self.redeem(code, username="mallory", locale="en",
                                     firstName="M", lastName="X", email="m@example.org")
        self.assertEqual(status, 200)
        self.assertEqual(len(FAKE.users), 1)
        self.assertIsNone(FAKE.user_by_name("mallory"))
        self.assertEqual(FAKE.minted[-1]["userId"], FAKE.user_by_name("anna")["id"])
        # The latest language choice follows the account.
        self.assertEqual(FAKE.user_by_name("anna")["attributes"]["locale"], ["en"])

    def test_a_passkey_uses_the_invitation_up(self):
        invitation, code = self.invite()
        self.redeem(code, username="anna", **PROFILE)
        FAKE.add_passkey(FAKE.user_by_name("anna")["id"])
        self.assertEqual(api("/api/invitation", {"code": code}), (410, {"error": "used"}))
        self.assertEqual(self.redeem(code), (410, {"error": "used"}))
        self.assertEqual(store.by_id(invitation.id).state(), "done")

    def test_the_browser_cannot_name_an_account(self):
        victim = FAKE.add_user("victim", passkey_at=time.time() - 100)
        _, code = self.invite()
        status, _ = self.redeem(code, username="anna", userId=victim,
                                keycloak_user_id=victim, **PROFILE)
        self.assertEqual(status, 200)
        self.assertNotEqual(FAKE.minted[-1]["userId"], victim)
        self.assertEqual(FAKE.minted[-1]["userId"], FAKE.user_by_name("anna")["id"])

    def test_profile_problems_are_reported_by_field(self):
        _, code = self.invite()
        status, answer = self.redeem(code, username="a", firstName="", lastName="<b>",
                                     email="not-an-address")
        self.assertEqual(status, 400)
        self.assertEqual(answer["fields"], {"username": "username_length", "firstName": "required",
                                            "lastName": "name_characters", "email": "email_format"})
        self.assertEqual(FAKE.users, {})

    def test_taken_username_and_email(self):
        FAKE.add_user("anna", email="anna@example.org")
        _, code = self.invite()
        self.assertEqual(self.redeem(code, username="anna", **PROFILE)[1]["fields"],
                         {"username": "username_taken"})
        self.assertEqual(self.redeem(code, username="anna2", **PROFILE)[1]["fields"],
                         {"email": "email_taken"})
        # Nothing was claimed, so the member can still choose.
        self.assertEqual(self.redeem(code, username="anna2", firstName="Anna", lastName="M",
                                     email="other@example.org")[0], 200)

    def test_a_link_on_the_wrong_issuer_is_refused(self):
        invitation, code = self.invite()
        FAKE.issuer = "http://keycloak-keycloakx-http.keycloak"
        self.assertEqual(self.redeem(code, username="anna", **PROFILE), (502, {"error": "unavailable"}))
        # The account exists and is recorded, so fixing Keycloak is enough.
        self.assertEqual(store.by_id(invitation.id).state(), "started")
        FAKE.issuer = "https://identity.example.test"
        self.assertEqual(self.redeem(code)[0], 200)
        self.assertEqual(len(FAKE.users), 1)

    def test_keycloak_down(self):
        _, code = self.invite()
        FAKE.down = True
        self.assertEqual(self.redeem(code, username="anna", **PROFILE), (502, {"error": "unavailable"}))

    def test_deleted_account(self):
        _, code = self.invite()
        self.redeem(code, username="anna", **PROFILE)
        with FAKE.lock:
            FAKE.users.clear()
        self.assertEqual(self.redeem(code), (410, {"error": "account_gone"}))

    def test_unsupported_locale_falls_back_to_the_invitation(self):
        _, code = self.invite(locale="de")
        _, answer = self.redeem(code, username="anna", locale="fr", **PROFILE)
        self.assertTrue(answer["redirect"].endswith("kc_locale=de"))


class Recovery(Fresh):
    def test_a_new_passkey_for_an_existing_member(self):
        anna = FAKE.add_user("anna", passkey_at=time.time() - 86400)
        invitation, code = store.create("recovery", "en", user_id=anna, username="anna")
        self.assertEqual(api("/api/invitation", {"code": code})[1],
                         {"state": "open", "kind": "recovery", "locale": "en", "username": "anna"})
        status, answer = api("/api/redeem", {"code": code, "username": "evil", **PROFILE})
        self.assertEqual(status, 200)
        self.assertEqual(FAKE.minted[-1]["userId"], anna)
        self.assertEqual(len(FAKE.users), 1)
        # The old passkey does not count; only one registered after the invitation.
        self.assertEqual(store.by_id(invitation.id).state(), "started")
        self.assertEqual(api("/api/invitation", {"code": code})[0], 200)
        FAKE.add_passkey(anna)
        self.assertEqual(api("/api/invitation", {"code": code}), (410, {"error": "used"}))


if __name__ == "__main__":
    unittest.main(verbosity=1)
