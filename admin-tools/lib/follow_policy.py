#!/usr/bin/env python3
"""The Follow Policy: when an overlay is offered a new SmallWorlds release.

docs/adr/0060, design-update-strategy.md §5.1. The Operator's second decision
beside which applications run. It becomes one rule in the GitOps Overlay's
renovate.json, on the base dependency (`stephan271/smallworlds`), and every
policy only proposes: Renovate opens a pull request, the Operator merges it, and
the merge is what Argo CD deploys.

    follow_policy.py <renovate.json> <policy>

writes or replaces that rule and leaves the rest of the file alone, so running
it again with another policy changes only the policy. The rule is found by its
description, which starts with MARKER.

Release numbers carry meaning (design §4.3): a patch release has only fixes and
security fixes, a minor one features and migrations, a major one a step every
Operator must take. Majors always come as their own pull request (Renovate's
separateMajorMinor), so a routine merge never brings one by accident.
"""
import json
import sys

DEP = "stephan271/smallworlds"
MARKER = "smallworlds:follow-policy"
DEFAULT = "two-weeks"

# policy -> (what the Operator is told, the packageRules that carry it)
POLICIES = {
    "immediately": (
        "Offer each release on the day it is published.",
        [{"schedule": ["at any time"]}],
    ),
    "two-weeks": (
        "Offer a release once it is two weeks old.",
        [{"schedule": ["at any time"], "minimumReleaseAge": "14 days"}],
    ),
    "security-now": (
        "Offer fix-only releases at once, everything else after two weeks.",
        [{"schedule": ["at any time"], "minimumReleaseAge": "14 days"},
         {"matchUpdateTypes": ["patch"], "minimumReleaseAge": None}],
    ),
    "manual": (
        "Offer nothing; the Operator moves the overlay with bump-overlay-release.sh.",
        [{"enabled": False}],
    ),
}


def rules(policy: str) -> list[dict]:
    if policy not in POLICIES:
        raise ValueError(f"unknown Follow Policy {policy!r}: one of {', '.join(POLICIES)}")
    summary, bodies = POLICIES[policy]
    out = []
    for i, body in enumerate(bodies):
        description = f"{MARKER} {policy}: {summary}" if i == 0 else f"{MARKER} {policy} (fixes)"
        out.append({"description": description, "matchDepNames": [DEP], **body})
    return out


def current(config: dict) -> str | None:
    """The policy a renovate.json carries, or None."""
    for rule in config.get("packageRules", []):
        description = rule.get("description", "")
        if description.startswith(MARKER + " "):
            return description[len(MARKER) + 1:].split(":")[0].split(" ")[0]
    return None


def apply(config: dict, policy: str) -> dict:
    kept = [r for r in config.get("packageRules", [])
            if not r.get("description", "").startswith(MARKER)]
    # After the base rule, so the policy's settings win where both speak.
    config["packageRules"] = kept + rules(policy)
    return config


def main(argv: list[str]) -> int:
    if len(argv) != 3:
        print(__doc__.split("\n\n")[2].strip(), file=sys.stderr)
        return 2
    path, policy = argv[1], argv[2]
    try:
        with open(path) as f:
            config = json.load(f)
        config = apply(config, policy)
    except (OSError, ValueError) as error:
        print(f"follow_policy: {error}", file=sys.stderr)
        return 1
    with open(path, "w") as f:
        json.dump(config, f, indent=2, ensure_ascii=False)
        f.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
