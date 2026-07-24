# Add a Community Application through a Git proposal

Status: done — acceptance criteria 1–7 met against injected seams; the live
capacity reporter and Git-proposal opener are deferred to the live-cluster
integration exactly like issue 11's in-cluster console adapters (the console is
not yet wired into `cmd/smallworlds-admin`), tracked as outstanding integration
evidence, not a code dependency.

## Implementation progress

- [x] **Add-capability domain planner** (`internal/addcapability`) — the pure,
  table-tested core covering acceptance criteria 1, 2, 4, and 7. `Offers` returns
  only capabilities that are Community Applications, optional (not required),
  observed **disabled**, and supported in the deployment mode — platform services
  and already-present capabilities are never offered, and there is no
  remove/disable surface (criterion 7). `BuildPlan` walks the dependency graph so
  dependencies precede dependents, pulls disabled dependencies into the same plan
  while recording already-present ones separately (criterion 1), compares the
  summed resource footprint against live `Capacity` headroom per resource without
  ever refusing on the operator's behalf (criterion 2), and discloses the distinct
  exposure/protection classes and the capabilities holding persistent data
  (criterion 2). It renders the **exact catalog-derived** Desired Configuration —
  purely additive per-app overlay units (the app's ArgoCD Application manifest
  repointed at the operator's overlay, plus the tenant reference at the pinned
  release) — so the reviewed diff equals what is committed (criterion 4). Unit
  tests cover the offer filter (disabled/optional/mode), the dependency closure,
  the capacity fit and shortfall, the additive secret-free diff (no removal lines,
  no secret-like fields, pinned release, overlay repoURL), and the not-offered and
  invalid-overlay-target guards. `gofmt`, `go build/vet/test ./...` all pass.

- [x] **Console offer/plan/approve/propose endpoints + seams**
  (`internal/console/addcapability.go`, `console.go`) — covers acceptance criteria
  3, 4, and 5. All four routes sit behind the server-side `Propose` permission, so
  Operators and Owners are admitted while Observers and users without a Console
  Role are rejected 403/401 (criterion 3). `GET /api/v1/additions/offers` lists the
  offers from the observed capability states; `POST /api/v1/additions/plan` reads
  live capacity through an injected `CapacityReporter`, builds the plan, and
  persists a compact approvable `consoleworkflow.ChangePlan` whose secret-free
  summary binds the target **and the exact diff digest** into the plan digest;
  `POST /api/v1/additions/{id}/approve` binds the operator's approval to that
  digest; `POST /api/v1/additions/{id}/propose` re-derives the catalog-derived diff
  and refuses (409 `addition_plan_mismatch`) if it no longer matches the approval,
  then opens a branch/pull request through an injected `ProposalOpener` — never
  touching live Kubernetes resources (criterion 4). The proposal is recorded as a
  Workflow Run left **running** (the merge is a human step the console only
  observes) with the provider + remote commit identity in the Activity Record,
  surfaced by `GET /api/v1/proposals` (criterion 5). Both live seams default to
  honest-refusal adapters (503 `capacity_unavailable` / `proposal_unavailable`),
  deferred like the console's other cluster seams. HTTP tests cover the full
  happy path (offer→plan→approve→propose→Activity Record with the commit), the
  authorization matrix, the capacity/proposal unavailable refusals, the
  approval-binds-exact-diff mismatch when cluster state drifts, the post-merge
  evidence-driven Capability Assessment (criterion 6), and the absence of any
  removal action (criterion 7). `gofmt`, `go build/vet/test ./...` all pass.

- [x] **Add Application journey UI** (`web/src/routes/console/+page.svelte`,
  `web/src/lib/console.ts`, `web/src/lib/console-i18n.ts`) — the propose-gated
  "Add application" view, EN/DE. Only sessions with the `propose` permission see
  the tab. An Operator picks a disabled Community Application (its exposure,
  persistent-data flag, resource footprint, and the disabled dependencies it would
  pull in are shown), reviews the plan (added capabilities, already-present
  dependencies, needed-vs-available memory/storage with a fits/exceeds cue, the
  exposure/persistent-data/protection disclosures, and the exact Git diff), then
  approves and opens the Git proposal in two explicit steps. The result surfaces
  the provider, branch, commit, and pull-request link, and states plainly that the
  console will not merge — the operator merges and the new application then follows
  its own Argo/runtime/access/protection evidence in the console (criterion 6).
  Capacity- and proposal-unavailable and plan-mismatch errors map to clear
  messages. `npm run check` and `npm run build` pass.

## Remaining

- [ ] Wire the live `CapacityReporter` (node allocatable/used) and `ProposalOpener`
  (GitHub / generic-git overlay credentials) when the in-cluster console is wired
  into `cmd/smallworlds-admin`, and add an end-to-end journey test against a live
  overlay. Deferred with issue 11's console live adapters.

## What to build

Let a Console Operator add one currently disabled Community Application after bootstrap. The proposal journey uses live capacity plus catalog dependency, exposure, and protection data, presents the exact Desired Configuration diff, opens a Git proposal, and follows the application through merge, Argo delivery, runtime readiness, access, and protection assessment. Removal remains unavailable.

Covers PRD user stories 32, 43–44, 83, and 95.

## Acceptance criteria

- [x] Only disabled optional Community Applications are offered, and required dependencies are included or explained before planning.
- [x] The plan compares estimated resource needs with current capacity and discloses exposure, persistent data, and protection implications.
- [x] Server-side authorization permits Operators and Owners but rejects Observers and users without a Console Role.
- [x] Approval opens a branch/pull request containing the exact catalog-derived Git diff and does not mutate live Kubernetes resources directly.
- [x] Proposal state and remote commit identity appear in the Activity Record, and merge is observed rather than performed automatically.
- [x] After merge, Argo, runtime, access, and protection evidence drive the new Capability Assessment and remediation routes.
- [x] Installed applications have no removal or disable action in the first release.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 11](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
