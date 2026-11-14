"""Ring State: what Ring 0 is doing, published as ring0.json (design §6).

The follower writes one public file, `ring0.json` on the branch `ring-state` of
the base repository, read without a token through raw.githubusercontent.com. The
public Ring 0 board (`ring0-board/`, GitHub Pages) is drawn from it, and the
Update Desk will read the same file.

This is the part of §6 the board needs: `head` and `now` in §6's shape, plus
`applications` (Argo CD's own report on each Application of Ring 0), `commits`
(main's recent history with each verdict) and `pulls` (the open PRs). Every
commit and PR names the `cards` it touches: the Ring 0 Applications its files
belong to. The Update Units, their fingerprints, `verified`, `contents` and the
back-fill come with phase D1 proper.

Nothing private goes in: no log, no Secret, no hostname beyond what the public
repository names. Application fields are Argo CD's status, and a sync message
is cut to 300 characters.

A write is one commit through the contents API. The file is rewritten only when
something but its timestamp changed, or after HEARTBEAT seconds so a reader can
tell a quiet Ring 0 from a follower that stopped reporting.
"""
import json
import logging
import re
import time
import urllib.error
from datetime import datetime, timezone

from github import FINAL_STATES

log = logging.getLogger("ringstate")

SCHEMA = 1
FILE = "ring0.json"

# Member-facing applications; every other Application is the Platform.
COMMUNITY_APPS = frozenset({"bulwark", "collabora", "dashboard", "euro-office", "excalidraw",
                            "forgejo", "immich", "jitsi", "join", "nextcloud", "pod-gateway",
                            "stalwart"})
# Ring 0's own machinery: what verifies, not what is verified.
MACHINERY = frozenset({"canary-follower", "smallworlds-root"})

# Files in apps/ that are no Application: the root Application applies them
# itself. tests/test_ringstate.py checks this list against apps/.
ROOT_RESOURCES = frozenset({"alertmanager-config", "argocd-ingress", "backup-alerts",
                            "monitoring-private-middleware", "node-disk-alerts",
                            "persistent-storage", "pod-archive-alerts", "priorityclasses",
                            "update-alerts"})
# Tenant folders that another Application deploys.
TENANT_ALIASES = {"gallery": "immich"}

ROOT = "smallworlds-root"
REPOSITORY = "repository"  # changes nothing Ring 0 runs: docs, scripts, Terraform

_TENANT = re.compile(r"infrastructure/kubernetes/tenants/([^/]+)/")
_APP = re.compile(r"infrastructure/kubernetes/apps/([^/]+)\.yaml$")
_PULL = re.compile(r"\(#(\d+)\)$|^Merge pull request #(\d+)")

COMMITS = 25
MESSAGE_LIMIT = 300

README = """# ring-state

Written by the Canary Follower on Ring 0, never by hand, and never merged.
`ring0.json` is what Ring 0 is doing and has verified
(`plans-and-walkthroughs/design-per-app-updates.md` §6); the public Ring 0
board reads it. Each update is one commit, so this branch's history is the record.
"""


def cards_for(paths: list[str]) -> list[str]:
    """The Ring 0 cards a change touches, from the files it changed."""
    found = set()
    for path in paths:
        if m := _TENANT.match(path):
            found.add(TENANT_ALIASES.get(m.group(1), m.group(1)))
        elif m := _APP.match(path):
            name = m.group(1)
            found.add(ROOT if name in ROOT_RESOURCES else name)
        elif path.startswith("infrastructure/kubernetes/"):
            found.add(ROOT)  # bases/, the root kustomization, namespaces
        elif path.startswith(("e2e-tests/", "admin-tools/canary/")):
            found.add("canary-follower")  # what Ring 0's checks run
        else:
            found.add(REPOSITORY)
    return sorted(found)


def group_of(name: str) -> str:
    if name in MACHINERY:
        return "machinery"
    return "apps" if name in COMMUNITY_APPS else "platform"


