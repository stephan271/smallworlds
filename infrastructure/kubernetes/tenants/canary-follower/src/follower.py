"""One commit of main, followed onto Ring 0 and verified (design §3.4, §3.5).

The verdict lives on the commit as the GitHub status `ring0/verified`, so it is
also the follower's memory: a restarted pod skips a commit that already has a
final verdict and verifies again one left at `pending`.

Steps: a pre-update backup when the commit touches a migrating tenant, follow,
converge, smoke, the e2e and data checks Job, a quiet period. After a verdict
the merge queue merges the next update (success) or reacts to the rejection.

States:
  success  every step passed
  failure  Ring 0 did not take the commit: not converged, smoke or alert
  error    the commit could not be judged: main moved on mid-way, or the
           follower itself failed. Not a verdict on the commit.
"""
import logging
import time
from dataclasses import dataclass, field

import checks
from argocd import UNREACHABLE
from backups import BackupFailed
from github import FINAL_STATES

log = logging.getLogger("follower")

NOT_YET = "e2e and data checks not yet run"


@dataclass
class Outcome:
    sha: str
    state: str
    description: str
    steps: list[str] = field(default_factory=list)
    # Migrating tenants the commit touched: reverting it will not bring them
    # back (design §3.6). None when the changed files could not be read.
    restore_only: list[str] | None = field(default_factory=list)
    superseded: bool = False

    @property
    def failed(self) -> bool:
        """What the Operator is alerted about: a rejected commit, or one the
        follower could not judge — not one that main simply moved past."""
        return self.state in ("failure", "error") and not self.superseded


def touched_tenants(paths: list[str], tenants: list[str]) -> list[str]:
    found = set()
    for tenant in tenants:
        for path in paths:
            if (path.startswith(f"infrastructure/kubernetes/tenants/{tenant}/")
                    or path == f"infrastructure/kubernetes/apps/{tenant}.yaml"):
                found.add(tenant)
    return sorted(found)


def _list(items: list[str], limit: int = 3) -> str:
    shown = ", ".join(items[:limit])
    return shown + (f" +{len(items) - limit} more" if len(items) > limit else "")


