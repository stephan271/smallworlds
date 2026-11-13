"""Canary Follower: Ring 0 follows main and reports whether each commit holds.

Runs on the Canary Cluster only (docs/adr/0060, design §3.4). Pure stdlib, run
from a ConfigMap like join and pod-gateway. /healthz answers while the
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
from backups import Backups
from checksjob import ChecksJob
from evidence import Evidence
from follower import Follower
from github import GitHub
from incidents import Incidents
from kube import Kube
from mergequeue import MergeQueue

log = logging.getLogger("main")


def _label(value: str) -> str:
    return value.replace("\\", "\\\\").replace('"', '\\"').replace("\n", " ")


def metrics(outcome, incidents=None) -> str:
    """The last verdict, for the CanaryCommitFailed alert (PrometheusRule next to
    this tenant). Nothing before the first verdict: no verdict is not a failure.
    And every incident handed to a person, for Ring0NeedsPerson."""
    lines = ["# HELP smallworlds_canary_failed 1 while main's last commit is rejected on Ring 0.",
             "# TYPE smallworlds_canary_failed gauge"]
    if outcome is not None:
        if outcome.restore_only is None:
            restore = "unknown"
        else:
            restore = ",".join(outcome.restore_only)
        lines.append(f'smallworlds_canary_failed{{sha="{outcome.sha[:12]}",state="{outcome.state}",'
                     f'restore_only="{_label(restore)}",description="{_label(outcome.description[:200])}"}} '
                     f'{1 if outcome.failed else 0}')
    if incidents is not None:
        lines += ["# HELP smallworlds_ring0_needs_person 1 for each Ring 0 incident handed to a person.",
                  "# TYPE smallworlds_ring0_needs_person gauge"]
        for incident in incidents.needing_person:
            lines.append(f'smallworlds_ring0_needs_person{{issue="{incident.number}",'
                         f'key="{_label(incident.key)}"}} 1')
    return "\n".join(lines) + "\n"


def serve(follower: Follower, port: int) -> None:
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path == "/healthz":
                body = b"ok\n"
            elif self.path == "/metrics":
                body = metrics(follower.last_outcome, follower.incidents).encode()
            elif self.path == "/status":
                outcome = follower.last_outcome
                queue = follower.queue
                body = json.dumps({"last_sha": follower.last_sha,
                                   "last_outcome": asdict(outcome) if outcome else None,
                                   "last_action": follower.last_action,
                                   "queue": queue.waiting if queue else "off",
                                   "incidents": [{"issue": i.number, "key": i.key, "phase": i.phase,
                                                  "attempts": i.attempts}
                                                 for i in follower.incidents.open]
                                   if follower.incidents else "off"},
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
    github = GitHub(s.github_api, s.github_repo, s.github_token, s.status_context)
    writes = bool(s.github_token)
    follower = Follower(
        s, github, ArgoCD(kube, s.argocd_namespace, s.root_app), kube,
        backups=Backups(kube, s.namespaces, s.backup_timeout_seconds, s.backups_keep)
        if s.on(s.pre_update_backups) else None,
        checks_job=ChecksJob(kube, s.checks_namespace, s.checks_cronjob, s.checks_timeout_seconds)
        if s.checks_cronjob else None,
        # Merging and reverting write to the repository: never without a token.
        queue=MergeQueue(github, s.branch, s.queue_label, s.queue_author,
                         merge=s.on(s.merge_queue), revert=s.on(s.auto_revert),
                         steward=s.steward_login if s.on(s.steward) else "",
                         owner=s.owner_login, protected=s.protected)
        if writes and (s.on(s.merge_queue) or s.on(s.auto_revert)) else None,
    )
    if writes and s.on(s.incidents):
        argocd = follower.argocd
        follower.incidents = Incidents(
            s, github, GitHub(s.github_api, s.overlay_repo, s.github_token, s.status_context),
            Evidence(kube, argocd, s.checks_namespace))
    serve(follower, s.http_port)
    log.info("following %s@%s every %ss; statuses %s; backups %s; checks %s; merge queue %s; revert %s; "
             "incidents %s; steward %s",
             s.github_repo, s.branch, s.poll_seconds, "on" if writes else "OFF (no token, log only)",
             "on" if follower.backups else "off", s.checks_cronjob or "off",
             "on" if writes and s.on(s.merge_queue) else "off",
             "on" if writes and s.on(s.auto_revert) else "off",
             s.overlay_repo if follower.incidents else "off",
             "on" if follower.incidents and s.on(s.steward) else "off")
    while True:
        try:
            follower.tick()
        except Exception:
            log.exception("tick failed; trying again next poll")
        time.sleep(s.poll_seconds)


if __name__ == "__main__":
    main()
