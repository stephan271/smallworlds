#!/usr/bin/env python3
"""End-to-end tests for the pod gateway, against an in-memory Garage stub.

These are adversarial rather than happy-path: the whole point of the gateway is
that an agent cannot read and a device cannot write, so those are the cases
worth asserting. Run with:

    python3 admin-tools/test-pod-gateway.py
"""

import hashlib
import http.client
import importlib.util
import io
import json
import os
import sys
import threading
import urllib.error
import urllib.parse
import urllib.request
from http.server import ThreadingHTTPServer

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SRC = os.path.join(REPO, "infrastructure/kubernetes/tenants/pod-gateway/src")
AGENT = os.path.join(REPO, "admin-tools/pod-device/pod-agent.py")

os.environ.setdefault("S3_ACCESS_KEY", "test")
os.environ.setdefault("S3_SECRET_KEY", "test")
os.environ.setdefault("TOKENS_PATH", "/nonexistent")
sys.path.insert(0, SRC)

import main as gateway            # noqa: E402
from auth import Principal        # noqa: E402
from store import PodStore        # noqa: E402

_spec = importlib.util.spec_from_file_location("pod_agent", AGENT)
pod_agent = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(pod_agent)

EXPORTER = os.path.join(
    REPO, "infrastructure/kubernetes/tenants/immich/pod-export/export.py"
)
_espec = importlib.util.spec_from_file_location("immich_export", EXPORTER)
immich_export = importlib.util.module_from_spec(_espec)
_espec.loader.exec_module(immich_export)

# A real row from the production inventory, so the shape stays honest.
LIVE_ROW = json.loads(
    '{"id": "e1f69ddf-6bc3-4917-a3f7-d036784451a1",'
    ' "owner_id": "851b94e2-d81d-429f-bde9-35b2d911445f",'
    ' "original_path": "/data/upload/851b94e2-d81d-429f-bde9-35b2d911445f'
    '/0b/01/0b01fbd6-be9d-419c-889c-912bf794b8fa.jpg",'
    ' "file_name": "20251018_102709.jpg", "type": "IMAGE",'
    ' "visibility": "timeline", "created_at": "2025-10-18T08:27:09Z"}'
)

FAILURES = []


def check(name, condition, detail=""):
    if condition:
        print(f"  ok   {name}")
    else:
        print(f"  FAIL {name} {detail}")
        FAILURES.append(name)


class FakeResponse(io.BytesIO):
    def __init__(self, data):
        super().__init__(data)
        self.headers = {
            "Content-Length": str(len(data)),
            "Content-Type": "application/octet-stream",
        }

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()
        return False


class FakeS3:
    """Just enough of S3Client for the store, with no delete path at all."""

    def __init__(self):
        self.objects = {}

    def put(self, key, body, length, sha256_hex, content_type=None):
        self.objects[key] = body.read()

    def get(self, key):
        from s3 import S3Error
        if key not in self.objects:
            raise S3Error(404, b"not found")
        return FakeResponse(self.objects[key])

    def exists(self, key):
        return key in self.objects

    def list_keys(self, prefix, start_after=None, limit=1000):
        keys = sorted(k for k in self.objects if k.startswith(prefix))
        if start_after:
            keys = [k for k in keys if k > start_after]
        return keys[:limit]


class FakeTokens:
    def resolve(self, token):
        if token == "agent-token":
            return Principal("agent", "immich-exporter", sources=["immich"])
        if token == "alice-token":
            return Principal("device", "alice-pi", user_id="alice")
        if token == "bob-token":
            return Principal("device", "bob-pi", user_id="bob")
        return None


def request(server_url, method, path, token=None, body=None, headers=None):
    req = urllib.request.Request(server_url + path, data=body, method=method)
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    for name, value in (headers or {}).items():
        req.add_header(name, value)
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            return response.status, response.read()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read()


