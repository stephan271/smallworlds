#!/usr/bin/env python3
"""Draft the release notes an Operator decides by (docs/adr/0060, design §4.5).

    admin-tools/release-notes.py <previous-tag> <release-tag-or-ref> > notes.md

Fills in what the repository can say for certain: the kind and flags (from
the tag message bump-version.sh writes), every image and chart that changed
per application (by rendering both refs), the commits that touched each one,
bootstrap pin changes, the Ring 0 evidence, and which variants Ring 0 soaked.
What only a human can say is left as `TODO:` lines: what changes for members,
the migration, the rollback, opt-in features, resources. Finish those, then

    gh release create <tag> --notes-file notes.md

Renovate copies these notes into the pull request it opens in each GitOps
Overlay, so they are what an Operator reads before merging.

Needs git and `kubectl kustomize --enable-helm` (the charts are vendored).
"""
import argparse
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
K8S = "infrastructure/kubernetes"

# Sections an Operator may skip when they do not run the app. Everything else
# under tenants/ and every chart in apps/ is Platform.
APPLICATIONS = {
    "nextcloud": "Files (Nextcloud)",
    "gallery": "Photos (Gallery)",
    "immich": "Photos (Immich)",
    "forgejo": "Git (Forgejo)",
    "plane": "Plan (Plane)",
    "jitsi": "Meet (Jitsi)",
    "stalwart": "Mail (Stalwart)",
    "bulwark": "Webmail (Bulwark)",
    "excalidraw": "Whiteboard (Excalidraw)",
    "collabora": "Office (Collabora)",
    "euro-office": "Office (Euro-Office)",
    "pod-gateway": "Personal data pods",
}
# Ring 0 runs one side of each exclusive choice (design §3.2).
SOAKED = {"gallery", "euro-office"}
FRESH_INSTALL_ONLY = {"immich": "gallery", "collabora": "euro-office"}
# Tenants that ship with every install but are not member applications.
NOT_RELEASED = {"canary-follower", "operator-console"}

TODO_APPLICATION = """\
- TODO: two or three lines on what changes for members.
- **Database migration:** TODO none / forward-only.
- **Rollback:** TODO revert the overlay commit / restore the pre-update backup.
- TODO: opt-in features and how to enable them, or remove this line.
- **Resources:** TODO expected change in memory, or "no change"."""


def git(*args: str) -> str:
    return subprocess.run(["git", "-C", str(ROOT), *args], check=True,
                          capture_output=True, text=True).stdout


# --- What a ref deploys ------------------------------------------------------

def images_in(rendered: str) -> set[str]:
    """Every container image a rendered manifest names. A tiny YAML reader is
    enough: image references are single-line scalars."""
    found = set()
    for line in rendered.splitlines():
        m = re.match(r"\s*-?\s*(image|imageName):\s*['\"]?([^'\"\s]+)['\"]?\s*$", line)
        if m:
            found.add(m.group(2))
    return found


def charts_in_apps(tree: Path) -> dict[str, str]:
    """`chart@version` for every Helm chart an Application in apps/ installs."""
    charts = {}
    for path in sorted((tree / K8S / "apps").glob("*.yaml")):
        text = path.read_text()
        chart = re.search(r"^\s*chart:\s*['\"]?([\w.-]+)", text, re.M)
        version = re.search(r"^\s*targetRevision:\s*['\"]?([\w.+-]+)", text, re.M)
        if chart and version:
            charts[path.stem] = f"{chart.group(1)}@{version.group(1)}"
    return charts


def charts_in_tenant(tenant_dir: Path) -> set[str]:
    k = tenant_dir / "kustomization.yaml"
    if not k.exists():
        return set()
    text = k.read_text()
    return {f"{n}@{v}" for n, v in re.findall(
        r"-\s*name:\s*([\w.-]+)\s*\n(?:\s+(?!-)\S.*\n)*?\s+version:\s*['\"]?([\w.+-]+)", text)}


