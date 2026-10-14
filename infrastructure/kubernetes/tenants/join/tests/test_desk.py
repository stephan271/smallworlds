"""Tests for the Invitation Desk, against a fake Keycloak (design §4, §9, §11).

    python3 infrastructure/kubernetes/tenants/join/tests/test_desk.py

Covers the sign-in (PKCE, state, the master `admin` role), that every API needs
a session and the CSRF header, issuing, renewing and withdrawing invitations,
deleting only passkey-less accounts, recovery with passkey removal, and that the
two listeners serve nothing of each other's.
"""
import json
import time
import unittest
import urllib.parse

from harness import DESK_BASE, FAKE, Fresh, api, call  # noqa: E402  (sets up the environment first)

import codes  # noqa: E402
import desk  # noqa: E402
import store  # noqa: E402


def cookies(headers) -> dict:
    jar = {}
    for line in headers.get_all("Set-Cookie") or []:
        name, _, rest = line.partition("=")
        jar[name] = {"value": rest.split(";", 1)[0], "attributes": line}
    return jar


def sign_in(test) -> str:
    """Walk the browser's part of the sign-in and return the session id."""
    status, headers, _ = call("GET", "/desk/login?lang=de", base=DESK_BASE)
    test.assertEqual(status, 302)
    state_cookie = cookies(headers)[desk.STATE_COOKIE]["value"]
    auth = urllib.parse.urlsplit(headers["Location"])
    test.assertEqual(f"{auth.scheme}://{auth.netloc}", "https://identity.example.test")
    # The browser would now visit Keycloak; the fake stands in for it.
    status, headers, _ = call("GET", f"{auth.path}?{auth.query}", base=FAKE.url)
    callback = urllib.parse.urlsplit(headers["Location"])
    test.assertEqual(f"{callback.scheme}://{callback.netloc}{callback.path}",
                     "https://join.example.test/desk/callback")
    status, headers, body = call("GET", f"/desk/callback?{callback.query}", base=DESK_BASE,
                                 headers={"Cookie": f"{desk.STATE_COOKIE}={state_cookie}"})
    if status != 200:
        return ""
    return cookies(headers)[desk.SESSION_COOKIE]["value"]


class Desk(Fresh):
    def setUp(self):
        super().setUp()
        self.sid = sign_in(self)
        self.assertTrue(self.sid)

    def get(self, path, sid=None):
        status, _, raw = call("GET", path, base=DESK_BASE,
                              headers={"Cookie": f"{desk.SESSION_COOKIE}={sid or self.sid}"})
        return status, json.loads(raw or b"{}")

    def post(self, path, body, sid=None, csrf=True):
        headers = {"Cookie": f"{desk.SESSION_COOKIE}={sid or self.sid}"}
        if csrf:
            headers["X-Join-Desk"] = "1"
        status, _, raw = call("POST", path, body, headers, base=DESK_BASE)
        return status, json.loads(raw or b"{}")

    def issue(self, **body):
        status, answer = self.post("/desk/api/invitations", {"label": "Anna", **body})
        self.assertEqual(status, 200, answer)
        return answer


