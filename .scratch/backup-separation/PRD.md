# Backup/operational storage separation

Status: needs-triage

Decision record: `docs/adr/0048-backup-data-is-physically-separated-from-operational-data.md`.
Evidence: the 2026-08-14 Restore Drill on a purpose-built LAN cluster
(`admin-tools/lab/`, report at
https://claude.ai/code/artifact/7ade0492-7178-4378-acf6-5e02f7a62a5f).

## Problem

Everything stateful shares one 200 GB volume: the Immich library, all six CNPG
data directories, every `local-path` claim, and all of Garage. So hop one of
every backup chain — the thing that is supposed to survive the loss of the
primary data — sits on the same disk as the primary data, at
`replicationFactor: 1`. Nothing enforces a size anywhere, so the nominal ~520 Gi
of claims against a 200 GB volume is workable only by accident, and a single
runaway consumer starves the backups meant to recover from it.

The Restore Drill also found the chain does not behave as documented:

| # | Found | Severity |
|---|---|---|
| 1 | Every CNPG `ScheduledBackup` runs **hourly**, not daily — 5-field cron, CNPG wants 6 | high |
| 2 | The `pod-gateway` bucket **is** replicated offsite, contradicting three documents and ADR 0047 | high |
| 3 | Garage never used its 120 Gi PV — `apps/garage.yaml` sets value keys chart 0.7.1 ignores | medium |
| 4 | rclone-auto-created offsite buckets are **un-versioned**, removing point-in-time recovery | medium |
| 5 | No restore procedure existed for Immich originals after ADR 0047 removed `pv-backup` | medium |

Separately, and not previously recorded: **Nextcloud users' files have no
server-side backup at all.** `pv-backup` covers `/var/www/html` (app code and
`config.php`); the files themselves are primary in the `nextcloud` bucket, whose
only other copy is the offsite mirror.

## Goals

- Backup data and operational data on physically separate block volumes.
- Restores read from `garage-backup`; members' home devices are the disaster
  tier only.
- The offsite leg carries everything **except** `pod-gateway`, and stays on by
  default.
- Every Recovery Point the docs claim exists actually exists, at the claimed
  frequency.

## Non-goals

- **Migration of existing clusters.** Every deployment is currently disposable
  and rebuilt from scratch, so no issue in this feature carries a migration
  path, a compatibility shim, or a backwards-compatible default. Breaking
  changes to bucket names, paths and endpoints are free. Revisit when a cluster
  holds data somebody would miss.
- Multi-node or replicated storage. Both volumes attach to one machine; this
  addresses disk failure and capacity starvation, not node loss.
- Encrypting pods at rest (still open, see `doc/pod-archive.md`).
- Nextcloud files as a pod source — see "Deferred" below.

## Deferred: Nextcloud as a second pod source

Rejected for now as the *backup* mechanism for Nextcloud, because an
append-only store models immutable source data and Nextcloud files are mutable,
renamed, deleted, and shared. Every save would become a permanent new object;
files shared into a member would persist on their hardware after access is
revoked; group folders have no single owner to attribute a pod to.

If pursued later, the shape is an **opt-in, owner-only, single designated
folder** (the member asserts what is source data, as `doc/pod-archive.md`
requires), keyed by real path and filename — never `urn:oid:<fileid>`, which is
meaningless without the Nextcloud database.

## Open questions for the operator

1. **Is device enrolment mandatory?** Pods only cover users in
   `immich-pod-users`. If home devices are the disaster tier, an unenrolled
   user's photos exist nowhere else. Needs a decision and a metric for users
   without a device.
2. **How does data come back from a device?** The agent is deliberately
   pull-only and outbound-only, and the gateway gives devices no write verb.
   Recovering from members' hardware means collecting disks physically or
   building an upload path — and an upload path needs a write-capable
   credential in members' hands, which cuts against the design's spine.
3. **Second volume size.** 100 GB covers today's backups with room for the
   Nextcloud copy; the CNPG cron fix (issue 06) must land first or hourly base
   backups will size it wrongly.

## Issues

| # | Title | Blocked by |
|---|---|---|
| 01 | Provision a separate backup volume | — |
| 02 | Add the garage-backup instance | 01 |
| 03 | Teach the init jobs which Garage instance a bucket belongs to | 02 |
| 04 | Move every backup producer to garage-backup | 03 |
| 05 | Back up Nextcloud's bucket into garage-backup | 03 |
| 06 | Fix the CNPG backup schedules | — |
| 07 | Scope the offsite leg and require versioned buckets | 04 |
| 08 | Restore Immich originals from the pod bucket | 04 |
| 09 | Document disaster recovery from home devices | 08 |
