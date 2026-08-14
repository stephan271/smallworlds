"""Bearer-token authentication.

Tokens are never stored, only their SHA-256 digests, in a JSON document mounted
from a Secret:

    {
      "agents":  {"<sha256>": {"name": "immich-exporter", "sources": ["immich"]}},
      "devices": {"<sha256>": {"name": "alice-pi", "user_id": "<immich user id>"}}
    }

An agent may only append. A device may only read, and only its own user's pod.
There is deliberately no principal that can do both.
"""

import hashlib
import json
import os
import threading


class Principal:
    def __init__(self, kind, name, user_id=None, sources=None):
        self.kind = kind          # "agent" | "device"
        self.name = name
        self.user_id = user_id    # devices only: the single pod they may read
        self.sources = sources or []

    @property
    def may_append(self):
        return self.kind == "agent"

    @property
    def may_read(self):
        return self.kind == "device"

    def may_access(self, user_id):
        if self.kind == "device":
            return self.user_id == user_id
        return True


class TokenStore:
    """Reloads the token file whenever the Secret projection changes on disk."""

    def __init__(self, path):
        self._path = path
        self._lock = threading.Lock()
        self._stamp = None
        self._agents = {}
        self._devices = {}
        self.reload()

    def reload(self):
        try:
            stamp = os.stat(self._path).st_mtime_ns
        except FileNotFoundError:
            with self._lock:
                self._agents, self._devices, self._stamp = {}, {}, None
            return
        if stamp == self._stamp:
            return
        with open(self._path, "r", encoding="utf-8") as handle:
            document = json.load(handle)
        with self._lock:
            self._agents = document.get("agents", {})
            self._devices = document.get("devices", {})
            self._stamp = stamp

    def resolve(self, token):
        """Return a Principal for a presented bearer token, or None."""
        if not token:
            return None
        self.reload()
        digest = hashlib.sha256(token.encode("utf-8")).hexdigest()
        with self._lock:
            agent = self._agents.get(digest)
            device = self._devices.get(digest)
        if agent:
            return Principal("agent", agent.get("name", "agent"),
                             sources=agent.get("sources"))
        if device and device.get("user_id"):
            return Principal("device", device.get("name", "device"),
                             user_id=device["user_id"])
        return None


    def devices(self):
        """(name, user_id) for every enrolled device.

        Enrolment is mandatory, so monitoring has to be able to name a device
        that has NEVER reported — including one whose last heartbeat predates a
        gateway restart, since the heartbeat table is in-process. Without this
        a silently dead device simply vanishes from the metrics instead of
        alerting.
        """
        self.reload()
        with self._lock:
            return sorted(
                (entry.get("name", "device"), entry["user_id"])
                for entry in self._devices.values()
                if entry.get("user_id")
            )


def bearer_token(header_value):
    if not header_value:
        return None
    parts = header_value.split(None, 1)
    if len(parts) != 2 or parts[0].lower() != "bearer":
        return None
    return parts[1].strip()