class SignIn(Fresh):
    def test_the_desk_sends_a_stranger_to_sign_in(self):
        status, headers, _ = call("GET", "/desk/", base=DESK_BASE)
        self.assertEqual((status, headers["Location"]), (302, "/desk/login"))

    def test_the_login_redirect_asks_for_pkce_in_the_chosen_language(self):
        _, headers, _ = call("GET", "/desk/login?lang=de", base=DESK_BASE)
        query = dict(urllib.parse.parse_qsl(urllib.parse.urlsplit(headers["Location"]).query))
        self.assertEqual(query["client_id"], "join-desk")
        self.assertEqual(query["code_challenge_method"], "S256")
        self.assertEqual(query["redirect_uri"], "https://join.example.test/desk/callback")
        self.assertEqual(query["ui_locales"], "de")
        self.assertTrue(urllib.parse.urlsplit(headers["Location"]).path
                        .startswith("/realms/master/protocol/openid-connect/auth"))

    def test_a_signed_in_operator_gets_the_desk(self):
        sid = sign_in(self)
        status, headers, body = call("GET", "/desk/", base=DESK_BASE,
                                     headers={"Cookie": f"{desk.SESSION_COOKIE}={sid}"})
        self.assertEqual(status, 200)
        self.assertIn(b"desk.js", body)

    def test_the_session_cookie_is_strict(self):
        _, headers, _ = call("GET", "/desk/login", base=DESK_BASE)
        state = cookies(headers)[desk.STATE_COOKIE]["value"]
        auth = urllib.parse.urlsplit(headers["Location"])
        _, headers, _ = call("GET", f"{auth.path}?{auth.query}", base=FAKE.url)
        query = urllib.parse.urlsplit(headers["Location"]).query
        _, headers, _ = call("GET", f"/desk/callback?{query}", base=DESK_BASE,
                             headers={"Cookie": f"{desk.STATE_COOKIE}={state}"})
        attributes = cookies(headers)[desk.SESSION_COOKIE]["attributes"]
        for part in ("HttpOnly", "Secure", "SameSite=Strict", "Path=/desk"):
            self.assertIn(part, attributes)

    def test_an_account_without_the_admin_role_is_refused(self):
        FAKE.login_as = {"username": "anna", "roles": ["offline_access"]}
        self.assertEqual(sign_in(self), "")

    def test_a_callback_without_its_state_cookie_is_refused(self):
        _, headers, _ = call("GET", "/desk/login", base=DESK_BASE)
        auth = urllib.parse.urlsplit(headers["Location"])
        _, headers, _ = call("GET", f"{auth.path}?{auth.query}", base=FAKE.url)
        query = urllib.parse.urlsplit(headers["Location"]).query
        status, _, _ = call("GET", f"/desk/callback?{query}", base=DESK_BASE)
        self.assertEqual(status, 403)
        status, _, _ = call("GET", f"/desk/callback?{query}", base=DESK_BASE,
                            headers={"Cookie": f"{desk.STATE_COOKIE}=forged"})
        self.assertEqual(status, 403)

    def test_a_code_works_once(self):
        _, headers, _ = call("GET", "/desk/login", base=DESK_BASE)
        state = cookies(headers)[desk.STATE_COOKIE]["value"]
        auth = urllib.parse.urlsplit(headers["Location"])
        _, headers, _ = call("GET", f"{auth.path}?{auth.query}", base=FAKE.url)
        query = urllib.parse.urlsplit(headers["Location"]).query
        cookie = {"Cookie": f"{desk.STATE_COOKIE}={state}"}
        self.assertEqual(call("GET", f"/desk/callback?{query}", base=DESK_BASE, headers=cookie)[0], 200)
        self.assertEqual(call("GET", f"/desk/callback?{query}", base=DESK_BASE, headers=cookie)[0], 403)

    def test_a_token_from_another_issuer_is_refused(self):
        FAKE.issuer = "https://elsewhere.example.test"
        self.assertEqual(sign_in(self), "")


