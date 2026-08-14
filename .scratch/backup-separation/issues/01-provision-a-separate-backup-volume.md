# Provision a separate backup volume

Status: complete

## What to build

A second block volume, mounted at `/mnt/smallworlds-backup`, on both
provisioning targets. This is the physical separation ADR 0048 rests on;
everything else in this feature is repointing consumers at it.

On the Hetzner target that is a second `hcloud_volume` + attachment, sized 100 GB,
with `prevent_destroy = true` like its sibling. On the local target it is a
second directory whose backing device must differ from `DATA_DIR`'s — the
laptop reference build already has this (NVMe for operational, a second disk for
bulk).

Both bootstraps must **refuse to continue** when the two paths resolve to the
same block device. A silently co-located backup volume looks identical to a
correctly separated one until the day it is needed, which is exactly the class
of failure this feature exists to remove.

Teardown already copes: `destroy-cluster.sh` rewrites `prevent_destroy = true`
to `false` with a global sed before `terraform destroy`, so a second volume
carrying the same guard is torn down without further work. Worth confirming
rather than assuming, since a volume that survives teardown turns the next
"clean" rebuild into a dirty one.

## Acceptance criteria

- [x] `infrastructure/terraform/main.tf` declares `hcloud_volume.smallworlds_backup`
      (100 GB, ext4, `prevent_destroy = true`) and its attachment with `automount`.
- [x] `infrastructure/cloud-init/k3s-node.yaml.tpl` mounts it at
      `/mnt/smallworlds-backup` and creates the `garage-backup` subdirectory.
- [x] `infrastructure/local/bootstrap-local-node.sh` accepts `BACKUP_DIR`
      (default `/var/lib/smallworlds-backup`), symlinks it to
      `/mnt/smallworlds-backup`, and creates the same subdirectory.
- [x] Both bootstraps compare `df --output=source` for the data and backup paths
      and exit non-zero with an explanatory message when they match.
- [x] `smallworlds-init.sh` prompts for or derives the backup location on the
      local target and passes it through.
- [x] `doc/storage-and-backup.md` §1 documents both volumes and which datasets
      live on each.
- [x] `admin-tools/destroy-cluster.sh` still removes both volumes; a rebuild
      afterwards starts genuinely empty.
- [x] `terraform validate` passes; `bash -n` passes on both shell scripts.
