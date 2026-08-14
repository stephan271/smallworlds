#!/usr/bin/env python3
"""Restore Immich originals from a personal pod archive back into the library.

Why this exists: since docs/adr/0047 removed Immich from `pv-backup`, the pod
archive is the ONLY server-side copy of the pixels — but doc/storage-and-backup.md
§7 has no procedure for getting them back. This is that procedure.

The archive alone is NOT sufficient. Its keys are built for a human
(immich/<year>/<date>/<id8>-<original name>), while Immich's library is keyed by
asset UUID (/data/upload/<user>/<xx>/<yy>/<uuid>.<ext>) and the database is what
knows the mapping. So a restore needs BOTH the restored database and the pod
copy — which is exactly why doc/pod-archive.md insists the CNPG backup is "not
optional".

Mapping, mirroring pod-export/export.py:object_key():
    key = <root>/<YYYY>/<YYYY-MM-DD>/<first 8 hex of asset id>-<originalFileName>
    root = "immich-locked" if visibility = 'locked' else "immich"

Usage (paths are local; run after mounting the PVC or inside a pod):
    ./restore-immich-originals.py --inventory rows.jsonl --archive ~/pod/objects \
                                  --library /library [--apply]
Without --apply it only reports what it would do.
"""
import argparse
import hashlib
import json
import os
import shutil
import sys


def object_key(asset):
    """Must stay identical to pod-export/export.py:object_key()."""
    day = (asset.get("fileCreatedAt") or "1970-01-01")[:10]
    short = asset["id"].replace("-", "")[:8]
    name = os.path.basename(asset.get("originalFileName") or "asset")
    root = "immich-locked" if asset.get("visibility") == "locked" else "immich"
    return f"{root}/{day[:4]}/{day}/{short}-{name}"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--inventory", required=True,
                        help="JSONL of asset rows: id, ownerId, originalPath, "
                             "originalFileName, fileCreatedAt, visibility")
    parser.add_argument("--archive", required=True,
                        help="the device's objects/ directory")
    parser.add_argument("--library", required=True,
                        help="where the library PVC is mounted")
    parser.add_argument("--media-root", default="/data",
                        help="prefix of originalPath as immich-server sees it")
    parser.add_argument("--apply", action="store_true",
                        help="actually write files (default: report only)")
    args = parser.parse_args()

    stats = {"present": 0, "restored": 0, "missing_in_archive": 0, "would_restore": 0}
    for line in open(args.inventory, encoding="utf-8"):
        line = line.strip()
        if not line:
            continue
        asset = json.loads(line)

        # originalPath is absolute as immich-server sees it; re-root it onto
        # wherever the library is mounted here.
        relative = asset["originalPath"]
        if relative.startswith(args.media_root):
            relative = relative[len(args.media_root):]
        target = os.path.join(args.library, relative.lstrip("/"))

        if os.path.exists(target):
            stats["present"] += 1
            continue

        source = os.path.join(args.archive, object_key(asset))
        if not os.path.exists(source):
            print(f"MISSING FROM ARCHIVE  {asset['id']}  {object_key(asset)}")
            stats["missing_in_archive"] += 1
            continue

        if not args.apply:
            print(f"would restore  {object_key(asset)}  ->  {target}")
            stats["would_restore"] += 1
            continue

        os.makedirs(os.path.dirname(target), exist_ok=True)
        shutil.copy2(source, target)
        digest = hashlib.sha256(open(target, "rb").read()).hexdigest()
        print(f"restored  {target}  sha256={digest[:16]}")
        stats["restored"] += 1

    print(f"\n{stats}")
    return 1 if stats["missing_in_archive"] else 0


if __name__ == "__main__":
    sys.exit(main())
