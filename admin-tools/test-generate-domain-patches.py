#!/usr/bin/env python3
"""generate_domain_patches.py must be safe to run twice.

The setup wizard's "Install again" reruns prepare-community-repo.sh, which calls
the generator for every application. It used to append every patch a second
time (2026-11-10, Ring 0), and three of them `add` env entries, which then
appeared twice in the container.
"""

import pathlib
import subprocess
import sys
import tempfile

ROOT = pathlib.Path(__file__).resolve().parent.parent
GENERATOR = ROOT / "admin-tools" / "generate_domain_patches.py"
APPS = ["dashboard", "keycloak", "join", "forgejo", "immich", "nextcloud",
        "stalwart", "bulwark", "excalidraw", "jitsi", "plane", "pod-gateway",
        "euro-office", "argocd", "monitoring"]


def run(app: str, path: pathlib.Path) -> None:
    extra = ["--office-backend", "euro-office"] if app == "nextcloud" else []
    subprocess.run([sys.executable, str(GENERATOR), "--app", app,
                    "--domain", "example.org", "--ext=.canary",
                    "--kustomization-file", str(path), *extra],
                   check=True, capture_output=True, text=True)


def main() -> int:
    failures = 0
    with tempfile.TemporaryDirectory() as tmp:
        for app in APPS:
            path = pathlib.Path(tmp) / f"{app}.yaml"
            path.write_text(f"resources:\n  - ../base/{app}\n")
            run(app, path)
            first = path.read_text()
            run(app, path)
            if path.read_text() != first:
                print(f"FAIL {app}: a second run changed the kustomization")
                failures += 1
            elif "target:" not in first and app != "euro-office":
                print(f"FAIL {app}: the first run wrote no patch")
                failures += 1
    if not failures:
        print(f"PASS a second run leaves all {len(APPS)} kustomizations unchanged")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
