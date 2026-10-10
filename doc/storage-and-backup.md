# Storage & Backup Management

The single reference for the cluster's storage layout, backup chain, restore
procedures and scaling paths. Derived from the manifests in this repo (baseline:
commit `e1755fd`, 2026-07-18). This document absorbed and replaces the former
`admin-tools/restore-procedures.md` and `admin-tools/scaling-guide.md`. Related:
`doc/bases.md` (the init-job bases that provision buckets/keys), and
`plans-and-walkthroughs/design-personal-data-pods.md` (design exploration: pushing
the file-shaped, per-user slice into user-owned Solid pods as an append-only
sovereignty layer on top of the backup chain described here — not implemented).

> **Two storage modes.** Everything below describes the default, in-house mode:
> two Garage instances on two volumes. A community may instead choose an
> external S3 provider at install time, which replaces both Garage instances
> and the second volume — see §1a and `docs/adr/0053`.

## 1. Physical storage foundation

Stateful data lives on **two separate volumes**, one operational and one holding
every Recovery Point (`docs/adr/0048`). Hop one of a backup chain that shares a
disk with the data it protects adds no redundancy at all, and since nothing on
the cluster enforces a size, a runaway consumer could otherwise starve the very
backups meant to recover from it.

**Operational** — a 200 GB Hetzner Cloud volume
(`hcloud_volume.smallworlds_data`, `prevent_destroy = true`) mounted at
`/mnt/smallworlds-data`, or on the local target a directory (default
`/var/lib/smallworlds-data`) symlinked to the same path. Survives VM re-creation.

| Path | Used by |
|---|---|
| `/mnt/smallworlds-data/garage` | Operational Garage S3 data (static PV `garage-data-pv`) — the `nextcloud`, `forgejo` and `plane` buckets, which are primary application storage |
| `/mnt/smallworlds-data/immich-library` | Immich photo/video originals (static PV `immich-library-pv`) |
| `/mnt/smallworlds-data/k3s` | Symlink target of `/var/lib/rancher/k3s` — **all `local-path` PVCs** (`.../k3s/storage/`) and the container image store |
| `/var/lib/smallworlds-etcd` | The cluster datastore, deliberately **not** on either volume — see below |

**Backup** — a 100 GB volume (`hcloud_volume.smallworlds_backup`) at
`/mnt/smallworlds-backup`, or `BACKUP_DIR` (default
`/var/lib/smallworlds-backup`) on the local target.

| Path | Used by |
|---|---|
| `/mnt/smallworlds-backup/garage-backup/data` | `garage-backup` object blocks — barman, Velero, `pv-backup`, the pod archive, the Nextcloud copy |
| `/mnt/smallworlds-backup/garage-backup/meta` | `garage-backup`'s LMDB index. **Must stay on this volume**: it is what resolves a bucket key to blocks, so putting it on faster operational storage would leave the surviving blocks unaddressable after exactly the failure this split exists to survive |

Both bootstraps **refuse to continue** when the two paths resolve to the same
block device, because a co-located backup volume is indistinguishable from a
correctly separated one until the day it is needed. On Hetzner, `automount` is
off for both volumes and cloud-init mounts each by the exact
`/dev/disk/by-id/...` path Terraform passes — with two volumes attached, the
older "first unmounted disk" heuristic could not tell them apart.

Both volumes attach to one machine, so this addresses disk failure and capacity
starvation, **not** node loss, compromise or provider failure. The offsite leg
(§4 row 5) remains the disaster tier.

### 1a. External object storage (`docs/adr/0053`)

Chosen at install time (`prepare-community-repo.sh` / the setup wizard). The
`garage`, `garage-backup` and `backup-replicator` Applications and the three
Garage PersistentVolumes are removed from the overlay, and the node needs one
disk: the bootstrap skips the Garage directories and the two-disk check.

| What | In-house (Garage) | External provider |
|---|---|---|
| Tenant bucket (`nextcloud`, `forgejo`, `plane`) | `<tenant>` on `garage` | `<prefix>-<tenant>` at the primary endpoint |
| Tenant backup bucket (pv-backup, bucket copies) | `<tenant>` on `garage-backup` | `<prefix>-<tenant>-backup` at the backup endpoint |
| Database backups | `postgres-backups`, `postgres-backups-keycloak` on `garage-backup` | `<prefix>-postgres-backups`, `<prefix>-postgres-backups-keycloak` |
| Velero | `velero-backups` on `garage-backup` | `<prefix>-velero-backups` |
| Pod archive | `pod-gateway` on `garage-backup` | `<prefix>-pod-gateway` at the backup endpoint |
| Offsite leg | `backup-replicator` (§4 row 5) | not deployed — Recovery Points are already offsite |
| Immich library | static PV on the data volume | unchanged: still on the node's disk |
| Photos with Gallery (`docs/adr/0054`) | `immich` bucket on `garage`; copy with versions in `immich` on `garage-backup` | `<prefix>-immich` at the primary endpoint; copy with versions in `<prefix>-immich-backup` |

The backup endpoint is the primary one unless a second was given. Paths below
the bucket are identical in both modes, so the restore procedures in §7 apply
with the bucket and endpoint substituted; credentials, endpoint, region and
bucket name are all in the tenant's `garage-secret*` Secrets either way.

With Gallery in place of Immich the library PVC holds only uploads in flight,
`pv-backup` for it and the pod export are not deployed, and §7.4 does not
apply: the photos are restored like Nextcloud's files (§7.3), bucket and
database together.

