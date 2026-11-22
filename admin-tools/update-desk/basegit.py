"""Read-only access to the base repository's history.

The Desk runs from a checkout of the base repository and only ever reads its
objects: it never checks anything out, so the Operator's own branch and working
tree stay as they are. Everything is read from `origin/main` after a fetch.
"""
from __future__ import annotations

import subprocess
from dataclasses import dataclass

MAIN = "origin/main"


class GitError(RuntimeError):
    pass


@dataclass
class Commit:
    sha: str
    parent: str
    subject: str
    date: str  # committer date, ISO 8601
    author: str


class BaseRepo:
    def __init__(self, path: str, main: str = MAIN):
        self.path = path
        self.main = main
        self._blobs: dict[str, str] = {}

    def git(self, *args: str, check: bool = True, input_text: str | None = None) -> str:
        result = subprocess.run(["git", "-C", self.path, *args], capture_output=True,
                                text=True, input=input_text)
        if check and result.returncode != 0:
            raise GitError(f"git {' '.join(args[:3])}: {result.stderr.strip()[:300]}")
        return result.stdout

    def fetch(self) -> None:
        self.git("fetch", "--quiet", "--tags", "origin", "main", "ring-state", check=False)

    def resolve(self, ref: str) -> str | None:
        result = subprocess.run(["git", "-C", self.path, "rev-parse", "--verify", "--quiet",
                                 ref + "^{commit}"], capture_output=True, text=True)
        return result.stdout.strip() or None

    def head(self) -> str:
        sha = self.resolve(self.main)
        if not sha:
            raise GitError(f"{self.main} is not known here; run git fetch origin main")
        return sha

    def is_ancestor(self, older: str, newer: str) -> bool:
        return subprocess.run(["git", "-C", self.path, "merge-base", "--is-ancestor", older, newer],
                              capture_output=True).returncode == 0

    def on_main(self, sha: str) -> bool:
        """True when the commit is on main's first-parent line."""
        return sha in set(self.git("rev-list", "--first-parent", self.main).split())

    def first_parent_after(self, start: str) -> list[Commit]:
        """main's first-parent commits after `start`, oldest first."""
        out = self.git("log", "--first-parent", "--reverse", "--format=%H%x1f%P%x1f%s%x1f%cI%x1f%an",
                       f"{start}..{self.main}")
        commits = []
        for line in out.splitlines():
            sha, parents, subject, date, author = line.split("\x1f")
            commits.append(Commit(sha, parents.split()[0] if parents else "", subject, date, author))
        return commits

    def commit(self, sha: str) -> Commit:
        line = self.git("log", "-1", "--format=%H%x1f%P%x1f%s%x1f%cI%x1f%an", sha).strip()
        sha, parents, subject, date, author = line.split("\x1f")
        return Commit(sha, parents.split()[0] if parents else "", subject, date, author)

    def paths_per_commit(self, start: str) -> dict[str, list[str]]:
        """{commit: files it changed against its first parent} for main's
        first-parent commits after `start`."""
        out = self.git("log", "--first-parent", "-m", "--name-only", "--format=%x1e%H",
                       f"{start}..{self.main}")
        result = {}
        for chunk in out.split("\x1e"):
            lines = [line for line in chunk.splitlines() if line.strip()]
            if lines:
                result[lines[0]] = lines[1:]
        return result

    def changed_paths(self, old: str, new: str) -> list[str]:
        return [p for p in self.git("diff", "--name-only", old, new).splitlines() if p]

    def show(self, sha: str, path: str) -> str | None:
        result = subprocess.run(["git", "-C", self.path, "show", f"{sha}:{path}"],
                                capture_output=True, text=True)
        return result.stdout if result.returncode == 0 else None

    def files(self, sha: str, prefixes: list[str]) -> list[tuple[str, str]]:
        """(blob id, path) of every file under the prefixes at that commit."""
        if not prefixes:
            return []
        out = self.git("ls-tree", "-r", sha, "--", *prefixes)
        found = []
        for line in out.splitlines():
            meta, path = line.split("\t", 1)
            _, kind, blob = meta.split()
            if kind == "blob":
                found.append((blob, path))
        return found

    def blobs(self, ids: list[str]) -> dict[str, str]:
        """Contents of blobs, read once each through one `git cat-file --batch`."""
        missing = [blob for blob in dict.fromkeys(ids) if blob not in self._blobs]
        if missing:
            proc = subprocess.run(["git", "-C", self.path, "cat-file", "--batch"],
                                  input="\n".join(missing).encode() + b"\n", capture_output=True)
            data = proc.stdout
            position = 0
            for blob in missing:
                header_end = data.index(b"\n", position)
                header = data[position:header_end].split()
                if len(header) < 3 or header[1] == b"missing":
                    position = header_end + 1
                    self._blobs[blob] = ""
                    continue
                size = int(header[2])
                start = header_end + 1
                self._blobs[blob] = data[start:start + size].decode("utf-8", "replace")
                position = start + size + 1
        return {blob: self._blobs[blob] for blob in ids}

    def release_points(self) -> dict[str, str]:
        """{main commit: tag} for every release tag: the main commit it was cut
        from. A release commit sits beside main (bump-version.sh), so that is its
        first parent; an older tag on main itself is its own commit."""
        out = self.git("for-each-ref", "refs/tags/v*", "--format=%(refname:short)")
        points = {}
        for tag in out.split():
            sha = self.resolve(tag)
            if not sha:
                continue
            if self.is_ancestor(sha, self.main):
                points.setdefault(sha, tag)
                continue
            parent = self.resolve(sha + "^")
            if parent and self.is_ancestor(parent, self.main):
                points.setdefault(parent, tag)
        return points
