# Move every backup producer to garage-backup

Status: complete

## What to build

Repoint everything that writes a Recovery Point at the new instance. The
producers are the only things that change; the three application buckets stay
where they are.

| Producer | Currently writes to | Must write to |
|---|---|---|
| CNPG `barmanObjectStore` × 5 tenants | `garage.garage-system:3900` | `garage-backup` |
| CNPG Keycloak (`postgres-backups-keycloak`) | same | `garage-backup` |
| Velero (`velero-backups`) | same | `garage-backup` |
| `bases/pv-backup-job` (forgejo, nextcloud) | tenant bucket, `pv-backup/` prefix | `garage-backup` |
| `pod-gateway` (`S3_ENDPOINT`) | same | `garage-backup` |

Moving `pod-gateway` is what turns the pod bucket into a genuinely separate copy
of the Immich pixels and lets restores read it instead of a member's hardware
(issue 08). It also means the gateway's `409`-on-existing-key guarantee now
protects data on storage the operational cluster cannot fill.

No migration is needed: every deployment is rebuilt from scratch, so the new
endpoints simply take effect on the next build and no barman history has to be
carried across. Do not add compatibility handling for clusters archiving to the
old location.

## Acceptance criteria

- [x] All six `cnpg-cluster.yaml` files read `endpointURL` from
      `garage-secret-cnpg` (or the backup Service) rather than the operational
      Service.
- [x] Velero's `BackupStorageLocation` targets `garage-backup`; deployed on the
      lab and reported `Available` against it.
- [x] `bases/pv-backup-job` writes to `garage-backup`, and its consumers
      (forgejo, nextcloud) still land under a per-tenant prefix.
- [x] `pod-gateway-config.yaml` `S3_ENDPOINT` targets `garage-backup`, and the
      gateway's `garage-secret` is provisioned there.
- [x] After a sync on the lab cluster, `/mnt/smallworlds-backup` contains
      barman objects (`immich-database/base/20260814T165138/`, `wals/`) and all
      20 pod objects + 20 manifest entries. A device pull against the relocated
      archive still verifies clean, so the move is transparent to members.
- [x] Velero objects on `/mnt/smallworlds-backup`: a 163-item backup of the
      `immich` and `pod-gateway` namespaces completed with no errors and landed
      in `velero-backups/backups/split-verify/` on the backup instance. The
      bucket exists **only** there — the operational instance never had one.
- [x] `doc/storage-and-backup.md` §4 table updated with the new destinations.

## Comments

Deployed and verified on the lab cluster. All three producers confirmed writing
to the separate device:

| Producer | Evidence |
|---|---|
| CNPG barman | `postgres-backups/immich-database/base/20260814T165138/` + `wals/` |
| pod-gateway | 20 objects + 20 manifest entries; a device pull still verifies clean |
| Velero | `velero-backups/backups/split-verify/` (163 items, 0 errors) |

Backup volume data dir grew to 53,623,405 bytes on `/dev/sda1`; the operational
Garage dir on the NVMe holds 178,494,360 bytes, all of it pre-split residue.

Two things worth knowing for anyone repeating this:

- **`kubectl get backup` is ambiguous once both operators are installed.** CNPG
  and Velero both register the `backups` short name, and the CNPG CRD wins, so
  a Velero backup looks like it never appeared. Use `backups.velero.io`.
- **Velero's `Schedule` uses standard five-field cron**, unlike CNPG's
  `ScheduledBackup` (issue 06). `"0 2 * * *"` in `tenants/velero` is correct and
  must not be "fixed" to six fields.
- Applying the tenant on a fresh cluster needs two passes: the CRDs ship in the
  same manifest set as the `BackupStorageLocation` and `Schedule` that depend on
  them. This is the deferred-CRD behaviour `test-pr-locally.sh` already tolerates.
