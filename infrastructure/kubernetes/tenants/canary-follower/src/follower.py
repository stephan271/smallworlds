"""One commit of main, followed onto Ring 0 and verified (design §3.4, §3.5).

The verdict lives on the commit as the GitHub status `ring0/verified`, so it is
also the follower's memory: a restarted pod skips a commit that already has a
final verdict and verifies again one left at `pending`.

States:
  success  every step passed
  failure  Ring 0 did not take the commit: not converged, smoke or alert
  error    the commit could not be judged: main moved on mid-way, or the
           follower itself failed. Not a verdict on the commit.
"""
import logging
from dataclasses import dataclass, field

import checks
from github import FINAL_STATES

log = logging.getLogger("follower")

NOT_YET = "e2e and data checks not yet run"


@dataclass
class Outcome:
    sha: str
    state: str
    description: str
    steps: list[str] = field(default_factory=list)


def _list(items: list[str], limit: int = 3) -> str:
    shown = ", ".join(items[:limit])
    return shown + (f" +{len(items) - limit} more" if len(items) > limit else "")


class Follower:
    def __init__(self, settings, github, argocd, kube,
                 smoke=checks.smoke, quiet=checks.quiet, hosts=checks.ingress_hosts):
        self.s = settings
        self.github = github
        self.argocd = argocd
        self.kube = kube
        self._smoke = smoke
        self._quiet = quiet
        self._hosts = hosts
        self.last_sha: str | None = None
        self.last_outcome: Outcome | None = None

    def tick(self) -> Outcome | None:
        sha = self.github.head_sha(self.s.branch)
        if sha == self.last_sha:
            return None
        if self.github.status(sha) in FINAL_STATES:
            log.info("%s already has a verdict; nothing to do", sha[:12])
            self.last_sha = sha
            return None
        outcome = self.verify(sha)
        self.last_sha = sha
        self.last_outcome = outcome
        return outcome

    def _finish(self, sha: str, state: str, description: str, steps: list[str]) -> Outcome:
        log.info("%s %s: %s | %s", sha[:12], state, description, " | ".join(steps))
        self.github.set_status(sha, state, description)
        return Outcome(sha, state, description, steps)

    def verify(self, sha: str) -> Outcome:
        s = self.s
        steps: list[str] = []
        self.github.set_status(sha, "pending", "Ring 0 is syncing and verifying this commit")
        try:
            synced = self.argocd.follow(s.refresh_timeout_seconds)
            steps.append(f"synced {len(synced)} Applications")

            converged, lagging = self.argocd.converge(s.converge_timeout_seconds,
                                                      s.converge_interval_seconds)
            if not converged:
                return self._finish(sha, "failure", "Not converged: " + _list(lagging), steps)
            steps.append("converged")

            stale = self.argocd.not_at(sha, s.base_repo_url)
            if stale:
                return self._finish(sha, "error",
                                    "Superseded before verified: " + _list(stale), steps)

            hosts = self._hosts(self.kube)
            failed = self._smoke(hosts, s.smoke_attempts, s.smoke_retry_seconds)
            if failed:
                return self._finish(sha, "failure", "Smoke failed: " + _list(failed), steps)
            steps.append(f"smoke {len(hosts)} hosts")

            firing = self._quiet(s.alertmanager_url, s.quiet_seconds, s.quiet_interval_seconds)
            if firing:
                return self._finish(sha, "failure", "Critical alert: " + _list(firing), steps)
            steps.append(f"quiet {s.quiet_seconds // 60} min")

            return self._finish(sha, "success", "Ring 0: " + ", ".join(steps[1:])
                                + f"; {NOT_YET}", steps)
        except Exception as error:
            log.exception("verifying %s failed", sha[:12])
            return self._finish(sha, "error",
                                f"Follower error: {type(error).__name__}: {error}", steps)
