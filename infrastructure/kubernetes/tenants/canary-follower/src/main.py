"""Canary Follower: Ring 0 follows main and reports whether each commit holds.

Runs on the Canary Cluster only (docs/adr/0060, design §3.4). Pure stdlib, run
from a ConfigMap like Hermes and Remediation. /healthz answers while the
process lives; /status shows the last verdict.
"""
import json
import logging
import threading
import time
from dataclasses import asdict
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import settings as settings_module
from argocd import ArgoCD
from follower import Follower
from github import GitHub
from kube import Kube

log = logging.getLogger("main")


def serve(follower: Follower, port: int) -> None:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path == "/healthz":
                body = b"ok\n"
            elif self.path == "/status":
                outcome = follower.last_outcome
                body = json.dumps({"last_sha": follower.last_sha,
                                   "last_outcome": asdict(outcome) if outcome else None},
                                  indent=2).encode()
            else:
                self.send_error(404)
                return
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *args):
            pass

    server = ThreadingHTTPServer(("", port), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()


def main() -> None:
    logging.basicConfig(level=logging.INFO,
                        format="%(asctime)s %(levelname)s %(name)s: %(message)s")
    s = settings_module.from_environment()
    kube = Kube()
    follower = Follower(
        s,
        GitHub(s.github_api, s.github_repo, s.github_token, s.status_context),
        ArgoCD(kube, s.argocd_namespace, s.root_app),
        kube,
    )
    serve(follower, s.http_port)
    log.info("following %s@%s every %ss; statuses %s", s.github_repo, s.branch,
             s.poll_seconds, "on" if s.github_token else "OFF (no token, log only)")
    while True:
        try:
            follower.tick()
        except Exception:
            log.exception("tick failed; trying again next poll")
        time.sleep(s.poll_seconds)


if __name__ == "__main__":
    main()
