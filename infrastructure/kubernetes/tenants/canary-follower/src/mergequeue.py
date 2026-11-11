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
"""
import hashlib
import logging

log = logging.getLogger("mergequeue")

REJECTED = "ring0-rejected-"


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
                 merge: bool = True, revert: bool = True):
        self.github = github
        self.branch = branch
        self.label = label
        self.author = author
        self.merge_enabled = merge
        self.revert_enabled = revert
        self.waiting: str | None = None  # why nothing was merged, for /status
        # PR number -> head that GitHub refused to merge (a token without the
        # Workflows permission cannot merge a change to .github/workflows).
        # Skipped until Renovate pushes a new head, so one refusal does not
        # block every update behind it.
        self.refused: dict[int, str] = {}

    def _queued(self, pull: dict) -> bool:
        return (pull.get("user", {}).get("login") == self.author
                and not pull.get("draft")
                and self.label in {l["name"] for l in pull.get("labels", [])})

    def advance(self) -> int | None:
        """Merge the next update. Call only while main's head is green on Ring 0.
        Returns the merged PR's number, or None."""
        if not self.merge_enabled:
            return None
        rejected = {name[len(REJECTED):] for name in self.github.label_names()
                    if name.startswith(REJECTED)}
        for pull in self.github.open_pulls(self.branch):
            if not self._queued(pull):
                continue
            number = pull["number"]
            detail = self.github.pull(number)
            if detail.get("mergeable") is not True:
                continue  # None while GitHub computes it; False on a conflict Renovate will rebase
            if fingerprint(self.github.pull_files(number)) in rejected:
                continue
            head = detail["head"]["sha"]
            if self.refused.get(number) == head or not self.github.checks_passed(head):
                continue
            try:
                merged = self.github.merge(number, head, detail["title"])
            except Exception as error:
                log.warning("GitHub refused to merge #%s: %s", number, error)
                self.refused[number] = head
                continue
            log.info("merged #%s (%s) as %s", number, detail["title"], merged[:12])
            self.waiting = None
            return number
        self.waiting = "no queued update is ready"
        return None

    def reject(self, outcome, head: str) -> str:
        """React to a rejected commit. Returns what was done, for the log."""
        sha = outcome.sha
        pulls = [p for p in self.github.pulls_of(sha) if p.get("merged_at")]
        pull = pulls[0] if pulls else None
        from_queue = pull is not None and self._queued(pull)
        if pull is not None:
            mark = REJECTED + fingerprint(self.github.pull_files(pull["number"]))
            self.github.add_label(pull["number"], mark)
            self.github.comment(pull["number"], f"Ring 0 rejected {sha[:12]}: {outcome.description}\n\n"
                                f"Labelled `{mark}`: the merge queue will not merge this same update again.")
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
            return f"reverted by #{number}"
        return f"revert offered in #{number}, for a person to merge"
