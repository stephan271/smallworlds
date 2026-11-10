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
    if any(r.get("automerge") for r in rules) or config.get("automerge"):
        # Allowed only from phase 6, once Ring 0 reverts what it rejects.
        problems.append("automerge is on; it needs Ring 0's revert (design §3.6) first")

    for line in problems:
        print(f"FAIL {line}")
    if not problems:
        print(f"PASS {len(patterns)} group patterns cover every tenant and chart")
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