Not yet done for this mode: provider-side versioning and lifecycle rules (set
them at the provider by hand, as §8 describes for the offsite bucket), and a
restore drill against a real provider.

### Why etcd is not on the data volume

`…/k3s/server/db` is a symlink to `/var/lib/smallworlds-etcd` on the machine's
own disk, on every deployment target. etcd fsyncs every write and gives up its
leader lease when the disk cannot answer in time; k3s then exits with status 1
and systemd restarts it. The symptom is not a storage error — it is a control
plane that dies every few minutes, pods stuck in `Unknown`, and Argo CD waiting
on hook jobs that no longer exist, while the cluster's own objects all look
fine. A first LAN installation ran into exactly this with the data directory on
a rotating disk: 22 restarts and 17,523 etcd "took too long" warnings in half an
hour.

The bulk data has the opposite need — capacity and sequential throughput — so it
stays on the large volume. Bootstrap warns when the datastore's disk reports
itself as rotational, but does not refuse: virtualized disks routinely
misreport, and a false reading must not fail an installation.

Every `local-path` claim lands on the operational volume; `static-local` spans
both, since which volume a static PV uses is decided by its `local.path`:

- **`static-local`** (renamed from `hetzner-local`; it was never Hetzner-specific) —
  static/no-provisioner, `Retain`, `WaitForFirstConsumer`, node-pinned via
  `nodeAffinity` to hostnames `cc-pilot-node-01` / `smallworlds-local-node`. Its only
  job is to matchmake the four static PVs — Garage and the Immich library on the
  operational volume, `garage-backup`'s data and meta on the backup volume — with
  their claims; the same manifest serves both provisioning targets because each
  bootstrap satisfies the `/mnt/smallworlds-data` and `/mnt/smallworlds-backup`
  contracts.
- **`local-path`** (k3s default) — dynamic, used by everything else. Note: local-path
  **neither enforces nor can expand** the requested size; the numbers below are
  scheduling hints/documentation, not quotas.

## 2. Where user/app data lives

