#!/usr/bin/env python3
"""Pull a personal pod archive onto a home device.

Runs on the owner's hardware, connects outbound only, and copies — it has no
delete path at all. A logical disaster on the server (ransomware, mass delete,
an admin mistake) therefore cannot propagate here.

Before trusting a page of the manifest the agent verifies the hash chain that
links it to everything it has already pulled. If the server ever removes,
reorders or rewrites history, the chain breaks and the agent stops rather than
quietly accepting the new version of the past.

Python 3 standard library only, so it runs on a stock Raspberry Pi OS image.
"""

import argparse
import hashlib
import json
import os
import shutil
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

CONFIG_PATH = os.environ.get("POD_CONFIG", "/etc/pod-archive/config.json")
DATA_ROOT = os.environ.get("POD_DATA", "/var/lib/pod-archive")
GENESIS_HASH = "0" * 64
PAGE_SIZE = 200


class ChainBroken(Exception):
    """The server's history no longer matches what this device already holds."""


def log(message):
    print(f"{time.strftime('%Y-%m-%d %H:%M:%S')} {message}", flush=True)


def canonical(document):
    return json.dumps(document, sort_keys=True, separators=(",", ":"))


def entry_hash(entry):
    body = {k: v for k, v in entry.items() if k != "entry_hash"}
    return hashlib.sha256(canonical(body).encode("utf-8")).hexdigest()


def safe_relative_path(key):
    if key.startswith("/") or "\\" in key:
        raise ValueError(f"unsafe object key: {key!r}")
    parts = key.split("/")
    if any(part in ("", ".", "..") for part in parts):
        raise ValueError(f"unsafe object key: {key!r}")
    return os.path.join(*parts)


class Pod:
    def __init__(self, config):
        self.url = config["url"].rstrip("/")
        self.user_id = config["user_id"]
        self.token = config["token"]
        self.name = config.get("name", "device")

    def _request(self, path, method="GET", body=None, timeout=1800):
        request = urllib.request.Request(
            f"{self.url}{path}", data=body, method=method
        )
        request.add_header("Authorization", f"Bearer {self.token}")
        if body is not None:
            request.add_header("Content-Type", "application/json")
        return urllib.request.urlopen(request, timeout=timeout)

    def manifest(self, since, limit=PAGE_SIZE):
        path = (
            f"/pod/v1/{urllib.parse.quote(self.user_id)}/manifest"
            f"?since={since}&limit={limit}"
        )
        with self._request(path, timeout=120) as response:
            return json.loads(response.read())

    def open_object(self, key):
        path = (
            f"/pod/v1/{urllib.parse.quote(self.user_id)}/objects/"
            f"{urllib.parse.quote(key)}"
        )
        return self._request(path)

    def heartbeat(self, state, objects, free_bytes):
        payload = json.dumps({
            "last_seq": state["last_seq"],
            "objects": objects,
            "free_bytes": free_bytes,
        }).encode("utf-8")
        path = f"/pod/v1/{urllib.parse.quote(self.user_id)}/heartbeat"
        with self._request(path, method="POST", body=payload, timeout=60) as response:
            response.read()


def load_state(path):
    if os.path.exists(path):
        with open(path, "r", encoding="utf-8") as handle:
            return json.load(handle)
    return {"last_seq": 0, "last_hash": GENESIS_HASH, "objects": 0}


def save_state(path, state):
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as handle:
        json.dump(state, handle, indent=2)
    os.replace(tmp, path)


