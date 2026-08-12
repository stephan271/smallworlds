# Personal data pods — the append-only archive

Each member can own a copy of their own source data on hardware in their own
home. An app exports into a per-user pod it can write but never read; the
member's device pulls from that pod and never deletes. See
`plans-and-walkthroughs/design-personal-data-pods.md` for why, and
`docs/adr/0047-pod-archive-is-an-append-only-object-protocol.md` for the choice
of substrate.

```
Immich ──read-only──> immich-pod-export ──append──> pod-gateway ──> Garage
  (CNPG role + library PVC)   (CronJob, nightly)     (per-user prefix)
                                                            │
                              home device ──pull, outbound only, read-only──┘
```

## What the archive is and is not

It holds **source** data: bytes that are irreplaceable because nothing can
recompute them. Immich pixels qualify. Embeddings, thumbnails and transcodes do
not — they are derived, and regenerating them costs only compute.

It does **not** hold user intent: album membership, the name given to a face,
tags, folder organisation. Those are irreplaceable too, but they are mutable and
relational, they have no per-user cut, and there is no tooling to reconstruct a
database from them. They stay in the central database and remain covered by the
normal CNPG backup, which is therefore **not** optional.

### What Immich exports, exactly

Every asset the user owns with `status = 'active'`, excluding external-library
assets (not Immich's files to copy) and offline ones (the row outlived the
file). Trashed and deleted assets are not newly offered; anything already in a
pod stays there, because nothing can remove it.

On disk Immich names files by asset UUID under
`/data/upload/<user>/<xx>/<yy>/<uuid>.jpg`, so the human-readable filename
exists only in the database. The pod is where the owner gets it back:
`immich/2025/2025-10-18/e1f69ddf-20251018_102709.jpg`.

Assets in Immich's **locked folder** are PIN-protected in the app, so they are
exported under `immich-locked/` rather than flattened in with everything else.
They are still exported — excluding them would leave the owner's most private
photos as the only ones with no copy of their own — but a member should know
they land on the device as ordinary files, which is one more reason to encrypt
the disk.

For Immich, the pod archive is now the only server-side copy of the originals —
`pv-backup` no longer mirrors the library PVC, and the `pods` bucket is not
replicated offsite. That leaves the pixels in two failure domains, the node disk
and the member's device. It is a deliberate trade: see the callout in
`doc/storage-and-backup.md` §7.

## The protocol

Base URL is `https://pod.<domain>`. All requests carry `Authorization: Bearer
<token>`. There are exactly two kinds of principal and they share no verbs.

