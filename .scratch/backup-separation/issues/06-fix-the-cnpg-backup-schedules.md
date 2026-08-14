# Fix the CNPG backup schedules

Status: complete

## What to build

Every tenant's `ScheduledBackup` uses a five-field cron. CloudNativePG's
`schedule` field takes a **six**-field expression with seconds first, so
`"0 2 * * *"` does not mean 02:00 daily — it means second 0 of minute 2 of
**every hour**.

Proven on the lab cluster:

```
lastScheduleTime:  2026-08-14T13:02:00Z
nextScheduleTime:  2026-08-14T14:02:00Z     ← one hour, not one day
two completed base backups within ten minutes
259 MB in postgres-backups from an empty database
```

In production that is roughly 24 base backups per cluster per day, ~144 across
six clusters, each retained seven days, on a volume shared with all primary
data. This should land **before** the backup volume is sized (issue 01), or the
sizing will be derived from hourly backups.

CNPG also warns at apply time that `barmanObjectStore` is deprecated and removed
in 1.31.0. Out of scope here — worth its own issue.

## Acceptance criteria

- [x] All five tenant `cnpg-cluster.yaml` files use `"0 0 2 * * *"`.
- [x] Keycloak's uses `"0 0 3 * * *"`.
- [x] Applied on a live cluster, `nextScheduleTime` is ~24 h after
      `lastScheduleTime`, not ~1 h.
      `last=2026-08-14T16:06:34Z next=2026-08-15T02:00:00Z` (was `16:02 → 17:02`).
      Note: CNPG does not recompute `nextScheduleTime` on an in-place edit —
      the ScheduledBackup has to be recreated before the status reflects a
      changed schedule, which is worth knowing when verifying this on a live
      cluster.
- [x] No CNPG admission warning about the number of cron arguments.
- [x] `doc/storage-and-backup.md` §4 rows 1–2 still say "daily" and are now
      true; no doc edit needed, the rows were always correct about intent.
- [x] A comment noting the six-field requirement — added at **all six**
      schedules rather than one, since each file is edited independently and a
      comment in immich would not be seen by someone editing forgejo.
