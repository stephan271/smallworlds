# Add a Community Application through a Git proposal

Status: ready-for-agent

## What to build

Let a Console Operator add one currently disabled Community Application after bootstrap. The proposal journey uses live capacity plus catalog dependency, exposure, and protection data, presents the exact Desired Configuration diff, opens a Git proposal, and follows the application through merge, Argo delivery, runtime readiness, access, and protection assessment. Removal remains unavailable.

Covers PRD user stories 32, 43–44, 83, and 95.

## Acceptance criteria

- [ ] Only disabled optional Community Applications are offered, and required dependencies are included or explained before planning.
- [ ] The plan compares estimated resource needs with current capacity and discloses exposure, persistent data, and protection implications.
- [ ] Server-side authorization permits Operators and Owners but rejects Observers and users without a Console Role.
- [ ] Approval opens a branch/pull request containing the exact catalog-derived Git diff and does not mutate live Kubernetes resources directly.
- [ ] Proposal state and remote commit identity appear in the Activity Record, and merge is observed rather than performed automatically.
- [ ] After merge, Argo, runtime, access, and protection evidence drive the new Capability Assessment and remediation routes.
- [ ] Installed applications have no removal or disable action in the first release.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 11](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
