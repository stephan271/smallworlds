"""Ring 0's verdicts on main's commits, and what Ring 0 is doing now.

Two sources:

- `ring0.json` on the branch `ring-state` (written by the Canary Follower,
  design-per-app-updates.md §6): public, no token. It carries `now` (Ring 0 up
  to date, updating or stuck) and the verdicts on main's last 25 commits.
- The commit status `ring0/verified` itself, through GitHub's GraphQL API, for
  the commits before those. GraphQL needs a token (any token: the base
  repository is public, so one without permissions is enough). It is taken from
  GH_TOKEN or GITHUB_TOKEN, or from `gh auth token`.

Final verdicts (success, failure, error) never change, so they are cached in
~/.smallworlds/update-desk/verdicts.json.
"""
from __future__ import annotations

import json
import os
import subprocess
import time
import urllib.error
import urllib.request

import pins

CONTEXT = "ring0/verified"
FINAL = {"success", "failure", "error"}
RING0_JSON = "https://raw.githubusercontent.com/{repo}/ring-state/ring0.json"
GRAPHQL = "https://api.github.com/graphql"
CACHE = os.path.expanduser("~/.smallworlds/update-desk/verdicts.json")

_QUERY = """
query($owner: String!, $name: String!, $since: GitTimestamp!, $after: String) {
  repository(owner: $owner, name: $name) {
    ref(qualifiedName: "main") {
      target {
        ... on Commit {
          history(first: 100, since: $since, after: $after) {
            pageInfo { hasNextPage endCursor }
            nodes { oid status { context(name: "%s") { state createdAt description } } }
          }
        }
      }
    }
  }
}
""" % CONTEXT


def _token() -> str | None:
    for name in ("GH_TOKEN", "GITHUB_TOKEN"):
        if os.environ.get(name):
            return os.environ[name]
    try:
        result = subprocess.run(["gh", "auth", "token"], capture_output=True, text=True, timeout=10)
    except (OSError, subprocess.TimeoutExpired):
        return None
    return result.stdout.strip() or None


class Verdicts:
    def __init__(self, repo: str = pins.BASE_REPO, cache_path: str = CACHE, fetch=None):
        self.repo = repo
        self.cache_path = cache_path
        self.known: dict[str, dict] = {}
        self.ring0: dict | None = None
        self.ring0_error: str | None = None
        self.lookup_error: str | None = None
        self._fetch = fetch or self._http
        try:
            with open(cache_path) as handle:
                self.known = json.load(handle)
        except (OSError, ValueError):
            self.known = {}

    # -- transport ---------------------------------------------------------
    def _http(self, url: str, body: dict | None = None, token: str | None = None) -> dict:
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(url, data=data, method="POST" if data else "GET")
        request.add_header("User-Agent", "smallworlds-update-desk")
        request.add_header("Cache-Control", "no-cache")
        if token:
            request.add_header("Authorization", f"bearer {token}")
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.load(response)

    def _save(self) -> None:
        final = {sha: v for sha, v in self.known.items() if v.get("state") in FINAL}
        os.makedirs(os.path.dirname(self.cache_path), exist_ok=True)
        tmp = self.cache_path + ".tmp"
        with open(tmp, "w") as handle:
            json.dump(final, handle, indent=0, sort_keys=True)
        os.replace(tmp, self.cache_path)

    # -- sources -----------------------------------------------------------
    def load_ring0(self) -> None:
        try:
            self.ring0 = self._fetch(RING0_JSON.format(repo=self.repo) + f"?t={int(time.time())}")
            self.ring0_error = None
        except (urllib.error.URLError, OSError, ValueError) as error:
            self.ring0_error = f"ring0.json could not be read: {error}"
            return
        for commit in self.ring0.get("commits") or []:
            verdict = commit.get("verdict") or {}
            if verdict.get("state"):
                self.known[commit["commit"]] = {"state": verdict["state"],
                                                "at": verdict.get("date"),
                                                "description": verdict.get("description")}

    def lookup(self, shas: list[str], since: str) -> None:
        """Fill in verdicts for commits not known yet, from GitHub."""
        missing = {sha for sha in shas if self.known.get(sha, {}).get("state") not in FINAL}
        if not missing:
            return
        token = _token()
        if not token:
            self.lookup_error = ("No GitHub token: set GH_TOKEN, or sign in with `gh auth login`. "
                                 f"{len(missing)} commit(s) have no known Ring 0 verdict.")
            return
        owner, name = self.repo.split("/", 1)
        after = None
        try:
            for _ in range(30):
                body = {"query": _QUERY, "variables": {"owner": owner, "name": name,
                                                       "since": since, "after": after}}
                data = self._fetch(GRAPHQL, body, token)
                if data.get("errors"):
                    raise ValueError(data["errors"][0].get("message"))
                history = data["data"]["repository"]["ref"]["target"]["history"]
                for node in history["nodes"]:
                    context = (node.get("status") or {}).get("context")
                    if context:
                        self.known[node["oid"]] = {"state": context["state"].lower(),
                                                   "at": context.get("createdAt"),
                                                   "description": context.get("description")}
                    missing.discard(node["oid"])
                if not missing or not history["pageInfo"]["hasNextPage"]:
                    break
                after = history["pageInfo"]["endCursor"]
            self.lookup_error = None
        except (urllib.error.URLError, OSError, ValueError, KeyError, TypeError) as error:
            self.lookup_error = f"GitHub did not answer the verdict lookup: {error}"
        self._save()

    def of(self, sha: str) -> dict:
        return self.known.get(sha) or {"state": None}

    def now(self) -> dict:
        ring0 = self.ring0 or {}
        return {"now": ring0.get("now"), "head": ring0.get("head"), "updated": ring0.get("updated"),
                "error": self.ring0_error}