class Gate(Desk):
    def test_every_api_needs_a_session(self):
        for path in ("/desk/api/me", "/desk/api/invitations", "/desk/api/members?q=an"):
            self.assertEqual(self.get(path, sid="nope"), (401, {"error": "signed_out"}), path)
        self.assertEqual(self.post("/desk/api/invitations", {}, sid="nope")[0], 401)

    def test_posts_need_the_csrf_header(self):
        self.assertEqual(self.post("/desk/api/invitations", {"label": "x"}, csrf=False),
                         (403, {"error": "csrf"}))

    def test_logout_ends_the_session_and_keycloaks(self):
        status, headers, _ = call("GET", "/desk/logout", base=DESK_BASE,
                                  headers={"Cookie": f"{desk.SESSION_COOKIE}={self.sid}"})
        self.assertEqual(status, 302)
        location = urllib.parse.urlsplit(headers["Location"])
        self.assertEqual(location.path, "/realms/master/protocol/openid-connect/logout")
        self.assertIn("post_logout_redirect_uri=https%3A%2F%2Fjoin.example.test%2Fdesk%2Fsigned-out",
                      location.query)
        self.assertEqual(self.get("/desk/api/me")[0], 401)

    def test_the_listeners_serve_nothing_of_each_other(self):
        for path in ("/", "/done/de", "/api/config", "/healthz"):
            self.assertEqual(call("GET", path, base=DESK_BASE)[0], 404, path)
        self.assertEqual(call("POST", "/api/invitation", {"code": "x"}, base=DESK_BASE)[0], 404)
        for path in ("/desk/", "/desk/login", "/desk/callback", "/desk/api/me", "/desk/static/desk.js"):
            self.assertEqual(call("GET", path)[0], 404, path)

    def test_me(self):
        status, answer = self.get("/desk/api/me")
        self.assertEqual((status, answer["username"]), (200, "operator"))


