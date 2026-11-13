---
status: accepted
---

# Pin cluster releases and require explicit compatible upgrades

> **Still holds; the console parts do not.** Clusters are still pinned and never
> updated silently. `docs/adr/0060` (Release Rings) and `docs/adr/0065` (per-
> application pins to commits Ring 0 verified, adopted on the Update Desk)
> refine how. The console is gone (`docs/adr/0062`).

New Cluster Profiles default to the latest stable signed SmallWorlds release but may select an older release; the GitOps Overlay pins an exact tag and immutable image digests, and the Cluster Capability catalog is versioned with that release. Cluster and launcher upgrades are never silent: Renovate and the Operator Console present proposals, release notes, compatibility checks, and Change Plans, while signed launcher updates require consent. A launcher outside a profile's supported release range may still inspect and export it but must refuse mutations until compatible.
