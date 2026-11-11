#!/usr/bin/env python3
"""Example data for the Canary Cluster (Ring 0), seeded once and checked after every update.

    admin-tools/canary/seed.py seed     create what is missing, never anything twice
    admin-tools/canary/seed.py check    assert the seeded data is still there and readable

Why and what: plans-and-walkthroughs/design-update-strategy.md §3.3, docs/adr/0060.
Ring 0 is only worth its cost if it upgrades applications that hold data, so the
data is seeded once and then ages with the cluster through every migration.
**Never point this at a cluster with real members**: it creates accounts and
content, and it refuses any domain but the canary's unless told otherwise.

The members join the way people do. An invitation is issued in the join pod
(the Desk's own code, `manage.py create`), and browser.mjs walks it in Chromium
with a virtual passkey: join page, Keycloak, recovery codes. The passkey's
private key is kept in the state file, so every later sign-in, the data checks'
included, goes through the same login theme a member sees.

State lives in ~/.smallworlds/canary/seed-state.json (mode 0600). It holds the
three passkeys, recovery codes and per-member API keys: treat it like a password
file. Every step records what it made there and is skipped once recorded; losing
the file does not lose data, but a new run would then fail on usernames that
already exist rather than seed twice.

Needs: kubectl with the canary kubeconfig, node, and `npm ci` in e2e-tests/
(Playwright and its Chromium).
"""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import pathlib
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid

HERE = pathlib.Path(__file__).resolve().parent
ROOT = HERE.parent.parent
HOME = pathlib.Path.home() / ".smallworlds"
UA = "smallworlds-canary-seeder/1"

MEMBERS = [
    # anna owns most things and shares them; ben receives; zoe is the non-ASCII
    # case and uses only some applications, so some of her accounts start late.
    {"username": "anna", "firstName": "Anna", "lastName": "Keller", "locale": "de",
     "apps": ["immich"]},
    {"username": "ben", "firstName": "Ben", "lastName": "Rossi", "locale": "en",
     "apps": ["immich"]},
    {"username": "zoe", "firstName": "Zoë", "lastName": "Müller-Lüthi", "locale": "de",
     "apps": ["immich"]},
]

ALBUM = "Astronautinnen und Astronauten"
SPACE = "Raumfahrt-Club"


# --------------------------------------------------------------------------- state

class State:
    def __init__(self, path: pathlib.Path):
        self.path = path
        self.data = json.loads(path.read_text()) if path.exists() else {}

    def section(self, *keys) -> dict:
        node = self.data
        for key in keys:
            node = node.setdefault(key, {})
        return node

    def save(self) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True)
        tmp = self.path.with_suffix(".tmp")
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w") as handle:
            json.dump(self.data, handle, ensure_ascii=False, indent=1)
        os.replace(tmp, self.path)


# --------------------------------------------------------------------------- helpers

def say(text: str) -> None:
    print(text, flush=True)


def http(method: str, url: str, *, headers=None, body: bytes | None = None, timeout=300):
    request = urllib.request.Request(url, data=body, method=method,
                                     headers={"User-Agent": UA, **(headers or {})})
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.read()


class Api:
    """Immich/Gallery's REST API, as one member."""

    def __init__(self, origin: str, *, key: str | None = None, session: str | None = None):
        self.origin = origin
        self.auth = {"x-api-key": key} if key else {"Authorization": f"Bearer {session}"}

    def call(self, method: str, path: str, payload=None, expect=(200, 201, 204)):
        body = json.dumps(payload).encode() if payload is not None else None
        headers = {**self.auth, "Accept": "application/json"}
        if body is not None:
            headers["Content-Type"] = "application/json"
        status, raw = http(method, f"{self.origin}/api{path}", headers=headers, body=body)
        if status not in expect:
            raise SystemExit(f"{method} {path}: HTTP {status}: {raw[:300]!r}")
        return json.loads(raw) if raw else None

    def upload(self, path: pathlib.Path, filename: str, taken: str) -> dict:
        boundary = uuid.uuid4().hex
        stamp = f"{taken}T12:00:00.000Z"
        parts = []
        for name, value in (("fileCreatedAt", stamp), ("fileModifiedAt", stamp), ("filename", filename)):
            parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"\r\n\r\n{value}\r\n'.encode())
        quoted = filename.replace('"', "%22")
        parts.append(f'--{boundary}\r\nContent-Disposition: form-data; name="assetData"; '
                     f'filename="{quoted}"\r\nContent-Type: image/jpeg\r\n\r\n'.encode())
        parts.append(path.read_bytes())
        parts.append(f"\r\n--{boundary}--\r\n".encode())
        status, raw = http("POST", f"{self.origin}/api/assets", body=b"".join(parts), timeout=900,
                           headers={**self.auth, "Content-Type": f"multipart/form-data; boundary={boundary}"})
        if status not in (200, 201):
            raise SystemExit(f"upload {filename}: HTTP {status}: {raw[:300]!r}")
        return json.loads(raw)


