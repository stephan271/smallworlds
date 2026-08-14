# Scope the offsite leg and require versioned buckets

Status: complete

## What to build

Two defects in the offsite leg, both proven in the drill.

**It replicates the pod archive.** `docs/adr/0047`, `doc/pod-archive.md` and
`doc/storage-and-backup.md` §4/§7 all state the `pod-gateway` bucket stays on the
cluster. The CronJob runs `rclone sync source: dest:` across the whole instance,
and `backup-replicator/README.md` §2 tells the operator to grant the replicator
key read on *every* bucket in a loop — so following the documented procedure
ships every member's pod, unencrypted, to a third-party provider, including the
`immich-locked/` prefix holding their PIN-protected photos.

Replace the whole-instance sync with an explicit bucket list, and narrow the key
grants to match. Two independent controls, so a mistake in either one alone
cannot leak pods.

**Auto-created destination buckets have no versioning.** The README offers
letting rclone create them on first sync. Buckets made that way are un-versioned,
which silently removes the point-in-time property that §3 says makes the offsite
leg a backup rather than a mirror. Proven: created a file, synced, deleted it
cluster-side, re-synced, and it was gone offsite; `mc version info` reported the
bucket un-versioned.

The offsite leg stays **enabled by default**. Both volumes attach to one machine,
so the separation in this feature does not address node loss, compromise, or
provider failure — and the offsite database copy is what indexes nearly every
other file backup.

## Acceptance criteria

- [x] `cronjob.yaml` syncs an explicit list — `postgres-backups`,
      `postgres-backups-keycloak`, `velero-backups`, the `pv-backup` prefixes,
      `nextcloud`, `forgejo`, `plane` — and never `pod-gateway`.
- [x] The job fails loudly if a configured bucket is missing, rather than
      silently syncing nothing.
- [x] `README.md` §2 grants read on exactly those buckets and states that
      `pod-gateway` must be excluded, with the reason.
- [x] `README.md` §1 requires pre-created **versioned** buckets and drops the
      auto-creation option.
- [x] A verification step is added that checks versioning is on before the first
      sync is trusted.
- [x] `doc/storage-and-backup.md` §4 row 5 and the §7 callout match the
      implementation.
- [x] Verified on the lab cluster against the MinIO stand-in: the scoped run
      processed only `postgres-backups`, `velero-backups` and `immich`; no pod
      objects were copied. The guard was then exercised by granting the key
      `pod-gateway` read exactly as the old README instructed — the job refused
      and exited non-zero without transferring anything. Revoking the grant
      returned it to healthy.

## Comments

The source is now `garage-backup` alone. After the split every Recovery Point is
collected there, so the operational instance never has to be read — which also
means a credential mistake cannot reach live application buckets.

**Deploying this fix does not undo the leak.** The lab's offsite target still
held 50 pod objects from the pre-fix whole-instance sync, four hours older than
anything the scoped run wrote. The new configuration simply stops adding to
them. Any community that ran the previous replicator has members' pods sitting
at their offsite provider right now, and those objects have to be deleted by
hand — with versioning enabled, deleting the current version is not enough, the
prior versions must go too. Worth a line in the release notes when this ships;
the lab's copy was purged as part of this verification.

Also worth noting for whoever writes the alerting: the guard exits non-zero
before transferring anything, so `KubeJobFailed` is what surfaces it. That is
the right signal, but the message only appears in the pod log — the alert alone
will not say "pods were about to leave the cluster".
