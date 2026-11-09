#!/usr/bin/env python3
"""Make every Application an overlay routes to itself follow the overlay's HEAD.

A release tag pins every base Application to that tag (`targetRevision: vX`,
written by admin-tools/bump-version.sh into the tagged commit only). An overlay
that routes an Application to its own repository replaces `repoURL` and `path`;
it has to replace `targetRevision` too, or Argo CD looks for the base's tag in
the overlay repository, which has no such tag.

    route_revision.py <overlay>/kustomization.yaml

Idempotent: a patch that already resets the revision is left alone. Only
patches that replace `/spec/source/repoURL` as their first operation are
touched — that is the shape prepare-community-repo.sh and
bump-overlay-release.sh have always written.
"""
import re
import sys

ROUTED = re.compile(
    r"(    patch: \|-\n)(      - op: replace\n        path: /spec/source/repoURL\n)")
RESET = ("      - op: replace\n"
         "        path: /spec/source/targetRevision\n"
         "        value: HEAD\n")


def main(path: str) -> int:
    with open(path) as handle:
        text = handle.read()
    updated, count = ROUTED.subn(lambda m: m.group(1) + RESET + m.group(2), text)
    if count:
        with open(path, "w") as handle:
            handle.write(updated)
        print(f"  routed Applications now follow the overlay's HEAD ({count} patched)")
    return 0


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    sys.exit(main(sys.argv[1]))