class Cluster:
    def __init__(self, args):
        self.domain = args.domain
        self.env = {**os.environ, "KUBECONFIG": str(args.kubeconfig)}
        self.playwright = ROOT / "e2e-tests" / "node_modules" / "playwright" / "index.mjs"

    def url(self, sub: str) -> str:
        return f"https://{sub}.{self.domain}"

    def kubectl(self, *argv: str) -> str:
        result = subprocess.run(["kubectl", *argv], env=self.env, capture_output=True, text=True)
        if result.returncode:
            raise SystemExit(f"kubectl {' '.join(argv[:4])}…: {result.stderr.strip()}")
        return result.stdout

    def browser(self, command: str, payload: dict) -> dict:
        if not self.playwright.exists():
            raise SystemExit("Playwright is missing: run `npm ci` in e2e-tests/ first.")
        payload = {**payload, "failureScreenshot": str(HOME / "canary" / f"failed-{command}.png")}
        result = subprocess.run(["node", str(HERE / "browser.mjs"), command, json.dumps(payload)],
                                env={**os.environ, "PLAYWRIGHT_MODULE": str(self.playwright)},
                                capture_output=True, text=True, timeout=600)
        line = next((l for l in result.stdout.splitlines() if l.startswith("RESULT ")), None)
        if result.returncode or not line:
            raise SystemExit(f"browser {command} failed:\n{result.stderr.strip()[-2000:]}")
        return json.loads(line[len("RESULT "):])


# --------------------------------------------------------------------------- members

def seed_members(cluster: Cluster, state: State) -> None:
    for member in MEMBERS:
        record = state.section("members", member["username"])
        if record.get("credential"):
            continue
        name = member["username"]
        if not record.get("code"):
            label = f'{member["firstName"]} {member["lastName"]} (canary example member)'
            out = cluster.kubectl("-n", "join", "exec", "deploy/join", "--", "python", "/app/manage.py",
                                  "create", "--locale", member["locale"], "--days", "2", "--label", label)
            match = re.search(r"^Code: (\S+)$", out, re.M)
            if not match:
                raise SystemExit(f"manage.py create printed no code:\n{out}")
            record["code"] = match.group(1)
            state.save()
        say(f"  {name}: joining with a passkey")
        result = cluster.browser("join", {
            "join": cluster.url("join"), "keycloak": cluster.url("identity"), "code": record["code"],
            "username": name, "firstName": member["firstName"], "lastName": member["lastName"],
            "email": f"{name}@{cluster.domain}", "locale": member["locale"]})
        record.pop("code")
        record.update(result)
        state.save()
        say(f"  {name}: member, {len(result['recoveryCodes'])} recovery codes kept")


def signin(cluster: Cluster, state: State, username: str, app: str, origin: str) -> str:
    member = next(m for m in MEMBERS if m["username"] == username)
    record = state.section("members", username)
    result = cluster.browser("signin", {"app": app, "origin": origin, "keycloak": cluster.url("identity"),
                                        "username": username, "locale": member["locale"],
                                        "credential": record["credential"]})
    record["credential"]["signCount"] = result["signCount"]
    state.save()
    return result["session"]


# --------------------------------------------------------------------------- gallery

def photos() -> list[dict]:
    return json.loads((HERE / "photos.json").read_text())["photos"]


def photo_file(photo: dict) -> pathlib.Path:
    cache = HOME / "canary" / "photos"
    cache.mkdir(parents=True, exist_ok=True)
    path = cache / photo["url"].rsplit("/", 1)[-1]
    if not path.exists() or hashlib.sha256(path.read_bytes()).hexdigest() != photo["sha256"]:
        status, data = http("GET", photo["url"], timeout=900)
        if status != 200 or hashlib.sha256(data).hexdigest() != photo["sha256"]:
            raise SystemExit(f"{photo['key']}: download failed or does not match its pinned SHA-256")
        path.write_bytes(data)
    return path


