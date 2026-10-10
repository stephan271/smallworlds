#!/usr/bin/env python3
"""The SmallWorlds setup wizard — a single-use local web wizard.

It runs on the future operator's own machine, before any cluster exists, asks
about five questions, and drives the two setup scripts that already do the
work. It is not a console: it has no post-install role, no database and no
authentication story beyond loopback plus a session token, because it is never
exposed.

It asks, validates, shows exactly what it will do, runs the two setup scripts,
hands over the credentials and stops. See
docs/adr/0051-a-single-use-setup-wizard-replaces-the-bootstrap-launcher.md for
the decision and plans-and-walkthroughs/design-setup-wizard.md for the design.

Standard library only — no dependencies, no build step.
"""

from __future__ import annotations

import argparse
import http.server
import json
import os
import queue
import secrets
import socket
import sys
import threading
import urllib.parse
import webbrowser

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import checks  # noqa: E402
import plan as planning  # noqa: E402
import runner as running  # noqa: E402

STATIC_DIRECTORY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "static")

# Requests are answered only for these Host values. Loopback binding alone does
# not stop a page on the internet from pointing a hostname it controls at
# 127.0.0.1 and driving this server from the operator's own browser; checking
# the Host header does.
ALLOWED_HOSTNAMES = {"127.0.0.1", "localhost", "[::1]", "::1"}

CONTENT_TYPES = {
    ".html": "text/html; charset=utf-8",
    ".js": "text/javascript; charset=utf-8",
    ".css": "text/css; charset=utf-8",
    ".svg": "image/svg+xml",
}


class Session:
    """Everything the wizard holds, which is deliberately very little.

    Secrets live here for the lifetime of the process and are never written to
    the browser, never logged, and (until Phase 2 hands them to the scripts)
    never written to disk.
    """

    def __init__(self):
        self.token = secrets.token_urlsafe(32)
        self.secrets: dict[str, str] = {}
        # At most one installation at a time. A second would race the first
        # over the same cache files, the same repository and the same node.
        self.run: running.Run | None = None

    def remember_secret(self, name: str, value: str):
        if value:
            self.secrets[name] = value

    def resolve_secret(self, name: str, submitted: str, use_saved: bool) -> str:
        """A submitted value wins; otherwise the one this session already holds;
        otherwise the one the setup scripts cached on this machine. The browser
        can ask for the saved token by name without ever being told it."""
        if submitted:
            self.remember_secret(name, submitted)
            return submitted
        if name in self.secrets:
            return self.secrets[name]
        if use_saved:
            cached = planning._read_shell_assignments(planning.INIT_CACHE)
            return cached.get(name, "")
        return ""


SESSION = Session()