| App | File / object data | Database | Cache / other |
|---|---|---|---|
| **Nextcloud** | User files → Garage S3 bucket `nextcloud` (S3 is the *primary* object store; objects are named `urn:oid:<fileid>`, so the bucket carries no filenames, folders or owners and is **restorable only together with the Nextcloud database**). App code + `config.php` → chart PVC (8 Gi, local-path, `/var/www/html`) | CNPG `database` (nextcloud ns), 20 Gi ×2 | Redis Deployment, ephemeral |
| **Immich** | Originals + thumbnails → `immich-library-pvc` → static 60 Gi PV on the data volume. **Not in Garage.** | CNPG `database` (immich ns, VectorChord image via ClusterImageCatalog), 20 Gi ×2 | Redis ephemeral; ML model cache emptyDir |
| **Forgejo** | Git repositories → chart data PVC (50 Gi, local-path). LFS/attachments/avatars → Garage bucket `forgejo` | CNPG `database` (forgejo ns), 20 Gi ×2 | Redis ephemeral |
| **Plane** | Uploads/attachments → Garage S3 bucket `plane` (chart MinIO disabled; `plane-doc-store` secret composed by `doc-store-init-job.yaml`; browser presigned-URL flows still need a public S3 endpoint, see §5) | CNPG `database` (plane ns), 20 Gi ×2 | RabbitMQ StatefulSet PVC (100 Mi, local-path); Redis ephemeral |
| **Stalwart** | All mail data *and blobs* stored in PostgreSQL (`@type: PostgreSql` store in `stalwart-config`) | CNPG `database` (stalwart ns), 20 Gi ×2 | — |
| **Keycloak** | — (realm/SPI mounted from ConfigMaps) | CNPG `keycloak-db` (pgvecto.rs image), 20 Gi ×2 | — |
| **Bulwark** | Admin state → `bulwark-data` PVC (512 Mi, local-path) | — | — |
| **Jitsi, Excalidraw, Collabora, Euro-Office, Dashboard, Hermes, Remediation** | Stateless (config via ConfigMaps/Secrets; users' Euro-Office source documents remain in Nextcloud) | — | — |
| **Monitoring** | Prometheus 20 Gi, Alertmanager 2 Gi, Loki 20 Gi (all local-path StatefulSet claims) | — | Grafana ephemeral |
| **Garage itself** | 120 Gi static PV, layout capacity assigned **100 G** by `garage-init-job`, `replicationFactor: 1` | — | — |
| **k3s control plane** | Embedded datastore under `/mnt/smallworlds-data/k3s` | — | — |

### PV/PVC quota inventory

| Claim | Namespace | Size | StorageClass | Enforced? |
|---|---|---|---|---|
| `garage-data-pv` | garage-system | 120 Gi | static-local | No (static PV, shared disk) |
| `garage-backup-data-pv` | garage-backup-system | 90 Gi | static-local | No (static PV, **backup volume**) |
| `garage-backup-meta-pv` | garage-backup-system | 5 Gi | static-local | No (static PV, **backup volume**) |
| `immich-library-pvc` | immich | 60 Gi | static-local | No (static PV, shared disk) |
| Forgejo data | forgejo | 50 Gi | local-path (explicit) | No |
| Nextcloud (`/var/www/html`) | nextcloud | 8 Gi (chart default) | local-path | No |
| CNPG clusters (6 × 2 instances) | nextcloud, immich, plane, forgejo, stalwart, keycloak | 20 Gi each → 240 Gi total | local-path | No |
| Prometheus / Alertmanager / Loki | monitoring | 20 Gi / 2 Gi / 20 Gi | local-path | No |
| Plane RabbitMQ | plane | 100 Mi | local-path | No |
| Bulwark | bulwark | 512 Mi | local-path | No |

**Nominal total ≈ 520 Gi requested against a 200 GB physical volume.** This
overcommit is workable only because nothing enforces the requests; the real
constraint is free space on `/mnt/smallworlds-data`, and a single runaway consumer
(Prometheus, Loki, a large CNPG WAL burst) can starve every other tenant including
Garage. There is no per-tenant disk quota mechanism.

## 3. Strategy

Decisions that govern how the sections below should evolve:

- **Garage-first hub-and-spoke, kept.** All backup producers write to in-cluster
  Garage (S3 is the lingua franca of CNPG/barman, Velero and rclone), and a single
  replicator carries *everything* offsite with one credential set and one sync
  window. Every future backup gap is closed the same way: "get the data into a
  bucket", after which offsite protection is inherited for free.
- **Garage is a staging tier, not a backup tier.** It runs `replicationFactor: 1` on
  the *same volume* as the primary data — hop 1 adds zero physical redundancy, and a
  disk failure destroys primary data and hop 1 together. Consequences: (a) the
  offsite leg is the *only* real backup and must be point-in-time capable
  (versioning or `rclone --backup-dir`), because a plain mirror would propagate
  corruption/mass-deletion to everything within 24 h; (b) bulk data routed through
  the hub exists twice on the shared volume — grow the Hetzner volume rather than
  splitting off a second replication path; volume growth is the one operation that
  scales online (§6).
- **Separate PVs per app, kept.** Physically it is all one volume anyway; the
  declared layer stays per-app because charts/operators require it (CNPG cannot
  share volumes), lifecycle/restore is per-dataset, and a future multi-node or
  CSI/Longhorn migration moves one PV at a time. The per-PV sizes are documented
  budgets, not quotas — enforcement, if ever needed, is a filesystem concern (XFS
  project quotas / real CSI), not a reason to merge PVs.
- **Velero stays, manifest-only.** In a GitOps cluster its value is exactly the
  state git does *not* own: runtime-generated Secrets (Keycloak clients, Garage
  keys, admin creds — and CNPG's `database-app` password, which must match the
  contents of a restored database), fast surgical namespace restore, and a 30-day
  drift record. The PV-data gaps (§5) are closed with rclone-to-Garage CronJobs, not
  by enabling Velero's node agent — one mechanism, and it fits the hub model.
- **`static-local` naming.** Renamed from `hetzner-local` while clusters are still
  routinely rebuilt from scratch; the class is a target-agnostic static-binding
  label and the old name was misleading on LAN deployments.

## 4. Backup concept — what exists today

The design is a two-hop chain: **app data → `garage-backup` (separate volume) → offsite mirror**.
Producers write to the backup instance; the operational Garage holds only the
`nextcloud`, `forgejo` and `plane` buckets, which are primary application
storage rather than Recovery Points (§1, `docs/adr/0048`).

| # | Data source | Mechanism | Destination | Schedule | Retention |
|---|---|---|---|---|---|
| 1 | 5 tenant CNPG DBs (`database` in nextcloud/immich/plane/forgejo/stalwart) | Barman object store (base backup + continuous WAL, gzip) via `ScheduledBackup`, per-tenant `serverName: <tenant>-database` | `s3://postgres-backups/<tenant>-database/` on **`garage-backup`** (shared bucket, credential `garage-secret-cnpg` per namespace, which also carries `endpointURL`) | daily 02:00 | 7 d |
| 2 | Keycloak DB (`keycloak-db`) | Same, but dedicated bucket + key (custom `garage-init-job.yaml`, credential `garage-secret`) | `s3://postgres-backups-keycloak/` on **`garage-backup`** | daily 03:00 | 7 d |
| 3 | Kubernetes resources (all namespaces except `kube-system`) | Velero 12.x, AWS S3 plugin, `deployNodeAgent: false`, no volume snapshots | **`garage-backup`** bucket `velero-backups` | daily 02:00 | 720 h (30 d) |
| 4 | PVC file data: **all users' Immich originals**, Forgejo git repos, Nextcloud `/var/www/html` | `bases/pv-backup-job` rclone CronJob per tenant (PVC mounted read-only, RWO is fine on a single node) | **`garage-backup`**, under `pv-backup/` (`immich/pv-backup/library`, `forgejo/pv-backup/data`, `nextcloud/pv-backup/html`) | daily 00:30 / 00:45 / 01:00 | Current-state mirror in Garage; historical retention is only the offsite destination's version policy |
| 4b | Immich member-owned copy | `immich-pod-export` CronJob appends each enrolled user's originals to their pod (`doc/pod-archive.md`) | `pod-gateway` bucket on **`garage-backup`**, `<user>/objects/`, then pulled by that user's home device | daily 01:15 | Append-only; never overwritten or deleted |
| 4c | **Nextcloud user files** — the `nextcloud` bucket itself | `bases/backup-job` rclone CronJob, `sync src:nextcloud dst:nextcloud/current --backup-dir dst:nextcloud/versions/<date>` | `nextcloud` bucket on **`garage-backup`** | daily 03:15 | `current/` mirrors the live bucket; superseded/deleted objects accumulate under `versions/<date>/` with no in-cluster expiry yet |
| 5 | An **explicit list** of `garage-backup` buckets: `postgres-backups`, `postgres-backups-keycloak`, `velero-backups`, `immich`, `nextcloud`, `forgejo`, `plane`. **Never `pod-gateway`** — see below | `backup-replicator` CronJob, per-bucket `rclone sync source:<b> dest:<b>`; refuses to start if the key can even see `pod-gateway`, and fails loudly on a missing bucket | Offsite S3 — operator-provisioned per `tenants/backup-replicator/README.md` (**pre-created versioned** buckets, §8) | daily 04:00 | Operator-managed; runbook recommends destination versioning plus expiry of prior versions after about 30 d |
| 6 | Let's Encrypt certificates | `admin-tools/backup-certs-to-laptop.sh` / `restore-certs-from-laptop.sh` | Operator laptop `~/.smallworlds/cert-backups/<env>/` | manual (part of rebuild flow) | n/a |

> **The all-user Immich mirror and personal pods have different jobs.** The
> `immich` backup bucket is the cluster Recovery Point and is replicated
> offsite; it covers every member in one operation. `pod-gateway` is an
> append-only, member-owned copy and is deliberately absent from
> `REPLICATE_BUCKETS`. The database remains essential because album membership,
> face names and tags cannot be recomputed from pixels. See ADR 0052.
>
> Note this was **not** true before `docs/adr/0048`: the job ran a
> whole-instance `rclone sync` and the setup guide granted read on every
> bucket, so any community that configured the offsite leg under the old
> instructions has pods at its provider today. Fixing the config stops further
> copies; the objects already there must be deleted by hand, prior versions
> included.

Backup health is monitored: the CNPG clusters expose metrics via PodMonitors and
`apps/backup-alerts.yaml` alerts on WAL-archiving failures (`CNPGWALArchivingFailing`),
stale base backups (`CNPGBackupStale`) and stale Velero schedules
(`VeleroBackupStale`); failed replicator/pv-backup CronJob runs are caught by the
stock `KubeJobFailed` alert. All of it routes through the Alertmanager email
receiver like every other alert.

What this chain **covers end-to-end** (once the offsite leg is configured):
all PostgreSQL databases — and therefore Stalwart mail, Plane/Forgejo/Nextcloud/Immich
metadata, Keycloak identities — plus Nextcloud user files and Forgejo LFS/attachments
(both live in Garage buckets), all Immich originals, and the cluster's resource manifests.

`bases/backup-job` handles the operational-bucket→backup-bucket direction and is
currently consumed by Nextcloud. `backup-replicator` is the independent second
hop from an explicit backup-bucket allow-list to offsite storage;
`bases/pv-backup-job` covers filesystem→backup-bucket data.

## 5. Remaining gaps

The 2026-07-18 hardening pass closed the worst of the original findings — for the
record: the CNPG `serverName` collision (all five tenant clusters archived to the
same `s3://postgres-backups/database/` path, so at most one had working backups),
the completely unprotected PVC data (Immich originals, Forgejo git repos, Nextcloud
`config.php` — now `bases/pv-backup-job`), the absent backup monitoring (now
PodMonitors + `apps/backup-alerts.yaml`), Plane's missing object storage, and —
found during that work — plane's kustomization never included the
`garage-init-job` base, so `garage-secret-cnpg` didn't exist in the plane
namespace and its CNPG backups could not authenticate at all.

What still remains, ranked:

1. **The offsite leg needs operator provisioning.** The repo now documents the
   full setup (`tenants/backup-replicator/README.md`: B2 versioned bucket,
   Garage `replicator-key` grants, `replicator-config-secret`), but until an
   operator performs it, nothing leaves the node. Until then the nightly Job
   fails and `KubeJobFailed` emails about it — by design.
2. **The in-cluster Immich + Nextcloud restore is drilled; offsite loss is not.**
   On 2026-09-01 the disposable Hetzner drill created three users in both apps,
   destroyed both databases and all operational file stores, restored exclusively
   from the central `garage-backup`, and verified every user's bytes and metadata.
   It also produced new post-restore database backups. §7.3 from a real offsite
   provider (not configured here), §7.4's `--from-device` fallback, and total loss
   in §7.6 remain untested.

3. **The former deprecated database API has been migrated.** Migration to the
   Barman Cloud plugin is `docs/adr/0050`, and it is **done and drilled**: one
   `ObjectStore` per tenant namespace, the plugin at wave −9, each
   `Cluster`/`ScheduledBackup` switched over, and — the part that mattered — a
   recovery cluster bootstrapped *through the plugin* from archives the in-tree
   implementation had written, recovering the same rows as before the migration.
   Expect a handful of `pg_stat_archiver` failures in the cutover window itself
   (four here, on one WAL segment) before the next segment archives cleanly; that
   is the gap between removing one archiver and the other taking over, not a
   fault. What remains is to carry the change into a release: the CNPG chart stays
   pinned until the version that removes the in-tree code is one the cluster is
   ready to take.
4. **Garage `replicationFactor: 1`.** In-cluster S3 holds a single copy; disk
   corruption on the data volume takes out both the primary data *and* hop 1 of
   every backup chain simultaneously. The offsite copy is the only real
   redundancy, which makes item 1 the highest-stakes open task.
5. **Plane presigned-URL flows need a public S3 endpoint.** Server-side upload
   storage now lands in the `plane` Garage bucket (internal endpoint), but
   Plane hands browsers presigned URLs pointing at that endpoint — full
   upload/download UX needs Garage exposed on a public hostname
   (`s3.<domain>` ingress + DNS + cert) and the endpoint URL in
   `tenants/plane/doc-store-init-job.yaml` switched to it.
6. **Verify against a live cluster** (§9): confirm each CNPG cluster reports a
   first recoverability point after the serverName change, that the pv-backup
   jobs succeed, and that the alerts stay green.

## 6. Scalability of each storage layer

| Layer | Scale up on the fly? | Procedure |
|---|---|---|
| Hetzner data volume (200 GB) | **Yes, online** | Bump `size` in `infrastructure/terraform/main.tf` → `terraform apply` (volume is `prevent_destroy`; Hetzner resizes live) → on the node: `resize2fs` on the volume device. No pod restarts needed. |
| `static-local` PVs (Garage 120 Gi, Immich 60 Gi) | **Mostly cosmetic** | Capacity on a static PV is declarative; there is no filesystem behind it other than the shared volume. Edit the PV/`persistent-storage.yaml` size for bookkeeping. For Garage, additionally raise the layout allocation: `garage layout assign -z dc1 -c <newsize> <node>` + `garage layout apply` (the init job pins 100 G). |
| `local-path` PVCs (Forgejo, Nextcloud, monitoring, RabbitMQ, Bulwark) | **No expansion support** | The k3s local-path provisioner has no `allowVolumeExpansion`; editing the PVC request is rejected. In practice sizes aren't enforced either, so apps grow until the disk is full. To honestly resize: recreate the PVC (for StatefulSets: `kubectl delete sts --cascade=orphan`, recreate claim, restore data). |
| CNPG clusters (20 Gi each) | **Special procedure, but zero-downtime** | On a non-expandable storage class, follow the CNPG resize dance: raise `spec.storage.size`, then one instance at a time (replica first, primary after switchover) delete the pod **and its PVC** so the operator recreates it at the new size and re-clones from the primary. `instances: 2` makes this rolling. |
| Prometheus / Loki / Alertmanager | Same as local-path | `volumeClaimTemplates` are immutable; orphan-delete the StatefulSet and recreate the claim, or accept losing metrics/log history. |
| Garage capacity overall | Layout + PV + volume together | Growing usable S3 space = grow the Hetzner volume, then the layout assignment (and optionally the PV number). Multi-node Garage is natively supported (see below). |
| Node (vertical) | Reboot required | Change `server_type` in `infrastructure/terraform/main.tf` → `terraform apply`. Hermes currently only *reports* resource pressure (its tools are read-only + `send_report`); an `open_pr` tool that proposes such changes automatically is planned but not implemented. |
| Node (horizontal) | **Major undertaking** | See below. |

### Adding a worker node (horizontal scaling)

Needed only when the largest suitable Hetzner VM no longer suffices or
multi-node HA is required. A `worker-node` Terraform module exists:

```hcl
module "worker_1" {
  source       = "./modules/worker-node"
  hcloud_token = var.hcloud_token
  cluster_name = "smallworlds"
  server_type  = "cx43"
  ssh_keys     = [hcloud_ssh_key.default.id]
  k3s_url      = "https://${hcloud_server.smallworlds_pilot_node.ipv4_address}:6443"
  k3s_token    = "…" # on the control plane: cat /var/lib/rancher/k3s/server/node-token
  # location defaults to nbg1
}
```

`terraform apply` boots the node, installs k3s and joins the cluster. But be aware
that the single-node assumption is baked into the storage layer:

1. **Ingress/DNS**: external DNS must point at a load balancer or all nodes.
2. **Persistent volumes**: both storage classes are node-local. `static-local` PVs
   are nodeAffinity-pinned to the bootstrap hostnames and `local-path` data cannot
   migrate, so every stateful workload stays pinned to node 1 until state moves to
   Garage S3 or replicated storage (e.g. Longhorn).
3. **Garage**: natively multi-node — add the new node to the Garage layout to spread
   S3 storage.

## 7. Restore procedures

The destructive, reproducible Immich + Nextcloud drill is automated for the
disposable Hetzner staging workspace:

```bash
./admin-tools/restore-drill.sh all
# Keep a failed or successful cluster for inspection:
KEEP_CLUSTER=1 ./admin-tools/restore-drill.sh all
```

It creates a fresh node with distinct operational and backup volumes, seeds
three synthetic users through both application APIs, quiesces writers, creates
explicit CNPG/file/Velero Recovery Points, deletes the operational databases and
file data, restores from `garage-backup`, and verifies every user's bytes and
metadata through both restored applications. It refuses any node not named
`cc-staging-node-01` and never reads `pod-gateway`. Evidence lands under
`admin-tools/restore-drill/evidence/`; synthetic credentials live under a
private runtime directory in `/tmp` while the cluster exists and are removed by
the normal teardown.

> Absorbed from the former `admin-tools/restore-procedures.md`, with corrections:
> the CNPG example there referenced `garage-auth-secret` (`access-key`/`secret-key`),
> which actually holds Garage's `rpcSecret`/`adminToken` — following it verbatim
> would fail. The correct credential is `garage-secret-cnpg`
> (`accessKeyId`/`secretAccessKey`).

> **PostgreSQL is the index for nearly every file restore here.** Immich stores
> pixels under asset UUIDs with the human name only in its database; Nextcloud
> stores objects as `urn:oid:<fileid>` with filenames, folders and owners only in
> its database. Neither bucket is restorable to anything a person recognises
> without a database restored to a consistent point. A pod archive is
> self-describing enough for a member to read their copy without a cluster, but
> restoring the application still requires the database.

### 7.1 PostgreSQL databases (CloudNativePG)

CNPG restores by bootstrapping a *new* cluster from the object store of the old one.

1. Identify backups: `kubectl get backups -n <namespace>`.
2. Create a recovery cluster:
   ```yaml
   apiVersion: postgresql.cnpg.io/v1
   kind: Cluster
   metadata:
     name: database-restore
     namespace: <namespace>
   spec:
     instances: 2
     imageName: ghcr.io/cloudnative-pg/postgresql:16
     storage:
       size: 20Gi
     bootstrap:
       recovery:
         source: database
     externalClusters:
       - name: database
         plugin:
           name: barman-cloud.cloudnative-pg.io
           parameters:
             barmanObjectName: postgres-backups
             # Must match the original Cluster's plugin parameter.
             serverName: <tenant>-database
   ```
   `barmanObjectName` names the tenant namespace's `ObjectStore`; that object
   owns the `garage-backup` endpoint, credentials, region and 7-day policy. For
   Keycloak use its `postgres-backups-keycloak` object store and original
   `serverName`.

   Deliberately do **not** copy the source Cluster's `spec.plugins` WAL-archiver
   block into this recovery manifest. During bootstrap it points at the same
   non-empty `serverName`, and the plugin refuses recovery with “expected empty
   archive”. Add the original archiver block only after recovery reaches Ready;
   this restarts the instance once. Then create a new explicit `Backup` and wait
   for `phase: completed` before declaring the database operational.
3. Apply and wait for the cluster to reach `Cluster in healthy state`. Recovery
   replays WAL as well as restoring the base backup, so the result is current to
   the last archived segment rather than to the last base backup.
4. Reconcile the credential before pointing the app at the new cluster. The
   operator generates a *new* `<cluster>-app` Secret **and reconciles the restored
   role to it**, so the restored database accepts the new password and rejects the
   one the application is still configured with. Either update the application's
   credential to the recovery cluster's Secret, or restore the original Secret
   (e.g. from Velero) and let the operator set the role back.

> Re-drilled on 2026-09-01 for both Immich and Nextcloud through the Barman Cloud
> plugin. Both recovery clusters reached Ready, the original WAL-archiver blocks
> were re-enabled, and a new base backup completed for each. The drill found the
> archiver-ordering rule above; enabling it during bootstrap makes recovery fail.

### 7.2 Cluster state and workloads (Velero)

Requires the `velero` CLI (not installed by bootstrap — `brew install velero` /
GitHub release binary, pointed at the cluster kubeconfig).

```bash
velero backup get
velero restore create --from-backup <backup-name> --include-namespaces <namespace>
velero restore create --from-backup <backup-name> \
  --include-resources deployment,service --include-namespaces <namespace>
velero restore get && velero restore describe <restore-name>
```

Most valuable for recovering runtime-generated Secrets that GitOps cannot recreate
with the same values (see §3).

The CLI is convenient, not required: a `Restore` object does the same thing, which
is worth knowing because the CLI is exactly what a rebuilt operator laptop will
not have. Restoring into a *different* namespace is also how to rehearse without
touching live state:

```yaml
apiVersion: velero.io/v1
kind: Restore
metadata:
  name: drill-secrets
  namespace: velero
spec:
  backupName: <backup-name>
  includedNamespaces: [nextcloud]
  includedResources: [secrets]
  namespaceMapping:
    nextcloud: velero-drill      # restore beside the live namespace, not over it
  restorePVs: false
```

> Drilled on 2026-08-16: 11 of 11 items restored, and every runtime-generated
> credential — the CNPG `database-app` password, both Garage keys, the Nextcloud
> admin password — came back byte-identical to the live value. That is the claim
> in §3 tested rather than asserted. One warning is expected and benign on this
> cluster (`VolumeGroupSnapshotContent` has no CRD without CSI snapshots).

### 7.3 Application data (Garage S3 buckets)

If data is lost from the in-cluster Garage, sync it back from the offsite copy:

```bash
kubectl run rclone-restore -it --rm \
  --image=rclone/rclone:1.74 --restart=Never -- /bin/sh
# configure remotes (or mount replicator-config-secret): offsite as 'source',
# cluster Garage as 'dest', then per bucket:
rclone sync source:<bucket> dest:<bucket> -v --dry-run   # verify first
rclone sync source:<bucket> dest:<bucket> -v
```

After restoring buckets on a *rebuilt* Garage, the tenant init jobs will have
generated fresh access keys; their `bucket allow … || true` grants re-attach the new
keys to the restored buckets on the next sync retry.

For Nextcloud, restore both `nextcloud/current` into the operational `nextcloud`
bucket and `nextcloud/pv-backup/html` onto the `nextcloud-nextcloud` PVC. An S3
object copy does not preserve Unix ownership/mode: before starting Nextcloud,
set the raw PVC tree to uid/gid 33 and ensure its `data` subdirectory has no
permissions for “other” (the automated drill uses `chown -R 33:33` and
`chmod 0770`). Without this, `status.php` returns 503 even though every byte is
present. Restore the matching database before opening the application.

The 2026-09-01 first copy measured 795.080 MiB across 28,056 Nextcloud PVC files:
8m35s to back up and 4m45s to restore on the disposable single-node cluster.
These are ordinary objects, not ZIP files. `rclone sync` is full on its first
run and transfers only changed/new files on later runs; disaster restore still
recreates the full tree. The jobs use 16 transfers, 32 checkers and `--fast-list`
to reduce small-file overhead. A subsequent tuned run transferred only 927.607
KiB of runtime changes but still took 2m47s to compare all 28,056 objects. A
future switch to a metadata-preserving,
deduplicating archive such as restic/kopia would accelerate this further, but is
a backup-format migration rather than a tuning flag.

### 7.4 Immich originals (central all-user mirror)

The routine cluster restore reads `immich/pv-backup/library` from
`garage-backup`, never individual pod prefixes. Stop Immich, mount
`immich-library-pvc` read-write in a temporary rclone Job, and first inspect the
copy with `--dry-run`:

```bash
rclone sync garage:immich/pv-backup/library /library -v --links --dry-run
rclone sync garage:immich/pv-backup/library /library -v --links
```

Use `garage-secret-backup` and the backup-instance endpoint, exactly as the
`pv-backup` CronJob does. Restore the database from the same quiesced Recovery
Point before starting Immich, then verify originals and relational metadata
through the application for more than one user.

#### Personal pod fallback

If the central and offsite mirrors are both lost, the pod archive can recover
enrolled members one at a time. This is a disaster fallback, not the tested
all-user path.

> **The database must be restored first.** The archive's keys are built for a
> human — `immich/<year>/<date>/<id8>-<originalFileName>` — while the library is
> keyed by asset UUID (`/data/upload/<user>/<xx>/<yy>/<uuid>.jpg`). Only the
> Immich database holds the mapping between them, so §7.1 is a prerequisite,
> not an alternative.

1. Restore the Immich database (§7.1) and let it become ready.
2. Dump the inventory the restore tool consumes:

   ```bash
   kubectl exec -n immich database-1 -c postgres -- psql -U postgres -d app -At -q -c \
     "select json_build_object('id',id,'ownerId',\"ownerId\",
        'originalPath',\"originalPath\",'originalFileName',\"originalFileName\",
        'fileCreatedAt',to_char(\"fileCreatedAt\" at time zone 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS\"Z\"'),
        'visibility',visibility)
      from asset where status='active'" > inventory.jsonl
   ```

3. Run the restore where the library PVC is mounted — a helper pod in the
   `immich` namespace with `immich-library-pvc` at `/library`, and the
   `pod-gateway` bucket credentials in the environment
   (`POD_S3_ACCESS_KEY` / `POD_S3_SECRET_KEY` from the `garage-secret` in the
   `pod-gateway` namespace; that Secret lives in another namespace, so it has to
   be copied into `immich` for the duration and deleted afterwards).

   The tool is not a single file. It imports the gateway's own SigV4 client
   rather than carrying a second implementation that could drift from it, and it
   resolves that import *relative to its own location in the repository*. A
   helper pod therefore needs both files in their repo layout, or it fails with
   `ModuleNotFoundError: No module named 's3'`:

   ```
   <root>/admin-tools/restore-immich-originals.py
   <root>/infrastructure/kubernetes/tenants/pod-gateway/src/s3.py
   ```

   ```bash
   ./admin-tools/restore-immich-originals.py \
       --inventory inventory.jsonl --user <immich-user-id> --library /library
   #   ... review the dry run, then:
   ./admin-tools/restore-immich-originals.py \
       --inventory inventory.jsonl --user <immich-user-id> --library /library --apply
   ```

   Every object is checked against the digest its manifest entry recorded at
   export time, and a mismatch is refused rather than written — so a corrupted
   archive object cannot be laundered into the library by the restore. Assets
   the archive does not hold are listed individually and make the run exit
   non-zero; the usual cause is a user who was never enrolled, or an asset added
   after the last nightly export.

4. Restore from a device copy instead with `--from-device <dir>` pointing at the
   device's `objects/` directory. Note there is no manifest there to verify
   against — `pod-agent.py` checks each object's digest at pull time and keeps
   only its sequence — so the tool says so rather than implying a check it did
   not make.

> Drilled on 2026-08-16 by deleting a real original from the library and
> restoring it: the dry run reported `{'present': 23, 'would_restore': 1,
> 'missing': 0, 'corrupt': 0}` — identifying exactly the deleted asset — and
> `--apply` returned the file with a SHA-256 identical to the one recorded before
> deletion, verified against the manifest entry before anything was written. The
> `--from-device` path (step 4) is still undrilled.

> Direct S3 access to the bucket bypasses the append-only guarantee: the
> gateway holds owner rights because S3 requires it, and anything else holding
> that credential can overwrite history. Restores only ever read.

### 7.5 Disaster recovery (complete cluster rebuild)

1. Re-run `smallworlds-init.sh` (restore TLS certs first via
   `admin-tools/restore-certs-from-laptop.sh` to avoid Let's Encrypt rate limits —
   see the README rebuild flow).
2. ArgoCD syncs the base infrastructure; wait for Garage to be online.
3. Restore S3 buckets from offsite (§7.3).
4. Restore databases from the recovered `postgres-backups*` buckets (§7.1).
5. Let ArgoCD finish syncing; apps reconnect to restored data.

Steps 3–5 were drilled for Immich and Nextcloud against the separate in-cluster
backup volume on 2026-09-01. Rebuilding from the external provider remains
untested because no offsite account is configured in this repository.


### 7.6 Total loss — rebuilding from offsite and members' devices

The case the architecture is shaped around: **both volumes are gone.** Operational
data and every local Recovery Point went together — a destroyed node, a
compromised cluster, a terminated account.

What survives is asymmetric, by design:

| Survives | Holds | Why it survived |
|---|---|---|
| Offsite S3 | all databases, Velero, the Nextcloud/Forgejo/Plane buckets | replicated nightly (§4 row 5) |
| Members' home devices | Immich originals | pulled by hardware the cluster cannot reach |
| — | **the pod bucket** | deliberately not replicated offsite |

So the pixels come back from members and everything else comes back from the
offsite copy. Note the dependency this creates: the database is what maps an
archive key to a library path (§7.4), so the offsite database copy is what makes
members' devices useful at all.

1. Rebuild the cluster (§7.5 steps 1–2).
2. Restore every database from offsite (§7.1) — this is the load-bearing step.
3. Restore the application buckets from offsite (§7.3).
4. Collect the Immich originals from members (below).
5. Reconcile, then let ArgoCD finish syncing.

**Collecting from devices.** The agent is pull-only and outbound-only, and the
gateway gives devices no write verb at all — that is the property that stops a
compromised cluster reaching into members' homes, and it is not weakened for a
restore. Today that means the copy travels physically: the member brings or ships
the disk, or mounts it somewhere the operator can read, and then

```bash
./admin-tools/restore-immich-originals.py \
    --inventory inventory.jsonl --from-device /mnt/member-disk/objects \
    --library /library --apply
```

per member. A remote push path would need a credential that can write into
exactly one pod, which does not exist — agent tokens may append to *any* pod —
and so needs its own decision record before it is built.

**Expect drift, and reconcile.** Export runs nightly and the database backup
daily, so after a total loss the two will disagree. The restore tool reports both
directions:

- an asset in the database that no device holds is listed as `MISSING FROM
  ARCHIVE` and makes the run exit non-zero — usually added after the last export;
- objects on a device that the restored database does not know about are simply
  not requested. They are not lost: they remain on the member's disk, and are the
  evidence for a manual reconciliation if someone is missing photos they
  remember.

**Members without a device have no copy.** Enrolment is mandatory precisely
because of this step: an unenrolled member's originals existed only on the node
disk, and in this scenario they are gone. The nightly export fails while any
member is unenrolled (`REQUIRE_ENROLMENT`, on by default) and names them, and
`PodArchiveDeviceNeverReported` fires for a device that never checks in — both
exist so this is discovered in advance rather than here.

**This sequence has not been drilled end-to-end.** The individual restores have
been (§7.1 and §7.4 on a lab cluster), but the full rebuild-from-nothing has not.

## 8. The offsite leg — target architecture

Nothing exists offsite today (§5 item 1). Requirements from §3: S3-compatible (so
the existing rclone replicator works unchanged), point-in-time capable, cheap at
the ~100–500 GB scale, and ideally on infrastructure independent of Hetzner.

Recommended: **Backblaze B2** ($6/TB/month, pay-per-GB — ~$1–3/month at current
data volumes). S3-compatible, native bucket versioning (turns the plain mirror
into point-in-time recovery with zero
rclone changes) plus optional object lock for ransomware-proof immutability, and a
different provider/failure domain than the cluster. Setup: create a versioned
bucket + application key, put an `rclone.conf` with `source:` (cluster Garage) and
`dest:` (B2) into `replicator-config-secret` in the `backup-replicator` namespace,
add a lifecycle rule pruning versions older than ~30 d.

Alternatives considered:
- **Hetzner Storage Box** (BX11, 1 TB, ~€3.2/month): cheapest fixed-price option and
  EU-hosted, but same provider as the cluster (correlated failure/account risk) and
  no S3/versioning — rclone would use SFTP with `--backup-dir` for point-in-time.
- **Home Garage** (spare hardware + disk at home): no recurring cost and fully
  sovereign — the design's original intent — but adds real ops burden (dyndns,
  availability, disk health) and Garage has no bucket versioning, so `--backup-dir`
  is required. Good *second* offsite copy later, weak primary.
- **Wasabi / Cloudflare R2 / Scaleway**: viable S3 targets but either minimum-charge
  (Wasabi: 1 TB min, 90 d retention min) or pricier per GB (R2) than B2 at this scale.

## 9. Code vs. live cluster — what this document can and cannot tell you

Everything above is derived statically from the manifests and is accurate as a
description of *intent*. A **new** cluster adds nothing (it would merely re-create the
same state, including the CNPG collision). What only a **long-lived** cluster
(production/dev) can answer, via read-only checks:

```bash
export KUBECONFIG=~/.smallworlds/kubeconfigs/<env>.yaml
kubectl get backups -A                                  # every cluster backing up post-serverName fix?
kubectl get cluster -A -o wide                          # WAL archiving / first-point-of-recoverability status
kubectl get secret -n backup-replicator replicator-config-secret   # is offsite replication configured? (§5 item 1)
kubectl get jobs -n backup-replicator                   # did last night's replication succeed?
kubectl get backupstoragelocation,schedule,backup -n velero        # Velero health
df -h /mnt/smallworlds-data                             # actual usage vs the 200 GB volume (on the node)
```
