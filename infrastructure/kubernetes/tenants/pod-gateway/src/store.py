"""Append-only pod storage on top of a single Garage bucket.

Layout, all under one bucket:

    <user_id>/objects/<key>              the bytes, written exactly once
    <user_id>/manifest/<seq:012d>.json   one immutable entry per appended object

Every manifest entry carries the hash of its predecessor, so a home device that
has pulled the chain can prove the server never removed, reordered or rewrote
history — the property Garage itself cannot provide, since its keys are
per-bucket read/write/owner with no prefix scoping and ``write`` includes
DeleteObject.

Nothing here ever issues a delete. The gateway holds owner rights on the bucket
because it must, but the code path does not exist.
"""

import hashlib
import json
import tempfile
import threading

from s3 import S3Client, S3Error

GENESIS_HASH = "0" * 64
_SEQ_WIDTH = 12


def canonical(document):
    return json.dumps(document, sort_keys=True, separators=(",", ":"))


def entry_hash(entry):
    body = {k: v for k, v in entry.items() if k != "entry_hash"}
    return hashlib.sha256(canonical(body).encode("utf-8")).hexdigest()


class Conflict(Exception):
    """The object key has already been written; append-only forbids replacing it."""


class TooLarge(Exception):
    pass


class DigestMismatch(Exception):
    pass


def _valid_key(key):
    if not key or key.startswith("/") or len(key) > 900:
        return False
    if any(segment in ("", ".", "..") for segment in key.split("/")):
        return False
    return all(ch.isprintable() and ch not in "\\\x7f" for ch in key)


class PodStore:
    def __init__(self, s3: S3Client, spool_bytes, max_bytes):
        self._s3 = s3
        self._spool_bytes = spool_bytes
        self._max_bytes = max_bytes
        self._locks = {}
        self._locks_guard = threading.Lock()
        self._heads = {}          # user_id -> (seq, entry_hash)
        self.orphans = []         # object keys written but never manifested

    # -- helpers ---------------------------------------------------------

    def _lock_for(self, user_id):
        with self._locks_guard:
            return self._locks.setdefault(user_id, threading.Lock())

    @staticmethod
    def _manifest_key(user_id, seq):
        return f"{user_id}/manifest/{seq:0{_SEQ_WIDTH}d}.json"

    @staticmethod
    def _object_key(user_id, key):
        return f"{user_id}/objects/{key}"

    def _last_manifest_key(self, user_id):
        prefix = f"{user_id}/manifest/"
        last, start = None, None
        while True:
            keys = self._s3.list_keys(prefix, start_after=start, limit=1000)
            if not keys:
                return last
            last, start = keys[-1], keys[-1]
            if len(keys) < 1000:
                return last

    def _head(self, user_id):
        """Return (seq, entry_hash) of the newest manifest entry, or (0, GENESIS)."""
        cached = self._heads.get(user_id)
        if cached is not None:
            return cached
        last = self._last_manifest_key(user_id)
        if last is None:
            head = (0, GENESIS_HASH)
        else:
            with self._s3.get(last) as response:
                entry = json.loads(response.read())
            head = (int(entry["seq"]), entry["entry_hash"])
        self._heads[user_id] = head
        return head

    # -- append ----------------------------------------------------------

    def append(self, user_id, key, stream, content_length, content_type,
               source, declared_sha256=None):
        if not _valid_key(key):
            raise ValueError(f"invalid object key: {key!r}")
        if content_length is not None and content_length > self._max_bytes:
            raise TooLarge()

        object_key = self._object_key(user_id, key)

        with self._lock_for(user_id):
            if self._s3.exists(object_key):
                raise Conflict()

            # Spool first so the digest is known before anything is stored: a
            # corrupted upload must not become an immutable manifest entry.
            with tempfile.SpooledTemporaryFile(max_size=self._spool_bytes) as spool:
                digest = hashlib.sha256()
                size = 0
                while True:
                    chunk = stream.read(1024 * 1024)
                    if not chunk:
                        break
                    size += len(chunk)
                    if size > self._max_bytes:
                        raise TooLarge()
                    digest.update(chunk)
                    spool.write(chunk)
                sha256_hex = digest.hexdigest()
                if declared_sha256 and declared_sha256.lower() != sha256_hex:
                    raise DigestMismatch()
                spool.seek(0)
                self._s3.put(object_key, spool, size, sha256_hex, content_type)

            seq, prev_hash = self._head(user_id)
            entry = {
                "seq": seq + 1,
                "user_id": user_id,
                "key": key,
                "sha256": sha256_hex,
                "size": size,
                "content_type": content_type or "application/octet-stream",
                "source": source or "unknown",
                "created_at": _now(),
                "prev_hash": prev_hash,
            }
            entry["entry_hash"] = entry_hash(entry)

            manifest_key = self._manifest_key(user_id, entry["seq"])
            try:
                body = canonical(entry).encode("utf-8")
                self._s3.put(manifest_key, _Bytes(body), len(body),
                             hashlib.sha256(body).hexdigest(), "application/json")
            except S3Error:
                # The object is stored but unreachable: devices only ever learn
                # of objects through the manifest. Recorded rather than deleted,
                # because this code path must not be able to remove data.
                self.orphans.append(object_key)
                self._heads.pop(user_id, None)
                raise

            self._heads[user_id] = (entry["seq"], entry["entry_hash"])
            return entry

    # -- read (devices) --------------------------------------------------

    def manifest_page(self, user_id, since, limit):
        start_after = self._manifest_key(user_id, since) if since > 0 else None
        keys = self._s3.list_keys(
            f"{user_id}/manifest/", start_after=start_after, limit=limit
        )
        entries = []
        for key in keys:
            with self._s3.get(key) as response:
                entries.append(json.loads(response.read()))
        return entries

    def open_object(self, user_id, key):
        if not _valid_key(key):
            raise ValueError(f"invalid object key: {key!r}")
        return self._s3.get(self._object_key(user_id, key))


class _Bytes:
    """Minimal read()-only wrapper so small bodies share the streaming PUT path."""

    def __init__(self, data):
        self._data = data
        self._offset = 0

    def read(self, size=-1):
        if size is None or size < 0:
            size = len(self._data) - self._offset
        chunk = self._data[self._offset:self._offset + size]
        self._offset += len(chunk)
        return chunk


def _now():
    import datetime
    return datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
