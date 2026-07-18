# Storage & Backup Management

The single reference for the cluster's storage layout, backup chain, restore
procedures and scaling paths. Derived from the manifests in this repo (baseline:
commit `e1755fd`, 2026-07-18). This document absorbed and replaces the former
`admin-tools/restore-procedures.md` and `admin-tools/scaling-guide.md`. Related:
`doc/bases.md` (the init-job bases that provision buckets/keys).

## 1. Physical storage foundation

Everything stateful lives on **one disk**: a 200 GB Hetzner Cloud volume
(`hcloud_volume.smallworlds_data`, `prevent_destroy = true`) mounted at
`/mnt/smallworlds-data`, or on the local target a directory (default
`/var/lib/smallworlds-data`) symlinked to the same path. The volume survives VM
re-creation. Bootstrap creates three subdirectories:

| Path | Used by |
|---|---|
| `/mnt/smallworlds-data/garage` | Garage S3 data (static PV `garage-data-pv`) |
| `/mnt/smallworlds-data/immich-library` | Immich photo/video originals (static PV `immich-library-pv`) |
| `/mnt/smallworlds-data/k3s` | Symlink target of `/var/lib/rancher/k3s` — the k3s datastore **and all `local-path` PVCs** (`.../k3s/storage/`) |

So both storage classes ultimately share the same 200 GB volume:

- **`static-local`** (renamed from `hetzner-local`; it was never Hetzner-specific) —
  static/no-provisioner, `Retain`, `WaitForFirstConsumer`, node-pinned via
  `nodeAffinity` to hostnames `cc-pilot-node-01` / `smallworlds-local-node`. Its only
  job is to matchmake the two static PVs (Garage, Immich library) with their claims;
  the same manifest serves both provisioning targets because each bootstrap satisfies
  the `/mnt/smallworlds-data` contract.
- **`local-path`** (k3s default) — dynamic, used by everything else. Note: local-path
  **neither enforces nor can expand** the requested size; the numbers below are
  scheduling hints/documentation, not quotas.

## 2. Where user/app data lives

| App | File / object data | Database | Cache / other |
|---|---|---|---|
| **Nextcloud** | User files → Garage S3 bucket `nextcloud` (S3 is the *primary* object store). App code + `config.php` → chart PVC (8 Gi, local-path, `/var/www/html`) | CNPG `database` (nextcloud ns), 20 Gi ×2 | Redis Deployment, ephemeral |
| **Immich** | Originals + thumbnails → `immich-library-pvc` → static 60 Gi PV on the data volume. **Not in Garage.** | CNPG `database` (immich ns, VectorChord image via ClusterImageCatalog), 20 Gi ×2 | Redis ephemeral; ML model cache emptyDir |
| **Forgejo** | Git repositories → chart data PVC (50 Gi, local-path). LFS/attachments/avatars → Garage bucket `forgejo` | CNPG `database` (forgejo ns), 20 Gi ×2 | Redis ephemeral |
| **Plane** | **No object storage configured** — chart MinIO disabled, no S3 replacement wired in (uploads/attachments have nowhere durable to go; see gaps) | CNPG `database` (plane ns), 20 Gi ×2 | RabbitMQ StatefulSet PVC (100 Mi, local-path); Redis ephemeral |
| **Stalwart** | All mail data *and blobs* stored in PostgreSQL (`@type: PostgreSql` store in `stalwart-config`) | CNPG `database` (stalwart ns), 20 Gi ×2 | — |
| **Keycloak** | — (realm/SPI mounted from ConfigMaps) | CNPG `keycloak-db` (pgvecto.rs image), 20 Gi ×2 | — |
| **Bulwark** | Admin state → `bulwark-data` PVC (512 Mi, local-path) | — | — |
| **Jitsi, Excalidraw, Collabora, Dashboard, Hermes, Remediation** | Stateless (config via ConfigMaps; Hermes/Remediation source ships as ConfigMaps) | — | — |
| **Monitoring** | Prometheus 20 Gi, Alertmanager 2 Gi, Loki 20 Gi (all local-path StatefulSet claims) | — | Grafana ephemeral |
| **Garage itself** | 120 Gi static PV, layout capacity assigned **100 G** by `garage-init-job`, `replicationFactor: 1` | — | — |
| **k3s control plane** | Embedded datastore under `/mnt/smallworlds-data/k3s` | — | — |

### PV/PVC quota inventory

