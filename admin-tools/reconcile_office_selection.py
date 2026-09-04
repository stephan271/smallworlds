#!/usr/bin/env python3
"""Remove unselected Office Applications from an existing overlay root.

The per-application directories deliberately stay in place: they may contain an
operator's custom patches and are harmless while the root no longer references
their Argo CD Application. This makes switching back lossless.
"""

import argparse
import re
from pathlib import Path


BACKENDS = ("none", "collabora", "euro-office")


def remove_application(text: str, app: str) -> str:
    lines = text.splitlines(keepends=True)
    filtered: list[str] = []
    index = 0
    while index < len(lines):
        line = lines[index]
        if f"/apps/{app}.yaml" in line:
            if filtered and re.search(rf"# Include .* for {re.escape(app)}\s*$", filtered[-1]):
                filtered.pop()
            index += 1
            continue

        if line.rstrip("\r\n") == "  - target:":
            end = index + 1
            while end < len(lines):
                candidate = lines[end].rstrip("\r\n")
                indentation = len(candidate) - len(candidate.lstrip(" "))
                if candidate == "  - target:":
                    break
                if candidate.strip() and indentation < 4:
                    # A new top-level key or a comment between patch entries is
                    # not part of this Application patch and must survive.
                    break
                end += 1
            block = "".join(lines[index:end])
            is_application = re.search(r"(?m)^\s+kind: Application\s*$", block)
            names_app = re.search(rf"(?m)^\s+name: {re.escape(app)}\s*$", block)
            if is_application and names_app:
                index = end
                continue

        filtered.append(line)
        index += 1
    return "".join(filtered)


def reconcile(path: Path, selected: str) -> None:
    if selected not in BACKENDS:
        raise ValueError(f"unsupported Office backend: {selected}")
    content = path.read_text(encoding="utf-8")
    for app in BACKENDS[1:]:
        if app != selected:
            content = remove_application(content, app)
    path.write_text(content, encoding="utf-8")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("kustomization", type=Path)
    parser.add_argument("backend", choices=BACKENDS)
    args = parser.parse_args()
    reconcile(args.kustomization, args.backend)


if __name__ == "__main__":
    main()
