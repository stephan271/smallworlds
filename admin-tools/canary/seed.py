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
import urllib.parse
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
     "apps": ["immich", "nextcloud", "forgejo", "stalwart"]},
    {"username": "ben", "firstName": "Ben", "lastName": "Rossi", "locale": "en",
     "apps": ["immich", "nextcloud", "forgejo", "stalwart"]},
    {"username": "zoe", "firstName": "Zoë", "lastName": "Müller-Lüthi", "locale": "de",
     "apps": ["immich", "nextcloud"]},
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


def signin(cluster: Cluster, state: State, username: str, app: str, origin: str) -> dict:
    """Sign a member in to an application with their passkey. Gallery answers with
    a session; Nextcloud and Forgejo with an app password or token the member made
    in the application's own settings, as they would for any client."""
    member = next(m for m in MEMBERS if m["username"] == username)
    record = state.section("members", username)
    result = cluster.browser("signin", {"app": app, "origin": origin, "keycloak": cluster.url("identity"),
                                        "username": username, "locale": member["locale"],
                                        "credential": record["credential"], "tokenName": "canary-seed"})
    record["credential"]["signCount"] = result.pop("signCount")
    state.save()
    return result


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
        session = signin(cluster, state, name, "immich", origin)["session"]
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
            session = signin(cluster, state, "anna", "immich", cluster.url("photos"))["session"]
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


# --------------------------------------------------------------------------- nextcloud

def members_of(app: str) -> list[str]:
    return [m["username"] for m in MEMBERS if app in m["apps"]]


class Nextcloud:
    """WebDAV, CalDAV, CardDAV and OCS, as one member with an app password."""

    def __init__(self, origin: str, creds: dict):
        self.origin = origin
        self.uid = creds["uid"]
        basic = base64.b64encode(f'{creds["loginName"]}:{creds["token"]}'.encode()).decode()
        self.auth = {"Authorization": f"Basic {basic}", "OCS-APIRequest": "true"}

    def dav(self, method: str, path: str, body: bytes | None = None, headers=None, expect=(200, 201, 204, 207)):
        url = f"{self.origin}/remote.php/dav/{urllib.parse.quote(path)}"
        status, raw = http(method, url, headers={**self.auth, **(headers or {})}, body=body)
        if expect and status not in expect:
            raise SystemExit(f"nextcloud {method} {path}: HTTP {status}: {raw[:200]!r}")
        return status, raw

    def file(self, path: str) -> str:
        return f"files/{self.uid}/{path}"

    def ocs(self, method: str, path: str, form: dict | None = None):
        body = urllib.parse.urlencode(form).encode() if form else None
        headers = {**self.auth, "Accept": "application/json"}
        if body:
            headers["Content-Type"] = "application/x-www-form-urlencoded"
        status, raw = http(method, f"{self.origin}/ocs/v2.php{path}", headers=headers, body=body)
        if status != 200:
            raise SystemExit(f"nextcloud OCS {method} {path}: HTTP {status}: {raw[:200]!r}")
        return json.loads(raw)["ocs"]["data"]


def nextcloud_files() -> list[dict]:
    """What each member keeps in Files. Contents are fixed, so the check can
    compare checksums; one is a photo, so not everything is text."""
    coast = next(p for p in photos() if p["key"] == "earth-coast")
    return [
        {"owner": "anna", "path": "Projekte/Übersicht 2026.md",
         "text": "# Sternwarte 2026\n\n- Teleskop kalibrieren\n- Beobachtungsabende planen\n"},
        {"owner": "anna", "path": "Projekte/Budget.csv",
         "text": "Posten;Betrag\nOkular;240\nStativ;180\n"},
        {"owner": "anna", "path": "Fotos/Küste aus dem All.jpg", "photo": coast["key"]},
        {"owner": "ben", "path": "Notes/reading list.txt",
         "text": "Cosmos\nPale Blue Dot\n"},
        {"owner": "zoe", "path": "Notizen/Grüße aus Zürich.txt",
         "text": "Grüezi! Ünïcödé bleibt hoffentlich ganz.\n"},
    ]


