---
status: superseded
---

# Manage multiple Cluster Profiles but scope each in-cluster console to one cluster

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

One Bootstrap Launcher may retain multiple named Cluster Profiles for production, development, local, or other installations, including enough state to resume interrupted work. Each in-cluster Operator Console is scoped only to the cluster in which it runs. This preserves the existing separation between installations without turning every cluster into a privileged fleet controller for every other cluster.