class Handler(http.server.BaseHTTPRequestHandler):
    server_version = "smallworlds-setup-wizard"
    protocol_version = "HTTP/1.1"

    # ---------------------------------------------------------------- helpers

    def _host_is_loopback(self) -> bool:
        host = (self.headers.get("Host") or "").strip()
        hostname = host.rsplit(":", 1)[0] if host.count(":") == 1 else host
        return hostname.strip("[]").lower() in {h.strip("[]") for h in ALLOWED_HOSTNAMES}

    def _send(self, status: int, body: bytes, content_type: str):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        # This page must never be embedded, cached or sent anywhere: it carries
        # a session token in its URL and credentials in its forms.
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Frame-Options", "DENY")
        self.send_header("Referrer-Policy", "no-referrer")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.end_headers()
        if self.command != "HEAD":
            self.wfile.write(body)

    def _send_json(self, status: int, payload):
        self._send(status, json.dumps(payload).encode("utf-8"), "application/json; charset=utf-8")

    def _read_json(self) -> dict:
        try:
            length = int(self.headers.get("Content-Length") or 0)
        except ValueError:
            return {}
        if length <= 0 or length > 1_000_000:
            return {}
        try:
            return json.loads(self.rfile.read(length) or b"{}")
        except (ValueError, OSError):
            return {}

    def _authorised(self) -> bool:
        supplied = self.headers.get("X-Setup-Token") or ""
        return secrets.compare_digest(supplied, SESSION.token)

    def log_message(self, format_string, *args):
        """Silence the default per-request logging: the URL carries the session
        token, and the terminal is the operator's, not a server log."""
        return

    # ----------------------------------------------------------------- routing

    def do_GET(self):
        if not self._host_is_loopback():
            self._send_json(403, {"error": "This wizard answers only on localhost."})
            return

        path = urllib.parse.urlparse(self.path)
        route = path.path

        if route in ("/", "/index.html"):
            query = urllib.parse.parse_qs(path.query)
            supplied = (query.get("t") or [""])[0]
            if not secrets.compare_digest(supplied, SESSION.token):
                self._send(
                    403,
                    b"<h1>Wrong or missing session token</h1><p>Open the exact "
                    b"link the wizard printed in your terminal.</p>",
                    "text/html; charset=utf-8",
                )
                return
            self._serve_index()
            return

        if route.startswith("/static/"):
            self._serve_static(route[len("/static/"):])
            return

        if route == "/api/state":
            if not self._authorised():
                self._send_json(403, {"error": "Bad session token."})
                return
            self._send_json(200, planning.initial_state())
            return

        if route == "/api/install/status":
            if not self._authorised():
                self._send_json(403, {"error": "Bad session token."})
                return
            self._send_json(200, SESSION.run.snapshot() if SESSION.run
                            else {"status": "idle"})
            return

        if route == "/api/events":
            # EventSource cannot set headers, so this one route takes its token
            # from the query string. Same value, same loopback-only server, and
            # the page's own URL already carries it.
            query = urllib.parse.parse_qs(path.query)
            supplied = (query.get("t") or [""])[0]
            if not secrets.compare_digest(supplied, SESSION.token):
                self._send_json(403, {"error": "Bad session token."})
                return
            self._stream_events()
            return

        self._send_json(404, {"error": "No such route."})

    def do_POST(self):
        if not self._host_is_loopback():
            self._send_json(403, {"error": "This wizard answers only on localhost."})
            return
        if not self._authorised():
            self._send_json(403, {"error": "Bad session token."})
            return

        route = urllib.parse.urlparse(self.path).path
        body = self._read_json()

        try:
            handler = {
                "/api/check/local": self._check_local,
                "/api/check/hetzner": self._check_hetzner,
                "/api/check/dns": self._check_dns,
                "/api/check/git": self._check_git,
                "/api/check/target": self._check_target,
                "/api/check/s3": self._check_s3,
                "/api/plan": self._build_plan,
                "/api/install": self._install,
                "/api/install/cancel": self._cancel,
                "/api/shutdown": self._shutdown,
            }[route]
        except KeyError:
            self._send_json(404, {"error": "No such route."})
            return

        try:
            self._send_json(200, handler(body))
        except Exception as error:  # noqa: BLE001 - a failed check must not kill the wizard
            self._send_json(200, checks.unknown(f"The check itself failed: {error}"))

    # ------------------------------------------------------------------ static

    def _serve_index(self):
        try:
            with open(os.path.join(STATIC_DIRECTORY, "index.html"), "rb") as handle:
                page = handle.read()
        except OSError:
            self._send(500, b"index.html is missing", "text/plain; charset=utf-8")
            return
        # The page needs the token to make its own API calls. Injected rather
        # than fetched, so there is no unauthenticated route that hands it out.
        page = page.replace(b"__SESSION_TOKEN__", SESSION.token.encode("ascii"))
        self._send(200, page, "text/html; charset=utf-8")

    def _serve_static(self, name: str):
        # Static assets carry nothing secret, so they need no token — but they
        # must stay inside the static directory.
        safe = os.path.normpath("/" + name).lstrip("/")
        full = os.path.join(STATIC_DIRECTORY, safe)
        if not os.path.abspath(full).startswith(os.path.abspath(STATIC_DIRECTORY) + os.sep):
            self._send(403, b"no", "text/plain; charset=utf-8")
            return
        try:
            with open(full, "rb") as handle:
                payload = handle.read()
        except OSError:
            self._send(404, b"not found", "text/plain; charset=utf-8")
            return
        extension = os.path.splitext(full)[1]
        self._send(200, payload, CONTENT_TYPES.get(extension, "application/octet-stream"))

    # ------------------------------------------------------------------ checks

    def _check_local(self, body: dict) -> dict:
        return {
            "tools": checks.check_local_tools(),
            "ssh_key": checks.check_ssh_public_key(),
        }

    def _check_hetzner(self, body: dict) -> dict:
        token = SESSION.resolve_secret(
            "HCLOUD_TOKEN", body.get("token") or "", bool(body.get("use_saved")))
        return checks.check_hetzner_token(
            token,
            (body.get("domain") or "").strip().lower(),
            (body.get("env_ext") or "").strip(),
        )

    def _check_dns(self, body: dict) -> dict:
        return checks.check_domain_nameservers((body.get("domain") or "").strip().lower())

    def _check_git(self, body: dict) -> dict:
        token = SESSION.resolve_secret(
            "GITOPS_REPO_TOKEN", body.get("token") or "", bool(body.get("use_saved")))
        remote_url = (body.get("remote_url") or "").strip()
        result = checks.check_git_token(token, remote_url)
        # For an existing repository the operator names, "the token is valid" is
        # only half the question: the install fails later if that token cannot
        # read that repository.
        if remote_url and result["status"] == "ok":
            result["remote"] = checks.check_git_remote(
                remote_url, token, (body.get("username") or "").strip())
        return result

    def _check_target(self, body: dict) -> dict:
        return checks.check_local_target(
            (body.get("ssh_target") or "").strip(),
            (body.get("data_dir") or "").strip(),
            (body.get("backup_dir") or "").strip(),
            external_storage=body.get("object_storage") == "external",
        )

    def _check_s3(self, body: dict) -> dict:
        # `which` selects the primary key or the separate backup one; each is
        # remembered under its own name so "use the saved secret" cannot hand
        # one provider the other's key.
        name = "S3_BACKUP_SECRET_ACCESS_KEY" if body.get("which") == "backup" \
            else "S3_SECRET_ACCESS_KEY"
        secret = SESSION.resolve_secret(
            name, body.get("secret") or "", bool(body.get("use_saved")))
        return checks.check_s3(
            (body.get("endpoint") or "").strip(),
            (body.get("region") or "").strip(),
            (body.get("access_key_id") or "").strip(),
            secret,
        )

    # ------------------------------------------------------------- installing

    def _install(self, body: dict) -> dict:
        if SESSION.run and SESSION.run.status in ("starting", "running"):
            return {"started": False, "reason": "already_running",
                    "message": "An installation is already under way."}

        answers = self._with_session_secrets(dict(body.get("answers") or {}))
        problems = planning.validate_answers(answers)
        if problems:
            return {"started": False, "reason": "incomplete", "problems": problems}

        # The submitted value first, then whatever a validation call along the
        # way already remembered, then the token cached on this machine. Reading
        # only the session would silently install with no token at all when the
        # operator typed one and pressed Install before the debounced check
        # behind that field had fired.
        token_source = {
            "HCLOUD_TOKEN": SESSION.resolve_secret(
                "HCLOUD_TOKEN", answers.get("hcloud_token") or "", True),
            "GITOPS_REPO_TOKEN": SESSION.resolve_secret(
                "GITOPS_REPO_TOKEN", answers.get("git_token") or "", True),
            "S3_SECRET_ACCESS_KEY": SESSION.resolve_secret(
                "S3_SECRET_ACCESS_KEY", answers.get("s3_secret_access_key") or "", True),
            "S3_BACKUP_SECRET_ACCESS_KEY": SESSION.resolve_secret(
                "S3_BACKUP_SECRET_ACCESS_KEY",
                answers.get("s3_backup_secret_access_key") or "", True),
        }
        if answers.get("object_storage") == "external" and not token_source["S3_SECRET_ACCESS_KEY"]:
            return {"started": False, "reason": "missing_token",
                    "message": "No S3 secret access key — the cluster would have "
                               "nowhere to store a single file."}
        if not token_source["GITOPS_REPO_TOKEN"]:
            return {"started": False, "reason": "missing_token",
                    "message": "No Git access token — Argo CD cannot read your "
                               "settings repository without one."}

        # The repository has to exist, and hold the overlay, before any machine
        # is touched: installing IS the act of pointing a new cluster at it.
        if answers.get("git_mode") == "create":
            created = checks.create_github_repository(
                token_source["GITOPS_REPO_TOKEN"], answers["repo_name"])
            if created["status"] != "ok":
                return {"started": False, "reason": "repository",
                        "message": created["summary"]}
            answers["remote_url"] = created["clone_url"]

        apps = list(answers.get("apps") or [])
        running.write_repository_config(answers, planning.COMMUNITY_CONFIG, apps)
        running.write_installer_cache(answers, planning.INIT_CACHE, token_source)

        SESSION.run = running.Run(answers, token_source, planning.REPO_ROOT)
        SESSION.run.start()
        return {"started": True, "remote_url": answers["remote_url"],
                "stages": SESSION.run.stages}

    def _shutdown(self, body: dict) -> dict:
        """The wizard is single-use. Once the cluster is up it has no further
        job, and a loopback server left running still holds the credentials in
        memory. Refused while an install is under way — closing then would
        orphan terraform mid-apply."""
        if SESSION.run and SESSION.run.status in ("starting", "running"):
            return {"stopped": False,
                    "message": "An installation is still running."}
        threading.Timer(0.5, self.server.shutdown).start()
        return {"stopped": True}

    def _cancel(self, body: dict) -> dict:
        if not SESSION.run or SESSION.run.status not in ("starting", "running"):
            return {"cancelled": False, "message": "Nothing is running."}
        return {"cancelled": SESSION.run.cancel()}

    def _with_session_secrets(self, answers: dict) -> dict:
        answers["use_saved_hcloud_token"] = bool(
            answers.get("use_saved_hcloud_token") or SESSION.secrets.get("HCLOUD_TOKEN"))
        answers["use_saved_git_token"] = bool(
            answers.get("use_saved_git_token") or SESSION.secrets.get("GITOPS_REPO_TOKEN"))
        answers["use_saved_s3_secret"] = bool(
            answers.get("use_saved_s3_secret") or SESSION.secrets.get("S3_SECRET_ACCESS_KEY"))
        answers["use_saved_s3_backup_secret"] = bool(
            answers.get("use_saved_s3_backup_secret")
            or SESSION.secrets.get("S3_BACKUP_SECRET_ACCESS_KEY"))
        return answers

    def _stream_events(self):
        """Server-sent events for the whole of one installation.

        Each new watcher is handed a snapshot first, so reopening the page in
        the middle of an install shows everything that has happened rather than
        only what arrives next.
        """
        run = SESSION.run
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Connection", "close")
        self.end_headers()

        def write(event: str, data) -> bool:
            try:
                self.wfile.write(
                    f"event: {event}\ndata: {json.dumps(data)}\n\n".encode("utf-8"))
                self.wfile.flush()
                return True
            except (BrokenPipeError, ConnectionResetError, OSError):
                return False

        if run is None:
            write("snapshot", {"status": "idle"})
            return

        watcher = run.subscribe()
        try:
            while True:
                try:
                    message = watcher.get(timeout=20)
                except queue.Empty:
                    # A comment frame: proxies and browsers both drop a stream
                    # that says nothing for long enough, and Argo CD can take a
                    # quarter of an hour to converge.
                    try:
                        self.wfile.write(b": keep-alive\n\n")
                        self.wfile.flush()
                        continue
                    except (BrokenPipeError, ConnectionResetError, OSError):
                        return
                if not write(message["event"], message["data"]):
                    return
                if message["event"] == "done":
                    return
        finally:
            run.unsubscribe(watcher)

    def _build_plan(self, body: dict) -> dict:
        # Secrets submitted along the way are held by the session, so a plan
        # request need not carry them again — and the plan itself never
        # contains one.
        return planning.build_plan(
            self._with_session_secrets(dict(body.get("answers") or {})))


