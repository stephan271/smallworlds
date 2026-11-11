---
status: accepted
---

# Plane is removed until a replacement is chosen

Plane, the project planning application, is taken out of SmallWorlds entirely:
its Application, its tenant manifests, its e2e spec, its place in the setup
wizard, the installer's app list, the DNS maps and the backup replicator's
bucket list. Planning comes back later as a different application, chosen on
its own merits; nothing here assumes which one.

Three things made it the one to drop rather than to keep and fix.

**It was the most expensive application to keep current.** It ran several
services plus RabbitMQ, Redis and its own database, and its chart restarted
every Plane pod on every render (`timestamp: now`, patched out on Ring 0). Every
release would have had to carry it through Ring 0 (`docs/adr/0060`).

**It was never finished.** Browser uploads needed a public S3 endpoint that was
never built, so attachments only half worked.

**Nobody uses it.** Ring 1 (smallworlds.network) never selected it; only the
Canary Cluster ran it, with no member data in it.

## Consequences

- An overlay that still lists `apps/plane.yaml` or `tenants/plane` cannot
  render once it follows a base without them. The Canary overlay has to drop
  Plane **before** `main` does, because it follows `main`. An overlay pinned to
  an older tag keeps working until its next bump.
- Removing the Application from an overlay deletes Plane's resources through
  Argo CD's finalizer, the database volumes with their CNPG Cluster. Left
  behind, to be removed by hand: the `plane` namespace, RabbitMQ's volume
  claim (a StatefulSet's claims outlive it), the `plane` bucket and the
  `plane-database/` prefix in `postgres-backups` at the S3 provider.
- A `STORED_APP_plane` line in an old installer preference file is ignored.
- `doc/plane-architecture.md` is removed; its lessons (the PreSync migrate Job
  that ran before its ServiceAccount existed) are in git history and are cited
  where the same trap is documented.
- `admin-tools/release-notes.py` keeps the `plane` section name, so the release
  that removes Plane files that removal under Plane rather than Platform.
