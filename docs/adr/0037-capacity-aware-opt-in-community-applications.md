---
status: accepted
---

# Make Community Applications capacity-aware and opt-in

> **Still holds; the console parts do not.** Community Applications are still
> opt-in, chosen in the Setup Wizard (`admin-tools/setup-wizard/plan.py`) and
> `prepare-community-repo.sh`. The Setup Journey is gone (`docs/adr/0051`).

Platform Services required for a functioning cluster remain selected, while Community Applications become opt-in through transparent Minimal, Collaboration, Full, and Custom selections. The Setup Journey will show estimated memory, storage, and network-exposure implications and evaluate them against the Deployment Mode and Cluster Node capacity. A Platform-Service-only cluster is valid, and applications can be proposed later through the GitOps Overlay; the launcher will no longer default every optional workload on.
