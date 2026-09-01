# Immich + Nextcloud Restore Drill — 2026-09-01

Result: **PASS**

## Scope

- Fresh disposable Hetzner node `cc-staging-node-01`
- Distinct operational (`106761932`) and backup (`106761931`) volumes
- Three users: `drill-alice`, `drill-bob`, `drill-carol`
- Central `garage-backup` Recovery Points only; no personal pod archive data

Each user received two unique Immich JPEGs, one Immich album, and two unique
Nextcloud text files. SHA-256 digests were recorded before loss.

## Destructive proof

After explicit CloudNativePG, PVC/object, and Velero backups completed, the
drill stopped both applications and removed:

- both PostgreSQL clusters and their PVCs;
- both runtime `database-app` Secrets;
- the complete Immich library;
- the complete Nextcloud HTML PVC; and
- every object in Nextcloud's operational bucket.

The Secrets were restored through Velero, both databases through the Barman
Cloud plugin, the Immich and Nextcloud PVCs from their central mirrors, and the
Nextcloud user objects from `nextcloud/current`.

## Verification

- All three Immich users logged in; six originals matched their recorded
  SHA-256 digests and all three albums were present.
- All three Nextcloud users logged in; all six WebDAV files matched their
  recorded SHA-256 digests.
- WAL archiving was re-enabled after recovery, both database instances
  restarted healthy, and a new `post-restore-20260901t082243z` base backup
  completed in each namespace.
- Application verification passed again after those database restarts.

The Nextcloud PVC contained 795.080 MiB across 28,056 files. Its first backup
took 8m35s and restore took 4m45s. After adding `--fast-list`, 16 transfers and
32 checkers, a steady-state run transferred 927.607 KiB of runtime changes and
took 2m47s; most remaining time was object enumeration.

## Findings fixed during the drill

1. A recovery Cluster must omit its normal WAL-archiver `spec.plugins` block
   during bootstrap. Pointing an archiver at the old non-empty `serverName`
   makes the Barman plugin reject recovery. The automation now adds it only
   after the recovery Cluster is Ready and proves it with a new base backup.
2. `rclone`'s S3 object format preserves file content and modification times,
   but not Unix uid/gid/mode. Nextcloud returned 503 after a byte-complete
   restore until `config.php` ownership and the data-directory permissions were
   repaired. The restore job now does that before application startup.

Terraform destroyed the VM, both volumes, their attachments, the firewall, and
the ephemeral cloud SSH key after verification (seven resources destroyed).
