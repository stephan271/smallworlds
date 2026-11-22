"""Which versions a commit deploys, per tenant and per Application, read from the
files in git without rendering.

Rendering every tenant with Helm for every step would take minutes and need the
chart registries. Every version the base pins is written in its own files: an
image reference, a Helm values pair (`repository:` with `tag:`), a kustomize
`images:` override, a `helmCharts:` entry or an Application's chart and
`targetRevision`. Images a chart brings without naming them in our values are
not seen; the chart's own version is, and that is what changes them.
"""
from __future__ import annotations

import re

_IMAGE = re.compile(r"^\s*-?\s*(?:image|imageName):\s*['\"]?([^'\"\s{}]+:[^'\"\s{}]+)['\"]?\s*$")
_REPOSITORY = re.compile(r"^(\s*)repository:\s*['\"]?([^'\"\s]+)['\"]?\s*$")
_TAG = re.compile(r"^(\s*)tag:\s*['\"]?([^'\"\s]+)['\"]?\s*$")
_HELM_CHART = re.compile(
    r"-\s*name:\s*([\w.-]+)\s*\n(?:\s+(?!-)\S.*\n)*?\s+version:\s*['\"]?([\w.+-]+)")
_OVERRIDE = re.compile(r"-\s*name:\s*([^\s]+)\s*\n((?:\s+(?!-)\S.*\n)*?\s+newTag:\s*['\"]?[\w.+-]+)")
_NEW_NAME = re.compile(r"newName:\s*['\"]?([^'\"\s]+)")
_NEW_TAG = re.compile(r"newTag:\s*['\"]?([\w.+-]+)")
_APP_CHART = re.compile(r"^\s*chart:\s*['\"]?([\w.-]+)", re.M)
_APP_REVISION = re.compile(r"^\s*targetRevision:\s*['\"]?([\w.+-]+)", re.M)

# Images every tenant uses for its init jobs and helpers; shown apart, they
# change across the board and say nothing about the application itself.
HELPER_IMAGES = ("alpine/k8s", "python", "busybox", "curlimages/curl", "alpine",
                 "rclone/rclone", "bitnami/kubectl")


def split_image(reference: str) -> tuple[str, str]:
    """('registry/name', 'tag'); the digest is dropped, it follows the tag."""
    reference = reference.split("@", 1)[0]
    name, _, tag = reference.rpartition(":")
    if not name or "/" in tag:
        return reference, ""
    return name, tag


def short_name(name: str) -> str:
    for prefix in ("docker.io/library/", "docker.io/", "index.docker.io/"):
        if name.startswith(prefix):
            return name[len(prefix):]
    return name


def is_helper(name: str) -> bool:
    short = short_name(name)
    return any(short == helper or short.endswith("/" + helper) for helper in HELPER_IMAGES)


def versions_in(path: str, text: str) -> dict[str, set[str]]:
    """{'image:<name>' or 'chart:<name>': {versions}} named in one file."""
    found: dict[str, set[str]] = {}

    def add(kind: str, name: str, version: str):
        if version and "{" not in version and "$" not in version:
            found.setdefault(f"{kind}:{name}", set()).add(version)

    if not path.endswith((".yaml", ".yml")):
        return found
    lines = text.splitlines()
    for index, line in enumerate(lines):
        if m := _IMAGE.match(line):
            name, tag = split_image(m.group(1))
            if tag:
                add("image", short_name(name), tag)
        elif m := _REPOSITORY.match(line):
            indent = len(m.group(1))
            for following in lines[index + 1:index + 8]:
                stripped_indent = len(following) - len(following.lstrip())
                if following.strip() and stripped_indent < indent:
                    break
                if (t := _TAG.match(following)) and len(t.group(1)) == indent:
                    add("image", short_name(m.group(2)), t.group(2).split("@", 1)[0])
                    break
    if path.endswith("kustomization.yaml"):
        for name, version in _HELM_CHART.findall(text):
            add("chart", name, version)
        renamed = set()
        for name, body in _OVERRIDE.findall(text):
            new_name = _NEW_NAME.search(body)
            add("image", short_name(new_name.group(1) if new_name else name),
                _NEW_TAG.search(body).group(1))
            if new_name:
                renamed.add(short_name(name))
        # An image this file renames is not what runs under its old name, in
        # this file or in the tenant it builds on (settle() drops it).
        for name in renamed:
            found.setdefault(f"renamed:{name}", set()).add(name)
    if "/apps/" in path and "kind: Application" in text:
        chart = _APP_CHART.search(text)
        revision = _APP_REVISION.search(text)
        if chart and revision:
            add("chart", chart.group(1), revision.group(1))
    return found


def merge(into: dict[str, set[str]], other: dict[str, set[str]]) -> None:
    for key, values in other.items():
        into.setdefault(key, set()).update(values)


def settle(found: dict[str, set[str]]) -> dict[str, set[str]]:
    """Drop images that a kustomize `images:` entry renames to another one."""
    renamed = {key.split(":", 1)[1] for key in found if key.startswith("renamed:")}
    return {key: values for key, values in found.items()
            if not key.startswith("renamed:")
            and not (key.startswith("image:") and key.split(":", 1)[1] in renamed)}


def changes(old: dict[str, set[str]], new: dict[str, set[str]]) -> list[dict]:
    """What moved, as {'kind', 'name', 'from', 'to', 'helper'}; new and removed
    entries have an empty side."""
    result = []
    for key in sorted(set(old) | set(new)):
        before = ", ".join(sorted(old.get(key, ())))
        after = ", ".join(sorted(new.get(key, ())))
        if before == after:
            continue
        kind, name = key.split(":", 1)
        result.append({"kind": kind, "name": name, "from": before, "to": after,
                       "helper": kind == "image" and is_helper(name)})
    # Charts first, then the application's images, helpers last.
    result.sort(key=lambda c: (c["helper"], c["kind"] != "chart", c["name"]))
    return result
