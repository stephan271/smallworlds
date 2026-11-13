"""Ring 0 merges Renovate's updates one at a time, and takes back the ones it rejects
(design §3.6, §4.1; docs/adr/0060).

Merging: while main's head is green on Ring 0, the oldest open Renovate PR that
carries the queue label (renovate.json puts it on minor, patch and digest
updates of what Ring 0 runs; never on majors), is mergeable and has passed its
PR checks is squash-merged. The next commit on main is then that one update,
and its verdict says whether the update holds. Nothing is merged while the
head is pending, rejected, or could not be judged, so one failure stops the
queue instead of burying it under the next update.

Rejecting: when Ring 0 rejects a commit, the PR it came from is labelled
`ring0-rejected-<fingerprint>`. The fingerprint covers the lines the update
adds, so Renovate re-opening the same update (it does, for a grouped PR) is
recognised and not merged again; a newer upstream release has new lines and is
tried. A rejected commit is then
  - reverted and the revert merged, when it came from the queue and touches no
    tenant whose database may have migrated, and is still main's head;
  - offered as a revert PR that a person merges, when a person made it;
  - not reverted at all when it touches a migrating tenant: reverting cannot
    bring that application back (Gallery v5.10 -> v5.8 on Ring 0). The restore
    from the pre-update backup is the Operator's, and the queue stays stopped
    until main is green again.

The Ring 0 Steward (design-per-app-updates.md §5.3–5.4) opens PRs too. One that
carries the queue label and `steward` is merged like an update, and also while
main is red when it is the fix for a stuck Ring 0 (advance_fix). It never merges
itself. A Steward PR that touches what judges Ring 0 (PROTECTED) waits for the
repository owner's approving review of its current head, so the agent cannot
make Ring 0 green by weakening the checks.
"""
import hashlib
import logging
import time

log = logging.getLogger("mergequeue")

REJECTED = "ring0-rejected-"
STEWARD = "steward"


def fingerprint(files: list[dict]) -> str:
    """The update's identity: which files and which lines it adds."""
    digest = hashlib.sha256()
    for entry in sorted(files, key=lambda f: f.get("filename", "")):
        digest.update(entry.get("filename", "").encode() + b"\n")
        for line in entry.get("patch", "").splitlines():
            if line.startswith("+") and not line.startswith("+++"):
                digest.update(line.encode() + b"\n")
    return digest.hexdigest()[:10]


