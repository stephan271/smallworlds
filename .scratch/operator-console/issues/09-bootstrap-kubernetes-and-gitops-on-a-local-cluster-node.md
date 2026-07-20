# Bootstrap Kubernetes and GitOps on a Local Cluster Node

Status: in-progress

## What to build

Take an inspected Local Cluster Node through an approved, resumable bootstrap to a working k3s and Argo CD installation using the selected GitOps Overlay. The workflow must preserve the shared behavior of the existing Local bootstrap path while making every mutation planned, checkpointed, redacted, idempotent, and verified from external state.

Covers PRD user stories 20–23, 54, and 61.

## Acceptance criteria

- [x] The node is reinspected immediately before planning, and the Change Plan explains privileged changes, data paths, exposure, downtime, and recovery behavior.
- [x] Approval is bound to the inspected node identity, pinned host key, selected release, rendered overlay, and relevant preconditions.
- [x] Execution installs the supported k3s/bootstrap contract, injects required Cluster Secrets outside Git, and configures Argo CD against the selected overlay.
- [x] SSH/bootstrap steps use durable markers and observed resource identities so a launcher or network interruption can be reinspected and resumed without repeating unsafe work.
- [x] Verification distinguishes successful command execution from observed Kubernetes readiness and GitOps convergence.
- [x] Cancellation stops only at declared safe checkpoints and reports when an atomic operation must finish.
- [x] The resulting Activity Record is structured, attributable, and free of command output or credential leakage.
- [ ] A dedicated Linux-node acceptance test completes this workflow from the browser and includes forced interruption and recovery.

## Remaining qualification

- Commit, push, and tag the prepared `v1.2.27` release, then explicitly publish its locally reproduced signed bootstrap payload.
- Recreate the secret-free acceptance overlay pinned to `v1.2.27` and repeat the clean-node browser run. It must reach Launcher's externally observed `verification-complete` state with the root Argo CD Application `Synced` and `Healthy`.

## Destructive acceptance evidence (2026-07-20)

- The Svelte browser journey created the private, secret-free overlay, acquired and verified the signed `v1.2.26` payload, inspected `egli@192.168.178.52`, and deliberately selected `/data/smallworlds-acceptance` as the persistent filesystem.
- The browser approved the exact bootstrap Change Plan. The harness waited for the remote `bootstrap-started` marker, terminated the exact Launcher process, restarted it with the same data directory, unlocked the Vault, and resumed without repeating unsafe work.
- Recovery produced `k3s-ready`, `argocd-ready`, `overlay-applied`, and `bootstrap-complete`; removed `bootstrap-interrupted`; left k3s active; and injected all four required Secret objects without reading or logging their values.
- Launcher correctly withheld `verification-complete`: the release's cert-manager webhook consumer and Trivy ServiceMonitor consumer exhausted Argo CD sync before their providers were ready. The captured failures were converted into explicit sync-wave ordering, bounded retries, and `admin-tools/test-gitops-bootstrap-ordering.sh`.
- The run was safely cancelled through the browser. The official k3s uninstaller then removed the disposable cluster, and verification confirmed no k3s process/service/binary, SmallWorlds marker/staging path, or acceptance data remained on the host.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 07](07-acquire-and-resume-verified-bootstrap-assets.md)
- [Issue 08](08-inspect-a-remote-or-same-host-local-cluster-node.md)
