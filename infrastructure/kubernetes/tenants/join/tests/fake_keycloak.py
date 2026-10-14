"""Just enough of Keycloak's admin API and the action-token-link SPI for join.

Behaves like Keycloak where join depends on it: exact username/email lookups,
409 on a duplicate with Keycloak's own wording, Location on create, credentials
with millisecond `createdDate`, and links built on a configurable issuer so a
wrong one can be simulated.
"""
import json
import threading
import time
import urllib.parse
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

REALM = "smallworlds"


class FakeKeycloak:
    def __init__(self, client_secret="test-secret", issuer="https://identity.example.test"):
        self.client_secret = client_secret
        self.issuer = issuer
        self.users: dict[str, dict] = {}
        self.credentials: dict[str, list] = {}
        self.minted: list[dict] = []
        self.down = False
        self.lock = threading.Lock()
        fake = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *args):
                pass

            def _send(self, status, payload=None, headers=None):
                body = json.dumps(payload).encode() if payload is not None else b""
                self.send_response(status)
                for name, value in (headers or {}).items():
                    self.send_header(name, value)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def _body(self):
                raw = self.rfile.read(int(self.headers.get("Content-Length") or 0))
                if self.headers.get("Content-Type", "").startswith("application/x-www-form-urlencoded"):
                    return dict(urllib.parse.parse_qsl(raw.decode()))
                return json.loads(raw) if raw else None

            def _authorized(self):
                return self.headers.get("Authorization") == "Bearer service-token"

            def _route(self, method):
                if fake.down:
                    self._send(503, {"error": "down"})
                    return
                url = urllib.parse.urlparse(self.path)
                query = dict(urllib.parse.parse_qsl(url.query))
                path = url.path
                base = f"/admin/realms/{REALM}/users"
                with fake.lock:
                    if method == "POST" and path == f"/realms/{REALM}/protocol/openid-connect/token":
                        form = self._body()
                        if form.get("client_id") == "join" and form.get("client_secret") == fake.client_secret:
                            self._send(200, {"access_token": "service-token", "expires_in": 300})
                        else:
                            self._send(401, {"error": "unauthorized_client"})
                        return
                    if not self._authorized():
                        self._send(401, {"error": "HTTP 401 Unauthorized"})
                        return
                    if method == "POST" and path == f"/realms/{REALM}/action-token-link/generate-link":
                        body = self._body()
                        if body["userId"] not in fake.users:
                            self._send(404, {"error": "User not found"})
                            return
                        fake.minted.append(body)
                        token = uuid.uuid4().hex
                        self._send(200, {"link": f"{fake.issuer}/realms/{REALM}/login-actions/"
                                                 f"action-token?key={token}&client_id={body['clientId']}&tab_id="})
                        return
                    if path == base and method == "GET":
                        found = list(fake.users.values())
                        if "username" in query:
                            found = [u for u in found if u["username"] == query["username"].lower()]
                        if "email" in query:
                            found = [u for u in found if u.get("email", "").lower() == query["email"].lower()]
                        self._send(200, found)
                        return
                    if path == base and method == "POST":
                        body = self._body()
                        username = body["username"].lower()
                        if any(u["username"] == username for u in fake.users.values()):
                            self._send(409, {"errorMessage": "User exists with same username"})
                            return
                        if body.get("email") and any(u.get("email", "").lower() == body["email"].lower()
                                                     for u in fake.users.values()):
                            self._send(409, {"errorMessage": "User exists with same email"})
                            return
                        user_id = str(uuid.uuid4())
                        fake.users[user_id] = {**body, "username": username, "id": user_id}
                        fake.credentials[user_id] = []
                        self._send(201, None, {"Location": f"http://keycloak{base}/{user_id}"})
                        return
                    if path.startswith(base + "/"):
                        rest = path[len(base) + 1:].split("/")
                        user_id = urllib.parse.unquote(rest[0])
                        if user_id not in fake.users:
                            self._send(404, {"error": "User not found"})
                            return
                        if len(rest) == 1 and method == "GET":
                            self._send(200, fake.users[user_id])
                            return
                        if len(rest) == 1 and method == "PUT":
                            fake.users[user_id] = {**self._body(), "id": user_id}
                            self._send(204)
                            return
                        if len(rest) == 1 and method == "DELETE":
                            del fake.users[user_id]
                            fake.credentials.pop(user_id, None)
                            self._send(204)
                            return
                        if rest[1:] == ["credentials"] and method == "GET":
                            self._send(200, fake.credentials[user_id])
                            return
                    self._send(404, {"error": "Unable to find matching target resource method"})

            def do_GET(self):
                self._route("GET")

            def do_POST(self):
                self._route("POST")

            def do_PUT(self):
                self._route("PUT")

            def do_DELETE(self):
                self._route("DELETE")

        self.server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_port}"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    # ---- helpers for tests -------------------------------------------------

    def add_user(self, username, email=None, passkey_at=None):
        with self.lock:
            user_id = str(uuid.uuid4())
            self.users[user_id] = {"id": user_id, "username": username, "email": email or "",
                                   "attributes": {}}
            self.credentials[user_id] = []
        if passkey_at is not None:
            self.add_passkey(user_id, passkey_at)
        return user_id

    def add_passkey(self, user_id, at=None):
        with self.lock:
            self.credentials[user_id].append({
                "id": str(uuid.uuid4()), "type": "webauthn-passwordless",
                "createdDate": int((time.time() if at is None else at) * 1000)})

    def user_by_name(self, username):
        return next((u for u in self.users.values() if u["username"] == username), None)

    def close(self):
        self.server.shutdown()
