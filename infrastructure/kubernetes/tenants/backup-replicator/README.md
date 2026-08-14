# backup-replicator — offsite leg setup

The CronJob in this tenant mirrors the **entire in-cluster Garage instance** to an
offsite S3 target every night at 04:00 (`rclone sync source: dest:`). It is the
*only* real backup hop — Garage itself runs `replicationFactor: 1` on the same
disk as the primary data (see `doc/storage-and-backup.md` §3/§8).

Everything below is **operator setup**: the base repo deliberately ships no
credentials, and the `replicator-config-secret` is mounted `optional: true`, so
until you complete these steps the nightly Job simply fails (which the stock
`KubeJobFailed` alert reports).

## 1. Offsite target: Backblaze B2 (recommended)

Any S3-compatible target works, but the recommended one is a B2 bucket
(~$6/TB/month, pay-per-GB) because its **native versioning** is what turns the
plain mirror into point-in-time recovery — deletions/corruption synced offsite
remain recoverable as older versions.

1. Create **one bucket per replicated bucket name**, each with **versioning
   enabled** (B2 buckets keep all versions by default — do not switch to "keep
   only the latest"). The names must match the source exactly:
   `postgres-backups`, `postgres-backups-keycloak`, `velero-backups`,
   `nextcloud`, `forgejo`, `plane`.
2. Add a lifecycle rule: hide/delete prior versions after ~30 days.
3. Create an **application key** scoped to those buckets (read+write).

> **Pre-create them; do not let rclone do it.** Buckets rclone creates on first
> sync have versioning **off**, which silently turns the offsite copy from a
> point-in-time backup into a plain mirror — a deletion or corruption on the
> cluster then propagates within 24 h and is unrecoverable. This was measured:
> a file was created, synced, deleted cluster-side and re-synced, and it was
> gone offsite with no version to recover. Verify before trusting the leg:
>
> ```bash
> # B2
> b2 bucket get <bucket> | grep -i versioning
> # any S3-compatible target
> aws s3api get-bucket-versioning --bucket <bucket> --endpoint-url <url>
> ```
>
> Expect `Enabled`. An empty response means versioning is off.

## 2. Source key: cluster Garage

The per-tenant init jobs create keys scoped to their own buckets; the replicator
needs one key that can read *all* buckets. On the node (or via `kubectl exec`
into the Garage pod):

The key lives on **`garage-backup`**, not the operational instance — that is
where every Recovery Point is collected (`docs/adr/0048`), so the replicator
never needs to read operational storage at all.

```bash
# in the garage-backup-system namespace, NOT garage-system
garage key create replicator-key
for b in postgres-backups postgres-backups-keycloak velero-backups \
         nextcloud forgejo plane; do
  garage bucket allow "$b" --read --key replicator-key
done
```

**Grant these buckets and no others.** In particular never grant
`pod-gateway`: members' personal pods stay on the cluster, they are stored
unencrypted at rest, and the archive includes the `immich-locked/` prefix
holding their PIN-protected photos. The CronJob refuses to run if the key can
see that bucket, so a mistake here fails loudly rather than quietly shipping
private photos to a third party — but the grant list is the control that
matters; the check is only a backstop.

Do **not** substitute `$(garage bucket list)` for the explicit loop. That is
what the earlier version of this file did, and combined with a whole-instance
`rclone sync` it replicated every pod offsite in contradiction of
`docs/adr/0047`.

Adding a tenant does not extend the list automatically. Grant its backup-side
bucket here *and* add it to `REPLICATE_BUCKETS` in `cronjob.yaml` — a dataset
leaving the cluster should be a decision. Note the Key ID / secret from
`garage key info replicator-key`.

## 3. The rclone config secret

`rclone.conf` with exactly two remotes named `source` and `dest`:

```ini
[source]
type = s3
provider = Other
endpoint = http://garage-backup.garage-backup-system.svc.cluster.local:3900
region = garage
force_path_style = true
access_key_id = <replicator-key id>
secret_access_key = <replicator-key secret>

[dest]
type = s3
provider = Other
endpoint = https://s3.<b2-region>.backblazeb2.com
access_key_id = <b2 keyID>
secret_access_key = <b2 applicationKey>
```

With `bucket_acl`/paths: the job syncs the instance root, so buckets map 1:1
onto `dest:` — if the B2 side is a *single* bucket, instead set
`dest = :s3:<bucket>` style paths by pointing the job at a prefix; the simplest
setup is one B2 bucket per Garage bucket name (rclone creates them on first
sync if the key may create buckets, otherwise pre-create them).

```bash
kubectl create secret generic replicator-config-secret \
  -n backup-replicator --from-file=rclone.conf
```

This secret lives only in the cluster / your private overlay — never in this repo.

## 4. Verify

```bash
kubectl create job -n backup-replicator --from=cronjob/backup-replicator manual-test
kubectl logs -n backup-replicator job/manual-test -f
```

Then confirm objects (including `postgres-backups*` and the `*/pv-backup/*`
prefixes from the pv-backup CronJobs) appear on the B2 side.

## Restore

See `doc/storage-and-backup.md` §7.3 — swap `source`/`dest` direction with a
temporary rclone pod; recover deleted/overwritten objects via B2 file versions.
