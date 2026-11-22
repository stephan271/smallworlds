"""The base commit a GitOps Overlay runs, read and written.

An overlay names the base repository once per remote reference: the root
kustomization (`…/infrastructure/kubernetes?ref=X` plus the raw URLs of optional
Applications) and each application folder (`…/tenants/<name>?ref=X`). All of them
name the same ref; the Desk moves them together.

A release tag pins every base Application's `targetRevision` inside the tagged
commit (bump-version.sh). A commit of main does not: there 19 Applications say
`targetRevision: HEAD`, and an overlay pinned to that commit would still take
those tenants from whatever main is now (design-per-app-updates.md §7.3). So for
a commit pin the root kustomization gets a generated block that sets
`targetRevision` of every base Application tracking HEAD to the same commit.
Applications the overlay routes to its own repository are left out: they follow
the overlay's own HEAD, and their folder's remote reference carries the pin.
"""
from __future__ import annotations

import os
import re
import subprocess
from dataclasses import dataclass, field

BASE_REPO = os.environ.get("SMALLWORLDS_BASE_REPO", "stephan271/smallworlds")

BLOCK_START = "  # >>> smallworlds:base-pin — written by the Update Desk, do not edit"
BLOCK_END = "  # <<< smallworlds:base-pin"
_BLOCK = re.compile(r"\n?  # >>> smallworlds:base-pin[^\n]*\n.*?  # <<< smallworlds:base-pin\n?", re.S)

SHA = re.compile(r"^[0-9a-f]{40}$")
TAG = re.compile(r"^v\d+\.\d+\.\d+$")


def _git_ref(repo: str) -> re.Pattern:
    return re.compile(r"(https://github\.com/" + re.escape(repo) + r"\.git/[^\s?]*\?ref=)([^\s&#'\"]+)")


def _raw_ref(repo: str) -> re.Pattern:
    return re.compile(r"(https://raw\.githubusercontent\.com/" + re.escape(repo) + r"/)([^/\s]+)(/)")


_RELEASE = re.compile(r"(smallworldsRelease:\s*)(\S+)")
_ENTRY = re.compile(r"^  - target:\n(?:(?!^  - ).*\n?)*", re.M)
_TARGET_NAME = re.compile(r"^      name:\s*(\S+)", re.M)
_TARGET_KIND = re.compile(r"^      kind:\s*(\S+)", re.M)
_ROUTED_REPO = re.compile(r"path: /spec/source/repoURL\n\s+value:\s*(\S+)")
_ROUTED_PATH = re.compile(r"path: /spec/source/path\n\s+value:\s*(\S+)")
_RAW_APP = re.compile(r"/infrastructure/kubernetes/apps/([\w.-]+)\.yaml")
_FOLDER_TENANT = re.compile(r"/infrastructure/kubernetes/tenants/([\w.-]+)\?ref=")


class PinError(RuntimeError):
    pass


@dataclass
class Overlay:
    path: str
    refs: dict[str, list[str]] = field(default_factory=dict)  # ref -> files naming it
    routed: dict[str, str] = field(default_factory=dict)  # Application -> overlay folder
    deleted: set[str] = field(default_factory=set)  # Applications the overlay deletes
    patched: set[str] = field(default_factory=set)  # Applications any patch targets
    raw_apps: list[str] = field(default_factory=list)  # optional Applications it adds
    folder_tenants: dict[str, list[str]] = field(default_factory=dict)  # folder -> base tenants
    has_block: bool = False
    domain: str | None = None

    @property
    def ref(self) -> str | None:
        return next(iter(self.refs)) if len(self.refs) == 1 else None


def kustomization_files(overlay: str) -> list[str]:
    found = []
    for root, dirs, files in os.walk(overlay):
        dirs[:] = [d for d in dirs if not d.startswith(".")]
        for name in files:
            if name in ("kustomization.yaml", "overlay-config.yaml"):
                found.append(os.path.join(root, name))
    return sorted(found)


def read(overlay: str, repo: str = BASE_REPO, texts: dict[str, str] | None = None) -> Overlay:
    """What the overlay says. `texts` ({relative path: content}) reads another
    revision of it instead of the working tree."""
    if texts is None:
        texts = {}
        for path in kustomization_files(overlay):
            with open(path) as handle:
                texts[os.path.relpath(path, overlay)] = handle.read()
    result = Overlay(path=overlay)
    git_ref, raw_ref = _git_ref(repo), _raw_ref(repo)
    for relative, text in sorted(texts.items()):
        refs = {m.group(2) for m in git_ref.finditer(text)} | {m.group(2) for m in raw_ref.finditer(text)}
        if relative.endswith("overlay-config.yaml"):
            refs |= {m.group(2) for m in _RELEASE.finditer(text)}
        for ref in refs:
            result.refs.setdefault(ref, []).append(relative)
        folder = os.path.dirname(relative)
        if folder and relative.endswith("kustomization.yaml"):
            result.folder_tenants[folder] = _FOLDER_TENANT.findall(text)
            if folder == "dashboard":
                m = re.search(r"value:\s*dashboard\.(\S+)", text)
                result.domain = m.group(1) if m else result.domain
    root = texts.get("kustomization.yaml")
    if root is None:
        raise PinError(f"no kustomization.yaml in {overlay}")
    result.has_block = bool(_BLOCK.search(root))
    result.raw_apps = _RAW_APP.findall(root)
    for entry in _ENTRY.finditer(_BLOCK.sub("\n", root)):
        body = entry.group(0)
        name, kind = _TARGET_NAME.search(body), _TARGET_KIND.search(body)
        if not name or not kind or kind.group(1) != "Application":
            continue
        result.patched.add(name.group(1))
        if "$patch: delete" in body:
            result.deleted.add(name.group(1))
        repo_url = _ROUTED_REPO.search(body)
        if repo_url and repo not in repo_url.group(1):
            folder = _ROUTED_PATH.search(body)
            result.routed[name.group(1)] = folder.group(1) if folder else name.group(1)
    return result