def gallery_api(cluster: Cluster, state: State, username: str) -> Api:
    return Api(cluster.url("photos"), key=state.section("gallery", "keys")[username])


def seed_gallery(cluster: Cluster, state: State) -> None:
    origin = cluster.url("photos")
    gallery = state.section("gallery")
    keys = state.section("gallery", "keys")
    users = state.section("gallery", "users")

    for member in MEMBERS:
        name = member["username"]
        if "immich" not in member["apps"] or name in keys:
            continue
        say(f"  {name}: first sign-in to Gallery")
        session = signin(cluster, state, name, "immich", origin)
        api = Api(origin, session=session)
        created = api.call("POST", "/api-keys", {"name": "canary-seed", "permissions": ["all"]})
        keys[name] = created["secret"]
        users[name] = api.call("GET", "/users/me")["id"]
        state.save()

    assets = state.section("gallery", "assets")
    for photo in photos():
        if photo["key"] in assets:
            continue
        path = photo_file(photo)
        result = gallery_api(cluster, state, photo["owner"]).upload(path, photo["filename"], photo["taken"])
        assets[photo["key"]] = {"id": result["id"], "owner": photo["owner"],
                                "checksum": base64.b64encode(hashlib.sha1(path.read_bytes()).digest()).decode()}
        state.save()
        say(f"  {photo['owner']}: {photo['filename']} ({result['status']})")

    anna = gallery_api(cluster, state, "anna")
    if "album" not in gallery:
        ids = [assets[p["key"]]["id"] for p in photos() if p["owner"] == "anna" and p.get("person")]
        album = anna.call("POST", "/albums", {"albumName": ALBUM, "assetIds": ids,
                                              "description": "Beispieldaten für Ring 0",
                                              "albumUsers": [{"userId": users["ben"], "role": "editor"}]})
        gallery["album"] = album["id"]
        state.save()
        say(f"  anna: album shared with ben ({len(ids)} photos)")

    if "space" not in gallery:
        space = anna.call("POST", "/shared-spaces", {"name": SPACE, "description": "Beispieldaten für Ring 0"})
        anna.call("POST", f"/shared-spaces/{space['id']}/members", {"userId": users["ben"], "role": "editor"})
        anna.call("POST", f"/shared-spaces/{space['id']}/members", {"userId": users["zoe"], "role": "viewer"})
        ids = [assets[p["key"]]["id"] for p in photos() if p["owner"] == "anna" and not p.get("person")]
        anna.call("POST", f"/shared-spaces/{space['id']}/assets", {"assetIds": ids})
        # An editor adds to a space someone else owns: content links across members.
        gallery_api(cluster, state, "ben").call("POST", f"/shared-spaces/{space['id']}/assets",
                                                {"assetIds": [assets["aurora"]["id"]]})
        gallery["space"] = space["id"]
        state.save()
        say("  anna: shared space with ben (editor) and zoe (viewer)")

    name_people(cluster, state)


def name_people(cluster: Cluster, state: State, wait_seconds: int = 1200) -> None:
    """Face recognition runs after upload; a person exists once it has enough faces.
    Name each by the person most often found on that person's photos."""
    people = state.section("gallery", "people")
    assets = state.section("gallery", "assets")
    wanted = {}
    for photo in photos():
        if photo.get("person"):
            wanted.setdefault((photo["owner"], photo["person"]), []).append(assets[photo["key"]]["id"])
    deadline = time.time() + wait_seconds
    for (owner, person), ids in wanted.items():
        if person in people:
            continue
        api = gallery_api(cluster, state, owner)
        while True:
            counts = {}
            for asset_id in ids:
                for found in api.call("GET", f"/assets/{asset_id}").get("people") or []:
                    counts[found["id"]] = counts.get(found["id"], 0) + 1
            best = max(counts.items(), key=lambda kv: kv[1], default=(None, 0))
            if best[1] >= 3:
                break
            if time.time() > deadline:
                say(f"  {owner}: {person} not recognised yet (best {best[1]} of {len(ids)} photos); "
                    "run seed again later")
                return
            time.sleep(30)
        api.call("PUT", f"/people/{best[0]}", {"name": person})
        people[person] = {"id": best[0], "owner": owner}
        state.save()
        say(f"  {owner}: named {person} ({best[1]} of {len(ids)} photos)")


