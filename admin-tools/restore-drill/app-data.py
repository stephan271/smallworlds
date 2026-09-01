#!/usr/bin/env python3
"""Seed and verify deterministic multi-user Immich and Nextcloud data.

The caller supplies port-forwarded HTTP origins. Evidence is a JSON document;
it contains synthetic credentials and is therefore written with mode 0600.
Only Python's standard library is required.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import io
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid
from pathlib import Path


USERS = ("drill-alice", "drill-bob", "drill-carol")

# A valid 1x1 JPEG. A unique trailer after EOI keeps every test object small,
# valid and byte-distinguishable without adding Pillow to the drill host.
JPEG = base64.b64decode(
    "/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAP//////////////////////////////////////////////////////////////////////////////////////"
    "2wBDAf//////////////////////////////////////////////////////////////////////////////////////"
    "wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAf/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIQAxAAAAF//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABBQJ//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAwEBPwF//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAgEBPwF//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQAGPwJ//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABPyF//9oADAMBAAIAAwAAABB//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAwEBPxB//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAgEBPxB//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABPxB//9k="
)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: ANN001
        return None


def request(
    url: str,
    *,
    method: str = "GET",
    body: bytes | None = None,
    headers: dict[str, str] | None = None,
    expected: tuple[int, ...] = (200,),
) -> tuple[int, bytes, dict[str, str]]:
    req = urllib.request.Request(url, data=body, method=method)
    for key, value in (headers or {}).items():
        req.add_header(key, value)
    try:
        with urllib.request.build_opener(NoRedirect()).open(req, timeout=120) as response:
            status, payload = response.status, response.read()
            response_headers = dict(response.headers.items())
    except urllib.error.HTTPError as error:
        status, payload = error.code, error.read()
        response_headers = dict(error.headers.items())
    if status not in expected:
        preview = payload.decode("utf-8", "replace")[:800]
        raise RuntimeError(f"{method} {url}: HTTP {status}: {preview}")
    return status, payload, response_headers


def json_request(
    url: str,
    *,
    method: str = "GET",
    data: object | None = None,
    token: str | None = None,
    expected: tuple[int, ...] = (200,),
) -> object:
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    _, payload, _ = request(
        url,
        method=method,
        body=json.dumps(data).encode() if data is not None else None,
        headers=headers,
        expected=expected,
    )
    return json.loads(payload or b"null")


def immich_login(origin: str, email: str, password: str) -> str:
    result = json_request(
        f"{origin}/api/auth/login",
        method="POST",
        data={"email": email, "password": password},
        expected=(200, 201),
    )
    assert isinstance(result, dict)
    return str(result["accessToken"])


def immich_upload(origin: str, token: str, user: str, index: int) -> dict[str, str]:
    filename = f"{user}-{index}.jpg"
    payload = JPEG + f"smallworlds-restore-drill:{user}:{index}\n".encode()
    boundary = uuid.uuid4().hex
    fields = {
        "deviceAssetId": f"restore-drill-{user}-{index}",
        "deviceId": "smallworlds-restore-drill",
        "fileCreatedAt": f"2026-08-{10 + index:02d}T12:00:00.000Z",
        "fileModifiedAt": f"2026-08-{10 + index:02d}T12:00:00.000Z",
        "isFavorite": "false",
    }
    stream = io.BytesIO()
    for key, value in fields.items():
        stream.write(f"--{boundary}\r\n".encode())
        stream.write(f'Content-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
    stream.write(f"--{boundary}\r\n".encode())
    stream.write(
        f'Content-Disposition: form-data; name="assetData"; filename="{filename}"\r\n'.encode()
    )
    stream.write(b"Content-Type: image/jpeg\r\n\r\n")
    stream.write(payload)
    stream.write(f"\r\n--{boundary}--\r\n".encode())
    _, response, _ = request(
        f"{origin}/api/assets",
        method="POST",
        body=stream.getvalue(),
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": f"multipart/form-data; boundary={boundary}",
        },
        expected=(200, 201),
    )
    result = json.loads(response)
    return {
        "id": str(result["id"]),
        "filename": filename,
        "sha256": hashlib.sha256(payload).hexdigest(),
    }


def seed_immich(args: argparse.Namespace, state: dict) -> None:
    admin_token = immich_login(args.immich_url, args.immich_admin_email, args.immich_admin_password)
    existing = json_request(f"{args.immich_url}/api/admin/users", token=admin_token)
    existing_emails = {
        str(item.get("email")) for item in existing if isinstance(item, dict)
    } if isinstance(existing, list) else set()
    records = []
    for user in USERS:
        email = f"{user}@restore.invalid"
        if email not in existing_emails:
            json_request(
                f"{args.immich_url}/api/admin/users",
                method="POST",
                token=admin_token,
                data={
                    "email": email,
                    "password": args.user_password,
                    "name": user,
                    "shouldChangePassword": False,
                },
                expected=(200, 201),
            )
        token = immich_login(args.immich_url, email, args.user_password)
        assets = [immich_upload(args.immich_url, token, user, index) for index in (1, 2)]
        album = json_request(
            f"{args.immich_url}/api/albums",
            method="POST",
            token=token,
            data={"albumName": f"Restore drill {user}", "assetIds": [item["id"] for item in assets]},
            expected=(200, 201),
        )
        records.append({"username": user, "email": email, "assets": assets, "album_id": album["id"]})
        print(f"Immich: seeded {user} with {len(assets)} originals and one album")
    state["immich"] = records


def verify_immich(args: argparse.Namespace, state: dict) -> None:
    for record in state["immich"]:
        token = immich_login(args.immich_url, record["email"], args.user_password)
        for asset in record["assets"]:
            _, payload, _ = request(
                f"{args.immich_url}/api/assets/{asset['id']}/original",
                headers={"Authorization": f"Bearer {token}"},
            )
            digest = hashlib.sha256(payload).hexdigest()
            if digest != asset["sha256"]:
                raise RuntimeError(f"Immich digest mismatch for {record['username']}/{asset['filename']}")
        albums = json_request(f"{args.immich_url}/api/albums", token=token)
        expected_album = f"Restore drill {record['username']}"
        if not isinstance(albums, list) or not any(
            album.get("albumName") == expected_album for album in albums if isinstance(album, dict)
        ):
            raise RuntimeError(f"Immich album metadata missing for {record['username']}")
        print(f"Immich: verified login, album and {len(record['assets'])} originals for {record['username']}")


def basic_auth(username: str, password: str) -> str:
    return "Basic " + base64.b64encode(f"{username}:{password}".encode()).decode()


def nc_headers(args: argparse.Namespace, username: str, password: str) -> dict[str, str]:
    return {
        "Authorization": basic_auth(username, password),
        "Host": args.nextcloud_host,
        "OCS-APIRequest": "true",
        "X-Forwarded-Proto": "https",
    }


def seed_nextcloud(args: argparse.Namespace, state: dict) -> None:
    admin_headers = nc_headers(args, args.nextcloud_admin_user, args.nextcloud_admin_password)
    records = []
    for user in USERS:
        form = urllib.parse.urlencode({"userid": user, "password": args.user_password}).encode()
        request(
            f"{args.nextcloud_url}/ocs/v1.php/cloud/users",
            method="POST",
            body=form,
            headers={**admin_headers, "Content-Type": "application/x-www-form-urlencoded"},
            expected=(100, 200),
        )
        files = []
        for index in (1, 2):
            filename = f"{user}-{index}.txt"
            payload = f"SmallWorlds central restore drill\nuser={user}\nfile={index}\n".encode()
            path = urllib.parse.quote(f"/remote.php/dav/files/{user}/{filename}")
            request(
                f"{args.nextcloud_url}{path}",
                method="PUT",
                body=payload,
                headers=nc_headers(args, user, args.user_password),
                expected=(201, 204),
            )
            files.append({"filename": filename, "sha256": hashlib.sha256(payload).hexdigest()})
        records.append({"username": user, "files": files})
        print(f"Nextcloud: seeded {user} with {len(files)} files")
    state["nextcloud"] = records


def verify_nextcloud(args: argparse.Namespace, state: dict) -> None:
    for record in state["nextcloud"]:
        for item in record["files"]:
            path = urllib.parse.quote(
                f"/remote.php/dav/files/{record['username']}/{item['filename']}"
            )
            _, payload, _ = request(
                f"{args.nextcloud_url}{path}",
                headers=nc_headers(args, record["username"], args.user_password),
            )
            if hashlib.sha256(payload).hexdigest() != item["sha256"]:
                raise RuntimeError(f"Nextcloud digest mismatch for {record['username']}/{item['filename']}")
        print(f"Nextcloud: verified login and {len(record['files'])} files for {record['username']}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("action", choices=("seed", "verify"))
    parser.add_argument("--state", required=True)
    parser.add_argument("--immich-url", default="http://127.0.0.1:18082")
    parser.add_argument("--immich-admin-email", required=True)
    parser.add_argument("--immich-admin-password", required=True)
    parser.add_argument("--nextcloud-url", default="http://127.0.0.1:18081")
    parser.add_argument("--nextcloud-host", default="files.smallworlds.network")
    parser.add_argument("--nextcloud-admin-user", required=True)
    parser.add_argument("--nextcloud-admin-password", required=True)
    parser.add_argument("--user-password", required=True)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    path = Path(args.state)
    if args.action == "seed":
        state: dict = {"schema": 1, "users": list(USERS)}
        seed_immich(args, state)
        seed_nextcloud(args, state)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(state, indent=2) + "\n")
        path.chmod(0o600)
        print(f"Wrote seed evidence to {path}")
    else:
        state = json.loads(path.read_text())
        verify_immich(args, state)
        verify_nextcloud(args, state)
        print("All three users' Immich and Nextcloud data verified through the applications")
    return 0


if __name__ == "__main__":
    sys.exit(main())