def fetch_object(pod, entry, objects_dir):
    destination = os.path.join(objects_dir, safe_relative_path(entry["key"]))
    if os.path.exists(destination) and os.path.getsize(destination) == entry["size"]:
        return False

    os.makedirs(os.path.dirname(destination), exist_ok=True)
    digest = hashlib.sha256()
    handle, tmp_path = tempfile.mkstemp(dir=os.path.dirname(destination))
    try:
        with os.fdopen(handle, "wb") as tmp, pod.open_object(entry["key"]) as response:
            while True:
                chunk = response.read(1024 * 1024)
                if not chunk:
                    break
                digest.update(chunk)
                tmp.write(chunk)
        if digest.hexdigest() != entry["sha256"]:
            raise ValueError(
                f"digest mismatch for {entry['key']}: "
                f"expected {entry['sha256']}, got {digest.hexdigest()}"
            )
        os.replace(tmp_path, destination)
        return True
    except BaseException:
        # Only ever removes its own incomplete download.
        if os.path.exists(tmp_path):
            os.unlink(tmp_path)
        raise


def sync(pod, state, objects_dir, state_path):
    fetched = 0
    while True:
        page = pod.manifest(state["last_seq"])
        entries = page.get("entries", [])
        if not entries:
            break

        for entry in entries:
            if entry["seq"] != state["last_seq"] + 1:
                raise ChainBroken(
                    f"expected entry {state['last_seq'] + 1}, got {entry['seq']} — "
                    "the server skipped or removed history"
                )
            if entry["prev_hash"] != state["last_hash"]:
                raise ChainBroken(
                    f"entry {entry['seq']} does not follow the history this "
                    "device already holds — the archive was rewritten"
                )
            if entry_hash(entry) != entry["entry_hash"]:
                raise ChainBroken(f"entry {entry['seq']} has been tampered with")

            if fetch_object(pod, entry, objects_dir):
                fetched += 1
                state["objects"] += 1
                log(f"pulled {entry['key']} ({entry['size']} bytes)")

            state["last_seq"] = entry["seq"]
            state["last_hash"] = entry["entry_hash"]
            save_state(state_path, state)
    return fetched


def main():
    parser = argparse.ArgumentParser(description="Pull a personal pod archive.")
    parser.add_argument("--config", default=CONFIG_PATH)
    parser.add_argument("--data", default=DATA_ROOT)
    parser.add_argument("--verify-only", action="store_true",
                        help="Re-check local copies against the manifest, download nothing.")
    args = parser.parse_args()

    with open(args.config, "r", encoding="utf-8") as handle:
        pod = Pod(json.load(handle))

    objects_dir = os.path.join(args.data, "objects")
    state_path = os.path.join(args.data, "state.json")
    os.makedirs(objects_dir, exist_ok=True)
    state = load_state(state_path)

    if args.verify_only:
        return verify(pod, objects_dir)

    log(f"syncing from {pod.url} at seq {state['last_seq']}")
    try:
        fetched = sync(pod, state, objects_dir, state_path)
    except ChainBroken as exc:
        log(f"REFUSING TO CONTINUE: {exc}")
        log("Nothing local was changed. Report this to the community operator.")
        return 2
    except (urllib.error.URLError, OSError, ValueError) as exc:
        log(f"sync failed: {exc}")
        return 1

    usage = shutil.disk_usage(objects_dir)
    log(f"up to date at seq {state['last_seq']} ({fetched} new, "
        f"{usage.free // (1024 ** 3)} GiB free)")
    try:
        pod.heartbeat(state, state["objects"], usage.free)
    except (urllib.error.URLError, OSError) as exc:
        log(f"heartbeat failed (not fatal): {exc}")
    return 0


def verify(pod, objects_dir):
    """Re-hash every local object against the manifest it was pulled from."""
    checked = bad = 0
    since = 0
    while True:
        page = pod.manifest(since)
        entries = page.get("entries", [])
        if not entries:
            break
        for entry in entries:
            since = entry["seq"]
            path = os.path.join(objects_dir, safe_relative_path(entry["key"]))
            if not os.path.exists(path):
                log(f"MISSING {entry['key']}")
                bad += 1
                continue
            digest = hashlib.sha256()
            with open(path, "rb") as handle:
                for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                    digest.update(chunk)
            checked += 1
            if digest.hexdigest() != entry["sha256"]:
                log(f"CORRUPT {entry['key']}")
                bad += 1
    log(f"verified {checked} object(s), {bad} problem(s)")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
