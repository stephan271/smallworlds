#!/usr/bin/env python3
"""Restore Immich originals from the pod archive back into the library PVC.

Since docs/adr/0047 removed Immich from `pv-backup`, the pod archive is the only
server-side copy of the pixels. This is the procedure for getting them back —
see doc/storage-and-backup.md §7.4.

Two sources, in order of preference:

  --from-pod-bucket   (default) the pod-gateway bucket on garage-backup. This
                      is the normal path: separate volume, no member involved.
  --from-device DIR   a member's home device copy, for the disaster case where
                      both volumes are gone.

**The archive alone is not enough.** Its keys are built for a human
(immich/<year>/<date>/<id8>-<originalFileName>) while the library is keyed by
asset UUID (/data/upload/<user>/<xx>/<yy>/<uuid>.jpg), and only the Immich
database holds the mapping. So the database must be restored FIRST and its
inventory dumped; this tool consumes that inventory. That dependency is not an
accident of this script — it is why doc/pod-archive.md insists the CNPG backup
is never optional.

Usage (run where the library PVC is mounted, e.g. a helper pod in the immich
namespace):

    ./restore-immich-originals.py --inventory rows.jsonl --user <immich-user-id> \\
        --library /library [--apply]

S3 settings come from the environment, matching what the gateway itself uses:
POD_S3_ENDPOINT, POD_S3_REGION, POD_S3_BUCKET, POD_S3_ACCESS_KEY,
POD_S3_SECRET_KEY.
"""
import argparse
import hashlib
import json
import os
import shutil
import sys

# The gateway's SigV4 client, rather than a second implementation of request
# signing that could drift from it (it must sign region=garage or Garage 400s).
sys.path.insert(0, os.path.join(
    os.path.dirname(os.path.abspath(__file__)),
    "..", "infrastructure", "kubernetes", "tenants", "pod-gateway", "src"))
from s3 import S3Client, S3Error  # noqa: E402


def object_key(asset):
    """Rebuild the key pod-export chose for this asset.

    MUST stay behaviourally identical to object_key() in
    infrastructure/kubernetes/tenants/immich/pod-export/export.py — the two are
    the write and read halves of the same naming scheme, and a restore that
    computes a different key silently finds nothing. Change them together.
    """
    day = (asset.get("fileCreatedAt") or "1970-01-01")[:10]
    short = asset["id"].replace("-", "")[:8]
    name = os.path.basename(asset.get("originalFileName") or "asset")
    root = "immich-locked" if asset.get("visibility") == "locked" else "immich"
    return f"{root}/{day[:4]}/{day}/{short}-{name}"


class PodBucketSource:
    """Reads objects and the manifest straight from the pod-gateway bucket."""

    def __init__(self, user_id):
        def need(name):
            value = os.environ.get(name)
            if not value:
                sys.exit(f"{name} must be set for --from-pod-bucket")
            return value

        self.user_id = user_id
        self.client = S3Client(
            endpoint=os.environ.get(
                "POD_S3_ENDPOINT",
                "http://garage-backup.garage-backup-system.svc.cluster.local:3900"),
            region=os.environ.get("POD_S3_REGION", "garage"),
            bucket=os.environ.get("POD_S3_BUCKET", "pod-gateway"),
            access_key=need("POD_S3_ACCESS_KEY"),
            secret_key=need("POD_S3_SECRET_KEY"))
        self.digests = self._load_manifest()

    def _load_manifest(self):
        """key -> sha256, from the hash-chained manifest.

        The manifest is what makes verification meaningful: it records the
        digest the exporter computed at append time, so a restored file is
        checked against what Immich actually held, not merely against whatever
        the archive happens to contain now.
        """
        digests = {}
        prefix = f"{self.user_id}/manifest/"
        start_after = None
        while True:
            keys = self.client.list_keys(prefix, start_after=start_after)
            if not keys:
                break
            for key in keys:
                # get() hands back the raw response and leaves closing to us.
                with self.client.get(key) as response:
                    entry = json.loads(response.read())
                digests[entry["key"]] = entry["sha256"]
            start_after = keys[-1]
        return digests

    def fetch(self, key):
        with self.client.get(f"{self.user_id}/objects/{key}") as response:
            return response.read()

    def expected_digest(self, key):
        return self.digests.get(key)

    def describe(self):
        return (f"pod bucket {self.client.bucket} "
                f"({len(self.digests)} manifest entries)")


