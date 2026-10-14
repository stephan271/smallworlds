"""Start join against a fake Keycloak, for the browser test.

Prints one JSON line — the page's URL and a code per scenario — then serves
until killed. Used by admin-tools/test-join.sh; handy for looking at the page
by hand too:  python3 infrastructure/kubernetes/tenants/join/tests/serve.py
"""
import json
import logging
import os
import sys
import tempfile
import threading
import time

HERE = os.path.dirname(os.path.abspath(__file__))
TENANT = os.path.dirname(HERE)
sys.path.insert(0, HERE)
sys.path.insert(0, os.path.join(TENANT, "src"))

from fake_keycloak import FakeKeycloak  # noqa: E402

fake = FakeKeycloak()
scratch = tempfile.mkdtemp(prefix="join-serve-")
os.environ.update({
    "KEYCLOAK_URL": fake.url,
    "KEYCLOAK_CLIENT_SECRET": "test-secret",
    "PUBLIC_IDENTITY_URL": "https://identity.example.test",
    "JOIN_URL": "https://join.example.test",
    "DASHBOARD_URL": "https://dashboard.example.test",
    "DB_PATH": os.path.join(scratch, "invitations.db"),
    "STATIC_DIR": os.path.join(TENANT, "static"),
    "BIND": "127.0.0.1",
})

import codes  # noqa: E402
import main  # noqa: E402
import store  # noqa: E402

logging.disable(logging.CRITICAL)
store.init()
fake.add_user("taken", email="taken@example.org")

_, open_de = store.create("new", "de", label="Anna")
_, open_en = store.create("new", "en", label="Ben")
started, started_code = store.create("new", "de", label="Carla")
store.claim(started.id, fake.add_user("carla", email="carla@example.org"), "carla")
dora = fake.add_user("dora", email="dora@example.org", passkey_at=time.time() - 86400)
_, recovery = store.create("recovery", "en", user_id=dora, username="dora")
expired, expired_code = store.create("new", "de", now=time.time() - 15 * 86400)

server = main.serve_public(int(sys.argv[1]) if len(sys.argv) > 1 else 0)
threading.Thread(target=server.serve_forever, daemon=True).start()
print(json.dumps({
    "url": f"http://127.0.0.1:{server.server_port}",
    "codes": {name: codes.display(code) for name, code in {
        "open_de": open_de, "open_en": open_en, "started": started_code,
        "recovery": recovery, "expired": expired_code}.items()},
}), flush=True)
try:
    threading.Event().wait()
except KeyboardInterrupt:
    pass