def head_tracking(applications: dict[str, str], repo: str = BASE_REPO) -> list[str]:
    """Names of the base Applications ({file stem: text}) that say
    `targetRevision: HEAD` for the base repository."""
    names = []
    for stem, text in sorted(applications.items()):
        if "kind: Application" not in text or repo not in text:
            continue
        if not re.search(r"^\s*targetRevision:\s*['\"]?HEAD['\"]?\s*$", text, re.M):
            continue
        name = re.search(r"^metadata:\n(?:[ \t]+.*\n)*?[ \t]+name:\s*['\"]?([\w.-]+)", text, re.M)
        names.append(name.group(1) if name else stem)
    return names


def _block(ref: str, names: list[str]) -> str:
    lines = [BLOCK_START,
             f"  # Base commit {ref}: Applications that track HEAD on main read it too",
             "  # (design-per-app-updates.md §7.3)."]
    for name in names:
        lines += ["  - target:",
                  "      group: argoproj.io",
                  "      kind: Application",
                  f"      name: {name}",
                  "    patch: |-",
                  "      - op: replace",
                  "        path: /spec/source/targetRevision",
                  f"        value: {ref}"]
    lines.append(BLOCK_END)
    return "\n".join(lines) + "\n"


def _place_block(root: str, block: str) -> str:
    root = _BLOCK.sub("\n", root).rstrip("\n") + "\n"
    lines = root.split("\n")
    try:
        start = next(i for i, line in enumerate(lines) if re.match(r"^patches:\s*$", line))
    except StopIteration:
        return root + "\npatches:\n" + block
    end = len(lines)
    for index in range(start + 1, len(lines)):
        line = lines[index]
        if line and not line.startswith((" ", "#")):
            end = index
            break
    while end > start + 1 and not lines[end - 1].strip():
        end -= 1
    return "\n".join(lines[:end]) + "\n" + block + ("\n".join(lines[end:]) if end < len(lines) else "")


def rewrite(texts: dict[str, str], new_ref: str, tracking: list[str], overlay: Overlay,
            repo: str = BASE_REPO) -> dict[str, str]:
    """New contents of every file that changes ({relative path: text})."""
    if not (SHA.match(new_ref) or TAG.match(new_ref)):
        raise PinError(f"not a full commit hash or release tag: {new_ref}")
    git_ref, raw_ref = _git_ref(repo), _raw_ref(repo)
    changed = {}
    for relative, text in texts.items():
        new = git_ref.sub(lambda m: m.group(1) + new_ref, text)
        new = raw_ref.sub(lambda m: m.group(1) + new_ref + m.group(3), new)
        if relative.endswith("overlay-config.yaml"):
            new = _RELEASE.sub(lambda m: m.group(1) + new_ref, new)
        if relative == "kustomization.yaml":
            names = [n for n in tracking if n not in overlay.routed and n not in overlay.deleted]
            if SHA.match(new_ref) and names:
                new = _place_block(new, _block(new_ref, names))
            elif _BLOCK.search(new):
                new = _BLOCK.sub("\n", new).rstrip("\n") + "\n"
        if new != text:
            changed[relative] = new
    return changed


def write(overlay: str, changed: dict[str, str]) -> None:
    for relative, text in changed.items():
        with open(os.path.join(overlay, relative), "w") as handle:
            handle.write(text)


# --- Checking the rendered result ----------------------------------------------

def render(overlay: str, timeout: int = 180) -> str:
    result = subprocess.run(["kubectl", "kustomize", overlay], capture_output=True, text=True,
                            timeout=timeout)
    if result.returncode != 0:
        raise PinError("the overlay does not render: " + result.stderr.strip()[-600:])
    return result.stdout


def applications_in(rendered: str) -> list[dict]:
    found = []
    for document in re.split(r"^---\s*$", rendered, flags=re.M):
        if not re.search(r"^kind:\s*Application\s*$", document, re.M):
            continue
        name = re.search(r"^metadata:\n(?:[ \t]+.*\n)*?[ \t]+name:\s*(\S+)", document, re.M)
        repo_url = re.search(r"^\s+repoURL:\s*['\"]?([^'\"\s]+)", document, re.M)
        revision = re.search(r"^\s+targetRevision:\s*['\"]?([^'\"\s]+)", document, re.M)
        found.append({"name": name.group(1) if name else "?",
                      "repoURL": repo_url.group(1) if repo_url else "",
                      "targetRevision": revision.group(1) if revision else ""})
    return found


def never_head(rendered: str, ref: str, repo: str = BASE_REPO) -> list[str]:
    """Base Applications in a rendered overlay that do not read `ref` (§7.3)."""
    return [f"{a['name']} reads {a['targetRevision'] or 'nothing'}"
            for a in applications_in(rendered)
            if repo + ".git" in a["repoURL"] or a["repoURL"].rstrip("/").endswith(repo)
            if a["targetRevision"] != ref]
