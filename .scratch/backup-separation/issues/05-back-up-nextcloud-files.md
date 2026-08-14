# Back up Nextcloud's bucket into garage-backup

Status: complete

## What to build

Nextcloud users' files currently have **no server-side backup**. `pv-backup`
covers `/var/www/html`, which is application code and `config.php`; the files
themselves are primary storage in the `nextcloud` Garage bucket at
`replicationFactor: 1`, and their only other copy is the offsite mirror. Every
other tenant has a local Recovery Point; this one does not.

`bases/backup-job` is already the right shape — a per-bucket
`rclone sync "source:${S3_BUCKET}" "dest:${S3_BUCKET}"` CronJob — and currently
has no consumers. Wire it up with `source` = operational Garage and `dest` =
`garage-backup`, and have Nextcloud consume it.

Two things to get right:

**Deletions.** A plain `sync` mirrors them, so a user deleting a file removes it
from the backup on the next run. Use `--backup-dir` with a dated prefix so the
local copy is point-in-time rather than a mirror, matching what the offsite leg
is supposed to provide.

**The objects are opaque.** Nextcloud's S3 primary storage names objects
`urn:oid:<fileid>` — no filenames, no folders, no owners; all of that is in the
Nextcloud database. The bucket copy is therefore only restorable *together with*
that database, and the restore procedure must say so plainly.

The same pattern applies to `forgejo` and `plane` later; do Nextcloud first
because it is the one holding users' documents.

## Acceptance criteria

- [x] `bases/backup-job` is consumed by the nextcloud tenant with
      `S3_BUCKET: nextcloud`, source = operational Garage, dest = `garage-backup`.
- [x] The sync uses `--backup-dir` with a dated prefix; a file deleted in
      Nextcloud remains recoverable from the backup instance afterwards.
- [x] Scheduled off the hour used by other backup jobs, and covered by the
      existing `KubeJobFailed` alerting.
- [x] `doc/storage-and-backup.md` §4 gains a row for it, and §2 states that
      Nextcloud objects are `urn:oid`-named and unrestorable without the
      Nextcloud database.
- [x] `doc/tenant-nextcloud.md` cross-references the restore dependency.
- [x] Verified on the lab cluster, though **not** with a real Nextcloud —
      it is not deployed there. Instead the rendered `nextcloud-files-backup`
      CronJob spec was run verbatim as a one-off Job against the `immich`
      bucket: two objects copied, then one deleted and one edited at the
      source. After the second run `current/documents/photo.txt` held the edit
      and `versions/2026-08-14/documents/report.txt` still returned the deleted
      file's contents. The job logic is proven; the Nextcloud wiring itself
      (bucket name, schedule) is only proven by rendering.

## Comments

Uncovered a real bug in issue 04 while building this: `pv-backup-job` had its
endpoint moved to `garage-backup` but kept authenticating with `garage-secret`,
a key minted on the *operational* instance, writing to a bucket that exists only
there. It would have failed on its first run in production. Missed originally
because neither of its consumers (nextcloud, forgejo) is deployed on the lab.

The fix both issues needed is the same missing piece: **a per-tenant bucket and
credential on the backup instance**. `garage-init-job` gained a third
provisioning step creating `<tenant>` on `garage-backup` with a
`<tenant>-backup-key`, exposed as `garage-secret-backup`. Three keys per tenant
now, each with one job: `garage-secret` (the app's own bucket, operational),
`garage-secret-cnpg` (postgres backups), `garage-secret-backup` (file backups).
`pv-backup-job` was switched to the third.

`bases/backup-job` was rewritten to define both remotes from env like
`pv-backup-job` does, rather than needing a hand-managed `rclone-config-secret`.