class Follower:
    def __init__(self, settings, github, argocd, kube,
                 smoke=checks.smoke, quiet=checks.quiet, hosts=checks.ingress_hosts,
                 settle=checks.settle,
                 backups=None, checks_job=None, queue=None, clock=time.monotonic):
        self.s = settings
        self.github = github
        self.argocd = argocd
        self.kube = kube
        self._smoke = smoke
        self._quiet = quiet
        self._settle = settle
        self._hosts = hosts
        self.backups = backups
        self.checks_job = checks_job
        self.queue = queue
        self.last_sha: str | None = None
        self.last_outcome: Outcome | None = None
        self.last_action: str | None = None
        self.clock = clock
        # sha -> (verifications that ended in error, when the last one did).
        # An error is no verdict on the commit (GitHub, the object store or npm
        # was away), so the head is verified again after a while rather than
        # leaving the queue stopped until someone pushes.
        self.errors: dict[str, tuple[int, float]] = {}

    def _note_error(self, sha: str) -> None:
        attempts, _ = self.errors.get(sha, (0, 0.0))
        self.errors[sha] = (attempts + 1, self.clock())

    def _retry_due(self, sha: str) -> bool:
        attempts, at = self.errors.get(sha, (0, 0.0))
        return (0 < attempts <= self.s.error_retries
                and self.clock() - at >= self.s.error_retry_seconds)

    def tick(self) -> Outcome | None:
        sha = self.github.head_sha(self.s.branch)
        if sha == self.last_sha:
            if not self._retry_due(sha):
                self._advance(sha)
                return None
            log.info("%s ended in error; verifying it again", sha[:12])
        else:
            verdict = self.github.status(sha)
            if verdict in FINAL_STATES:
                log.info("%s already has a verdict; nothing to do", sha[:12])
                self.last_sha = sha
                # A restarted pod still reports what it last found, or the alert on
                # a rejected commit would resolve itself with every restart.
                outcome = Outcome(sha, verdict, "verdict already posted")
                if verdict != "success":
                    outcome.restore_only = self._restore_only(sha)
                if verdict == "error":
                    self._note_error(sha)
                self.last_outcome = outcome
                self._advance(sha)
                return None
        outcome = self.verify(sha)
        self.last_sha = sha
        self.last_outcome = outcome
        if outcome.state == "error" and not outcome.superseded:
            self._note_error(sha)
        else:
            # A verdict ends the retries: a failure is final, and a success
            # must not be verified again an hour later.
            self.errors.pop(sha, None)
        if outcome.state == "success":
            self._advance(sha)
        # Only a rejection is acted on: an error says nothing about the commit.
        elif outcome.state == "failure" and self.queue is not None:
            try:
                self.last_action = self.queue.reject(outcome, self.github.head_sha(self.s.branch))
            except Exception as error:
                log.exception("reacting to the rejection of %s failed", sha[:12])
                self.last_action = f"reaction failed: {type(error).__name__}: {error}"
            log.info("%s rejected: %s", sha[:12], self.last_action)
        return outcome

    def _advance(self, sha: str) -> None:
        """Merge the next queued update, only while this head is green."""
        if self.queue is None:
            return
        outcome = self.last_outcome
        if outcome is None or outcome.sha != sha or outcome.state != "success":
            return
        # Green, but only for itself: a commit that landed on main meanwhile
        # (a person's push during the hour of checks) has not been verified,
        # and a merge now would put an update on top of it unverified. Ring 0,
        # 2026-11-12: aa71398 went green while 598d061 sat on top, and #38 merged.
        head = self.github.head_sha(self.s.branch)
        if head != sha:
            log.info("%s is green but main has moved on to %s; merging waits for its verdict",
                     sha[:12], head[:12])
            return
        try:
            merged = self.queue.advance()
        except Exception as error:
            log.exception("the merge queue failed")
            self.last_action = f"merge queue failed: {type(error).__name__}: {error}"
            return
        if merged is not None:
            self.last_action = f"merged #{merged}"

    def _restore_only(self, sha: str) -> list[str] | None:
        try:
            return touched_tenants(self.github.changed_paths(sha), self.s.migrating)
        except Exception:
            log.exception("could not read the files %s changed", sha[:12])
            return None

    def _finish(self, sha: str, state: str, description: str, steps: list[str],
                superseded: bool = False) -> Outcome:
        outcome = Outcome(sha, state, description, steps, superseded=superseded)
        if outcome.failed:
            outcome.restore_only = self._restore_only(sha)
            if outcome.restore_only:
                description += f"; touches {', '.join(outcome.restore_only)}: restore, do not revert"
            elif outcome.restore_only is None:
                description += "; changed files unreadable"
            outcome.description = description
        log.info("%s %s: %s | %s", sha[:12], state, description, " | ".join(steps))
        self.github.set_status(sha, state, description)
        return outcome

    def verify(self, sha: str) -> Outcome:
        s = self.s
        steps: list[str] = []
        self.github.set_status(sha, "pending", "Ring 0 is syncing and verifying this commit")
        try:
            if self.backups is not None:
                touched = self._restore_only(sha)
                # Changes unreadable: back up every migrating tenant rather than guess.
                touched = s.migrating if touched is None else touched
                if touched:
                    try:
                        taken = self.backups.before(sha, touched)
                    except BackupFailed as error:
                        return self._finish(sha, "error", f"Pre-update backup failed, not synced: {error}",
                                            steps)
                    if taken:
                        steps.append(f"backup {len(taken)} databases")

            synced, deferred = self.argocd.follow(s.refresh_timeout_seconds)
            steps.append(f"synced {len(synced)} Applications"
                         + (f", {len(deferred)} after their running sync" if deferred else ""))

            converged, lagging = self.argocd.converge(s.converge_timeout_seconds,
                                                      s.converge_interval_seconds, deferred)
            if not converged:
                if all(UNREACHABLE in entry for entry in lagging):
                    # Only an upstream that did not answer kept Ring 0 from the
                    # commit: no verdict, tried again later.
                    return self._finish(sha, "error", "Could not render, " + _list(lagging), steps)
                return self._finish(sha, "failure", "Not converged: " + _list(lagging), steps)
            steps.append("converged")

            stale = self.argocd.not_at(sha, s.base_repo_url)
            if stale:
                return self._finish(sha, "error",
                                    "Superseded before verified: " + _list(stale), steps,
                                    superseded=True)

            note = self._settle(s.prometheus_url, s.settle_load_percent / 100,
                                s.settle_timeout_seconds, s.settle_interval_seconds)
            if note:
                steps.append(note)

            hosts = self._hosts(self.kube)
            failed = self._smoke(hosts, s.smoke_attempts, s.smoke_retry_seconds)
            if failed:
                return self._finish(sha, "failure", "Smoke failed: " + _list(failed), steps)
            steps.append(f"smoke {len(hosts)} hosts")

            if self.checks_job is not None:
                passed, summary = self.checks_job.run(sha)
                if passed is None:
                    return self._finish(sha, "error", "Checks could not run: " + summary, steps)
                if not passed:
                    return self._finish(sha, "failure", "Checks failed: " + summary, steps)
                steps.append(summary)

            firing = self._quiet(s.alertmanager_url, s.quiet_seconds, s.quiet_interval_seconds)
            if firing:
                return self._finish(sha, "failure", "Critical alert: " + _list(firing), steps)
            steps.append(f"quiet {s.quiet_seconds // 60} min")

            shown = [step for step in steps if not step.startswith("synced ")]
            return self._finish(sha, "success", "Ring 0: " + ", ".join(shown)
                                + ("" if self.checks_job is not None else f"; {NOT_YET}"), steps)
        except Exception as error:
            log.exception("verifying %s failed", sha[:12])
            return self._finish(sha, "error",
                                f"Follower error: {type(error).__name__}: {error}", steps)
