"""Append-only pod archive gateway.

Agents may create objects and nothing else; devices may read exactly one user's
pod and nothing else. The verb table is the whole security model, so it is kept
in one place, in `_dispatch`.
"""

import json
import threading
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import config
from auth import TokenStore, bearer_token
from s3 import S3Client, S3Error
from store import Conflict, DigestMismatch, PodStore, TooLarge

STARTED_AT = time.time()

_metrics_lock = threading.Lock()
_appends = {}          # source -> count
_append_bytes = 0
_conflicts = 0
_denied = 0
_heartbeats = {}       # (device, user_id) -> dict


class _LimitedReader:
    """Reads at most ``remaining`` bytes from the request body."""

    def __init__(self, stream, remaining):
        self._stream = stream
        self._remaining = remaining

    def read(self, size=-1):
        if self._remaining <= 0:
            return b""
        if size is None or size < 0:
            size = self._remaining
        chunk = self._stream.read(min(size, self._remaining))
        self._remaining -= len(chunk)
        return chunk


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    server_version = "pod-gateway"

    # -- plumbing --------------------------------------------------------

    def log_message(self, fmt, *args):
        print(f"{self.address_string()} {fmt % args}", flush=True)

    def _send_json(self, status, document):
        body = json.dumps(document).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _error(self, status, message):
        global _denied
        if status in (401, 403):
            with _metrics_lock:
                _denied += 1
        self._send_json(status, {"error": message})

    def _principal(self):
        return self.server.tokens.resolve(
            bearer_token(self.headers.get("Authorization"))
        )

    # -- routing ---------------------------------------------------------

    def do_GET(self):
        self._dispatch("GET")

    def do_PUT(self):
        self._dispatch("PUT")

    def do_POST(self):
        self._dispatch("POST")

    def _dispatch(self, method):
        path, _, raw_query = self.path.partition("?")
        parts = [urllib.parse.unquote(p) for p in path.split("/")]

        if method == "GET" and path == "/healthz":
            return self._send_json(200, {"status": "ok"})
        if method == "GET" and path == "/metrics":
            return self._metrics()

        # /pod/v1/<user_id>/<verb>[/<key...>]
        if len(parts) < 5 or parts[1] != "pod" or parts[2] != "v1":
            return self._error(404, "not found")
        user_id, verb = parts[3], parts[4]
        key = "/".join(parts[5:])

        principal = self._principal()
        if principal is None:
            return self._error(401, "invalid or missing bearer token")
        if not principal.may_access(user_id):
            return self._error(403, "token is not scoped to this pod")

        if method == "PUT" and verb == "objects":
            if not principal.may_append:
                return self._error(403, "this token may not append")
            return self._append(principal, user_id, key)

        if method == "GET" and verb == "manifest":
            if not principal.may_read:
                return self._error(403, "this token may not read")
            return self._manifest(user_id, raw_query)

        if method == "GET" and verb == "objects":
            if not principal.may_read:
                return self._error(403, "this token may not read")
            return self._read_object(user_id, key)

        if method == "POST" and verb == "heartbeat":
            if not principal.may_read:
                return self._error(403, "this token may not report")
            return self._heartbeat(principal, user_id)

        return self._error(404, "not found")

    # -- handlers --------------------------------------------------------

    def _append(self, principal, user_id, key):
        try:
            length = int(self.headers.get("Content-Length", ""))
        except ValueError:
            return self._error(411, "Content-Length is required")
        if length > config.MAX_OBJECT_BYTES:
            return self._error(413, "object exceeds MAX_OBJECT_BYTES")

        source = self.headers.get("X-Pod-Source") or (
            principal.sources[0] if principal.sources else "unknown"
        )
        if principal.sources and source not in principal.sources:
            return self._error(403, f"token may not append source {source!r}")

        try:
            entry = self.server.store.append(
                user_id,
                key,
                _LimitedReader(self.rfile, length),
                length,
                self.headers.get("Content-Type"),
                source,
                self.headers.get("X-Pod-Sha256"),
            )
        except Conflict:
            global _conflicts
            with _metrics_lock:
                _conflicts += 1
            # Expected and harmless: exporters re-offer known assets, and this
            # 409 is what makes the export idempotent without a read grant.
            return self._error(409, "object already exists; pods are append-only")
        except DigestMismatch:
            return self._error(400, "body does not match X-Pod-Sha256")
        except TooLarge:
            return self._error(413, "object exceeds MAX_OBJECT_BYTES")
        except ValueError as exc:
            return self._error(400, str(exc))
        except S3Error as exc:
            self.log_message("append failed: %s", exc)
            return self._error(502, "storage backend error")

        global _append_bytes
        with _metrics_lock:
            _appends[entry["source"]] = _appends.get(entry["source"], 0) + 1
            _append_bytes += entry["size"]
        return self._send_json(201, entry)

    def _manifest(self, user_id, raw_query):
        query = urllib.parse.parse_qs(raw_query)
        try:
            since = int(query.get("since", ["0"])[0])
            limit = int(query.get("limit", [str(config.MANIFEST_PAGE_DEFAULT)])[0])
        except ValueError:
            return self._error(400, "since and limit must be integers")
        limit = max(1, min(limit, config.MANIFEST_PAGE_MAX))

        try:
            entries = self.server.store.manifest_page(user_id, max(0, since), limit)
        except S3Error as exc:
            self.log_message("manifest failed: %s", exc)
            return self._error(502, "storage backend error")
        next_since = entries[-1]["seq"] if entries else since
        return self._send_json(200, {"entries": entries, "next": next_since})

    def _read_object(self, user_id, key):
        try:
            response = self.server.store.open_object(user_id, key)
        except ValueError as exc:
            return self._error(400, str(exc))
        except S3Error as exc:
            if exc.status == 404:
                return self._error(404, "no such object")
            self.log_message("read failed: %s", exc)
            return self._error(502, "storage backend error")

        with response:
            length = response.headers.get("Content-Length")
            self.send_response(200)
            self.send_header(
                "Content-Type",
                response.headers.get("Content-Type", "application/octet-stream"),
            )
            if length:
                self.send_header("Content-Length", length)
            self.end_headers()
            while True:
                chunk = response.read(1024 * 1024)
                if not chunk:
                    break
                self.wfile.write(chunk)

    def _heartbeat(self, principal, user_id):
        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length) or b"{}")
        except (ValueError, json.JSONDecodeError):
            return self._error(400, "body must be JSON")
        with _metrics_lock:
            _heartbeats[(principal.name, user_id)] = {
                "at": time.time(),
                "last_seq": int(payload.get("last_seq", 0) or 0),
                "free_bytes": int(payload.get("free_bytes", 0) or 0),
                "objects": int(payload.get("objects", 0) or 0),
            }
        return self._send_json(200, {"status": "ok"})

    # -- metrics ---------------------------------------------------------

    def _metrics(self):
        lines = [
            "# HELP pod_gateway_up Gateway uptime in seconds.",
            "# TYPE pod_gateway_up gauge",
            f"pod_gateway_up {time.time() - STARTED_AT:.0f}",
            "# HELP pod_gateway_appends_total Objects appended, by source app.",
            "# TYPE pod_gateway_appends_total counter",
        ]
        with _metrics_lock:
            for source, count in sorted(_appends.items()):
                lines.append(f'pod_gateway_appends_total{{source="{source}"}} {count}')
            lines += [
                "# HELP pod_gateway_append_bytes_total Bytes appended.",
                "# TYPE pod_gateway_append_bytes_total counter",
                f"pod_gateway_append_bytes_total {_append_bytes}",
                "# HELP pod_gateway_conflicts_total Rejected re-writes (expected).",
                "# TYPE pod_gateway_conflicts_total counter",
                f"pod_gateway_conflicts_total {_conflicts}",
                "# HELP pod_gateway_denied_total Rejected unauthorised requests.",
                "# TYPE pod_gateway_denied_total counter",
                f"pod_gateway_denied_total {_denied}",
                "# HELP pod_gateway_orphan_objects Objects stored without a manifest entry.",
                "# TYPE pod_gateway_orphan_objects gauge",
                f"pod_gateway_orphan_objects {len(self.server.store.orphans)}",
                "# HELP pod_gateway_device_heartbeat_age_seconds Age of the last device heartbeat.",
                "# TYPE pod_gateway_device_heartbeat_age_seconds gauge",
            ]
            now = time.time()
            for (device, user_id), state in sorted(_heartbeats.items()):
                labels = f'device="{device}",user_id="{user_id}"'
                lines.append(
                    f"pod_gateway_device_heartbeat_age_seconds{{{labels}}} "
                    f"{now - state['at']:.0f}"
                )
                lines.append(f"pod_gateway_device_last_seq{{{labels}}} {state['last_seq']}")
                lines.append(
                    f"pod_gateway_device_free_bytes{{{labels}}} {state['free_bytes']}"
                )

        body = ("\n".join(lines) + "\n").encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; version=0.0.4")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    s3 = S3Client(
        config.S3_ENDPOINT,
        config.S3_REGION,
        config.S3_BUCKET,
        config.S3_ACCESS_KEY,
        config.S3_SECRET_KEY,
    )
    server = ThreadingHTTPServer(("", config.LISTEN_PORT), Handler)
    server.store = PodStore(s3, config.SPOOL_MEMORY_BYTES, config.MAX_OBJECT_BYTES)
    server.tokens = TokenStore(config.TOKENS_PATH)
    print(
        f"pod-gateway listening on :{config.LISTEN_PORT}, "
        f"bucket={config.S3_BUCKET} region={config.S3_REGION}",
        flush=True,
    )
    server.serve_forever()


if __name__ == "__main__":
    main()
