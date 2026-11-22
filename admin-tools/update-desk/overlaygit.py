"""The Operator's checkout of their GitOps Overlay.

The Desk changes only the files that name the base commit, commits only those
paths, and pushes only `main` to `origin`. It never rebases, resets or forces.
"""
from __future__ import annotations

import re
import subprocess

import pins

TRAILER_PIN = "Base-Pin"
TRAILER_FROM = "Base-Pin-From"


class OverlayError(RuntimeError):
    pass


class OverlayRepo:
    def __init__(self, path: str):
        self.path = path

    def git(self, *args: str, check: bool = True, timeout: int = 60) -> str:
        result = subprocess.run(["git", "-C", self.path, *args], capture_output=True, text=True,
                                timeout=timeout)
        if check and result.returncode != 0:
            raise OverlayError(f"git {' '.join(args[:2])}: {(result.stderr or result.stdout).strip()[:400]}")
        return result.stdout

    def fetch(self) -> str | None:
        try:
            self.git("fetch", "--quiet", "origin", "main", timeout=60)
            return None
        except (OverlayError, subprocess.TimeoutExpired) as error:
            return str(error)

    def sha(self, ref: str) -> str | None:
        out = self.git("rev-parse", "--verify", "--quiet", ref + "^{commit}", check=False).strip()
        return out or None

    def status(self) -> dict:
        branch = self.git("rev-parse", "--abbrev-ref", "HEAD").strip()
        dirty = [line[3:] for line in self.git("status", "--porcelain").splitlines() if line.strip()]
        head, origin = self.sha("HEAD"), self.sha("origin/main")
        ahead = behind = 0
        if head and origin:
            counts = self.git("rev-list", "--left-right", "--count", f"{head}...{origin}").split()
            ahead, behind = int(counts[0]), int(counts[1])
        remote = self.git("remote", "get-url", "origin", check=False).strip()
        return {"branch": branch, "dirty": dirty, "head": head, "origin": origin,
                "ahead": ahead, "behind": behind, "remote": _without_credentials(remote)}

    def texts_at(self, revision: str) -> dict[str, str]:
        names = [n for n in self.git("ls-tree", "-r", "--name-only", revision).splitlines()
                 if n.rsplit("/", 1)[-1] in ("kustomization.yaml", "overlay-config.yaml")]
        return {name: self.git("show", f"{revision}:{name}") for name in names}

    def pin_at(self, revision: str) -> pins.Overlay | None:
        try:
            return pins.read(self.path, texts=self.texts_at(revision))
        except (OverlayError, pins.PinError):
            return None

    def diff(self, paths: list[str]) -> str:
        return self.git("diff", "--", *paths)

    def between(self, old: str, new: str) -> dict | None:
        """What moving from overlay commit `old` to `new` brings: the commits,
        the files, and which of those files can change what Argo CD renders.
        None when `old` is not in this checkout."""
        if not self.sha(old) or not self.sha(new):
            return None
        files = [f for f in self.git("diff", "--name-only", old, new).splitlines() if f]
        commits = [line for line in self.git("log", "--format=%h %s", f"{old}..{new}").splitlines() if line]
        return {"files": files, "applied": [f for f in files if renders(f)], "commits": commits}


    def discard(self, paths: list[str]) -> None:
        self.git("checkout", "--", *paths)

    def commit(self, paths: list[str], message: str) -> str:
        self.git("add", "--", *paths)
        self.git("commit", "--quiet", "-m", message, "--", *paths)
        return self.sha("HEAD")

    def push(self) -> str:
        self.git("push", "--quiet", "origin", "HEAD:main", timeout=120)
        self.fetch()
        return self.sha("origin/main")

    def trailers(self, revision: str = "HEAD") -> dict[str, str]:
        message = self.git("log", "-1", "--format=%B", revision, check=False)
        return dict(re.findall(r"^([A-Za-z-]+):\s*(\S+)\s*$", message, re.M))

    def message(self, revision: str) -> str:
        return self.git("log", "-1", "--format=%s", revision, check=False).strip()


def _without_credentials(url: str) -> str:
    return re.sub(r"//[^/@]+@", "//", url)


# Files in an overlay that no kustomization reads.
NOT_RENDERED = ("renovate.json", ".gitignore", "LICENSE")


def renders(path: str) -> bool:
    name = path.rsplit("/", 1)[-1]
    return not (path.startswith(".github/") or name.endswith(".md") or name in NOT_RENDERED)
