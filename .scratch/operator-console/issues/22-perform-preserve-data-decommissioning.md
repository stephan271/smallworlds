# Perform preserve-data decommissioning

Status: ready-for-agent

## What to build

Give the Lifecycle Authority a preserve-data decommission journey for created clusters. A fresh inspection classifies external resources, protection evidence, ownership, retained data, and continuing cost; an approved plan stops or removes only the intended compute/workload resources while preserving declared persistent data, shared DNS zones, and the GitOps Overlay. Forgetting a local Cluster Profile remains a separate non-mutating operation.

Covers PRD user stories 111–112 and 115–117.

## Acceptance criteria

- [ ] A fresh inspection classifies resources from provider IDs, tags, state, and cluster identity as profile-owned, shared, retained, or unknown rather than relying on names.
- [ ] The Change Plan lists every resource to stop/delete or retain, expected downtime, retained data, continuing provider cost, and recovery path.
- [ ] Unknown or ambiguously owned resources are retained and block automatic deletion unless resolved through a new inspection/plan.
- [ ] Hetzner mode removes only approved compute/workload resources while preserving declared persistent data and showing ongoing volume/IP costs precisely.
- [ ] Local modes uninstall SmallWorlds/k3s resources through the known profile contract while preserving the data directory by default.
- [ ] Shared DNS zones and the GitOps Overlay are retained automatically, and only proven profile-owned records may be removed.
- [ ] Interruption at each mutation checkpoint can be reinspected and safely resumed without widening the deletion set.
- [ ] Forgetting a Cluster Profile has a separate path that performs no external infrastructure or cluster mutation.

## Blocked by

- [Issue 03](03-transfer-lifecycle-authority-with-a-recovery-bundle.md)
- [Issue 09](09-bootstrap-kubernetes-and-gitops-on-a-local-cluster-node.md)
- [Issue 19](19-provision-a-hetzner-cluster-and-complete-private-handoff.md)
- [Issue 20](20-bootstrap-a-local-internet-exposed-cluster.md)
