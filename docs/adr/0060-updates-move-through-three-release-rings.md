---
status: proposed
---

# Updates move through three release rings

> **Partly replaced by `docs/adr/0065`:** Rings 1 and 2 no longer adopt whole
> releases through a Renovate PR. They adopt Ring 0's verified state one
> application at a time through the Update Desk. Ring 0 and everything below
> about it still hold.

Every update to a supported application passes through three **Release Rings**
before a community's Operator is asked to adopt it. The design is
`plans-and-walkthroughs/design-update-strategy.md`.

- **Ring 0, the Canary Cluster:** a Contabo Cloud VPS 8 running every supported
  application at once, with example data that is never re-seeded. It follows
  `main` automatically, verifies every change, and reverts what breaks. Nothing
  is tagged until it has stayed green there.
- **Ring 1, smallworlds.network:** the project's own community adopts every
  release on the day it is tagged, by a deliberate merge.
- **Ring 2, every other community:** adopts by a deliberate merge, when its
  **Follow Policy** proposes it (default: after 14 days), or never.

An Operator decides two things: which applications run, and the Follow Policy.

The decision rests on four judgements.

**Ring 0 tests upgrades, not installs.** Ephemeral staging
(`test-pr-locally.sh`) builds a new cluster every time, so it cannot see a
migration that breaks existing data, which is how an update hurts a community.
A cluster that keeps its data across every update is the only place that
failure shows up before a member sees it. That is why Ring 0 is persistent, and
why it is the same machine type and install route as the communities it
protects, not a laptop on a LAN.

**Ring 0 runs on its own machine.** Putting it on Ring 1's server was
considered and rejected: the platform components are cluster-wide
(cert-manager, CNPG, Traefik, Argo CD), so a canary update of them is a
production update. A second k3s on the same host collides on ports and shares
the kernel, the disk and the memory. A canary that shares the kernel cannot test
a kernel change, and a canary that runs out of memory takes production down
with it.

**Only Ring 0 rolls back automatically.** Most supported applications migrate
their database forward on start, and the previous image is not guaranteed to
read the result. On a real community, an automatic image rollback can turn a
failed update into lost data. Rings 1 and 2 instead get a database backup taken
immediately before every update, an alert when an update does not become
healthy, and a per-application rollback instruction: revert, or restore.

**Ring 1 is the project's own community.** The release notes can then carry the
evidence an Operator trusts most: "running on smallworlds.network since <date>".
The cost is that the project's own members meet every release first. They
are protected by Ring 0 having approved it, and by the same pre-update backups.

## Consequences

- The release number carries meaning (patch, minor, major) so a Follow Policy can
  treat security fixes differently from features. `bump-version.sh` refuses to
  tag a commit that is not green on Ring 0.
- Release notes are part of the release, partly generated, and structured per
  application, because they are what an Operator reads in the Renovate PR.
- No live GitOps Overlay may stay below `v1.2.44`: an older one takes its tenants
  from `main` and would be a Ring 0 with real members.
- Ring 0 holds a GitHub token that can write commit statuses and merge revert
  PRs on the base repository. It holds no member data, which is what makes that
  acceptable.
- Ring 0 and Ring 1 share an S3 provider, but Ring 0's access key must not reach
  Ring 1's buckets.
- Immich and Collabora, which exclude Gallery and Euro-Office, are not soaked on
  Ring 0. They get fresh-install tests only, and the release notes say so.