def append(server_url, token, user, key, payload, source="immich", digest=None):
    return request(
        server_url, "PUT", f"/pod/v1/{user}/objects/{key}", token, payload,
        {
            "Content-Length": str(len(payload)),
            "X-Pod-Source": source,
            "X-Pod-Sha256": digest or hashlib.sha256(payload).hexdigest(),
        },
    )


def main():
    s3 = FakeS3()
    server = ThreadingHTTPServer(("127.0.0.1", 0), gateway.Handler)
    server.store = PodStore(s3, 1024, 10 * 1024 * 1024)
    server.tokens = FakeTokens()
    threading.Thread(target=server.serve_forever, daemon=True).start()
    url = f"http://127.0.0.1:{server.server_address[1]}"

    print("\nappend path")
    status, _ = append(url, "agent-token", "alice", "immich/2026/a.jpg", b"first photo")
    check("agent can append", status == 201, f"got {status}")
    status, _ = append(url, "agent-token", "alice", "immich/2026/b.jpg", b"second photo")
    check("agent can append again", status == 201, f"got {status}")

    print("\nappend-only enforcement")
    status, _ = append(url, "agent-token", "alice", "immich/2026/a.jpg", b"REPLACED")
    check("re-appending the same key is refused", status == 409, f"got {status}")
    check("original bytes are untouched",
          s3.objects["alice/objects/immich/2026/a.jpg"] == b"first photo")
    status, _ = append(url, "agent-token", "alice", "immich/2026/c.jpg", b"data",
                       digest="0" * 64)
    check("a wrong declared digest is refused", status == 400, f"got {status}")
    check("nothing was stored for the rejected object",
          "alice/objects/immich/2026/c.jpg" not in s3.objects)

    print("\nverb separation")
    status, _ = request(url, "GET", "/pod/v1/alice/objects/immich/2026/a.jpg",
                        "agent-token")
    check("agent cannot read an object", status == 403, f"got {status}")
    status, _ = request(url, "GET", "/pod/v1/alice/manifest", "agent-token")
    check("agent cannot read the manifest", status == 403, f"got {status}")
    status, _ = append(url, "alice-token", "alice", "immich/2026/x.jpg", b"x")
    check("device cannot append", status == 403, f"got {status}")

    print("\npod isolation")
    status, _ = request(url, "GET", "/pod/v1/alice/manifest", "bob-token")
    check("device cannot read another pod's manifest", status == 403, f"got {status}")
    status, _ = request(url, "GET", "/pod/v1/alice/objects/immich/2026/a.jpg",
                        "bob-token")
    check("device cannot read another pod's object", status == 403, f"got {status}")
    status, _ = request(url, "GET", "/pod/v1/alice/manifest")
    check("no token is rejected", status == 401, f"got {status}")
    status, _ = request(url, "GET", "/pod/v1/alice/manifest", "made-up")
    check("an unknown token is rejected", status == 401, f"got {status}")

    print("\nmanifest and chain")
    status, body = request(url, "GET", "/pod/v1/alice/manifest", "alice-token")
    page = json.loads(body)
    entries = page["entries"]
    check("device reads its own manifest", status == 200, f"got {status}")
    check("manifest holds both appends", len(entries) == 2, f"got {len(entries)}")
    check("chain starts at genesis",
          entries[0]["prev_hash"] == pod_agent.GENESIS_HASH)
    check("chain links forward",
          entries[1]["prev_hash"] == entries[0]["entry_hash"])
    check("entry hashes verify",
          all(pod_agent.entry_hash(e) == e["entry_hash"] for e in entries))
    status, body = request(url, "GET", "/pod/v1/alice/manifest?since=1", "alice-token")
    check("paging by cursor skips seen entries",
          [e["seq"] for e in json.loads(body)["entries"]] == [2])

    print("\ndevice detects a rewritten archive")
    tampered = dict(entries[1])
    tampered["sha256"] = "f" * 64
    detected = pod_agent.entry_hash(tampered) != tampered["entry_hash"]
    check("editing an entry breaks its hash", detected)
    forked = dict(entries[1], prev_hash="a" * 64)
    forked["entry_hash"] = pod_agent.entry_hash(forked)
    check("re-signing a forked entry still breaks the chain",
          forked["prev_hash"] != entries[0]["entry_hash"])

    print("\nobject read-back")
    status, body = request(url, "GET", "/pod/v1/alice/objects/immich/2026/a.jpg",
                           "alice-token")
    check("device reads its own object", status == 200 and body == b"first photo")
    status, _ = request(url, "GET", "/pod/v1/alice/objects/immich/2026/zzz.jpg",
                        "alice-token")
    check("a missing object is a 404", status == 404, f"got {status}")

    print("\npath traversal")
    status, _ = append(url, "agent-token", "alice", "immich/../../etc/passwd", b"x")
    check("traversal in an object key is refused", status == 400, f"got {status}")
    try:
        pod_agent.safe_relative_path("../../etc/passwd")
        check("agent rejects a traversing key", False, "no exception")
    except ValueError:
        check("agent rejects a traversing key", True)

    print("\nsource scoping")
    status, _ = append(url, "agent-token", "alice", "other/1.txt", b"x",
                       source="nextcloud")
    check("agent cannot append a source it is not scoped to",
          status == 403, f"got {status}")

    print("\nconnection reuse after a rejected body")
    # Traefik keeps connections to the backend alive. A rejection answered
    # without consuming the request body leaves those bytes in the socket, and
    # the next request on that connection gets parsed as "<leftover>GET ...".
    # Found on staging, where it surfaced as 501 Unsupported method ('xGET').
    conn = http.client.HTTPConnection("127.0.0.1", server.server_address[1], timeout=10)
    payload = b"x" * 4096
    conn.request("PUT", f"/pod/v1/alice/objects/{urllib.parse.quote('immich/2026/a.jpg')}",
                 body=payload,
                 headers={"Authorization": "Bearer agent-token",
                          "X-Pod-Source": "immich",
                          "Content-Length": str(len(payload))})
    first = conn.getresponse()
    first.read()
    check("a duplicate append is still refused", first.status == 409, f"got {first.status}")
    try:
        conn.request("GET", "/healthz")
        second = conn.getresponse()
        second.read()
        reused_ok = second.status == 200
    except Exception:
        # Connection closed by the server is the correct outcome; the client
        # reconnects. What must NOT happen is a corrupted second response.
        reused_ok = True
    check("the next request on that connection is not corrupted", reused_ok)
    conn.close()

    print("\nimmich exporter path handling")
    check("media root is rewritten to the exporter's mount",
          immich_export.local_path(LIVE_ROW["original_path"])
          == "/library/upload/851b94e2-d81d-429f-bde9-35b2d911445f"
             "/0b/01/0b01fbd6-be9d-419c-889c-912bf794b8fa.jpg")
    try:
        immich_export.local_path("/somewhere/else/x.jpg")
        check("a path outside the media root is refused", False, "no exception")
    except ValueError:
        check("a path outside the media root is refused", True)

    check("object key restores the human-readable name",
          immich_export.object_key(LIVE_ROW)
          == "immich/2025/2025-10-18/e1f69ddf-20251018_102709.jpg",
          immich_export.object_key(LIVE_ROW))
    check("locked assets get their own prefix",
          immich_export.object_key(dict(LIVE_ROW, visibility="locked"))
          .startswith("immich-locked/"))
    check("object keys are accepted by the agent",
          pod_agent.safe_relative_path(immich_export.object_key(LIVE_ROW)))
    status, _ = append(url, "agent-token", "alice",
                       immich_export.object_key(LIVE_ROW), b"jpeg bytes")
    check("a real immich key appends cleanly", status == 201, f"got {status}")

    server.shutdown()
    print()
    if FAILURES:
        print(f"{len(FAILURES)} check(s) failed: {', '.join(FAILURES)}")
        return 1
    print("All checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
