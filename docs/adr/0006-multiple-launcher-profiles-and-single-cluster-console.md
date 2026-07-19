---
status: accepted
---

# Manage multiple Cluster Profiles but scope each in-cluster console to one cluster

One Bootstrap Launcher may retain multiple named Cluster Profiles for production, development, local, or other installations, including enough state to resume interrupted work. Each in-cluster Operator Console is scoped only to the cluster in which it runs. This preserves the existing separation between installations without turning every cluster into a privileged fleet controller for every other cluster.