| Principal | May | May not |
|---|---|---|
| **Agent** (an app's exporter) | `PUT` new objects into any pod | Read anything, overwrite, delete |
| **Device** (a member's hardware) | Read the manifest and objects of **one** pod | Write anything, read another pod |

| Method | Path | Who | Notes |
|---|---|---|---|
| `PUT` | `/pod/v1/{user}/objects/{key}` | agent | `201` on success, **`409` if the key exists**. `X-Pod-Sha256` is verified before anything is stored; `X-Pod-Source` must be one the token is scoped to. |
| `GET` | `/pod/v1/{user}/manifest?since={seq}&limit={n}` | device | Hash-chained entries after `seq`. |
| `GET` | `/pod/v1/{user}/objects/{key}` | device | Streams the bytes. |
| `POST` | `/pod/v1/{user}/heartbeat` | device | `{last_seq, objects, free_bytes}` → Prometheus. |
| `GET` | `/healthz`, `/metrics` | anyone | Liveness and metrics. |

The `409` is load-bearing rather than an error case. An agent holding only
`Append` cannot read the pod to work out what it has already sent, so it simply
re-offers everything and lets the gateway reject duplicates. Export is therefore
idempotent without any read grant, and a lost cursor costs a slow run rather
than a wrong one.

### The manifest chain

Every append writes an immutable entry at `{user}/manifest/{seq:012d}.json`:

```json
{
  "seq": 42, "user_id": "…", "key": "immich/2026/2026-08-12/ab12cd34-IMG_1234.jpg",
  "sha256": "…", "size": 3847221, "content_type": "image/jpeg",
  "source": "immich", "created_at": "2026-08-12T01:15:04Z",
  "prev_hash": "…", "entry_hash": "…"
}
```

`entry_hash` is the SHA-256 of the canonical JSON of the entry without that
field; `prev_hash` is the predecessor's `entry_hash`, with the genesis entry
using 64 zeros. A device that has pulled up to sequence *n* can verify that
entries *n+1…* extend exactly the history it holds. **Removing, reordering or
rewriting any entry breaks the chain and the device stops** rather than
accepting a rewritten past. This is the property Garage cannot provide on its
own: its keys are per-bucket `read`/`write`/`owner` with no prefix scoping, and
`write` includes `DeleteObject`.

## Enrolling a member

```bash
# Once per community: mint the Immich exporter's append-only token.
./admin-tools/pod-enroll-device.sh --agent immich

# Once per member device.
./admin-tools/pod-enroll-device.sh --user <immich-user-id> --name alice-pi
```

The second command mints a device token, records only its SHA-256 digest in the
`pod-gateway-tokens` Secret, adds the user to the `immich-pod-users` ConfigMap
so the exporter starts including them, and prints a one-time enrolment string.
Tokens are never stored in recoverable form — a lost one is re-minted, not
recovered.

The member runs, on their own hardware:

```bash
sudo ./install.sh '<enrolment-string>'    # from admin-tools/pod-device/
```

## The home device

A **Raspberry Pi 5 (8 GB) with an NVMe HAT and a 2 TB NVMe** is the reference
build: enough throughput to keep up with a photo library, low enough power to
leave on. An old mini-PC or laptop works equally well, as does a NAS that can run
a Python script on a timer. The agent is a single standard-library Python file on
a 15-minute systemd timer.

Three properties matter more than the hardware:

- **Outbound only.** The device connects *to* the cluster. No inbound ports, no
  dynamic DNS, no exposure of the member's home network. It also means the
  device cannot write to or harm the cluster.
- **Copy, never sync.** There is no delete path in the agent. A mass-delete or
  ransomware event on the server does not propagate.
- **Verify everything.** Each object is checked against the manifest digest
  before it is put in place, and `--verify-only` re-hashes the whole local copy
  against the manifest on demand.

Encrypt the disk. The archive is stored as plain files, so whoever holds the
device can read every photo on it — the gateway's guarantees end at the member's
front door.

## Operating it

```bash
# Is the gateway healthy, and are devices still reporting in?
kubectl -n pod-gateway logs deploy/pod-gateway
curl -s https://pod.<domain>/metrics | grep pod_gateway_

# Run an export now instead of waiting for 01:15.
kubectl -n immich create job --from=cronjob/immich-pod-export pod-export-manual
kubectl -n immich logs job/pod-export-manual -f

# On the device.
journalctl -u pod-archive.service -f
sudo -u podarchive /usr/local/lib/pod-archive/pod-agent.py --verify-only
```

Metrics worth alerting on: `pod_gateway_device_heartbeat_age_seconds` (a device
that has stopped reporting is a copy that has silently stopped existing),
`pod_gateway_orphan_objects` (see below), and `pod_gateway_denied_total`.

### Known limitations

- **Orphaned objects.** If the gateway stores an object and then fails to write
  its manifest entry, the object exists but no device will ever see it, and a
  retry of the same key hits the `409`. The gateway counts these in
  `pod_gateway_orphan_objects` and logs the key. It does not clean them up,
  because this code deliberately has no delete path. Resolve by hand.
- **One writer.** The chain is serialised by an in-process per-user lock, so the
  gateway runs a single replica. A second replica could fork the chain.
- **Uploads are spooled** to verify their digest before storing, so the gateway
  needs ephemeral storage for the largest single asset (`MAX_OBJECT_BYTES`,
  4 GiB by default).
- **Immich schema coupling.** `pod-export/inventory.sql` is the only place that
  knows Immich's schema, and it asserts the table and every column it needs
  before selecting anything. An Immich upgrade that renames one fails the job
  loudly. This is not hypothetical — Immich's move to Kysely renamed the table
  to `asset`, singular, while leaving its columns camelCase. Two other facts are
  pinned by configuration rather than by the query and are worth re-checking on
  upgrade: `IMMICH_MEDIA_ROOT` (`/data`, where immich-server mounts the library
  PVC) and the `-q` flag on psql, without which the schema assertion's `DO` tag
  lands in the inventory.
- **No encryption at rest in the pod.** Objects are stored as-is, so the gateway
  and anyone with the Garage credential can read them. Per-user `age` encryption
  is the natural next phase; it would close the metadata leak too. Note that
  pods do not move the live-admin plaintext ceiling in any case — the server
  already holds the data it exports.

## Adding a second source app

Nextcloud files and Stalwart mail are the obvious next ones — mail is the best
structural fit of all, since messages are immutable by nature. The shape is the
same each time:

1. Mint an agent token scoped to the new source.
2. Produce a per-user inventory with a stable key per item, isolating any schema
   knowledge in one file.
3. Append with `X-Pod-Source: <app>`, treating `409` as success.
4. Choose keys that are unique and browsable — the member sees these as
   directories on their device.

Only ever export the **source** column. If it can be recomputed, leave it in the
app; if it is mutable intent, it belongs in the database, not the pod.
