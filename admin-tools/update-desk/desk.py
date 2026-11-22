#!/usr/bin/env python3
"""The SmallWorlds Update Desk, run on the Operator's own machine.

    ./smallworlds-update --overlay ~/development/my-community --kubeconfig ~/.smallworlds/kubeconfigs/local.yaml

It shows what Ring 0 has verified since the base commit this community runs, as
the steps Ring 0 itself took, and moves the community along them one step (or
one group of steps) at a time: it writes the overlay, commits, pushes, moves the
root Application's pin and follows Argo CD until every Application is Synced and
Healthy. Each of those is a separate button, and each shows the command it ran.

It answers on 127.0.0.1 only, for a Host of localhost, and only with the session
token printed in the terminal, as the setup wizard does
(admin-tools/setup-wizard/wizard.py). Standard library only.

Design: plans-and-walkthroughs/design-per-app-updates.md, and
docs/adr/0067 for why this runs locally and follows Ring 0's path.
"""
from __future__ import annotations

import argparse
import http.server
import json
import os
import secrets
import socket
import sys
import threading
import urllib.parse
import webbrowser

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)

import engine  # noqa: E402

STATIC = os.path.join(HERE, "static")
SETTINGS = os.path.expanduser("~/.smallworlds/update-desk/settings.json")
ALLOWED_HOSTNAMES = {"127.0.0.1", "localhost", "::1"}
CONTENT_TYPES = {".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8",
                 ".css": "text/css; charset=utf-8", ".svg": "image/svg+xml"}
TOKEN = secrets.token_urlsafe(32)
DESK: engine.Desk | None = None


class Handler(http.server.BaseHTTPRequestHandler):
    server_version = "smallworlds-update-desk"
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):  # the URL carries the session token
        return

    def _loopback(self) -> bool:
        host = (self.headers.get("Host") or "").strip()
        hostname = host.rsplit(":", 1)[0] if host.count(":") == 1 else host
        return hostname.strip("[]").lower() in ALLOWED_HOSTNAMES

    def _send(self, status: int, body: bytes, content_type: str):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Frame-Options", "DENY")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def _json(self, status: int, payload):
        self._send(status, json.dumps(payload, default=str).encode(), "application/json; charset=utf-8")

    def _body(self) -> dict:
        try:
            length = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            return {}
        if not 0 < length <= 100_000:
            return {}
        try:
            return json.loads(self.rfile.read(length) or b"{}")
        except (ValueError, OSError):
            return {}

    def _authorised(self) -> bool:
        return secrets.compare_digest(self.headers.get("X-Desk-Token") or "", TOKEN)

    def do_GET(self):
        if not self._loopback():
            return self._json(403, {"error": "The Update Desk answers only on localhost."})
        url = urllib.parse.urlparse(self.path)
        if url.path in ("/", "/index.html"):
            supplied = (urllib.parse.parse_qs(url.query).get("t") or [""])[0]
            if not secrets.compare_digest(supplied, TOKEN):
                return self._send(403, b"<h1>Wrong or missing session token</h1><p>Open the exact "
                                       b"link the Desk printed in your terminal.</p>", "text/html; charset=utf-8")
            with open(os.path.join(STATIC, "index.html"), "rb") as handle:
                page = handle.read().replace(b"__SESSION_TOKEN__", TOKEN.encode())
            return self._send(200, page, "text/html; charset=utf-8")
        if url.path.startswith("/static/"):
            name = os.path.normpath("/" + url.path[len("/static/"):]).lstrip("/")
            full = os.path.join(STATIC, name)
            if not os.path.abspath(full).startswith(os.path.abspath(STATIC) + os.sep):
                return self._send(403, b"no", "text/plain")
            try:
                with open(full, "rb") as handle:
                    payload = handle.read()
            except OSError:
                return self._send(404, b"not found", "text/plain")
            return self._send(200, payload, CONTENT_TYPES.get(os.path.splitext(full)[1], "application/octet-stream"))
        if url.path == "/api/state":
            if not self._authorised():
                return self._json(403, {"error": "Bad session token."})
            try:
                return self._json(200, DESK.state())
            except Exception as error:  # noqa: BLE001 - the page must keep answering
                return self._json(200, {"fatal": f"{type(error).__name__}: {error}"})
        return self._json(404, {"error": "No such route."})

    def do_POST(self):
        if not self._loopback():
            return self._json(403, {"error": "The Update Desk answers only on localhost."})
        if not self._authorised():
            return self._json(403, {"error": "Bad session token."})
        body = self._body()
        routes = {
            "/api/refresh": lambda: DESK.refresh(fetch=True) or {"ok": True},
            "/api/plan": lambda: DESK.plan(str(body.get("to") or "")),
            "/api/undo-plan": DESK.undo_plan,
            "/api/write": lambda: DESK.write(str(body.get("to") or "")),
            "/api/discard": DESK.discard,
            "/api/commit": DESK.commit,
            "/api/push": DESK.push,
            "/api/pin": DESK.move_pin,
            "/api/refresh-apps": DESK.refresh_applications,
            "/api/backup": lambda: DESK.backup(str(body.get("namespace") or ""), str(body.get("name") or "")),
        }
        route = urllib.parse.urlparse(self.path).path
        if route not in routes:
            return self._json(404, {"error": "No such route."})
        try:
            return self._json(200, routes[route]())
        except engine.DeskError as error:
            return self._json(409, {"error": str(error)})
        except Exception as error:  # noqa: BLE001
            return self._json(500, {"error": f"{type(error).__name__}: {error}"})


