"""main's head commit, the ring0/verified commit status, and the pull requests the
merge queue merges or reverts, over the GitHub REST API.

Reading works without a token (public repository, 60 requests an hour), but the
merge queue polls more than that. Writing needs the fine-grained token in the
Secret canary-follower-github (commit statuses, contents, pull requests: write);
without it the follower verifies and only logs, and merges nothing.
"""
import json
import logging
import urllib.request

log = logging.getLogger("github")

FINAL_STATES = ("success", "failure", "error")


class GitHub:
    def __init__(self, api: str, repo: str, token: str, context: str, opener=None):
        self.api = api.rstrip("/")
        self.repo = repo
        self.token = token
        self.context = context
        self._open = opener or urllib.request.urlopen

    def _request(self, method: str, path: str, body: dict | None = None):
        headers = {"Accept": "application/vnd.github+json",
                   "User-Agent": "smallworlds-canary-follower",
                   "X-GitHub-Api-Version": "2022-11-28"}
        if self.token:
            headers["Authorization"] = f"Bearer {self.token}"
        data = None
        if body is not None:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        request = urllib.request.Request(f"{self.api}/repos/{self.repo}{path}",
                                         data=data, method=method, headers=headers)
        with self._open(request, timeout=30) as response:
            raw = response.read()
            return json.loads(raw) if raw else None

    def head_sha(self, branch: str) -> str:
        return self._request("GET", f"/commits/{branch}")["sha"]

    def changed_paths(self, sha: str) -> list[str]:
        """The files a commit touched; for a merge, against its first parent."""
        return [f["filename"] for f in self._request("GET", f"/commits/{sha}").get("files", [])]

    def status(self, sha: str) -> str | None:
        """The newest state of our context on that commit, or None."""
        for entry in self._request("GET", f"/commits/{sha}/statuses?per_page=100"):
            if entry.get("context") == self.context:
                return entry.get("state")
        return None

    def set_status(self, sha: str, state: str, description: str) -> None:
        description = description if len(description) <= 140 else description[:139] + "…"
        if not self.token:
            log.warning("no GitHub token; would set %s on %s: %s", state, sha[:12], description)
            return
        self._request("POST", f"/statuses/{sha}",
                      {"state": state, "context": self.context, "description": description})
        log.info("set %s on %s: %s", state, sha[:12], description)

    # ------------------------------------------------------------ merge queue

    def open_pulls(self, base: str) -> list[dict]:
        return self._request("GET", f"/pulls?state=open&base={base}&sort=created&direction=asc&per_page=100")

    def pull(self, number: int) -> dict:
        return self._request("GET", f"/pulls/{number}")

    def pull_files(self, number: int) -> list[dict]:
        return self._request("GET", f"/pulls/{number}/files?per_page=100")

    def pulls_of(self, sha: str) -> list[dict]:
        """The pull requests a commit on main came from (a squash merge's PR)."""
        return self._request("GET", f"/commits/{sha}/pulls")

    def checks_passed(self, sha: str) -> bool:
        """Every status and check run on a PR head has passed, and the PR checks
        have run at all (a head with nothing reported yet is not green)."""
        combined = self._request("GET", f"/commits/{sha}/status")
        if combined.get("total_count", 0) and combined.get("state") != "success":
            return False
        runs = self._request("GET", f"/commits/{sha}/check-runs?per_page=100").get("check_runs", [])
        if not runs:
            return False
        return all(r.get("status") == "completed"
                   and r.get("conclusion") in ("success", "neutral", "skipped") for r in runs)

    def merge(self, number: int, head_sha: str, title: str) -> str:
        """Squash-merge: one commit on main per update, which one revert undoes."""
        answer = self._request("PUT", f"/pulls/{number}/merge",
                               {"merge_method": "squash", "sha": head_sha,
                                "commit_title": f"{title} (#{number})"})
        return answer["sha"]

    def label_names(self) -> list[str]:
        return [l["name"] for l in self._request("GET", "/labels?per_page=100")]

    def add_label(self, number: int, name: str) -> None:
        self._request("POST", f"/issues/{number}/labels", {"labels": [name]})

    def comment(self, number: int, body: str) -> None:
        self._request("POST", f"/issues/{number}/comments", {"body": body})

    # ------------------------------------------------------------ revert

    def revert_commit(self, sha: str, message: str) -> str:
        """A commit on top of `sha` whose tree is its first parent's: `git revert`
        of a single-parent commit, without a checkout."""
        commit = self._request("GET", f"/git/commits/{sha}")
        parent = self._request("GET", f"/git/commits/{commit['parents'][0]['sha']}")
        made = self._request("POST", "/git/commits", {"message": message, "tree": parent["tree"]["sha"],
                                                      "parents": [sha]})
        return made["sha"]

    def open_pull(self, branch: str, sha: str, base: str, title: str, body: str) -> int:
        existing = self._request("GET", f"/pulls?state=all&head={self.repo.split('/')[0]}:{branch}")
        if existing:
            return existing[0]["number"]
        self._request("POST", "/git/refs", {"ref": f"refs/heads/{branch}", "sha": sha})
        return self._request("POST", "/pulls", {"title": title, "head": branch, "base": base,
                                                "body": body})["number"]