| Claim | Namespace | Size | StorageClass | Enforced? |
|---|---|---|---|---|
| `garage-data-pv` | garage-system | 120 Gi | static-local | No (static PV, shared disk) |
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

The design is a two-hop chain: **app data → in-cluster Garage S3 → offsite mirror**.

| # | Data source | Mechanism | Destination | Schedule | Retention |
|---|---|---|---|---|---|
| 1 | 5 tenant CNPG DBs (`database` in nextcloud/immich/plane/forgejo/stalwart) | Barman object store (base backup + continuous WAL, gzip) via `ScheduledBackup` | `s3://postgres-backups/` on in-cluster Garage (shared bucket, credential `garage-secret-cnpg` per namespace) | daily 02:00 | 7 d |
| 2 | Keycloak DB (`keycloak-db`) | Same, but dedicated bucket + key (custom `garage-init-job.yaml`, credential `garage-secret`) | `s3://postgres-backups-keycloak/` | daily 03:00 | 7 d |
| 3 | Kubernetes resources (all namespaces except `kube-system`) | Velero 12.x, AWS S3 plugin, `deployNodeAgent: false`, no volume snapshots | Garage bucket `velero-backups` | daily 02:00 | 720 h (30 d) |
| 4 | **All** Garage buckets (1–3 above + `nextcloud`, `forgejo`, per-tenant buckets) | `backup-replicator` CronJob: `rclone sync source: dest:` | Offsite S3 — defined entirely by the operator-supplied `replicator-config-secret` (mounted `optional: true`); **no offsite target exists yet**, see §8 | daily 04:00 | Mirror only — no versioning |
| 5 | Let's Encrypt certificates | `admin-tools/backup-certs-to-laptop.sh` / `restore-certs-from-laptop.sh` | Operator laptop `~/.smallworlds/cert-backups/<env>/` | manual (part of rebuild flow) | n/a |

What this chain **covers end-to-end** (once the offsite leg is configured):
all PostgreSQL databases — and therefore Stalwart mail, Plane/Forgejo/Nextcloud/Immich
metadata, Keycloak identities — plus Nextcloud user files and Forgejo LFS/attachments
(both live in Garage buckets), and the cluster's resource manifests.

There is also an unused building block: `bases/backup-job` is a per-bucket rclone
CronJob template (`S3_BUCKET` patched per consumer) that no tenant currently consumes;
`backup-replicator` supersedes it with a whole-instance sync. It is the natural
starting point for the PV-to-bucket jobs in §5.

## 5. What is missing for a fully working backup operation

Ranked by severity:

1. **CNPG backup destination collision (likely means only one tenant DB actually has
   working backups).** All five tenant clusters are named `database` and share
   `s3://postgres-backups/` with no `serverName` override, so they all target
   `s3://postgres-backups/database/`. CNPG/Barman refuses to archive into a
   non-empty destination belonging to another server — after the first cluster claims
   the path, the other four fail WAL archiving and scheduled backups continuously.
   Keycloak already demonstrates the fix (dedicated bucket); the cheaper repo-wide fix
   is a distinct `spec.backup.barmanObjectStore.serverName` (e.g. `<tenant>-database`)
   per cluster. *Verify on a live cluster with `kubectl get backups -A` and cluster
   status conditions before/after.*
2. **Immich originals have no backup at all.** The 60 Gi library PV is outside Garage,
   Velero has no node agent, and the replicator only mirrors Garage buckets. The
   community's photos are the single largest irreplaceable dataset and exist in
   exactly one copy on one disk. Fix per §3: a filesystem→S3 rclone CronJob into a
   Garage bucket, which the replicator then carries offsite.
3. **Forgejo git repositories are not backed up.** Only LFS/attachments reach Garage;
   the repos live in the 50 Gi local-path PVC. Same fix as Immich (or a scheduled
   `forgejo dump` into a bucket).
4. **Nextcloud `config.php` is not backed up.** User files are safe in S3, but
   `instanceid`, `secret` and `passwordsalt` in the chart PVC are needed for a clean
   restore (password-reset tokens, encryption, S3 object mapping sanity).
5. **The offsite leg does not exist yet.** `replicator-config-secret` must be
   hand-created (it is not part of `prepare-community-repo.sh` or any init job) and
   is mounted `optional: true`, so its absence only shows up as a nightly Job
   failure. No offsite storage target has been provisioned. See §8 for the plan.