def _settings() -> dict:
    try:
        with open(SETTINGS) as handle:
            return json.load(handle)
    except (OSError, ValueError):
        return {}


def _save_settings(values: dict) -> None:
    os.makedirs(os.path.dirname(SETTINGS), exist_ok=True)
    with open(SETTINGS, "w") as handle:
        json.dump(values, handle, indent=2)


def main(argv=None) -> int:
    saved = _settings()
    parser = argparse.ArgumentParser(description="Bring a SmallWorlds community along Ring 0's verified path.")
    parser.add_argument("--overlay", default=saved.get("overlay"),
                        help="checkout of this community's GitOps Overlay (remembered)")
    parser.add_argument("--kubeconfig", default=saved.get("kubeconfig") or os.environ.get("KUBECONFIG"),
                        help="kubeconfig of this community's cluster (remembered)")
    parser.add_argument("--port", type=int, default=0, help="port on 127.0.0.1 (default: any free one)")
    parser.add_argument("--no-browser", action="store_true", help="print the link, do not open it")
    parser.add_argument("--no-fetch", action="store_true", help="do not fetch the base and overlay first")
    args = parser.parse_args(argv)

    if not args.overlay or not os.path.isfile(os.path.join(os.path.expanduser(args.overlay), "kustomization.yaml")):
        parser.error("--overlay must name a checkout of your GitOps Overlay (a directory with kustomization.yaml)")
    overlay = os.path.abspath(os.path.expanduser(args.overlay))
    kubeconfig = os.path.abspath(os.path.expanduser(args.kubeconfig)) if args.kubeconfig else None
    _save_settings({"overlay": overlay, "kubeconfig": kubeconfig})

    global DESK
    base = os.path.abspath(os.path.join(HERE, "..", ".."))
    DESK = engine.Desk(base, overlay, kubeconfig)
    print(f"Overlay:    {overlay}")
    print(f"Kubeconfig: {kubeconfig or '(kubectl default)'}")
    print("Reading Ring 0's path …", flush=True)
    DESK.refresh(fetch=not args.no_fetch)
    if DESK.path_error:
        print(f"  {DESK.path_error}")

    server = http.server.ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.daemon_threads = True
    port = server.server_address[1]
    link = f"http://localhost:{port}/?t={TOKEN}"
    print(f"\nUpdate Desk: {link}\n(stop with Ctrl+C)", flush=True)
    if not args.no_browser:
        threading.Timer(0.3, lambda: webbrowser.open(link)).start()
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nstopped")
    return 0


if __name__ == "__main__":
    socket.setdefaulttimeout(60)
    sys.exit(main())
