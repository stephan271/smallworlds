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

### 2026-07-28 — Gap 2 closed, and it was worse than recorded

Following the thread found a third renderer, in the sibling flow. Both are now
gone, merged into `capability.RenderChange` (commit `d6c614c`).

**What Gap 2 actually was.** Three places wrote overlay content; only
`capability.RenderOverlay` was right. Beside the pins file recorded above,
`addcapability` wrote `applications/<app>.yaml` — no overlay root has ever
included an `applications/` directory, so a merged proposal from that flow would
have created nothing at all — and its tenant units carried no domain patches, so
whatever it did create would have been published on this project's hostnames
instead of the operator's. Both failures could only ever have surfaced as a
merged pull request that changed nothing, which is the worst way to find out.

**What changed.** `RenderChange` renders the overlay at the proposed input and
diffs it against the current one, line by line. `addcapability.OverlayTarget`
and the new `releaseupdate.Overlay` both carry the domain, because a proposal
that cannot name the operator's hostnames cannot be correct.
`addcapability/proposal.go` and `releaseupdate`'s `renderPins`/`PinsPath` are
deleted.

**What holds it.** Parity tests in `internal/capability/overlay_parity_test.go`
compare each flow's proposal against a freshly established overlay, file by
file, in the same spirit as the Go/Python domain-patch parity test — which
exists for exactly this reason, and whose absence here is why three renderers
could drift unnoticed. Each was only ever tested against itself.

**Still open, unchanged:** Gap 3 (nothing sets the root Application's
`targetRevision` after the merge, so adoption remains a manual `kubectl patch`)
and Gap 1 (the cluster-side console ships from nothing — Issue 11).

### 2026-07-28 — Gap 3 closed for the Launcher

`internal/overlayadoption` (commit `bc0f44e`) carries a reviewed and merged
overlay commit to the cluster: it repoints the root Application's
`targetRevision`, reads the revision back, and records it only if the cluster
agrees. Only a full commit is accepted — a branch or tag can be moved under a
running cluster afterwards, which is why the pin exists — and the patch moves
one field rather than replacing an object that carries the installation's own
identity.

The decision lives in its own package precisely so both consoles can use it. The
Launcher exposes it at `POST /api/v1/overlay/adopt` and runs it over the same
trusted SSH path as every other privileged step, with the same node-identity and
sudo checks. The cluster-side console can reuse the package unchanged when it
ships; it needs only its own way of running a privileged command, which it will
have from inside the cluster.

Adoption is deliberately separate from proposing. Merging happens in the
Operator's own Git provider, and the console does not watch for it and act by
itself: adopting is a second, explicit approval that a reviewed commit may
become what the cluster runs.

**Still open:** no interface offers it yet — the endpoint exists, the button
does not — and Gap 1 (the cluster-side console ships from nothing, Issue 11).
