---
status: superseded
---

# Use one Lifecycle Authority per Cluster Profile

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

Each Cluster Profile has one authoritative Launcher Host for infrastructure lifecycle work. Authority can move through an encrypted Recovery Bundle containing the profile, infrastructure state, kubeconfig, and required secret material, but the first release will not synchronize profiles or allow concurrent OpenTofu execution across launchers. This avoids introducing remote state locking, shared secret infrastructure, and conflict resolution merely to support an uncommon bootstrap scenario; multi-Operator access to the in-cluster console remains a separate concern.