def snapshot(ref: str, render=None) -> dict:
    """{'tenants': {name: {'images': set, 'charts': set}}, 'apps': {...}, 'pins': {...}}"""
    with tempfile.TemporaryDirectory() as tmp:
        tree = Path(tmp) / "tree"
        tree.mkdir()
        archive = subprocess.run(["git", "-C", str(ROOT), "archive", ref],
                                 check=True, capture_output=True).stdout
        subprocess.run(["tar", "-x", "-C", str(tree)], input=archive, check=True)
        # Charts are not in git (.gitignore: charts/); kustomize pulls them into
        # each tenant's charts/ on first render. Reuse the working copy's pulls:
        # a version that is already there is used as is, any other is pulled.
        for cache in (ROOT / K8S / "tenants").glob("*/charts"):
            target = tree / K8S / "tenants" / cache.parent.name
            if target.is_dir():
                subprocess.run(["cp", "-r", str(cache), str(target)], check=True)
        tenants = {}
        for tenant in sorted((tree / K8S / "tenants").iterdir()):
            if not tenant.is_dir():
                continue
            rendered = (render or _render)(tenant)
            tenants[tenant.name] = {"images": images_in(rendered),
                                    "charts": charts_in_tenant(tenant)}
        pins_file = tree / "infrastructure" / "bootstrap-pins.env"
        pins = {}
        if pins_file.exists():
            for line in pins_file.read_text().splitlines():
                if re.match(r"^[A-Z0-9_]+_VERSION=", line):
                    key, value = line.split("=", 1)
                    pins[key] = value
        return {"tenants": tenants, "apps": charts_in_apps(tree), "pins": pins}


def _render(tenant: Path) -> str:
    result = subprocess.run(["kubectl", "kustomize", "--enable-helm", str(tenant)],
                            capture_output=True, text=True)
    if result.returncode != 0:
        print(f"warning: {tenant.name} does not render: {result.stderr.strip()[:200]}",
              file=sys.stderr)
        return ""
    return result.stdout


# --- Differences --------------------------------------------------------------

def _split(image: str) -> tuple[str, str]:
    """('registry/name', 'tag') — a digest is dropped, it changes with the tag."""
    image = image.split("@", 1)[0]
    name, _, tag = image.rpartition(":")
    if not name or "/" in tag:
        return image, ""
    return name, tag


def _split_chart(chart: str) -> tuple[str, str]:
    name, _, version = chart.rpartition("@")
    return (name, version) if name else (chart, "")


def changes(old: set[str], new: set[str], split=_split) -> list[str]:
    """'name: old → new' lines for images (or, with split=_split_chart, charts)
    whose version moved, plus added and removed ones. Same name and tag with
    another digest is not listed."""
    def index(items):
        out = {}
        for item in items:
            name, version = split(item)
            out.setdefault(name, set()).add(version)
        return out
    before, after = index(old), index(new)
    lines = []
    for name in sorted(before.keys() | after.keys()):
        a, b = before.get(name, set()), after.get(name, set())
        if a == b:
            continue
        if not a:
            lines.append(f"`{name}` new: {', '.join(sorted(b))}")
        elif not b:
            lines.append(f"`{name}` removed (was {', '.join(sorted(a))})")
        else:
            lines.append(f"`{name}` {', '.join(sorted(a))} → {', '.join(sorted(b))}")
    return lines


def commits_touching(old: str, new: str, path: str) -> list[str]:
    out = git("log", "--first-parent", "--format=%h %s", f"{old}..{new}", "--", path)
    return [line for line in out.splitlines()
            if not line.split(" ", 1)[1].startswith("Release v")]


def tag_message(ref: str) -> str:
    try:
        return git("tag", "-l", "--format=%(contents)", ref).strip()
    except subprocess.CalledProcessError:
        return ""


# --- The notes ------------------------------------------------------------------

