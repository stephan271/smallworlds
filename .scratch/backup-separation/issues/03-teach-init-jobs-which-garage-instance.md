# Teach the init jobs which Garage instance a bucket belongs to

Status: complete

## What to build

`bases/garage-init-job` currently assumes there is exactly one Garage: it
`kubectl exec`s into the pod matched by `app.kubernetes.io/name=garage` in
`garage-system`, creates the tenant bucket named after the namespace, and also
creates the shared `postgres-backups` bucket plus a dedicated CNPG key. With two
instances, that single job now has to place buckets on different sides of the
split — the tenant bucket on the operational instance, the CNPG backup bucket on
`garage-backup`.

Make the target instance an explicit, **required** parameter (namespace, label
selector and Service endpoint) and have the CNPG half of the job target
`garage-backup`. No default: since no cluster needs migrating, there is nothing
to stay compatible with, and a job that silently falls back to the operational
instance would write backups onto the volume this feature exists to get them
off — the exact failure the split is meant to prevent, arriving quietly. The
resulting `garage-secret-cnpg` must carry the backup endpoint so tenants do not
have to know which instance they are talking to.

`bases/velero-garage-init-job` gets the same treatment and targets
`garage-backup` outright.

Keep the existing least-privilege split: a tenant key that can reach only its own
bucket, and a separate CNPG key that can reach only `postgres-backups`.

## Acceptance criteria

- [x] `bases/garage-init-job` takes the target instance from env with no
      default, fails fast when unset, and the CNPG bucket/key half targets
      `garage-backup`.
- [x] `garage-secret-cnpg` gains an `endpointURL` key so consumers read the
      endpoint rather than hard-coding it.
- [x] `bases/velero-garage-init-job` provisions `velero-backups` on
      `garage-backup`.
- [x] The layout-assignment step is idempotent per instance and does not assign a
      layout on the wrong one.
- [x] Re-running either job on an already-provisioned cluster changes nothing.
- [x] `doc/bases.md` describes the instance parameter and which side each bucket
      lands on.

## Comments

Two deviations from the criteria as written, both deliberate:

- **`endpointURL` in `garage-secret-cnpg` is not read by CNPG.**
  `barmanObjectStore.endpointURL` is a plain string field and cannot reference a
  Secret, so the six clusters carry the backup endpoint as a literal. The key is
  still written, because restore tooling and the drill scripts do read it.
- **A third parameter was needed: `TENANT_BUCKET_NAMESPACE`.** Splitting only
  the CNPG half was not enough — `pod-gateway`'s "tenant bucket" *is* the
  archive, so it belongs on the backup instance, while its `S3_ENDPOINT` had
  already been moved there by issue 04. Without this the gateway would have
  presented a key minted on the operational instance to `garage-backup`. The
  base states `garage-system` outright and `tenants/pod-gateway` patches it.
