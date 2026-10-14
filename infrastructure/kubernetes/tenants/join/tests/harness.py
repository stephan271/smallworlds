"""Shared set-up for join's tests: a fake Keycloak, a scratch database, and both
listeners on free ports. Imported before anything from src/, because config.py
reads the environment at import time.
"""
import json
import logging
import os
import sys
import tempfile
import threading
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

import config  # noqa: E402
import desk  # noqa: E402
import main  # noqa: E402
import member  # noqa: E402
import store  # noqa: E402

logging.disable(logging.CRITICAL)  # the servers' request logs are noise here
store.init()
PUBLIC = main.serve_public(0)
DESK = main.serve_desk(0)
for server in (PUBLIC, DESK):
    threading.Thread(target=server.serve_forever, daemon=True).start()
BASE = f"http://127.0.0.1:{PUBLIC.server_port}"
DESK_BASE = f"http://127.0.0.1:{DESK.server_port}"


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


_opener = urllib.request.build_opener(_NoRedirect)


def call(method, path, body=None, headers=None, raw=None, base=BASE):
    """(status, headers, body) without following redirects. Headers keep every
    Set-Cookie (headers.get_all)."""
    data = raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    request = urllib.request.Request(base + path, data=data, method=method)
    if body is not None or raw is not None:
        request.add_header("Content-Type", "application/json")
    for name, value in (headers or {}).items():
        request.add_header(name, value)
    try:
        with _opener.open(request) as response:
            return response.status, response.headers, response.read()
    except urllib.error.HTTPError as error:
        with error:
            return error.code, error.headers, error.read()


def api(path, body, headers=None):
    status, _, raw = call("POST", path, body, headers)
    return status, json.loads(raw or b"{}")


class Fresh(unittest.TestCase):
    """Each test gets an empty Keycloak, an empty database and fresh limits."""

    def setUp(self):
        with FAKE.lock:
            FAKE.users.clear()
            FAKE.credentials.clear()
            FAKE.minted.clear()
            FAKE.codes.clear()
            FAKE.logouts.clear()
            FAKE.issuer = "https://identity.example.test"
            FAKE.login_as = {"username": "operator", "roles": ["admin", "offline_access"]}
            FAKE.down = False
        for suffix in ("", "-wal", "-shm"):
            if os.path.exists(config.DB_PATH + suffix):
                os.remove(config.DB_PATH + suffix)
        store.init()
        member.failed_lookups = member.Limiter(5, 3600)
        member.username_checks = member.Limiter(120, 600)

    def invite(self, **kwargs):
        kwargs.setdefault("kind", "new")
        kwargs.setdefault("locale", "de")
        return store.create(**kwargs)