6. **No backup monitoring.** There are no PrometheusRules or Alertmanager routes for
   CNPG backup/WAL-archive failures, failed `backup-replicator`/Velero runs, or
   backup age. Every failure mode above is currently silent.
7. **Mirror semantics propagate damage.** `rclone sync` mirrors deletions and
   corruption to the offsite copy within 24 h; with 7 d CNPG retention inside the
   mirrored bucket this is survivable for databases, but for the flat app buckets
   (e.g. `nextcloud`) there is no point-in-time recovery. Per §3 the offsite leg must
   provide versioning (destination-side bucket versioning, or `rclone sync
   --backup-dir` if the destination cannot version).
8. **Restore path is untested.** The procedures in §7 have never been exercised
   end-to-end; the `velero` CLI is not installed by bootstrap. A periodic restore
   drill (e.g. against a staging cluster) is the only way to know the chain works.
9. **Garage `replicationFactor: 1`.** In-cluster S3 holds a single copy; disk
   corruption on the data volume takes out both the primary data *and* hop 1 of every
   backup chain simultaneously. The offsite mirror is the only real redundancy, which
   raises the stakes on items 5–7.
10. **Plane uploads are unconfigured** (MinIO disabled, no S3 substitute) — less a
    backup gap than a data-loss-shaped functional bug; fixing it should include
    pointing Plane at a Garage bucket so uploads join the backup chain automatically.

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

> Absorbed from the former `admin-tools/restore-procedures.md`, with corrections:
> the CNPG example there referenced `garage-auth-secret` (`access-key`/`secret-key`),
> which actually holds Garage's `rpcSecret`/`adminToken` — following it verbatim
> would fail. The correct credential is `garage-secret-cnpg`
> (`accessKeyId`/`secretAccessKey`).

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
     bootstrap:
       recovery:
         source: database
     externalClusters:
       - name: database
         barmanObjectStore:
           destinationPath: s3://postgres-backups/
           # serverName must match what the original cluster archived under
           # (see gap 1 in §5 — set per-tenant serverNames before relying on this)
           endpointURL: http://garage.garage-system.svc.cluster.local:3900
           s3Credentials:
             accessKeyId:
               name: garage-secret-cnpg
               key: accessKeyId
             secretAccessKey:
               name: garage-secret-cnpg
               key: secretAccessKey
   ```
   For Keycloak use `s3://postgres-backups-keycloak/`, source `keycloak-db`, and the
   `garage-secret` credential (its custom init job's key layout).
3. Apply, wait for the cluster to become ready, then point the app at the new
   cluster (or rename/swap). Note: the operator generates a *new* `…-app` Secret for
   the recovery cluster while the restored database still contains the old
   passwords — restore the original Secret (e.g. from Velero) or reset the DB role
   password to match.

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

### 7.4 Disaster recovery (complete cluster rebuild)

1. Re-run `smallworlds-init.sh` (restore TLS certs first via
   `admin-tools/restore-certs-from-laptop.sh` to avoid Let's Encrypt rate limits —
   see the README rebuild flow).
2. ArgoCD syncs the base infrastructure; wait for Garage to be online.
3. Restore S3 buckets from offsite (§7.3).
4. Restore databases from the recovered `postgres-backups*` buckets (§7.1).
5. Let ArgoCD finish syncing; apps reconnect to restored data.

**This sequence has never been drilled end-to-end** (§5 gap 8). Treat it as a plan,
not a proven runbook, until a staging drill has validated it.

## 8. The offsite leg — target architecture

Nothing exists offsite today (§5 gap 5). Requirements from §3: S3-compatible (so
the existing rclone replicator works unchanged), point-in-time capable, cheap at
the ~100–500 GB scale, and ideally on infrastructure independent of Hetzner.

Recommended: **Backblaze B2** ($6/TB/month, pay-per-GB — ~$1–3/month at current
data volumes). S3-compatible, native bucket versioning (solves gap 7 with zero
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
kubectl get backups -A                                  # is the CNPG collision (gap 1) real? which clusters succeed?
kubectl get cluster -A -o wide                          # WAL archiving / first-point-of-recoverability status
kubectl get secret -n backup-replicator replicator-config-secret   # is offsite replication configured? (gap 5)
kubectl get jobs -n backup-replicator                   # did last night's replication succeed?
kubectl get backupstoragelocation,schedule,backup -n velero        # Velero health
df -h /mnt/smallworlds-data                             # actual usage vs the 200 GB volume (on the node)
```
