---
status: accepted
---

# Central Immich backup covers every member

Immich originals will again have an all-user central Recovery Point: the
read-only library PVC is mirrored to the `immich/pv-backup/library` prefix on
`garage-backup`, and the `immich` backup bucket is included in the explicit
offsite replication allow-list. The append-only pod archive remains in place as
a member-owned copy, but it is not the cluster restore source. A cluster
recovery must not depend on every member having enrolled, powered, and returned
a personal device, and it must be possible to exercise one bounded restore for
all members from the same backup chain as the application database.

This reverses ADR 0047 only where it made the pod archive the sole server-side
copy. Its append-only protocol, per-user isolation, and home-device ownership
remain unchanged. It also amends ADR 0048's byte accounting: the operational
library and its central Recovery Point deliberately occupy separate volumes,
because physical separation and complete recovery coverage are worth the
duplicate bytes. The central copy follows the same retention and access
controls as the other cluster backups.
