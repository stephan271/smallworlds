"""When Ring 0 cannot move on by itself, the Ring 0 Steward is called
(design-per-app-updates.md §5.1, §5.3; docs/adr/0064).

An **incident** is one issue in the private Ring 0 overlay repository. Its first
line is a hidden marker that holds its state, so a restarted follower picks up
where it was; everything below is for people and the Steward to read.

Kinds:
  cluster  Ring 0 itself is stuck, and the Steward may act on it:
           S1  main's head was rejected and not reverted (a migrating tenant,
               or a person's commit)
           S2  an error outlasted its retries
           S3  queued updates exist, but nothing was merged for a day
  pr       only a pull request needs work; Ring 0 keeps moving:
           S4  a queued update stayed unmergeable or red for 12 hours
           R   a queued update was rejected and reverted (it waits for the
               next upstream release unless the Steward makes it work)

Life of an incident (phase in the marker):
  new         opened; the Steward is asked (label steward-requested) when the
              day's run budget allows
  requested   waiting for the workflow to start (it swaps the label for
              steward-working). The follower verifies nothing meanwhile for a
              cluster incident, so a verdict never mixes with the Steward's work.
  working     the Steward runs. Its workflow ends with a comment carrying
              `<!-- steward-done result=… -->`.
  verifying   it acted on Ring 0: the follower verifies main's head again from
              the start; that verdict ends the attempt
  waiting-pr  it opened a PR (body line `Ring0-Incident: <issue>`); the verdict
              on the commit that PR becomes ends the attempt
  person      handed to the Operator (label ring0-needs-person, the alert
              Ring0NeedsPerson)

One attempt is one run that left something to test, and the verdict on it.
After `attempts` failed ones the incident goes to a person, or sooner when the
next step is not the Steward's to take (a restore, a credential).
"""
import datetime
import logging
import re
import time
from dataclasses import dataclass, field

import evidence as evidence_module

log = logging.getLogger("incidents")

STUCK = "ring0-stuck"
REJECTED_UPDATE = "ring0-rejected-update"
REQUESTED = "steward-requested"
WORKING = "steward-working"
NEEDS_PERSON = "ring0-needs-person"
STEWARD_LABEL = "steward"

_MARK = re.compile(r"<!-- ring0-incident (.*?) -->")
_DONE = re.compile(r"<!-- steward-done result=([a-z-]+) -->")
_LINK = re.compile(r"(?im)^Ring0-Incident:\s*#?(\d+)\s*$")

RESULTS = ("acted", "pr", "hold", "needs-person", "credential", "not-run")


def _iso(timestamp: str) -> float:
    return datetime.datetime.fromisoformat(timestamp.replace("Z", "+00:00")).timestamp()


@dataclass
class Incident:
    number: int
    key: str
    kind: str
    phase: str = "new"
    since: int = 0
    attempts: int = 0
    missed: int = 0
    labels: set[str] = field(default_factory=set)
    body: str = ""

    def marker(self) -> str:
        return (f"<!-- ring0-incident key={self.key} kind={self.kind} phase={self.phase} "
                f"since={self.since} attempts={self.attempts} missed={self.missed} -->")

    @classmethod
    def parse(cls, issue: dict) -> "Incident | None":
        found = _MARK.search(issue.get("body") or "")
        if not found:
            return None
        fields = dict(part.split("=", 1) for part in found.group(1).split() if "=" in part)
        return cls(number=issue["number"], key=fields.get("key", "?"), kind=fields.get("kind", "cluster"),
                   phase=fields.get("phase", "new"), since=int(fields.get("since", 0)),
                   attempts=int(fields.get("attempts", 0)), missed=int(fields.get("missed", 0)),
                   labels={l["name"] for l in issue.get("labels", [])}, body=issue.get("body") or "")


