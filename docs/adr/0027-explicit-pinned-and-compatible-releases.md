---
status: accepted
---

# Pin cluster releases and require explicit compatible upgrades

New Cluster Profiles default to the latest stable signed SmallWorlds release but may select an older release; the GitOps Overlay pins an exact tag and immutable image digests, and the Cluster Capability catalog is versioned with that release. Cluster and launcher upgrades are never silent: Renovate and the Operator Console present proposals, release notes, compatibility checks, and Change Plans, while signed launcher updates require consent. A launcher outside a profile's supported release range may still inspect and export it but must refuse mutations until compatible.
