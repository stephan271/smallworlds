---
status: accepted
---

# Gallery is a variant of the Immich tenant, not a second application

A community that selects Photos chooses which application runs it: Immich, or
Gallery (`github.com/open-noodle/gallery`), a community fork of Immich that adds
an S3 storage backend. Gallery is deployed as a variant of the existing tenant —
`tenants/gallery` is `tenants/immich` plus a handful of patches — and keeps the
`immich` namespace, Application, Secrets, database and `photos.<domain>`
hostname. An overlay selects it by pointing its `immich/` directory at
`tenants/gallery` instead of `tenants/immich`; nothing else in the overlay, the
DNS records or the installer knows there are two.

The reason is the disk. Immich needs a filesystem for its library, which is the
one dataset that outgrows a single-NVMe VPS and the last thing that kept a
second disk necessary after ADR 0053. With Gallery the originals, thumbnails
and transcodes go to the tenant's bucket, so a node with external object
storage holds no bulk data at all.

It is a variant rather than a second tenant because that is what Gallery is: it
keeps Immich's database schema, container layout, Helm chart and `IMMICH_*`
configuration. A second tenant would duplicate the OIDC client, the admin
bootstrap, the domain patches and the DNS entry for one changed image, and
would make "switch photo application" a matter of moving a database between
namespaces.

## What the variant changes

- **Images**: `gallery-server` and `gallery-ml`, pinned to a Gallery release
  (its version numbers are its own; each names the Immich release it is built
  on).
- **Storage**: `IMMICH_STORAGE_BACKEND=s3`, with bucket, region, endpoint and
  key all read from `garage-secret` (ADR 0053). The manifest is therefore the
  same for an in-cluster Garage and an external provider.
- **Serve mode**: `proxy`. `redirect` is faster — browsers fetch from the bucket
  directly — but needs an endpoint reachable from members' browsers and CORS on
  the bucket; an in-cluster Garage has neither and the install configures
  neither at a provider. An operator who has both patches it in the overlay.
- **Recovery Point**: `pv-backup` of the library is removed and replaced by
  `bases/backup-job`, a bucket-to-backup-bucket copy that keeps superseded
  objects under `versions/<date>`. The library PVC stays mounted, because
  uploads are staged there and ffmpeg/exiftool work on local files, but it now
  holds only what is in flight.
- **Pod export**: removed. `export.py` resolves assets to files under the
  library mount, and with S3 the database holds bucket keys instead.

## Consequences

- **A fork is a dependency.** Gallery trails upstream by days and can diverge:
  its releases may migrate the database in ways plain Immich cannot read back.
  Moving between the two is supported by Gallery's own migration tool, in both
  directions, but it is a migration, not a setting. Immich stays the default.
- **The pod archive does not work with Gallery.** The gateway still deploys if
  selected; nothing fills the pods. Both setup routes say so. Reading originals
  from the bucket in the exporter is possible follow-up work.
- **The drilled restore does not cover it.** `restore-drill.sh` and
  `restore-immich-originals.py` assume a library on a PVC. The bucket copy has
  the same shape as Nextcloud's, which is drilled, but Gallery's has not been.
- **Photos at a third party.** Combined with external storage, every member's
  photos sit unencrypted at the provider — the consequence ADR 0053 names, now
  applied to the most personal dataset the cluster has.

## Verification

`admin-tools/test-object-storage.py` renders the variant and asserts it differs
from the Immich tenant only in the ways listed above. Gallery itself has not
been started on a cluster from these manifests: that the chart pinned for
Immich v3.0 runs a Gallery built on Immich v3.2, and that the admin bootstrap
job still fits its API, are unverified.