class ThreadingServer(http.server.ThreadingHTTPServer):
    daemon_threads = True
    # A probe that never completes must not hold the wizard's shutdown open.
    allow_reuse_address = True


def pick_port(preferred: int, host: str) -> int:
    """The preferred port if it is free, otherwise whatever the OS hands out —
    a wizard that refuses to start because something else holds 8099 would be a
    silly way to fail."""
    for candidate in (preferred, 0):
        probe = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            probe.bind((host, candidate))
            return probe.getsockname()[1]
        except OSError:
            continue
        finally:
            probe.close()
    raise SystemExit("Could not bind a local port.")


def main() -> int:
    parser = argparse.ArgumentParser(description="SmallWorlds setup wizard")
    parser.add_argument("--port", type=int, default=8099)
    parser.add_argument("--no-browser", action="store_true",
                        help="print the URL instead of opening a browser")
    arguments = parser.parse_args()

    host = "127.0.0.1"
    port = pick_port(arguments.port, host)
    url = f"http://{host}:{port}/?t={SESSION.token}"

    server = ThreadingServer((host, port), Handler)

    # Flushed explicitly: stdout is block-buffered whenever it is not a
    # terminal, and the URL is the one thing this program exists to hand over.
    # Anything reading the wizard's output through a pipe would otherwise wait
    # for the buffer rather than for the server.
    banner = [
        "SmallWorlds setup wizard",
        "------------------------",
        f"  {url}",
        "",
        "  Bound to localhost only. The link carries a one-time session token,",
        "  so open it exactly as printed. Press Ctrl+C to stop.",
        "",
    ]
    print("\n".join(banner), flush=True)

    if not arguments.no_browser:
        threading.Timer(0.5, lambda: webbrowser.open(url)).start()

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        print("\nStopped.")
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