class Issuing(Desk):
    def test_issue_and_see_it_listed(self):
        answer = self.issue(locale="en", days=7, note="met at the market")
        self.assertRegex(answer["code"], r"^[0-9A-Z]{5}-[0-9A-Z]{5}$")
        self.assertEqual(answer["link"], f"https://join.example.test/#{answer['code']}")
        self.assertEqual(answer["invitation"]["state"], "open")
        self.assertAlmostEqual(answer["invitation"]["expiresAt"] - time.time(), 7 * 86400, delta=60)
        # The member's side accepts it.
        self.assertEqual(api("/api/invitation", {"code": answer["code"]})[0], 200)
        _, listed = self.get("/desk/api/invitations")
        self.assertEqual([(i["label"], i["note"], i["locale"]) for i in listed["invitations"]],
                         [("Anna", "met at the market", "en")])
        # Codes are shown once, at issue; the Desk cannot show them again.
        self.assertNotIn(answer["code"], json.dumps(listed))
        self.assertNotIn(answer["code"].replace("-", ""), json.dumps(listed))

    def test_invalid_settings(self):
        status, answer = self.post("/desk/api/invitations",
                                   {"label": "x" * 81, "locale": "fr", "days": 31})
        self.assertEqual(status, 400)
        self.assertEqual(set(answer["fields"]), {"label", "locale", "days"})
        self.assertEqual(self.post("/desk/api/invitations", {"days": True})[0], 400)

    def test_bulk(self):
        status, answer = self.post("/desk/api/invitations/bulk",
                                   {"labels": ["Anna", " ", "Ben", "Carla"], "locale": "de"})
        self.assertEqual(status, 200)
        self.assertEqual([i["invitation"]["label"] for i in answer["issued"]], ["Anna", "Ben", "Carla"])
        self.assertEqual(len({i["code"] for i in answer["issued"]}), 3)
        self.assertEqual(self.post("/desk/api/invitations/bulk", {"labels": []})[0], 400)
        self.assertEqual(self.post("/desk/api/invitations/bulk", {"labels": ["x"] * 101})[0], 400)

    def test_revoke(self):
        answer = self.issue()
        status, revoked = self.post(f"/desk/api/invitations/{answer['invitation']['id']}/revoke", {})
        self.assertEqual((status, revoked["invitation"]["state"]), (200, "revoked"))
        self.assertEqual(api("/api/invitation", {"code": answer["code"]}), (410, {"error": "revoked"}))
        self.assertEqual(self.post(f"/desk/api/invitations/{answer['invitation']['id']}/revoke", {}),
                         (409, {"error": "revoked"}))
        self.assertEqual(self.post("/desk/api/invitations/999/revoke", {})[0], 404)

    def test_revoking_a_started_invitation_can_delete_its_account(self):
        answer = self.issue()
        api("/api/redeem", {"code": answer["code"], "username": "anna", "firstName": "A",
                            "lastName": "B", "email": "a@example.org"})
        self.assertIsNotNone(FAKE.user_by_name("anna"))
        status, _ = self.post(f"/desk/api/invitations/{answer['invitation']['id']}/revoke",
                              {"deleteAccount": True})
        self.assertEqual(status, 200)
        self.assertIsNone(FAKE.user_by_name("anna"))

    def test_an_account_with_a_passkey_is_never_deleted(self):
        answer = self.issue()
        api("/api/redeem", {"code": answer["code"], "username": "anna", "firstName": "A",
                            "lastName": "B", "email": "a@example.org"})
        FAKE.add_passkey(FAKE.user_by_name("anna")["id"])
        status, refusal = self.post(f"/desk/api/invitations/{answer['invitation']['id']}/revoke",
                                    {"deleteAccount": True})
        # The listing would have noticed the passkey first; either way it is done.
        self.assertEqual(status, 409)
        self.assertIn(refusal["error"], ("has_passkey", "done"))
        self.assertIsNotNone(FAKE.user_by_name("anna"))
        self.assertEqual(store.by_id(answer["invitation"]["id"]).state(), "done")

    def test_renew_replaces_the_code_and_keeps_the_account(self):
        answer = self.issue()
        api("/api/redeem", {"code": answer["code"], "username": "anna", "firstName": "A",
                            "lastName": "B", "email": "a@example.org"})
        status, renewed = self.post(f"/desk/api/invitations/{answer['invitation']['id']}/renew",
                                    {"days": 3})
        self.assertEqual(status, 200)
        self.assertNotEqual(renewed["code"], answer["code"])
        self.assertEqual(api("/api/invitation", {"code": answer["code"]})[0], 404)
        self.assertEqual(api("/api/invitation", {"code": renewed["code"]})[1]["username"], "anna")

    def test_an_expired_invitation_can_be_renewed(self):
        invitation, _ = store.create("new", "de", now=time.time() - 20 * 86400)
        status, renewed = self.post(f"/desk/api/invitations/{invitation.id}/renew", {})
        self.assertEqual((status, renewed["invitation"]["state"]), (200, "open"))

    def test_a_done_invitation_cannot_be_renewed(self):
        invitation, _ = store.create("new", "de")
        store.complete(invitation.id)
        self.assertEqual(self.post(f"/desk/api/invitations/{invitation.id}/renew", {}),
                         (409, {"error": "done"}))

    def test_the_listing_notices_a_passkey(self):
        answer = self.issue()
        api("/api/redeem", {"code": answer["code"], "username": "anna", "firstName": "A",
                            "lastName": "B", "email": "a@example.org"})
        FAKE.add_passkey(FAKE.user_by_name("anna")["id"])
        _, listed = self.get("/desk/api/invitations")
        self.assertEqual(listed["invitations"][0]["state"], "done")


