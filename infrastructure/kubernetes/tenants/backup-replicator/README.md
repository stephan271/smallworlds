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

1. Create a bucket (e.g. `<community>-backups`) with **versioning enabled**
   (B2 buckets keep all versions by default — do not switch to "keep only the
   latest").
2. Add a lifecycle rule: hide/delete prior versions after ~30 days.
3. Create an **application key** scoped to that bucket (read+write).

## 2. Source key: cluster Garage

The per-tenant init jobs create keys scoped to their own buckets; the replicator
needs one key that can read *all* buckets. On the node (or via `kubectl exec`
into the Garage pod):

```bash
garage key create replicator-key
for b in $(garage bucket list | awk 'NR>1 {print $1}'); do
  garage bucket allow "$b" --read --key replicator-key
done
```

Re-run the loop after adding a new tenant (its bucket won't be granted
automatically). Note the Key ID / secret from `garage key info replicator-key`.

## 3. The rclone config secret

`rclone.conf` with exactly two remotes named `source` and `dest`:

```ini
[source]
type = s3
provider = Other
endpoint = http://garage.garage-system.svc.cluster.local:3900
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
