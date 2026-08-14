# Restore Immich originals from the pod bucket

Status: complete

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

- [x] The tool gains an S3 mode reading the `pod-gateway` bucket directly, and
      that is the documented default; the local-directory mode remains for the
      disaster path.
- [x] It verifies each restored object against the manifest digest and reports a
      non-zero exit when any asset is absent from the archive, rather than
      guessing.
- [x] Its `object_key()` stays byte-identical in behaviour to
      `pod-export/export.py`, with a comment in both saying they must move
      together.
- [x] It lands in `admin-tools/` proper, not only in the lab harness.
- [x] `doc/storage-and-backup.md` gains the ordered procedure: restore the
      database, dump the inventory, restore from the pod bucket, verify.
      Numbered **§7.4**, not §7.5 as this issue said: it belongs with the other
      per-dataset restores rather than after the whole-cluster rebuild, which
      moved to §7.5. That also leaves §7.6 free for issue 09's device path,
      directly after the rebuild sequence it extends.
- [x] §7 states the general rule that PostgreSQL is the index for Immich and
      Nextcloud file restores.
- [x] Re-verified end to end on the lab cluster after issue 04.

## Comments

Verified end to end on the lab, restoring **from the pod bucket** with no member
involved: six originals deleted from the library, all six restored and digest-
verified against their manifest entries, and Immich served one back byte-
identical to the digest recorded at export time.

The safety property was tested rather than assumed. Overwriting one archive
object with junk and re-running produced:

    DIGEST MISMATCH  immich/2025/2025-10-18/0560c5ed-IMG_2000.jpg
      manifest says 0a4258edd451156822d6d13f44661ce761bc2ae1c23ab053f392dc21bd5eec1b
      archive holds 4afb5250616a6389c16fab083efb7885a27e82a9776174fa5f9c32f83c730b63

Nothing was written and the run exited non-zero, so a corrupted archive object
cannot be laundered into the library by a restore.

Two things worth recording:

- The tool reuses the gateway's own `src/s3.py` rather than carrying a second
  SigV4 implementation that could drift from it — it must sign `region=garage`
  or Garage answers 400. `S3Client.get()` returns the raw response and leaves
  closing to the caller, which is easy to get wrong.
- Corrupting that object required **direct S3 access with the owner
  credential**, which bypasses the append-only guarantee entirely. The gateway
  holds owner rights because S3 requires it; anything else holding that
  credential can rewrite history. Noted in §7.4 — restores only ever read.
