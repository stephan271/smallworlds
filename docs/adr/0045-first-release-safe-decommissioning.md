---
status: superseded
---

# Include safe decommissioning in the first release

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

The Bootstrap Launcher will provide preserve-data and full decommission journeys because it creates paid and privileged resources. Plans distinguish profile-owned from shared resources, disclose retained costs, inspect backup and Recovery Bundle status, and require stronger typed confirmation before irreversible deletion; shared DNS zones and the GitOps Overlay are retained, while only profile-owned records are removed. Local uninstall preserves its data directory by default, wiping requires a separate confirmation, and forgetting a Cluster Profile never changes external resources.