class Incidents:
    def __init__(self, settings, base, overlay, evidence, now=time.time):
        self.s = settings
        self.base = base          # GitHub client on the base repository
        self.overlay = overlay    # GitHub client on the private Ring 0 overlay
        self.evidence = evidence  # evidence.Evidence, or None
        self.now = now
        self.open: list[Incident] = []
        self.requests: list[float] = []  # when the Steward was asked, for the daily budget
        self._reverify: str | None = None

    # ------------------------------------------------------------ reading

    def refresh(self) -> None:
        found = {}
        for label in (STUCK, REJECTED_UPDATE):
            for issue in self.overlay.issues(label):
                if (incident := Incident.parse(issue)) is not None:
                    found[incident.number] = incident
        self.open = sorted(found.values(), key=lambda i: i.number)

    def paused(self) -> bool:
        """The Steward is about to act, or acting, on Ring 0: verify and merge nothing."""
        return any(i.kind == "cluster" and (i.labels & {REQUESTED, WORKING}) for i in self.open)

    def has_open_cluster(self) -> bool:
        return any(i.kind == "cluster" for i in self.open)

    @property
    def needing_person(self) -> list[Incident]:
        return [i for i in self.open if NEEDS_PERSON in i.labels]

    def take_reverify(self) -> str | None:
        sha, self._reverify = self._reverify, None
        return sha

    # ------------------------------------------------------------ writing

    def _save(self, incident: Incident) -> None:
        incident.body = _MARK.sub(incident.marker(), incident.body, count=1)
        self.overlay.edit_issue(incident.number, body=incident.body)

    def _set(self, incident: Incident, phase: str) -> None:
        incident.phase = phase
        incident.since = int(self.now())
        self._save(incident)

    def _label(self, incident: Incident, name: str) -> None:
        if name not in incident.labels:
            self.overlay.add_label(incident.number, name)
            incident.labels.add(name)

    def _unlabel(self, incident: Incident, name: str) -> None:
        if name in incident.labels:
            self.overlay.remove_label(incident.number, name)
            incident.labels.discard(name)

    def _person(self, incident: Incident, why: str) -> None:
        self._unlabel(incident, REQUESTED)
        self._unlabel(incident, WORKING)
        self._label(incident, NEEDS_PERSON)
        self.overlay.comment(incident.number, f"**Handed to a person:** {why}")
        self._set(incident, "person")
        log.info("incident #%s (%s) needs a person: %s", incident.number, incident.key, why)

    def _close(self, incident: Incident, why: str) -> None:
        self._unlabel(incident, REQUESTED)
        self._unlabel(incident, WORKING)
        self._unlabel(incident, NEEDS_PERSON)
        self.overlay.comment(incident.number, f"**Resolved:** {why}")
        self.overlay.edit_issue(incident.number, state="closed")
        self.open = [i for i in self.open if i.number != incident.number]
        log.info("incident #%s (%s) resolved: %s", incident.number, incident.key, why)

    def _open(self, key: str, kind: str, title: str, header: str, sha: str | None,
              pull: dict | None, cluster: bool) -> Incident:
        incident = Incident(number=0, key=key, kind=kind, since=int(self.now()))
        cluster_text = self.evidence.cluster() if (cluster and self.evidence) else ""
        change = evidence_module.change_section(self.base, sha, pull)
        body = incident.marker() + "\n" + evidence_module.bundle(header, change, cluster_text)
        label = STUCK if kind == "cluster" else REJECTED_UPDATE
        incident.number = self.overlay.create_issue(title[:200], body, [label])
        incident.body, incident.labels = body, {label}
        self.open.append(incident)
        log.info("opened incident #%s: %s", incident.number, key)
        return incident

    # ------------------------------------------------------------ the tick

    def review(self, follower, verdict) -> None:
        """Open, advance and close incidents. `verdict` is the Outcome this tick
        produced, or None when nothing was verified."""
        head = follower.github.head_sha(self.s.branch)
        queue = follower.queue
        linked = self._linked_incident(verdict) if verdict is not None else None
        self._detect(follower, head, verdict, queue, linked)
        for incident in list(self.open):
            self._resolve_or_advance(incident, follower, head, verdict, queue, linked)

    def _linked_incident(self, verdict) -> int | None:
        """The incident a verified commit's PR was opened for, if any."""
        try:
            for pull in self.base.pulls_of(verdict.sha):
                if STEWARD_LABEL in {l["name"] for l in pull.get("labels", [])}:
                    if found := _LINK.search(pull.get("body") or ""):
                        return int(found.group(1))
        except Exception:
            log.exception("could not read the PR of %s", verdict.sha[:12])
        return None

    def _exists(self, key: str) -> bool:
        return any(i.key == key for i in self.open)

    def _detect(self, follower, head, verdict, queue, linked) -> None:
        outcome = follower.last_outcome
        rejection = queue.take_rejection() if queue is not None else None

        if rejection is not None and linked is None and rejection.get("reverted"):
            pull = rejection.get("pull")
            if pull is not None and STEWARD_LABEL not in {l["name"] for l in pull.get("labels", [])}:
                key = f"R:{rejection['fingerprint']}"
                if not self._exists(key):
                    self._open(key, "pr", f"Ring 0 rejected {pull['title']} (#{pull['number']}); reverted",
                               f"## Rejected and reverted\n\nRing 0 rejected `{rejection['sha'][:12]}`: "
                               f"{rejection['description']}\n\nThe follower reverted it, so Ring 0 moves on, "
                               "but this application stays on its old version until it works. Make the "
                               "update work in a PR (`Ring0-Incident:` line, label `steward`), or hold it "
                               "back if upstream is broken.",
                               rejection["sha"], pull, cluster=True)

        if outcome is not None and outcome.sha == head and not outcome.superseded:
            reverted = rejection is not None and rejection.get("reverted") and rejection["sha"] == head
            if outcome.state == "failure" and not reverted:
                key = f"S1:{head[:12]}"
                if not self._exists(key):
                    restore = outcome.restore_only
                    incident = self._open(
                        key, "cluster", f"Ring 0 stuck: {head[:12]} rejected and not reverted",
                        f"## S1: main's head was rejected\n\n{outcome.description}\n\nSteps: "
                        + " | ".join(outcome.steps)
                        + (f"\n\nIt touches **{', '.join(restore)}**, whose database may have migrated: "
                           "reverting will not bring it back. Fix forward, or ask for a restore."
                           if restore else ""),
                        head, self._pull_of(head), cluster=True)
                    if restore and not self.s.on(self.s.auto_restore):
                        self._label(incident, NEEDS_PERSON)
                        self.overlay.comment(incident.number, "**Restore needed** and automatic restore is "
                                             "off: the Operator is told now. The Steward still looks for a "
                                             "fix forward.")
            attempts = follower.errors.get(head, (0, 0.0))[0]
            if outcome.state == "error" and attempts > self.s.error_retries:
                key = f"S2:{head[:12]}"
                if not self._exists(key):
                    self._open(key, "cluster", f"Ring 0 stuck: {head[:12]} could not be verified",
                               f"## S2: an error outlasted {self.s.error_retries} retries\n\n"
                               f"{outcome.description}\n\nSteps: " + " | ".join(outcome.steps),
                               head, self._pull_of(head), cluster=True)

        if queue is not None:
            now = self.now()
            if (queue.candidates and now - queue.last_merge > self.s.stuck_queue_seconds
                    and not self.has_open_cluster() and not self._exists("S3:queue")):
                self._open("S3:queue", "cluster", "Ring 0 stuck: nothing merged for a day",
                           f"## S3: {queue.candidates} queued updates, nothing merged since "
                           f"{datetime.datetime.fromtimestamp(queue.last_merge, datetime.UTC):%Y-%m-%d %H:%M} UTC"
                           f"\n\nThe queue says: {queue.waiting}", head, None, cluster=True)
            for number, (since, why) in sorted(queue.failing.items()):
                key = f"S4:#{number}"
                if now - since > self.s.unmergeable_seconds and not self._exists(key):
                    self._open(key, "pr", f"Ring 0 queue: #{number} stays {why}",
                               f"## S4: queued update #{number} has been {why} for "
                               f"{int((now - since) // 3600)} hours\n\nMake it mergeable and green (a PR "
                               "on top of its branch, or a fix on main), or close it if the update is "
                               "not wanted.", None, self.base.pull(number), cluster=False)

    def _pull_of(self, sha: str) -> dict | None:
        try:
            pulls = [p for p in self.base.pulls_of(sha) if p.get("merged_at")]
            return pulls[0] if pulls else None
        except Exception:
            return None

    def _resolved(self, incident: Incident, follower, head, verdict, queue, linked) -> str | None:
        outcome = follower.last_outcome
        green = outcome is not None and outcome.sha == head and outcome.state == "success"
        if incident.kind == "cluster" and incident.key.startswith(("S1:", "S2:")) and green:
            return f"main is green on Ring 0 again at `{head[:12]}`."
        if incident.key == "S3:queue" and queue is not None and queue.last_merge > incident.since:
            return "the queue merged an update again."
        if incident.key.startswith("S4:#") and queue is not None:
            number = int(incident.key[4:])
            if number not in queue.failing:
                return f"#{number} is no longer stuck (merged, closed or ready)."
        if linked == incident.number and verdict is not None and verdict.state == "success":
            return f"the Steward's fix `{verdict.sha[:12]}` passed Ring 0's verification."
        return None

    def _resolve_or_advance(self, incident, follower, head, verdict, queue, linked) -> None:
        if why := self._resolved(incident, follower, head, verdict, queue, linked):
            self._close(incident, why)
            return
        if not self.s.on(self.s.steward):
            return
        phase = incident.phase
        now = self.now()
        if phase == "new":
            if now - incident.since < 0:
                return  # waiting out a retry delay
            self._request(incident)
        elif phase == "requested":
            if WORKING in incident.labels:
                self._set(incident, "working")
            elif REQUESTED not in incident.labels:
                # Started and finished between two polls: its result is there.
                self._finished(incident, head, follower)
            elif now - incident.since > self.s.steward_request_timeout_seconds:
                self._unlabel(incident, REQUESTED)
                self._missed(incident, "the Steward's workflow did not start (is the runner on Ring 0's "
                             "server up?)")
        elif phase == "working":
            if WORKING in incident.labels or REQUESTED in incident.labels:
                if now - incident.since > self.s.steward_working_timeout_seconds:
                    self._unlabel(incident, WORKING)
                    self._missed(incident, "the Steward's run did not finish")
                return
            self._finished(incident, head, follower)
        elif phase in ("verifying", "waiting-pr"):
            ours = (linked == incident.number) if phase == "waiting-pr" else (
                verdict is not None and verdict.sha == head)
            if (ours and verdict is not None and not verdict.superseded
                    and verdict.state in ("failure", "error")):
                self._failed_attempt(incident, verdict)

    def _request(self, incident: Incident) -> None:
        now = self.now()
        self.requests = [t for t in self.requests if now - t < 86400]
        if len(self.requests) >= self.s.steward_runs_per_day:
            return
        if incident.kind == "cluster" and any(
                i.kind == "cluster" and i.phase in ("requested", "working", "verifying")
                for i in self.open if i is not incident):
            return  # one Steward on Ring 0 at a time
        self.requests.append(now)
        self._label(incident, REQUESTED)
        self._set(incident, "requested")

    def _missed(self, incident: Incident, why: str) -> None:
        incident.missed += 1
        if incident.missed >= self.s.steward_attempts:
            self._person(incident, f"{why}, {incident.missed} times.")
            return
        self.overlay.comment(incident.number, f"_{why}; asking again in "
                             f"{self.s.steward_retry_seconds // 3600} h._")
        incident.phase = "new"
        incident.since = int(self.now()) + self.s.steward_retry_seconds
        self._save(incident)

    def _finished(self, incident: Incident, head: str, follower) -> None:
        result = "not-run"
        for comment in reversed(self.overlay.comments(incident.number)):
            if _iso(comment["created_at"]) < incident.since:
                break
            if found := _DONE.search(comment.get("body") or ""):
                result = found.group(1)
                break
        log.info("incident #%s: the Steward finished: %s", incident.number, result)
        if result == "acted" and incident.kind == "cluster":
            self._reverify = head
            self._set(incident, "verifying")
        elif result in ("acted", "pr"):
            self._set(incident, "waiting-pr")
        elif result == "hold" and incident.kind == "pr":
            self._close(incident, "the Steward held this version back; the next upstream release "
                        "will be tried.")
        elif result == "credential":
            self._person(incident, "the Steward's credential was refused (Claude token or GitHub App).")
        elif result in ("needs-person", "hold"):
            self._person(incident, "the Steward says the next step needs a person (see its comment).")
        else:
            self._missed(incident, "the Steward's run ended without a result (a usage limit, or it failed)")

    def _failed_attempt(self, incident: Incident, verdict) -> None:
        incident.attempts += 1
        if incident.attempts >= self.s.steward_attempts:
            self._save(incident)
            self._person(incident, f"{incident.attempts} attempts did not make Ring 0 accept it. "
                         f"Last verdict on `{verdict.sha[:12]}`: {verdict.description}")
            return
        cluster = self.evidence.cluster() if self.evidence else ""
        header = (f"## Attempt {incident.attempts} of {self.s.steward_attempts} failed\n\n"
                  f"Ring 0's verdict on `{verdict.sha[:12]}`: **{verdict.state}** — {verdict.description}"
                  "\n\nSteps: " + " | ".join(verdict.steps))
        self.overlay.comment(incident.number, evidence_module.bundle(header, "", cluster))
        self._set(incident, "new")
        self._request(incident)