class Recovery(Desk):
    def test_search(self):
        FAKE.add_user("anna", email="anna@example.org", passkey_at=time.time())
        FAKE.add_user("hannah")
        FAKE.add_user("service-account-join")
        status, answer = self.get("/desk/api/members?q=ann")
        self.assertEqual(status, 200)
        self.assertEqual(sorted((m["username"], m["passkeys"]) for m in answer["members"]),
                         [("anna", 1), ("hannah", 0)])
        self.assertEqual(self.get("/desk/api/members?q=a")[1], {"members": []})

    def test_a_new_passkey_for_a_member(self):
        anna = FAKE.add_user("anna", passkey_at=time.time() - 86400)
        FAKE.add_passkey(anna, time.time() - 3600)
        status, answer = self.post("/desk/api/recovery",
                                   {"userId": anna, "locale": "en", "removePasskeys": True})
        self.assertEqual(status, 200, answer)
        self.assertEqual(answer["removedPasskeys"], 2)
        self.assertEqual(FAKE.credentials[anna], [])
        self.assertEqual(FAKE.logouts, [anna])
        self.assertEqual(answer["invitation"]["kind"], "recovery")
        self.assertEqual(answer["invitation"]["label"], "anna")
        self.assertEqual(api("/api/invitation", {"code": answer["code"]})[1]["kind"], "recovery")

    def test_keeping_the_old_passkeys(self):
        anna = FAKE.add_user("anna", passkey_at=time.time() - 86400)
        _, answer = self.post("/desk/api/recovery", {"userId": anna, "removePasskeys": False})
        self.assertEqual((answer["removedPasskeys"], len(FAKE.credentials[anna]), FAKE.logouts),
                         (0, 1, []))

    def test_one_recovery_code_per_member(self):
        anna = FAKE.add_user("anna")
        _, first = self.post("/desk/api/recovery", {"userId": anna})
        _, second = self.post("/desk/api/recovery", {"userId": anna})
        self.assertEqual(api("/api/invitation", {"code": first["code"]}), (410, {"error": "revoked"}))
        self.assertEqual(api("/api/invitation", {"code": second["code"]})[0], 200)

    def test_unknown_and_service_accounts(self):
        self.assertEqual(self.post("/desk/api/recovery", {"userId": "nope"})[0], 404)
        robot = FAKE.add_user("service-account-join")
        self.assertEqual(self.post("/desk/api/recovery", {"userId": robot})[0], 404)


class Shell(Fresh):
    """manage.py, the shell route — the e2e spec cleans up with it."""

    def run_manage(self, *args):
        import contextlib
        import io
        import manage
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = manage.main(list(args))
        return code, out.getvalue() + err.getvalue()

    def test_create_list_and_revoke_with_the_account(self):
        code, output = self.run_manage("create", "--locale", "en", "--label", "Zoe")
        self.assertEqual(code, 0)
        issued = output.split("Code: ")[1].split()[0]
        invitation_id = int(output.split("Invitation ")[1].split(",")[0])
        api("/api/redeem", {"code": issued, "username": "zoe", "firstName": "Z",
                            "lastName": "Y", "email": "z@example.org"})
        self.assertIn("started", self.run_manage("list")[1])
        self.assertEqual(self.run_manage("revoke", str(invitation_id), "--delete-account")[0], 0)
        self.assertIsNone(FAKE.user_by_name("zoe"))
        self.assertEqual(self.run_manage("revoke", str(invitation_id))[0], 1)


class Shipping(unittest.TestCase):
    def test_keycloak_restarts_for_a_new_extension_jar(self):
        """The jar's ConfigMap keeps its name, so only the pod annotation that
        build.sh writes gets a rebuilt jar into Keycloak (docs/adr/0059)."""
        import hashlib
        import os
        from harness import TENANT
        keycloak = os.path.join(os.path.dirname(TENANT), "keycloak")
        with open(os.path.join(keycloak, "action-token-generator.jar"), "rb") as handle:
            digest = hashlib.sha256(handle.read()).hexdigest()
        with open(os.path.join(keycloak, "values.yaml")) as handle:
            self.assertIn(f'smallworlds/keycloak-extension-sha256: "{digest}"', handle.read(),
                          "run infrastructure/keycloak-spi/action-token-generator/build.sh")

    def test_every_source_and_static_file_is_in_the_configmaps(self):
        """A file missing from configMapGenerator is an import error in the pod,
        found only after a deploy. ConfigMap keys are flat, so neither may nest."""
        import os
        from harness import TENANT
        with open(os.path.join(TENANT, "kustomization.yaml")) as handle:
            kustomization = handle.read()
        for directory, suffixes in (("src", (".py",)), ("static", (".html", ".js", ".css"))):
            for name in sorted(os.listdir(os.path.join(TENANT, directory))):
                if name.endswith(suffixes):
                    self.assertIn(f"- {directory}/{name}\n", kustomization, name)


if __name__ == "__main__":
    unittest.main(verbosity=1)