class MergeQueue:
    def __init__(self, github, branch: str, label: str, author: str,
                 merge: bool = True, revert: bool = True, steward: str = "", owner: str = "",
                 protected: tuple[str, ...] = (), now=time.time):
        self.github = github
        self.branch = branch
        self.label = label
        self.author = author
        self.steward = steward      # the Steward's GitHub App login, "<slug>[bot]"; "" = none
        self.owner = owner          # who approves a Steward PR on a protected path
        self.protected = tuple(protected)
        self.merge_enabled = merge
        self.revert_enabled = revert
        self.now = now
        self.waiting: str | None = None  # why nothing was merged, for /status
        # For stuck detection (incidents.py): when something was last merged,
        # how many queued PRs are not merely waiting, and since when each
        # queued PR has been unmergeable or red (S4). Memory only: a restart
        # starts the clocks again, which errs towards waiting.
        self.last_merge: float = now()
        self.candidates = 0
        self.failing: dict[int, tuple[float, str]] = {}
        self.last_rejection: dict | None = None
        # PR number -> head that GitHub refused to merge (a token without the
        # Workflows permission cannot merge a change to .github/workflows).
        # Skipped until Renovate pushes a new head, so one refusal does not
        # block every update behind it.
        self.refused: dict[int, str] = {}

    def _queued(self, pull: dict) -> bool:
        labels = {l["name"] for l in pull.get("labels", [])}
        login = pull.get("user", {}).get("login")
        by_steward = bool(self.steward) and login == self.steward and STEWARD in labels
        return (login == self.author or by_steward) and not pull.get("draft") and self.label in labels

    def _is_steward(self, pull: dict) -> bool:
        return bool(self.steward) and pull.get("user", {}).get("login") == self.steward

    def _protected_paths(self, files: list[dict]) -> list[str]:
        if not self.protected:
            return []
        return sorted({f["filename"] for f in files
                       if f.get("filename", "").startswith(self.protected)
                       or (f.get("previous_filename") or "").startswith(self.protected)})

    def _fail(self, number: int, why: str) -> None:
        self.failing.setdefault(number, (self.now(), why))

    def take_rejection(self) -> dict | None:
        rejection, self.last_rejection = self.last_rejection, None
        return rejection

    def advance(self) -> int | None:
        """Merge the next update. Call only while main's head is green on Ring 0.
        Returns the merged PR's number, or None."""
        return self._merge_next(only_steward=False)

    def advance_fix(self) -> int | None:
        """While Ring 0 is stuck on a red head: merge a queued Steward PR, the
        fix forward. Updates wait; they would only bury the fix."""
        return self._merge_next(only_steward=True)

    def _merge_next(self, only_steward: bool) -> int | None:
        if not self.merge_enabled:
            return None
        rejected = {name[len(REJECTED):] for name in self.github.label_names()
                    if name.startswith(REJECTED)}
        candidates = 0
        still = set()
        for pull in self.github.open_pulls(self.branch):
            if not self._queued(pull) or (only_steward and not self._is_steward(pull)):
                continue
            number = pull["number"]
            still.add(number)
            detail = self.github.pull(number)
            files = self.github.pull_files(number)
            if fingerprint(files) in rejected:
                continue
            if detail.get("mergeable") is False:
                candidates += 1
                self._fail(number, "unmergeable (a conflict)")
                continue
            if detail.get("mergeable") is not True:
                continue  # None while GitHub computes it
            head = detail["head"]["sha"]
            readiness = self.github.readiness(head)
            if readiness == "waiting":
                self.failing.pop(number, None)
                continue  # checks running, or Renovate's release-age wait
            candidates += 1
            if readiness == "failed":
                self._fail(number, "red (a PR check failed)")
                continue
            if self.refused.get(number) == head:
                self._fail(number, "refused by GitHub")
                continue
            if self._is_steward(pull) and (paths := self._protected_paths(files)) \
                    and not self.github.approved_by(number, self.owner, head):
                log.info("#%s touches %s: waiting for %s's approval", number, ", ".join(paths), self.owner)
                continue
            try:
                merged = self.github.merge(number, head, detail["title"])
            except Exception as error:
                log.warning("GitHub refused to merge #%s: %s", number, error)
                self.refused[number] = head
                self._fail(number, "refused by GitHub")
                continue
            log.info("merged #%s (%s) as %s", number, detail["title"], merged[:12])
            self.failing.pop(number, None)
            self.last_merge = self.now()
            self.candidates = candidates
            self.waiting = None
            return number
        if not only_steward:
            self.candidates = candidates
            self.failing = {n: v for n, v in self.failing.items() if n in still}
        self.waiting = "no queued update is ready"
        return None

    def reject(self, outcome, head: str) -> str:
        """React to a rejected commit. Returns what was done, for the log."""
        sha = outcome.sha
        pulls = [p for p in self.github.pulls_of(sha) if p.get("merged_at")]
        pull = pulls[0] if pulls else None
        from_queue = pull is not None and self._queued(pull)
        mark = None
        if pull is not None:
            mark = REJECTED + fingerprint(self.github.pull_files(pull["number"]))
            self.github.add_label(pull["number"], mark)
            self.github.comment(pull["number"], f"Ring 0 rejected {sha[:12]}: {outcome.description}\n\n"
                                f"Labelled `{mark}`: the merge queue will not merge this same update again.")
        self.last_rejection = {"sha": sha, "pull": pull, "reverted": False,
                               "fingerprint": mark[len(REJECTED):] if mark else sha[:10],
                               "description": outcome.description}
        if outcome.restore_only or outcome.restore_only is None:
            return "not reverted: restore from the pre-update backup"
        if not self.revert_enabled:
            return "not reverted: automatic revert is off"
        if head != sha:
            return "not reverted: main has moved on"
        title = f"Revert \"{pull['title']}\"" if pull else f"Revert {sha[:12]}"
        message = (f"{title}\n\nRing 0 rejected {sha}: {outcome.description}\n\n"
                   f"Reverted by the Canary Follower (docs/adr/0060).")
        revert = self.github.revert_commit(sha, message)
        number = self.github.open_pull(f"ring0/revert-{sha[:12]}", revert, self.branch, title, message)
        if from_queue:
            self.github.merge(number, revert, title)
            self.last_rejection["reverted"] = True
            return f"reverted by #{number}"
        return f"revert offered in #{number}, for a person to merge"
