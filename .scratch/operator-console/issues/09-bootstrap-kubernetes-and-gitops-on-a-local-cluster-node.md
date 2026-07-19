# Bootstrap Kubernetes and GitOps on a Local Cluster Node

Status: ready-for-agent

## What to build

Take an inspected Local Cluster Node through an approved, resumable bootstrap to a working k3s and Argo CD installation using the selected GitOps Overlay. The workflow must preserve the shared behavior of the existing Local bootstrap path while making every mutation planned, checkpointed, redacted, idempotent, and verified from external state.

Covers PRD user stories 20–23, 54, and 61.

## Acceptance criteria

- [ ] The node is reinspected immediately before planning, and the Change Plan explains privileged changes, data paths, exposure, downtime, and recovery behavior.
- [ ] Approval is bound to the inspected node identity, pinned host key, selected release, rendered overlay, and relevant preconditions.
- [ ] Execution installs the supported k3s/bootstrap contract, injects required Cluster Secrets outside Git, and configures Argo CD against the selected overlay.
- [ ] SSH/bootstrap steps use durable markers and observed resource identities so a launcher or network interruption can be reinspected and resumed without repeating unsafe work.
- [ ] Verification distinguishes successful command execution from observed Kubernetes readiness and GitOps convergence.
- [ ] Cancellation stops only at declared safe checkpoints and reports when an atomic operation must finish.
- [ ] The resulting Activity Record is structured, attributable, and free of command output or credential leakage.
- [ ] A dedicated Linux-node acceptance test completes this workflow from the browser and includes forced interruption and recovery.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 07](07-acquire-and-resume-verified-bootstrap-assets.md)
- [Issue 08](08-inspect-a-remote-or-same-host-local-cluster-node.md)
