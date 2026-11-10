#!/usr/bin/env python3
"""Pick the commit a release may be cut from (docs/adr/0060, design §4.4).

A commit qualifies when its newest `ring0/verified` status is `success` (the
Canary Follower posts it) and that verdict is at least --soak-hours old: one
full backup cycle, and every nightly job run at least twice, on Ring 0. With
--security the soak is waived and one green verification is enough.

    ring0_gate.py --repo OWNER/REPO [--soak-hours 72] [--security] SHA [SHA ...]

SHAs are given newest first (`git rev-list --first-parent main`); the first that
qualifies wins. Prints `<sha>\t<verified-at>\t<hours>` and exits 0, or explains
on stderr why none qualifies and exits 1.

Reads the public GitHub API without a token (GITHUB_TOKEN is used if set).
SMALLWORLDS_GITHUB_API and SMALLWORLDS_GATE_NOW exist for the tests.
"""
import argparse
import json
import os
import sys
import urllib.error
import urllib.request
from datetime import datetime, timezone

CONTEXT = "ring0/verified"


def _parse(stamp: str) -> datetime:
    return datetime.fromisoformat(stamp.replace("Z", "+00:00"))


def verdict(api: str, repo: str, sha: str) -> dict | None:
    """The newest ring0/verified status on that commit, or None."""
    url = f"{api.rstrip('/')}/repos/{repo}/commits/{sha}/statuses"
    headers = {"Accept": "application/vnd.github+json", "User-Agent": "smallworlds-release"}
    if os.environ.get("GITHUB_TOKEN"):
        headers["Authorization"] = f"Bearer {os.environ['GITHUB_TOKEN']}"
    try:
        with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=20) as r:
            statuses = json.load(r)
    except urllib.error.HTTPError as error:
        if error.code in (404, 422):  # a commit GitHub does not have
            return None
        raise
    except urllib.error.URLError as error:
        if isinstance(error.reason, FileNotFoundError):  # the tests' file:// API
            return None
        raise
    for status in statuses:  # GitHub lists them newest first
        if status.get("context") == CONTEXT:
            return status
    return None


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--repo", required=True)
    parser.add_argument("--soak-hours", type=float, default=72)
    parser.add_argument("--security", action="store_true")
    parser.add_argument("shas", nargs="+")
    args = parser.parse_args(argv)

    api = os.environ.get("SMALLWORLDS_GITHUB_API", "https://api.github.com")
    now = (_parse(os.environ["SMALLWORLDS_GATE_NOW"]) if os.environ.get("SMALLWORLDS_GATE_NOW")
           else datetime.now(timezone.utc))
    needed = 0 if args.security else args.soak_hours

    reasons = []
    for sha in args.shas:
        status = verdict(api, args.repo, sha)
        if status is None:
            reasons.append(f"{sha[:12]}  not verified on Ring 0")
            continue
        state = status.get("state")
        if state != "success":
            reasons.append(f"{sha[:12]}  Ring 0 says {state}: {status.get('description', '')}")
            continue
        at = _parse(status["created_at"])
        hours = (now - at).total_seconds() / 3600
        if hours < needed:
            reasons.append(f"{sha[:12]}  green, but only {hours:.0f} h on Ring 0 (needs {needed:.0f})")
            continue
        print(f"{sha}\t{status['created_at']}\t{hours:.0f}")
        return 0

    print("No commit of main may be released yet:", file=sys.stderr)
    for line in reasons[:8]:
        print("  " + line, file=sys.stderr)
    if len(reasons) > 8:
        print(f"  … and {len(reasons) - 8} older commits", file=sys.stderr)
    return 1


if __name__ == "__main__":
    sys.exit(main())
