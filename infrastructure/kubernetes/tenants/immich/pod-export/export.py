"""Append each enrolled user's Immich originals into their pod.

Reads a per-user inventory produced by inventory.sql (init container) and the
bytes from the Immich library PVC, mounted read-only, and offers each asset to
the pod gateway exactly once.

The exporter holds an Append-only grant, so it cannot read the pod back to work
out what is already there. Correctness therefore comes from the gateway's 409:
re-offering a known object is harmless and expected. The cursor is only an
optimisation — losing it costs a slow run, never a wrong one.

Pixels and filenames only. Albums, face names and tags are user intent that
lives in the database and has no pod home; see docs/adr/0047.
"""

import hashlib
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

INVENTORY = os.environ.get("INVENTORY_PATH", "/work/inventory.jsonl")
ENROLLED = os.environ.get("ENROLLED_PATH", "/etc/pod-export/users.txt")
LIBRARY_MOUNT = os.environ.get("LIBRARY_MOUNT", "/library")
# Immich stores absolute in-container paths ("/data/upload/<user>/ab/cd/<uuid>.jpg"),
# rooted where immich-server mounts the library PVC. This job mounts the same
# PVC somewhere else, so the root has to be rewritten.
MEDIA_ROOT = os.environ.get("IMMICH_MEDIA_ROOT", "/data")
GATEWAY = os.environ.get("POD_GATEWAY_URL", "").rstrip("/")
TOKEN = os.environ.get("POD_AGENT_TOKEN", "")
DRY_RUN = os.environ.get("DRY_RUN", "false").lower() == "true"
MAX_ERRORS = int(os.environ.get("MAX_ERRORS", "25"))

_CONTENT_TYPES = {
    ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png",
    ".heic": "image/heic", ".heif": "image/heif", ".webp": "image/webp",
    ".gif": "image/gif", ".tif": "image/tiff", ".tiff": "image/tiff",
    ".dng": "image/x-adobe-dng", ".raw": "image/x-dcraw",
    ".mp4": "video/mp4", ".mov": "video/quicktime", ".avi": "video/x-msvideo",
    ".mkv": "video/x-matroska", ".webm": "video/webm",
}


def log(message):
    print(message, flush=True)


def enrolled_users():
    """User ids with a pod device; maintained by admin-tools/pod-enroll-device.sh."""
    if not os.path.exists(ENROLLED):
        return set()
    with open(ENROLLED, "r", encoding="utf-8") as handle:
        return {
            line.strip() for line in handle
            if line.strip() and not line.startswith("#")
        }


def local_path(original_path):
    if not original_path.startswith(MEDIA_ROOT + "/"):
        raise ValueError(
            f"asset path {original_path!r} is outside IMMICH_MEDIA_ROOT "
            f"{MEDIA_ROOT!r} — the library layout changed, refusing to guess"
        )
    return LIBRARY_MOUNT + original_path[len(MEDIA_ROOT):]


def object_key(asset):
    """Stable, collision-free and still browsable on the device.

    On disk Immich names files by asset UUID, so the human-readable name only
    exists in the database — the pod is where the owner gets it back.

    Assets in Immich's locked folder are PIN-protected in the app, so they go
    to their own prefix rather than being flattened in with everything else.
    They are still exported: excluding them would leave the owner's most
    private photos as the only ones with no copy of their own.
    """
    day = (asset.get("created_at") or "1970-01-01")[:10]
    short = asset["id"].replace("-", "")[:8]
    name = os.path.basename(asset.get("file_name") or "asset")
    root = "immich-locked" if asset.get("visibility") == "locked" else "immich"
    return f"{root}/{day[:4]}/{day}/{short}-{name}"


def content_type(name):
    return _CONTENT_TYPES.get(os.path.splitext(name)[1].lower(),
                              "application/octet-stream")


def sha256_of(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def append(user_id, key, path, mime, sha256_hex):
    """Return 'appended', 'exists', or raise."""
    url = f"{GATEWAY}/pod/v1/{urllib.parse.quote(user_id)}/objects/{urllib.parse.quote(key)}"
    size = os.path.getsize(path)
    with open(path, "rb") as body:
        request = urllib.request.Request(url, data=body, method="PUT")
        request.add_header("Authorization", f"Bearer {TOKEN}")
        request.add_header("Content-Type", mime)
        request.add_header("Content-Length", str(size))
        request.add_header("X-Pod-Source", "immich")
        request.add_header("X-Pod-Sha256", sha256_hex)
        try:
            with urllib.request.urlopen(request, timeout=1800) as response:
                response.read()
            return "appended"
        except urllib.error.HTTPError as exc:
            if exc.code == 409:
                return "exists"
            raise RuntimeError(f"gateway returned {exc.code}: {exc.read()[:300]!r}") from exc


def main():
    if not GATEWAY or not TOKEN:
        log("POD_GATEWAY_URL and POD_AGENT_TOKEN must be set")
        return 2

    users = enrolled_users()
    if not users:
        log("No users enrolled for pod export; nothing to do.")
        return 0
    log(f"Exporting for {len(users)} enrolled user(s).")

    counts = {"appended": 0, "exists": 0, "skipped": 0, "errors": 0}
    with open(INVENTORY, "r", encoding="utf-8") as handle:
        for line in handle:
            line = line.strip()
            if not line:
                continue
            if not line.startswith("{"):
                # psql command tags ("DO") if -q was ever dropped, or notices.
                log(f"skip (not an inventory row): {line[:80]}")
                continue
            asset = json.loads(line)
            if asset["owner_id"] not in users:
                continue

            try:
                path = local_path(asset["original_path"])
            except ValueError as exc:
                log(f"FATAL: {exc}")
                return 3

            if not os.path.isfile(path):
                # The DB row outlived its file, or the file is still uploading.
                log(f"skip (missing file): {asset['id']} {path}")
                counts["skipped"] += 1
                continue

            key = object_key(asset)
            try:
                digest = sha256_of(path)
                if DRY_RUN:
                    log(f"DRY_RUN would append {asset['owner_id']} {key}")
                    counts["appended"] += 1
                    continue
                result = append(asset["owner_id"], key, path,
                                content_type(asset.get("file_name") or ""), digest)
                counts[result] += 1
                if result == "appended":
                    log(f"appended {asset['owner_id']} {key}")
            except (OSError, RuntimeError) as exc:
                counts["errors"] += 1
                log(f"ERROR {asset['id']}: {exc}")
                if counts["errors"] >= MAX_ERRORS:
                    log(f"Aborting after {counts['errors']} errors.")
                    return 1

    log(
        "Done: {appended} appended, {exists} already present, "
        "{skipped} skipped, {errors} errors.".format(**counts)
    )
    return 1 if counts["errors"] else 0


if __name__ == "__main__":
    sys.exit(main())
