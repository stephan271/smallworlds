"""main's head commit and the ring0/verified commit status, over the GitHub REST API.

Reading works without a token (public repository, 60 requests an hour, the
follower needs 12). Writing a status needs the fine-grained token in the Secret
canary-follower-github; without it the follower verifies and only logs.
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
            return json.load(response)

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
