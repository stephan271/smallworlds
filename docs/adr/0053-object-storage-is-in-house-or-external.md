---
status: accepted
---

# Object storage is in-house or external, chosen at install time

A community chooses once, when it installs, where its object data lives: in the
two in-cluster Garage instances, as before, or at an external S3 provider that
replaces both of them. With an external provider nothing bulky is kept on the
node beyond what needs a filesystem, every Recovery Point is off the machine
from the moment it is written, and the node may have a single disk. The
in-cluster mode is unchanged and remains the default; it is the mode for a
fully local installation and for an operator who will not put members' data at
a third party.

The reason is hardware. ADR 0048 put Recovery Points on a second volume, which
a rented VPS with one NVMe disk and no attachable storage cannot offer. An
external bucket is a stronger answer to the same question — it survives the
loss of the node, not only of one disk — so the two-disk rule is not relaxed
for that mode, it is satisfied by other means.

## How the choice is carried

- **The switch is one Secret.** The installer plants
  `default/smallworlds-object-storage` (endpoint, region, bucket prefix, key,
  and optionally a second set for backups) only when the operator chose a
  provider. Every init job that would mint a key on Garage looks for it first
  and, finding it, creates its buckets at the provider instead. Its absence
  means Garage. Each job logs which way it went.
- **Coordinates travel with the credential.** `garage-secret`,
  `garage-secret-cnpg` and `garage-secret-backup` keep their names and now also
  carry `endpointURL`, `endpointHost`, `useSSL`, `region` and `bucket`.
  Consumers that can read a Secret (the rclone jobs, Forgejo, Plane) take all
  of it from there and no longer contain a Garage address. The names stay
  because renaming them would touch every tenant for no gain.
- **Literal fields are patched in the overlay.** The barman `ObjectStore`,
  Velero's `BackupStorageLocation`, Nextcloud's chart-rendered environment and
  the pod-gateway ConfigMap cannot read a Secret for these values.
  `admin-tools/generate_storage_patches.py` writes those patches into the
  overlay, together with the removal of the `garage`, `garage-backup` and
  `backup-replicator` Applications and the Garage volumes. The patches sit
  between marker comments, so a re-run replaces them and choosing Garage again
  removes them.
- **The overlay's record is the answer.** `prepare-community-repo.sh` asks for
  the non-secret coordinates and records them; `smallworlds-init.sh` takes them
  from that record and asks only for the key. An overlay without Garage on a
  node that was given no provider key would have nowhere to put an object, so
  the two scripts must not be able to disagree.
- **Primary and backup stay separate.** Tenant buckets are
  `<prefix>-<tenant>`; Recovery Points go to `<prefix>-<tenant>-backup`,
  `<prefix>-postgres-backups`, `<prefix>-postgres-backups-keycloak` and
  `<prefix>-velero-backups`. A second endpoint and key for the backup side is
  optional. Without one, backups share the provider with the live data, which
  survives losing the node but not losing the account — the wizard says so.

## Consequences

- **A third party holds plaintext.** Today a provider sees backups at most;
  in external mode it holds the live files. SmallWorlds does not encrypt them.
  This is the price of the mode and the reason it is not the default.
- **Least privilege is weaker.** Garage gave each tenant a key that could reach
  only its own bucket. Externally there is one key for primary data and one for
  backups, present in every tenant namespace.
- **The provider becomes a runtime dependency.** Its outage makes files
  unavailable while the cluster is otherwise healthy, and a stalled WAL archive
  fills the node's disk exactly as it did with Garage on 2026-08-09; the
  existing `CNPGWALArchivingFailing` alert covers it.
- **The pod archive moves off the cluster.** `pod-gateway`'s bucket is a
  Recovery Point and follows the backup side, which ADR 0047 avoided on
  purpose. It is allowed with a warning rather than refused; the question of
  whether the archive should exist in this mode at all is left open.
- **Switching modes later is a data migration**, not a setting: buckets have to
  be copied between Garage and the provider before the overlay is changed.
- **The Hetzner target still creates its backup volume.** It is unused in
  external mode. Making the Terraform volumes conditional is follow-up work.

## Not decided here

Whether Immich's originals can leave the node as well (an S3-capable Immich
fork) is a separate decision. Until then the Immich library stays on the node's
disk in both modes, with `pv-backup` copying it to the backup side.

## Verification

`admin-tools/test-object-storage.py` renders the patched overlays against the
real manifests and runs the init-job scripts against stub `kubectl` and `curl`.
No install against a real provider has been made yet.
