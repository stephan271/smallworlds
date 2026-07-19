# Propose an explicit SmallWorlds release update

Status: ready-for-agent

## What to build

Let a Console Operator review and propose a compatible SmallWorlds release update without silently changing the cluster. The console surfaces a signed available release, checks launcher/cluster/catalog compatibility, presents release notes and the exact Git diff with operational risks, and opens a proposal that remains under Operator merge control.

Covers PRD user stories 33–35 and 83.

## Acceptance criteria

- [ ] Available updates come only from signed release metadata and identify the exact base tag, catalog version, immutable image/tool digests, and compatibility range.
- [ ] An incompatible launcher may inspect and export the Cluster Profile but cannot plan or execute the mutation.
- [ ] The Change Plan presents release notes, Git diff, relevant capability changes, downtime/data/exposure risks, and recovery expectations.
- [ ] Server-side role checks permit Console Operators and Owners to create the proposal and deny Observers.
- [ ] Approval opens a branch/pull request without automatic merge, force push, or direct live-cluster mutation.
- [ ] After an Operator-controlled merge, Argo and Capability Assessment evidence track convergence and expose partial or failed adoption clearly.
- [ ] No launcher, cluster, capability, or infrastructure update installs silently.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 11](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