def check_gallery(cluster: Cluster, state: State, fresh_signin: bool) -> list[str]:
    failures = []
    gallery = state.section("gallery")
    if not gallery.get("keys"):
        return ["gallery: nothing seeded"]
    if fresh_signin:
        try:
            session = signin(cluster, state, "anna", "immich", cluster.url("photos"))
            me = Api(cluster.url("photos"), session=session).call("GET", "/users/me")
            if me["id"] != gallery["users"]["anna"]:
                failures.append("gallery: passkey sign-in landed on a different account")
        except SystemExit as error:
            failures.append(f"gallery: anna cannot sign in with her passkey: {error}")
    for key, asset in gallery.get("assets", {}).items():
        status, raw = http("GET", f"{cluster.url('photos')}/api/assets/{asset['id']}",
                           headers={"x-api-key": gallery["keys"][asset["owner"]]})
        if status != 200:
            failures.append(f"gallery: {key} is gone (HTTP {status})")
        elif json.loads(raw)["checksum"] != asset["checksum"]:
            failures.append(f"gallery: {key} has a different checksum")
    # Read the largest original back: it went up in parts, and v5.9 fixed reading
    # multipart objects back from S3 stores like ours.
    largest = max(photos(), key=lambda p: p["bytes"])
    asset = gallery.get("assets", {}).get(largest["key"])
    if asset:
        status, data = http("GET", f"{cluster.url('photos')}/api/assets/{asset['id']}/original",
                            headers={"x-api-key": gallery["keys"][asset["owner"]]}, timeout=900)
        if status != 200 or hashlib.sha256(data).hexdigest() != largest["sha256"]:
            failures.append(f"gallery: original of {largest['key']} does not read back intact (HTTP {status})")
    if "album" in gallery:
        album = gallery_api(cluster, state, "ben").call("GET", f"/albums/{gallery['album']}", expect=(200, 400, 404))
        if not album or album.get("albumName") != ALBUM:
            failures.append("gallery: ben no longer sees anna's album")
        elif album["assetCount"] != sum(1 for p in photos() if p["owner"] == "anna" and p.get("person")):
            failures.append(f"gallery: album has {album['assetCount']} photos")
    if "space" in gallery:
        members = gallery_api(cluster, state, "zoe").call("GET", f"/shared-spaces/{gallery['space']}/members",
                                                         expect=(200, 400, 403, 404))
        roles = {m["userId"]: m["role"] for m in members if isinstance(members, list)} if members else {}
        want = {gallery["users"]["anna"]: "owner", gallery["users"]["ben"]: "editor",
                gallery["users"]["zoe"]: "viewer"}
        if roles != want:
            failures.append(f"gallery: shared space members changed: {roles}")
    for person, record in gallery.get("people", {}).items():
        found = gallery_api(cluster, state, record["owner"]).call("GET", f"/people/{record['id']}",
                                                                  expect=(200, 400, 404))
        if not found or found.get("name") != person:
            failures.append(f"gallery: person {person} lost its name")
    return failures


# --------------------------------------------------------------------------- main

def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("command", choices=["seed", "check"])
    parser.add_argument("--domain", default="canary.smallworlds.network")
    parser.add_argument("--kubeconfig", type=pathlib.Path, default=HOME / "kubeconfigs" / "local-canary.yaml")
    parser.add_argument("--state", type=pathlib.Path, default=HOME / "canary" / "seed-state.json")
    parser.add_argument("--not-the-canary", action="store_true",
                        help="allow a domain other than the canary's (never one with real members)")
    parser.add_argument("--no-signin", action="store_true", help="check: skip the fresh passkey sign-in")
    args = parser.parse_args()
    if not args.domain.startswith("canary.") and not args.not_the_canary:
        raise SystemExit(f"{args.domain} is not the canary; this creates accounts and content. "
                         "Pass --not-the-canary if that is really meant.")
    cluster = Cluster(args)
    state = State(args.state)

    if args.command == "seed":
        say("members")
        seed_members(cluster, state)
        say("gallery")
        seed_gallery(cluster, state)
        say("done")
        return 0

    failures = check_gallery(cluster, state, fresh_signin=not args.no_signin)
    for failure in failures:
        say(f"  FAIL  {failure}")
    say("all data checks passed" if not failures else f"{len(failures)} data checks failed")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
