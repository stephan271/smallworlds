# Add the garage-backup instance

Status: complete

## What to build

A second Garage instance, `garage-backup`, in its own namespace, storing **both**
its metadata and its data on `/mnt/smallworlds-backup`. It serves only backup
buckets; the existing `garage` instance keeps `nextcloud`, `forgejo` and `plane`,
which are primary application storage rather than backups.

The metadata directory is not an optimisation detail here. Garage's LMDB index is
what resolves a bucket and key to content-addressed blocks, so putting `meta` on
the fast operational volume — which the chart's own values comment invites —
would leave the backup volume holding blocks nobody can address after exactly the
failure this feature exists to survive. Both directories go on the backup volume,
and the manifest should say why so the next person does not helpfully "fix" it.

This issue also fixes drill finding 3: `apps/garage.yaml` currently sets
`persistence.size` and `persistence.storageClass`, neither of which exists in
chart 0.7.1, so Helm ignores them and the operational Garage has been running on
a 1 Gi `local-path` claim while `garage-data-pv` sat unbound. The real keys are
`persistence.data.{storageClass,size}` and `persistence.meta.*`. Confirmed in the
lab; corroborated by a real install leaving `/mnt/smallworlds-data/garage` at
0 bytes.

## Acceptance criteria

- [x] `apps/garage.yaml` uses the nested `persistence.data` / `persistence.meta`
      keys, and `data-garage-0` binds `garage-data-pv` instead of a `local-path`
      claim.
- [x] `apps/garage-backup.yaml` deploys a second release (namespace
      `garage-backup-system`, `replicationFactor: "1"`, its own
      `garage-auth-secret`), at sync wave `-5`.
- [x] `apps/persistent-storage.yaml` declares `garage-backup-data-pv` and
      `garage-backup-meta-pv` under `/mnt/smallworlds-backup/garage-backup/`,
      `static-local`, with the same dual-hostname `nodeAffinity` as its siblings.
- [x] Both PVs bind, and a comment in the manifest states why `meta` may not be
      moved to fast storage.
- [x] The two instances have distinct Service names and neither init job nor
      tenant can reach the wrong one by accident.
- [x] `kubectl kustomize --enable-helm` renders both cleanly.
- [x] Verified on the lab cluster: writing an object to `garage-backup` lands
      under `/mnt/smallworlds-backup` (52 MB of barman + pod objects on the
      separate device after the first backup and export).
- [~] "nothing appears under `/mnt/smallworlds-data`" — only partly shown. The
      lab cluster still holds 172 MB of pre-split objects in the operational
      Garage from before the change; new writes all go to the backup instance.
      A build from scratch is what actually demonstrates this, and is the only
      case that matters given no cluster is migrated.
