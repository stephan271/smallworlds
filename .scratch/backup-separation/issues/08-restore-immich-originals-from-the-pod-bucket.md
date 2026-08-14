# Restore Immich originals from the pod bucket

Status: ready-for-agent

## What to build

Since `docs/adr/0047` removed Immich from `pv-backup`, the pod archive is the only
server-side copy of the pixels — and `doc/storage-and-backup.md` §7 has no
procedure for restoring them. §7.1 covers databases, §7.2 Velero, §7.3 Garage
buckets generically, §7.4 the disaster sequence. Nothing recovers a photo.

A working procedure was written and proven in the drill
(`admin-tools/lab/restore-immich-originals.py`): five originals were deleted from
the library, restored, and Immich served one back byte-identical to the digest
recorded in its manifest entry at export time. It currently reads a local
directory — a member's device copy — which is the wrong default. Once
`pod-gateway` writes to `garage-backup` (issue 04), the normal restore source is
that bucket, on separate storage, with no member involvement at all.

The archive alone is not sufficient and the procedure must be explicit about why:
its keys are human-shaped (`immich/<year>/<date>/<id8>-<originalFileName>`) while
the library is UUID-shaped (`/data/upload/<user>/<xx>/<yy>/<uuid>.jpg`), and only
the Immich database holds the mapping. **Restoring pixels requires a restored
database first.** That is the same dependency Nextcloud has (issue 05) and is
worth stating as a general property rather than a per-app quirk.

## Acceptance criteria

- [ ] The tool gains an S3 mode reading the `pod-gateway` bucket directly, and
      that is the documented default; the local-directory mode remains for the
      disaster path.
- [ ] It verifies each restored object against the manifest digest and reports a
      non-zero exit when any asset is absent from the archive, rather than
      guessing.
- [ ] Its `object_key()` stays byte-identical in behaviour to
      `pod-export/export.py`, with a comment in both saying they must move
      together.
- [ ] It lands in `admin-tools/` proper, not only in the lab harness.
- [ ] `doc/storage-and-backup.md` gains §7.5 with the ordered procedure: restore
      the database, dump the inventory, restore from the pod bucket, verify.
- [ ] §7 states the general rule that PostgreSQL is the index for Immich and
      Nextcloud file restores.
- [ ] Re-verified end to end on the lab cluster after issue 04.
