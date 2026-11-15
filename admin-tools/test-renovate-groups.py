#!/usr/bin/env python3
"""Every application gets its own Renovate PR (docs/adr/0060, design §4.1).

renovate.json groups updates by file path, one rule per application. A new
tenant directory, or a new Application in apps/ that carries a Helm chart,
would otherwise fall outside every group and get one PR per image, which Ring 0
cannot revert as a unit. This test fails until such a directory or file has a
group, and fails if a group names a path that no longer exists.
"""

import fnmatch
import json
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
K8S = ROOT / "infrastructure" / "kubernetes"


def main() -> int:
    config = json.loads((ROOT / "renovate.json").read_text())
    patterns: list[str] = []
    problems: list[str] = []
    for rule in config.get("packageRules", []):
        if "groupName" in rule:
            patterns.extend(rule.get("matchFileNames", []))

    def covered(path: str) -> bool:
        return any(fnmatch.fnmatch(path, p) for p in patterns)

    for tenant in sorted(p for p in (K8S / "tenants").iterdir() if p.is_dir()):
        probe = f"infrastructure/kubernetes/tenants/{tenant.name}/kustomization.yaml"
        if not covered(probe):
            problems.append(f"tenant {tenant.name} has no Renovate group")

    for app in sorted((K8S / "apps").glob("*.yaml")):
        if "chart:" not in app.read_text():
            continue
        rel = app.relative_to(ROOT).as_posix()
        if not covered(rel):
            problems.append(f"{rel} carries a Helm chart and has no Renovate group")

    for pattern in patterns:
        base = pattern.split("*", 1)[0].rstrip("/")
        if not (ROOT / base).exists():
            problems.append(f"group pattern {pattern} names a path that does not exist")

    rules = config.get("packageRules", [])
    # Ring 0's merge queue (design §3.6): the label goes on minor, patch and
    # digest updates only, and the paths it leaves out must exist.
    queued = [r for r in rules if "ring0-queue" in r.get("addLabels", []) + r.get("labels", [])]
    if len(queued) != 1:
        problems.append(f"expected one rule labelling ring0-queue, found {len(queued)}")
    for rule in queued:
        kinds = set(rule.get("matchUpdateTypes", []))
        if not kinds or "major" in kinds or not kinds <= {"minor", "patch", "digest", "pin", "pinDigest"}:
            problems.append(f"ring0-queue must never carry majors: matchUpdateTypes {sorted(kinds)}")
        for pattern in rule.get("matchFileNames", []):
            base = pattern.lstrip("!").split("*", 1)[0].rstrip("/")
            if not (ROOT / base).exists():
                problems.append(f"ring0-queue pattern {pattern} names a path that does not exist")

    # Majors go to the Ring 0 Steward instead (design-per-app-updates.md §5.5):
    # one rule, majors only, over exactly the paths the queue covers.
    majors = [r for r in rules if "ring0-major" in r.get("addLabels", []) + r.get("labels", [])]
    if len(majors) != 1:
        problems.append(f"expected one rule labelling ring0-major, found {len(majors)}")
    for rule in majors:
        if set(rule.get("matchUpdateTypes", [])) != {"major"}:
            problems.append(f"ring0-major must carry majors only: {rule.get('matchUpdateTypes')}")
        if queued and sorted(rule.get("matchFileNames", [])) != sorted(queued[0].get("matchFileNames", [])):
            problems.append("ring0-major and ring0-queue must cover the same paths")

    if any(r.get("automerge") for r in rules) or config.get("automerge"):
        # Renovate must not merge on its own: Ring 0's queue merges one update
        # at a time, and only while main is green (design §3.6).
        problems.append("Renovate automerge is on; Ring 0's merge queue merges instead")

    for line in problems:
        print(f"FAIL {line}")
    if not problems:
        print(f"PASS {len(patterns)} group patterns cover every tenant and chart")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