def compose(old_ref: str, new_ref: str, old: dict, new: dict,
            message: str, commits=commits_touching) -> str:
    kind = re.search(r"^Release \S+ \(([^)]*)\)", message, re.M)
    kind_text = kind.group(1) if kind else "TODO patch / minor / major"
    flags = []
    if "security" in kind_text:
        flags.append("**Security**")
    lines = [f"# {new_ref}", "",
             f"**Kind:** {kind_text.replace(', security', '')}"
             + (f" · {' · '.join(flags)}" if flags else "")
             + " · **Manual steps required:** TODO no / yes (say which)", ""]

    app_sections, platform = [], []
    for name in sorted(old["tenants"].keys() | new["tenants"].keys()):
        if name in NOT_RELEASED:
            continue
        a = old["tenants"].get(name, {"images": set(), "charts": set()})
        b = new["tenants"].get(name, {"images": set(), "charts": set()})
        moved = changes(a["images"], b["images"]) + changes(a["charts"], b["charts"], _split_chart)
        touched = commits(old_ref, new_ref, f"{K8S}/tenants/{name}")
        if not moved and not touched:
            continue
        if name in APPLICATIONS:
            body = [f"### {APPLICATIONS[name]}", ""]
            body += [f"- {m}" for m in moved] or ["- No version change; manifests changed (see below)."]
            body += ["- Upstream notes: TODO link", TODO_APPLICATION]
            if touched:
                body += ["", "<details><summary>Commits</summary>", ""]
                body += [f"- {c}" for c in touched] + ["", "</details>"]
            app_sections.append("\n".join(body))
        else:
            platform.append(f"- **{name}**: " + ("; ".join(moved) if moved else
                            "manifests changed: " + "; ".join(c.split(" ", 1)[1] for c in touched[:3])))

    for app in sorted(old["apps"].keys() | new["apps"].keys()):
        a, b = old["apps"].get(app), new["apps"].get(app)
        if a != b:
            platform.append(f"- **{app}** chart: {a or 'new'} → {b or 'removed'}")
    for key in sorted(old["pins"].keys() | new["pins"].keys()):
        a, b = old["pins"].get(key), new["pins"].get(key)
        if a != b and a is None:
            platform.append(f"- Bootstrap pin `{key}`: {b}, now in the release itself "
                            f"({old_ref} lacked bootstrap-pins.env)")
        elif a != b:
            platform.append(f"- Bootstrap pin `{key}`: {a} → {b} "
                            "(reaches new and rebuilt nodes only)")
    for c in commits(old_ref, new_ref, f"{K8S}/apps") + commits(old_ref, new_ref, f"{K8S}/bases"):
        line = f"- Platform manifests: {c.split(' ', 1)[1]} ({c.split(' ', 1)[0]})"
        if line not in platform:
            platform.append(line)
    overlay = commits(old_ref, new_ref, "admin-tools/bump-overlay-release.sh")
    for c in overlay:
        platform.append(f"- Overlay migration in `bump-overlay-release.sh`: {c.split(' ', 1)[1]}")

    lines += ["## Applications", "",
              "Skip the sections of applications you do not run.", ""]
    lines += ["\n\n".join(app_sections) if app_sections else "No application changed.", ""]
    lines += ["## Platform", ""]
    lines += platform or ["No platform change."]
    lines += [""]

    gate = re.search(r"^(Ring 0: .*|Released WITHOUT the Ring 0 gate: .*)$", message, re.M)
    changed = {s.split("### ", 1)[1].split("\n", 1)[0] for s in app_sections}
    soaked = [APPLICATIONS[n] for n in sorted(SOAKED) if APPLICATIONS[n] in changed]
    fresh = [APPLICATIONS[n] for n in sorted(FRESH_INSTALL_ONLY) if APPLICATIONS[n] in changed]
    lines += ["## Evidence", "",
              f"- {gate.group(1) if gate else 'TODO: Ring 0 verdict (not in the tag message)'}",
              "- Ring 0 runs every application with example data, and follows every commit.",
              "- Soaked on Ring 0: " + (", ".join(soaked) if soaked else "every changed application."),
              ]
    if fresh:
        lines.append("- Fresh install tested only, not soaked (Ring 0 runs the other variant): "
                     + ", ".join(fresh) + ". TODO: confirm the test-pr-locally.sh run.")
    lines += ["- Ring 1 (smallworlds.network): TODO append after adoption — "
              "\"running since <date>, no incidents\" or the incident.", "",
              "## Version path", "",
              "- TODO: releases that must not be skipped, if any, and the expected downtime.", ""]
    return "\n".join(lines)


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("previous")
    parser.add_argument("release")
    args = parser.parse_args(argv)
    for ref in (args.previous, args.release):
        git("rev-parse", "--verify", f"{ref}^{{commit}}")
    old, new = snapshot(args.previous), snapshot(args.release)
    print(compose(args.previous, args.release, old, new, tag_message(args.release)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