class DeviceSource:
    """Reads a member's home device copy — the disaster path.

    The device does not keep manifest entries (pod-agent.py verifies each
    object's digest at pull time and then only tracks its sequence), so there is
    no recorded digest to re-check here. What the copy has already been checked
    against is stated rather than silently skipped.
    """

    def __init__(self, directory):
        self.directory = directory

    def fetch(self, key):
        path = os.path.join(self.directory, *key.split("/"))
        if not os.path.exists(path):
            raise FileNotFoundError(path)
        with open(path, "rb") as handle:
            return handle.read()

    def expected_digest(self, key):
        return None

    def describe(self):
        return f"device copy at {self.directory} (no manifest; digests were verified at pull time)"


def main():
    parser = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--inventory", required=True,
                        help="JSONL asset rows from the RESTORED database")
    parser.add_argument("--library", required=True,
                        help="where the Immich library PVC is mounted here")
    parser.add_argument("--user", help="pod user id (required for --from-pod-bucket)")
    parser.add_argument("--from-device", metavar="DIR",
                        help="restore from a device copy instead of the pod bucket")
    parser.add_argument("--from-pod-bucket", action="store_true",
                        help="restore from the pod-gateway bucket (default)")
    parser.add_argument("--media-root", default="/data",
                        help="prefix of originalPath as immich-server sees it")
    parser.add_argument("--apply", action="store_true",
                        help="actually write files (default: report only)")
    args = parser.parse_args()

    if args.from_device:
        source = DeviceSource(args.from_device)
    else:
        if not args.user:
            sys.exit("--user is required when restoring from the pod bucket")
        source = PodBucketSource(args.user)
    print(f"Source: {source.describe()}")
    if not args.apply:
        print("Dry run — nothing will be written. Pass --apply to restore.\n")

    stats = {"present": 0, "restored": 0, "would_restore": 0,
             "missing": 0, "corrupt": 0}

    with open(args.inventory, encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line:
                continue
            asset = json.loads(line)

            relative = asset["originalPath"]
            if relative.startswith(args.media_root):
                relative = relative[len(args.media_root):]
            target = os.path.join(args.library, relative.lstrip("/"))

            if os.path.exists(target):
                stats["present"] += 1
                continue

            key = object_key(asset)
            if not args.apply:
                print(f"would restore  {key}  ->  {target}")
                stats["would_restore"] += 1
                continue

            try:
                payload = source.fetch(key)
            except (FileNotFoundError, S3Error, KeyError):
                # Never invent a file. An asset the archive does not hold is a
                # real gap the operator has to know about — most likely a user
                # who was never enrolled, or an asset added after the last
                # export ran.
                print(f"MISSING FROM ARCHIVE  {asset['id']}  {key}")
                stats["missing"] += 1
                continue

            digest = hashlib.sha256(payload).hexdigest()
            expected = source.expected_digest(key)
            if expected and digest != expected:
                print(f"DIGEST MISMATCH  {key}\n"
                      f"  manifest says {expected}\n"
                      f"  archive holds {digest}")
                stats["corrupt"] += 1
                continue

            os.makedirs(os.path.dirname(target), exist_ok=True)
            # Write beside the target and move into place, so an interrupted
            # run cannot leave Immich a half-written original.
            staging = target + ".restore-partial"
            with open(staging, "wb") as out:
                out.write(payload)
            shutil.move(staging, target)
            verified = " verified" if expected else " (unverified: no manifest)"
            print(f"restored  {target}  sha256={digest[:16]}{verified}")
            stats["restored"] += 1

    print(f"\n{stats}")
    if stats["missing"] or stats["corrupt"]:
        print("\nRestore INCOMPLETE — see the lines above.", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
