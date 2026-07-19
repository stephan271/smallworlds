---
status: accepted
---

# Use one Lifecycle Authority per Cluster Profile

Each Cluster Profile has one authoritative Launcher Host for infrastructure lifecycle work. Authority can move through an encrypted Recovery Bundle containing the profile, infrastructure state, kubeconfig, and required secret material, but the first release will not synchronize profiles or allow concurrent OpenTofu execution across launchers. This avoids introducing remote state locking, shared secret infrastructure, and conflict resolution merely to support an uncommon bootstrap scenario; multi-Operator access to the in-cluster console remains a separate concern.
