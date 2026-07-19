---
status: accepted
---

# Use a local launcher and an in-cluster Operator Console

SmallWorlds will present one browser-based Operator Console across its lifecycle. Before a cluster exists, a native Bootstrap Launcher serves the interface locally and performs machine-level provisioning; after bootstrap, an in-cluster deployment provides day-two functionality through the Private Network. A purely hosted web application cannot safely perform local Git, SSH, infrastructure, or network-client setup, while a conventional desktop-only UI would unnecessarily couple the product to a desktop framework.

## Consequences

The provisioning and observation workflows must live behind interfaces shared by CLI and web callers rather than inside either frontend. The Bootstrap Launcher and in-cluster deployment may use different adapters and privileges even when they share presentation and domain logic.
