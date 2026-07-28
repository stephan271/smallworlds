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

## Comments

### 2026-07-28 — What is already built, and the three gaps found while updating a live cluster

Found while taking a real LAN cluster from v1.2.29 to v1.2.30 by hand. The last
step of that update had no supported path in the console, so it ended at
`kubectl`. Recording why, so this issue starts from evidence rather than from
the acceptance criteria alone.

**Already built and green:** `internal/releaseupdate/` (416 lines, tested)
resolves signed release metadata, checks launcher/cluster/catalog
compatibility, builds a Change Plan with notes, Git diff, risks and recovery,
and assesses adoption afterwards. `internal/console/releaseupdate.go` carries
the matching handlers — profile, export, available, plan, approve, propose,
adoption.

**Gap 1 — nothing ships the cluster-side console.** `internal/console` is
imported by no `main` package, `cmd/` holds only `smallworlds-admin`, there is
no console tenant under `infrastructure/kubernetes/tenants/` and no Dockerfile
in the repo. The handlers compile and are unreachable. This is the Issue 11
dependency, and it is the reason the rest went unnoticed.

**Gap 2 — the plan writes a file nothing reads.** `BuildPlan` produces exactly
`smallworlds-release.yaml` (`releaseupdate.go:285,307`). A search across Go,
manifests and scripts finds no reader anywhere in the repo. What actually
decides which base release a cluster runs is different: the `?ref=` pins in the
overlay (root kustomization, one per application, plus `smallworldsRelease` in
`overlay-config.yaml`) and the root Application's `targetRevision`. A proposal
built from the pins file would merge without changing the deployed release.

`admin-tools/bump-overlay-release.sh` now performs exactly those edits, verified
byte-for-byte against `capability.RenderOverlay`. It is the shape `BuildPlan`
should produce.

**Gap 3 — nothing sets `targetRevision` after the merge.** The root Application
is pinned to the reviewed overlay commit, deliberately — `HEAD` must never
change an approved deployment. It is written only at provisioning time
(`bootstrap-local-node.sh`, `hetznerprovision/cloudinit.go:163`) and never
again. Even a correct overlay proposal therefore leaves the cluster on the old
commit until somebody patches the Application by hand. Note the root
Application is not part of the base kustomization, so such a patch is not
reverted — which is what makes the manual workaround work at all.

**Suggested order.** Gap 2 first: it is contained, the package already has
tests, and it makes the three existing handlers meaningful. Then Gap 3 as an
explicitly approved step — it needs cluster access, so it belongs either to the
cluster-side console or to the Launcher while it still holds that access. Gap 1
stays with Issue 11.