def file_bytes(entry: dict) -> bytes:
    if "photo" in entry:
        return photo_file(next(p for p in photos() if p["key"] == entry["photo"])).read_bytes()
    return entry["text"].encode()


EVENT_UID = "canary-sternwarte-abend@smallworlds"
EVENT = ("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//SmallWorlds//canary seed//DE\r\n"
         "BEGIN:VEVENT\r\nUID:" + EVENT_UID + "\r\nDTSTAMP:20261111T120000Z\r\n"
         "DTSTART:20261205T190000Z\r\nDTEND:20261205T220000Z\r\n"
         "SUMMARY:Sternwarte-Abend\r\nLOCATION:Dachterrasse\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
CONTACT = ("BEGIN:VCARD\r\nVERSION:3.0\r\nUID:canary-zoe@smallworlds\r\n"
           "FN:Zoë Müller-Lüthi\r\nN:Müller-Lüthi;Zoë;;;\r\nEMAIL:zoe@example.org\r\nEND:VCARD\r\n")


def seed_nextcloud(cluster: Cluster, state: State) -> None:
    origin = cluster.url("files")
    creds = state.section("nextcloud", "creds")
    for name in members_of("nextcloud"):
        if name not in creds:
            say(f"  {name}: first sign-in to Nextcloud, app password")
            creds[name] = signin(cluster, state, name, "nextcloud", origin)
            state.save()
    nc = {name: Nextcloud(origin, creds[name]) for name in creds}

    files = state.section("nextcloud", "files")
    for entry in nextcloud_files():
        key = f'{entry["owner"]}:{entry["path"]}'
        if key in files:
            continue
        client = nc[entry["owner"]]
        folder = entry["path"].rsplit("/", 1)[0]
        client.dav("MKCOL", client.file(folder), expect=(201, 405))  # 405: exists
        data = file_bytes(entry)
        client.dav("PUT", client.file(entry["path"]), body=data)
        files[key] = hashlib.sha256(data).hexdigest()
        state.save()
        say(f'  {entry["owner"]}: {entry["path"]}')

    share = state.section("nextcloud", "share")
    if not share:
        made = nc["anna"].ocs("POST", "/apps/files_sharing/api/v1/shares",
                              {"path": "/Projekte", "shareType": "0", "shareWith": creds["ben"]["uid"],
                               "permissions": "15"})
        share.update({"id": made["id"], "path": "Projekte"})
        state.save()
        say("  anna: Projekte shared with ben (edit)")

    if "calendar" not in state.section("nextcloud"):
        anna = nc["anna"]
        body = ('<?xml version="1.0"?><c:mkcalendar xmlns:d="DAV:" xmlns:c="urn:ietf:params:xml:ns:caldav">'
                '<d:set><d:prop><d:displayname>Sternwarte</d:displayname></d:prop></d:set></c:mkcalendar>')
        anna.dav("MKCALENDAR", f"calendars/{anna.uid}/sternwarte", body=body.encode(),
                 headers={"Content-Type": "application/xml"}, expect=(201, 405))
        anna.dav("PUT", f"calendars/{anna.uid}/sternwarte/sternwarte-abend.ics", body=EVENT.encode(),
                 headers={"Content-Type": "text/calendar; charset=utf-8"})
        # The default address book appears on first access to the home.
        anna.dav("PROPFIND", f"addressbooks/users/{anna.uid}/", headers={"Depth": "1"})
        anna.dav("PUT", f"addressbooks/users/{anna.uid}/contacts/zoe.vcf", body=CONTACT.encode(),
                 headers={"Content-Type": "text/vcard; charset=utf-8"})
        state.section("nextcloud")["calendar"] = True
        state.save()
        say("  anna: calendar Sternwarte with one event, contact Zoë")


def check_nextcloud(cluster: Cluster, state: State) -> list[str]:
    section = state.section("nextcloud")
    if not section.get("creds"):
        return ["nextcloud: nothing seeded"]
    failures = []
    nc = {name: Nextcloud(cluster.url("files"), creds) for name, creds in section["creds"].items()}
    for key, digest in section.get("files", {}).items():
        owner, path = key.split(":", 1)
        status, raw = nc[owner].dav("GET", nc[owner].file(path), expect=None)
        if status != 200 or hashlib.sha256(raw).hexdigest() != digest:
            failures.append(f"nextcloud: {owner}'s {path} is gone or changed (HTTP {status})")
    if section.get("share"):
        ben = nc["ben"]
        status, raw = ben.dav("GET", ben.file("Projekte/Übersicht 2026.md"), expect=None)
        if status != 200 or hashlib.sha256(raw).hexdigest() != section["files"].get("anna:Projekte/Übersicht 2026.md"):
            failures.append(f"nextcloud: ben cannot read anna's shared Projekte (HTTP {status})")
    if section.get("calendar"):
        anna = nc["anna"]
        status, raw = anna.dav("GET", f"calendars/{anna.uid}/sternwarte/sternwarte-abend.ics", expect=None)
        if status != 200 or b"SUMMARY:Sternwarte-Abend" not in raw:
            failures.append(f"nextcloud: the calendar event is gone (HTTP {status})")
        status, raw = anna.dav("GET", f"addressbooks/users/{anna.uid}/contacts/zoe.vcf", expect=None)
        if status != 200 or "Zoë Müller-Lüthi".encode() not in raw:
            failures.append(f"nextcloud: the contact is gone or garbled (HTTP {status})")
    return failures


# --------------------------------------------------------------------------- forgejo

class Forgejo:
    def __init__(self, origin: str, token: str):
        self.origin = origin
        self.auth = {"Authorization": f"token {token}"}

    def call(self, method: str, path: str, payload=None, expect=(200, 201, 204)):
        body = json.dumps(payload).encode() if payload is not None else None
        headers = {**self.auth, "Accept": "application/json"}
        if body is not None:
            headers["Content-Type"] = "application/json"
        status, raw = http(method, f"{self.origin}/api/v1{path}", headers=headers, body=body)
        if expect and status not in expect:
            raise SystemExit(f"forgejo {method} {path}: HTTP {status}: {raw[:200]!r}")
        return json.loads(raw) if raw and status != 204 else None


REPO = "sternwarte"
REPO_FILES = [  # one commit each, in this order
    ("beobachtungen/2026-11.md", "# November\n\n- 11.: Jupiter, klar\n- 14.: bewölkt\n", "Beobachtungen November"),
    ("sterne.csv", "name,helligkeit\nSirius,-1.46\nWega,0.03\n", "Sternliste anlegen"),
    ("Übersicht.md", "Ziele für 2026: ein Abend pro Monat.\n", "Übersicht mit Umlaut im Namen"),
]


def seed_forgejo(cluster: Cluster, state: State) -> None:
    origin = cluster.url("git")
    section = state.section("forgejo")
    tokens = state.section("forgejo", "tokens")
    users = state.section("forgejo", "users")
    for name in members_of("forgejo"):
        if name not in tokens:
            say(f"  {name}: first sign-in to Forgejo, access token")
            tokens[name] = signin(cluster, state, name, "forgejo", origin)["token"]
            users[name] = Forgejo(origin, tokens[name]).call("GET", "/user")["login"]
            state.save()
    anna, ben = Forgejo(origin, tokens["anna"]), Forgejo(origin, tokens["ben"])
    owner = users["anna"]
    repo = f"/repos/{owner}/{REPO}"

    if "repo" not in section:
        anna.call("POST", "/user/repos", {"name": REPO, "description": "Beispieldaten für Ring 0",
                                          "auto_init": True, "default_branch": "main", "private": False})
        section["repo"] = f"{owner}/{REPO}"
        state.save()
        say(f"  anna: repository {owner}/{REPO}")
    done = section.setdefault("files", [])
    for path, text, message in REPO_FILES:
        if path in done:
            continue
        anna.call("POST", f"{repo}/contents/{urllib.parse.quote(path)}",
                  {"content": base64.b64encode(text.encode()).decode(), "message": message, "branch": "main"})
        done.append(path)
        state.save()
        say(f"  anna: commit {message!r}")
    if "labels" not in section:
        section["labels"] = {l["name"]: l["id"] for l in (
            anna.call("POST", f"{repo}/labels", {"name": "Beobachtung", "color": "#1d76db"}),
            anna.call("POST", f"{repo}/labels", {"name": "Fehler", "color": "#e11d21"}))}
        anna.call("PUT", f"{repo}/collaborators/{users['ben']}", {"permission": "write"})
        state.save()
        say("  anna: labels, ben as collaborator")
    if "issue" not in section:
        issue = anna.call("POST", f"{repo}/issues", {"title": "Teleskop kalibrieren",
                                                      "body": "Die Montierung driftet nach Westen.",
                                                      "labels": [section["labels"]["Fehler"]]})
        comment = ben.call("POST", f"{repo}/issues/{issue['number']}/comments",
                           {"body": "I can bring a polar scope on Friday."})
        section["issue"] = {"number": issue["number"], "comment": comment["id"]}
        state.save()
        say(f"  anna: issue #{issue['number']}, ben commented")
    if "pull" not in section:
        ben.call("POST", f"{repo}/contents/sternkarte.md",
                 {"content": base64.b64encode(b"# Sternkarte\n\nOrion, Stier, Zwillinge.\n").decode(),
                  "message": "Add a star chart", "branch": "main", "new_branch": "ben/sternkarte"})
        pull = ben.call("POST", f"{repo}/pulls", {"head": "ben/sternkarte", "base": "main",
                                                   "title": "Sternkarte für den Winter"})
        section["pull"] = pull["number"]
        state.save()
        say(f"  ben: pull request #{pull['number']}")
    if "main" not in section:
        section["main"] = anna.call("GET", f"{repo}/branches/main")["commit"]["id"]
        state.save()


def check_forgejo(cluster: Cluster, state: State) -> list[str]:
    section = state.section("forgejo")
    if not section.get("tokens"):
        return ["forgejo: nothing seeded"]
    failures = []
    origin = cluster.url("git")
    anna = Forgejo(origin, section["tokens"]["anna"])
    repo = f"/repos/{section['repo']}"
    branch = anna.call("GET", f"{repo}/branches/main", expect=None)
    if not branch or branch.get("commit", {}).get("id") != section.get("main"):
        failures.append("forgejo: main no longer points at the seeded commit")
    for path, text, _ in REPO_FILES:
        status, raw = http("GET", f"{origin}/api/v1{repo}/raw/{urllib.parse.quote(path)}?ref=main",
                           headers=anna.auth)
        if status != 200 or raw != text.encode():
            failures.append(f"forgejo: {path} is gone or changed (HTTP {status})")
    issue = anna.call("GET", f"{repo}/issues/{section['issue']['number']}", expect=None) or {}
    if issue.get("title") != "Teleskop kalibrieren" or "Fehler" not in [l["name"] for l in issue.get("labels", [])]:
        failures.append("forgejo: the issue lost its title or label")
    comment = anna.call("GET", f"{repo}/issues/comments/{section['issue']['comment']}", expect=None) or {}
    if comment.get("user", {}).get("login") != section["users"]["ben"]:
        failures.append("forgejo: ben's comment is gone or has another author")
    pull = anna.call("GET", f"{repo}/pulls/{section['pull']}", expect=None) or {}
    if pull.get("state") != "open" or pull.get("head", {}).get("ref") != "ben/sternkarte":
        failures.append("forgejo: ben's pull request is gone or changed")
    return failures


# --------------------------------------------------------------------------- mail

USING = ["urn:ietf:params:jmap:core", "urn:ietf:params:jmap:mail", "urn:ietf:params:jmap:submission"]
MAILS = [  # (key, sender, recipient, subject, text)
    ("freitag", "anna", "ben", "Sternwarte am Freitag",
     "Hallo Ben,\n\nam Freitag ist es klar. Treffen um 19 Uhr auf der Dachterrasse?\n\nGrüße, Anna\n"),
    ("okular", "anna", "ben", "Okular für die Sammelbestellung",
     "Das 25-mm-Okular kostet 240 €. Bist du dabei?\n"),
    ("antwort", "ben", "anna", "Re: Sternwarte am Freitag",
     "Friday works. I'll bring the polar scope — Grüße!\n"),
]
FOLDER = "Sternwarte"


class Jmap:
    def __init__(self, origin: str, token: str):
        self.auth = {"Authorization": f"Bearer {token}"}
        status, raw = http("GET", f"{origin}/jmap/session", headers=self.auth)
        if status != 200:
            raise SystemExit(f"jmap session: HTTP {status}: {raw[:200]!r}")
        session = json.loads(raw)
        self.api = session["apiUrl"]
        self.account = session["primaryAccounts"]["urn:ietf:params:jmap:mail"]

    def call(self, *calls):
        body = json.dumps({"using": USING, "methodCalls": [[name, {"accountId": self.account, **args}, f"c{i}"]
                                                           for i, (name, args) in enumerate(calls)]}).encode()
        status, raw = http("POST", self.api, body=body,
                           headers={**self.auth, "Content-Type": "application/json"})
        if status != 200:
            raise SystemExit(f"jmap: HTTP {status}: {raw[:300]!r}")
        responses = json.loads(raw)["methodResponses"]
        for name, result, _ in responses:
            if name == "error" or result.get("notCreated") or result.get("notUpdated"):
                raise SystemExit(f"jmap: {name} refused: {json.dumps(result)[:400]}")
        return [result for _, result, _ in responses]

    def mailboxes(self) -> dict:
        found = self.call(("Mailbox/get", {"ids": None}))[0]["list"]
        return {(m.get("role") or m["name"]): m["id"] for m in found}

    def find(self, mailbox: str, subject: str) -> list[dict]:
        # Compared here, not by a subject filter: Stalwart's search does not find
        # "Re: Sternwarte am Freitag" by its full subject.
        query, got = self.call(
            ("Email/query", {"filter": {"inMailbox": mailbox}, "limit": 200}),
            ("Email/get", {"#ids": {"resultOf": "c0", "name": "Email/query", "path": "/ids"},
                           "properties": ["subject", "messageId", "bodyValues", "textBody"],
                           "fetchTextBodyValues": True}))
        return [m for m in got["list"] if m["subject"] == subject]


def mail_signin(cluster: Cluster, state: State, name: str) -> Jmap:
    token = signin(cluster, state, name, "stalwart", cluster.url("webmail"))["token"]
    return Jmap(cluster.url("mail"), token)


def seed_mail(cluster: Cluster, state: State) -> None:
    section = state.section("mail")
    sent = section.setdefault("sent", [])
    if len(sent) == len(MAILS) and section.get("filed"):
        return
    say("  anna, ben: signing in to webmail for a JMAP token")
    jmap = {name: mail_signin(cluster, state, name) for name in ("anna", "ben")}
    address = {}
    for name, client in jmap.items():
        identity = client.call(("Identity/get", {"ids": None}))[0]["list"][0]
        address[name] = identity
    section["addresses"] = {name: identity["email"] for name, identity in address.items()}
    state.save()

    for key, sender, recipient, subject, text in MAILS:
        if key in sent:
            continue
        client = jmap[sender]
        boxes = client.mailboxes()
        identity = address[sender]
        client.call(
            ("Email/set", {"create": {"m": {
                "mailboxIds": {boxes["drafts"]: True}, "keywords": {"$draft": True, "$seen": True},
                "from": [{"name": identity.get("name") or sender, "email": identity["email"]}],
                "to": [{"email": address[recipient]["email"]}],
                "subject": subject, "bodyValues": {"b": {"value": text}},
                "textBody": [{"partId": "b", "type": "text/plain"}]}}}),
            ("EmailSubmission/set", {"create": {"s": {"identityId": identity["id"], "emailId": "#m"}},
                                     "onSuccessUpdateEmail": {"#s": {
                                         f"mailboxIds/{boxes['drafts']}": None,
                                         f"mailboxIds/{boxes['sent']}": True,
                                         "keywords/$draft": None}}}))
        sent.append(key)
        state.save()
        say(f"  {sender} -> {recipient}: {subject}")

    if not section.get("filed"):
        ben = jmap["ben"]
        boxes = ben.mailboxes()
        if FOLDER not in boxes:
            created = ben.call(("Mailbox/set", {"create": {"f": {"name": FOLDER}}}))[0]["created"]["f"]
            boxes[FOLDER] = created["id"]
        subject = MAILS[0][3]
        deadline = time.time() + 300
        while not (found := ben.find(boxes["inbox"], subject)):
            if time.time() > deadline:
                raise SystemExit(f"mail: {subject!r} never reached ben's inbox")
            time.sleep(5)
        ben.call(("Email/set", {"update": {found[0]["id"]: {
            "mailboxIds": {boxes[FOLDER]: True}}}}))
        section["filed"] = {"subject": subject, "messageId": found[0]["messageId"]}
        state.save()
        say(f"  ben: filed {subject!r} into {FOLDER}")


def check_mail(cluster: Cluster, state: State) -> list[str]:
    section = state.section("mail")
    if not section.get("filed"):
        return ["mail: nothing seeded"]
    failures = []
    try:
        jmap = {name: mail_signin(cluster, state, name) for name in ("anna", "ben")}
    except SystemExit as error:
        return [f"mail: cannot sign in to webmail and reach JMAP: {error}"]
    texts = {subject: text for _, _, _, subject, text in MAILS}
    ben_boxes, anna_boxes = jmap["ben"].mailboxes(), jmap["anna"].mailboxes()
    if FOLDER not in ben_boxes:
        return failures + [f"mail: ben's folder {FOLDER} is gone"]
    expected = [(jmap["ben"], ben_boxes[FOLDER], MAILS[0][3]),
                (jmap["ben"], ben_boxes["inbox"], MAILS[1][3]),
                (jmap["anna"], anna_boxes["inbox"], MAILS[2][3]),
                (jmap["anna"], anna_boxes["sent"], MAILS[0][3])]
    for client, box, subject in expected:
        found = client.find(box, subject)
        if not found:
            failures.append(f"mail: {subject!r} is missing")
            continue
        body = "".join(v["value"] for v in found[0].get("bodyValues", {}).values())
        if body.strip() != texts[subject].strip():
            failures.append(f"mail: {subject!r} has a different body")
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
    parser.add_argument("--only", type=lambda v: v.split(","), default=None,
                        help="comma-separated applications, e.g. nextcloud,forgejo")
    args = parser.parse_args()
    if not args.domain.startswith("canary.") and not args.not_the_canary:
        raise SystemExit(f"{args.domain} is not the canary; this creates accounts and content. "
                         "Pass --not-the-canary if that is really meant.")
    cluster = Cluster(args)
    state = State(args.state)

    if args.command == "seed":
        say("members")
        seed_members(cluster, state)
        for name, step in (("gallery", seed_gallery), ("nextcloud", seed_nextcloud),
                           ("forgejo", seed_forgejo), ("mail", seed_mail)):
            if args.only and name not in args.only:
                continue
            say(name)
            step(cluster, state)
        say("done")
        return 0

    failures = []
    for name, check in (("gallery", lambda: check_gallery(cluster, state, fresh_signin=not args.no_signin)),
                        ("nextcloud", lambda: check_nextcloud(cluster, state)),
                        ("forgejo", lambda: check_forgejo(cluster, state)),
                        ("mail", lambda: check_mail(cluster, state))):
        if args.only and name not in args.only:
            continue
        found = check()
        say(f"  {name}: {'ok' if not found else f'{len(found)} failed'}")
        failures += found
    for failure in failures:
        say(f"  FAIL  {failure}")
    say("all data checks passed" if not failures else f"{len(failures)} data checks failed")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