def _iso(seconds: float) -> str:
    return datetime.fromtimestamp(seconds, timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _short(text: str | None) -> str | None:
    if not text:
        return None
    return text if len(text) <= MESSAGE_LIMIT else text[:MESSAGE_LIMIT - 1] + "…"


def application(app: dict, base_repo_url: str) -> dict:
    """One Application as Argo CD reports it; nothing from inside the cluster."""
    spec = app.get("spec", {})
    source = spec.get("source") or (spec.get("sources") or [{}])[0]
    status = app.get("status", {})
    operation = status.get("operationState", {})
    name = app["metadata"]["name"]
    revision = status.get("sync", {}).get("revision") or ""
    repo = source.get("repoURL", "")
    phase = operation.get("phase")
    return {
        "name": name,
        "group": group_of(name),
        "health": status.get("health", {}).get("status", "Unknown"),
        "sync": status.get("sync", {}).get("status", "Unknown"),
        # Helm chart, base repository, or the overlay that routes it (CLAUDE.md).
        "source": "chart" if source.get("chart") else ("base" if repo == base_repo_url else "overlay"),
        "chart": source.get("chart"),
        "version": source.get("targetRevision") if source.get("chart") else None,
        "revision": revision if not source.get("chart") else None,
        "images": sorted(status.get("summary", {}).get("images", [])),
        "operation": {"phase": phase, "finished": operation.get("finishedAt"),
                      "message": _short(operation.get("message")) if phase != "Succeeded" else None}
        if phase else None,
        "wave": int(app["metadata"].get("annotations", {}).get("argocd.argoproj.io/sync-wave", "0")),
    }


class RingState:
    def __init__(self, settings, github, kube, follower, wallclock=time.time):
        self.s = settings
        self.github = github
        self.kube = kube
        self.follower = follower
        self.wallclock = wallclock
        self._paths: dict[str, list[str]] = {}       # commit sha -> files
        self._pull_paths: dict[str, list[str]] = {}  # PR head sha -> files
        self._verdicts: dict[str, dict] = {}         # final verdicts only
        self._published: str | None = None           # last body, without `updated`
        self._published_at = 0.0
        self._file_sha: str | None = None

    # ------------------------------------------------------------ reading

    def _commit_paths(self, sha: str) -> list[str]:
        if sha not in self._paths:
            self._paths[sha] = self.github.changed_paths(sha)
        return self._paths[sha]

    def _verdict(self, sha: str) -> dict | None:
        if sha in self._verdicts:
            return self._verdicts[sha]
        verdict = self.github.verdict(sha)
        if verdict and verdict["state"] in FINAL_STATES:
            self._verdicts[sha] = verdict
        return verdict

    def _commits(self) -> list[dict]:
        commits = []
        for entry in self.github.commits(self.s.branch, COMMITS):
            sha = entry["sha"]
            lines = [l for l in entry["commit"]["message"].splitlines() if l.strip()]
            title = lines[0] if lines else ""
            m = _PULL.search(title)
            if title.startswith("Merge pull request #") and len(lines) > 1:
                title = lines[1]  # a merge commit's body starts with the PR's title
            commits.append({
                "commit": sha,
                "title": title,
                "author": (entry.get("author") or {}).get("login")
                or entry["commit"]["author"].get("name"),
                "date": entry["commit"]["committer"]["date"],
                "pull": int(m.group(1) or m.group(2)) if m else None,
                "cards": cards_for(self._commit_paths(sha)),
                "verdict": self._verdict(sha),
            })
        return commits

    def _pulls(self) -> list[dict]:
        pulls = []
        for pull in self.github.open_pulls(self.s.branch):
            head = pull["head"]["sha"]
            if head not in self._pull_paths:
                self._pull_paths[head] = [f["filename"] for f in self.github.pull_files(pull["number"])]
            labels = [l["name"] for l in pull.get("labels", [])]
            pulls.append({
                "number": pull["number"],
                "title": pull["title"],
                "author": pull["user"]["login"],
                "created": pull["created_at"],
                "draft": bool(pull.get("draft")),
                "queued": self.s.queue_label in labels,
                "rejected": any(l.startswith("ring0-rejected") for l in labels),
                "labels": labels,
                "cards": cards_for(self._pull_paths[head]),
            })
        return pulls

    def _applications(self) -> list[dict]:
        apps = self.kube.get(f"/apis/argoproj.io/v1alpha1/namespaces/{self.s.argocd_namespace}"
                             "/applications").get("items", [])
        return sorted((application(a, self.s.base_repo_url) for a in apps),
                      key=lambda a: (a["wave"], a["name"]))

    def _now(self, head: dict, pulls: list[dict], commits: list[dict]) -> dict:
        """§6's `now`: up-to-date, updating or stuck."""
        queued = [{"number": p["number"], "title": p["title"], "cards": p["cards"],
                   "since": p["created"]} for p in pulls if p["queued"] and not p["rejected"]]
        current = dict(self.follower.current) if self.follower.current else None
        now = {"state": "up-to-date", "commit": head["commit"], "pull": None, "step": None,
               "step_since": None, "started": None, "queued": queued, "stuck": None,
               "action": self.follower.last_action,
               "waiting": getattr(self.follower.queue, "waiting", None)}
        by_sha = {c["commit"]: c for c in commits}

        def pull_of(sha):
            number = (by_sha.get(sha) or {}).get("pull")
            if number is None:
                return None
            return {"number": number, "title": by_sha[sha]["title"], "cards": by_sha[sha]["cards"]}

        state = (head.get("verdict") or {}).get("state")
        if current:
            now.update(state="updating", commit=current["commit"], pull=pull_of(current["commit"]),
                       step=current["step"], step_since=current["step_since"],
                       started=current["started"])
        elif state == "failure":
            now.update(state="stuck", stuck={"condition": "S1", "since": head["verdict"]["date"],
                                              "summary": head["verdict"]["description"]})
        elif state == "error":
            attempts = self.follower.errors.get(head["commit"], (0, 0.0))[0]
            if attempts > self.s.error_retries:
                now.update(state="stuck", stuck={"condition": "S2", "since": head["verdict"]["date"],
                                                  "summary": head["verdict"]["description"]})
            else:
                now.update(state="updating", step="retry", pull=pull_of(head["commit"]))
        elif state == "success":
            if queued:
                now.update(state="updating", step="merge")
        else:  # pending or no verdict yet: the follower is about to start
            now.update(state="updating", step="waiting", pull=pull_of(head["commit"]))
        return now

    def snapshot(self) -> dict:
        commits = self._commits()
        head = commits[0] if commits else {"commit": self.github.head_sha(self.s.branch)}
        if "verdict" not in head:
            head["verdict"] = self._verdict(head["commit"])
        pulls = self._pulls()
        verdict = head.get("verdict") or {}
        return {
            "schema": SCHEMA,
            "updated": _iso(self.wallclock()),
            "repository": self.s.github_repo,
            "branch": self.s.branch,
            "head": {"commit": head["commit"], "state": verdict.get("state"),
                     "description": verdict.get("description"), "date": verdict.get("date")},
            "now": self._now(head, pulls, commits),
            "applications": self._applications(),
            "commits": commits,
            "pulls": pulls,
        }

    # ------------------------------------------------------------ writing

    def publish(self) -> bool:
        """Write ring0.json when it changed or the heartbeat is due; True if written."""
        doc = self.snapshot()
        body = {k: v for k, v in doc.items() if k != "updated"}
        key = json.dumps(body, sort_keys=True)
        due = self.wallclock() - self._published_at >= self.s.ring_state_heartbeat_seconds
        if key == self._published and not due:
            return False
        now = doc["now"]
        message = f"Ring 0 {now['state']}" + (f": {now['step']}" if now.get("step") else "") \
            + f" at {now['commit'][:12]}"
        self._write(json.dumps(doc, indent=1, sort_keys=False) + "\n", message)
        self._published = key
        self._published_at = self.wallclock()
        return True

    def _write(self, text: str, message: str) -> None:
        branch = self.s.ring_state_branch
        if self._file_sha is None:
            self._file_sha = self.github.file_sha(FILE, branch)
            if self._file_sha is None and not self.github.branch_exists(branch):
                self.github.create_orphan_branch(branch, {"README.md": README, FILE: text},
                                                 "Start the Ring State branch")
                log.info("created the %s branch", branch)
                self._file_sha = self.github.file_sha(FILE, branch)
                return
        try:
            self._file_sha = self.github.put_file(FILE, branch, text, message, self._file_sha)
        except urllib.error.HTTPError as error:
            # 409/422: someone else wrote the file; read its sha and try next time.
            self._file_sha = None
            raise RuntimeError(f"writing {FILE} failed: HTTP {error.code}") from error

    def run(self, sleep=time.sleep) -> None:
        """The publisher thread: never ends, never takes the follower down."""
        while True:
            try:
                if self.publish():
                    current = self.follower.current
                    log.info("published %s (%s)", FILE, current["step"] if current else "idle")
            except Exception:
                log.exception("publishing %s failed; trying again", FILE)
            sleep(self.s.ring_state_interval_seconds)

