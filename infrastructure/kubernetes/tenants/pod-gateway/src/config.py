"""Gateway configuration, read from the environment at import time."""

import os


def _required(name):
    value = os.environ.get(name)
    if not value:
        raise RuntimeError(f"{name} must be set")
    return value


LISTEN_PORT = int(os.environ.get("LISTEN_PORT", "8080"))

S3_ENDPOINT = os.environ.get(
    "S3_ENDPOINT", "http://garage.garage-system.svc.cluster.local:3900"
)
# Garage 400s on any other region — see doc/storage-and-backup.md.
S3_REGION = os.environ.get("S3_REGION", "garage")
S3_BUCKET = os.environ.get("S3_BUCKET", "pod-gateway")
S3_ACCESS_KEY = _required("S3_ACCESS_KEY")
S3_SECRET_KEY = _required("S3_SECRET_KEY")

TOKENS_PATH = os.environ.get("TOKENS_PATH", "/etc/pod-gateway/tokens.json")

# Objects are spooled through memory/disk so their digest can be verified before
# anything is written to Garage. Raise MAX_OBJECT_BYTES only alongside the
# gateway's ephemeral-storage limit.
SPOOL_MEMORY_BYTES = int(os.environ.get("SPOOL_MEMORY_BYTES", str(32 * 1024 * 1024)))
MAX_OBJECT_BYTES = int(os.environ.get("MAX_OBJECT_BYTES", str(4 * 1024 * 1024 * 1024)))

MANIFEST_PAGE_DEFAULT = int(os.environ.get("MANIFEST_PAGE_DEFAULT", "200"))
MANIFEST_PAGE_MAX = int(os.environ.get("MANIFEST_PAGE_MAX", "1000"))
