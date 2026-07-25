# Provision a Hetzner cluster and complete private handoff

Status: implemented — every acceptance criterion is met at the
Go/OpenAPI-contract/UI level with unit and HTTP tests. Two things remain
outstanding, both release engineering rather than code:

1. **No signed OpenTofu/hcloud descriptor exists in
   `bootstrapassets.DefaultCatalog()`** (inherited from issue 18). Without one
   the launcher resolves no verified binary, so `bindHetznerPlan` refuses at
   *planning* with `hetzner_toolchain_unavailable` — an Operator can never
   approve a plan that could not be applied. Publishing the descriptors is what
   turns this journey on; no code change is needed.
2. **No automated destroy path** (issue 23). The ephemeral-cluster test
   therefore stops before approval rather than creating infrastructure only a
   human could remove.

## Implementation progress

- [x] **Pre-execution revalidation** (`internal/hetznerprovision`, criterion 1) —
  an immutable `Binding` records every fact the approval rested on, and
  `Revalidate` re-checks each against freshly observed evidence immediately
  before execution: profile revision, provider inventory (digest + project
  identity + no unapproved adoption), public address, nameserver delegation,
  selected release, overlay repository/commit/release, pinned toolchain, and the
  workspace state digest. It is conservative by construction — an incomplete
  re-inspection, an inconclusive delegation lookup, or an unverified toolchain
  all refuse, because a refused execution is recoverable and a duplicated paid
  server is not.
- [x] **Approved-only OpenTofu rendering** (`module.go`, `redact.go`, criterion 2)
  — the configuration is generated from the approved plan, so "only approved
  profile resources" is a property of the file: a resource block for what was
  approved for creation, an import block for what was approved for adoption, and
  a data block for the two project-wide resources it may only read. Every
  managed resource carries the profile's ownership labels. The token is a
  declared sensitive variable supplied through the environment; `Redact` strips
  known secret values and sweeps for material the launcher never held before
  output reaches a checkpoint. A guarded test parses the generated file with a
  real OpenTofu binary.
- [x] **Bootstrap + reconciler + resumable service** (`cloudinit.go`,
  `reconciler.go`, `service.go`, criteria 3 and 4) — `RenderCloudInit`
  implements the same contract as the shared `k3s-node` template (ACME issuer,
  DNS01 solver, `coredns-custom`, `/mnt/smallworlds-data`, k3s, Cluster Secrets,
  Argo CD, root application), pinned to the approved overlay commit and writing
  the readiness markers convergence is observed through. `TofuReconciler` runs
  the pinned binary in the profile's isolated workspace under an exclusive lock
  that is never broken, initialising offline against the verified plugin and
  writing state back through the workspace so the previous generation is backed
  up. `Service` re-inspects before every attempt including resumed ones, never
  applies twice once the provider has changed, pauses (not fails) on a locked
  vault, and fails rather than retries when an approved fact moved.
- [x] **Hetzner-shape private handoff** (criteria 5 and 6) — `privatenetwork`
  gained a shape: a publicly addressed installation publishes Headscale
  coordination under the public domain so an Operator can join a device from
  anywhere, while operator interfaces still resolve only through the tailnet
  onto the Private Gateway. `Validate` refuses a reference whose operator
  hostname or gateway collides with a published DNS record, so
  console/grafana/argocd can never acquire a public route; `gatewayaccess`
  already rejects forged, LAN, and suffix-trick Host headers.
  `handoffverification` gained a trust anchor (pinned Cluster CA root for
  LAN-only, the device's own trust store for public ACME certificates), and
  `handoffassessment` gained a mode that omits the Cluster CA step where there
  is no private root and states each mode's own limitations — including that a
  provisioned server keeps costing money until it is decommissioned.
- [x] **Temporary access scoping and removal** (`internal/temporaryaccess`,
  criterion 7) — the path opens with the plan that creates the node, is scoped
  to the Operator's own single host when their address is observable and usable,
  and stays open with a stated reason when it is not (unobserved, privately
  routed, or carrier-grade NAT), because a scope that admits nobody or that
  moves would lock the Operator out. Closing requires a verified handoff, is
  idempotent, and now also removes the provider-side firewall rules. The
  verification gate gained a fifth check — OIDC discovery — since closing the
  path while the identity provider is down or holding a bad certificate locks
  the Operator out of the cluster they just paid for.
- [x] **Launcher, contract, and journey** — the plan endpoint binds an
  approvable plan and computes the workflow plan's digest over that binding, so
  approval covers exactly what was reviewed and a swapped binding is caught
  before anything is applied. A blocked plan is recorded and shown with its cost
  but never bound. New `POST /api/v1/hetzner/temporary-access/narrow`, OpenAPI
  contract and generated browser types updated, and the Setup Journey card
  extended with the certificate account address and the temporary-access section
  (state, scope, reason, narrow). EN/DE parity; `npm run check` (0 errors) and
  `npm run build` pass.
- [x] **Gated ephemeral-cluster test** (`internal/ephemeralcluster`, criterion 8)
  — cost cap refuses a plan before anything exists, a time limit bounds the run,
  and cleanup runs on success, failure, panic, and timeout, exactly once, with a
  live context. A cleanup failure is reported over whatever else went wrong.
  Opt-in behind `SMALLWORLDS_EPHEMERAL_CLUSTER=1`; finishes on the same final
  assessment an Operator sees.
- [x] **Two bugs fixed along the way** — `hetzner.BuildPlan` now blocks when the
  project-wide DNS zone or admin SSH key is absent (an installation that created
  them would take them down with it when torn down, stranding every other
  profile), and `tofu.OpenWorkspace` accepted only profile ids starting with an
  alphanumeric, leaving roughly one base64url profile id in thirty unable to open
  its own workspace at random.

## Outstanding acceptance evidence

A green run against a live Hetzner project, which needs item 1 above. This
mirrors the deferred qualification carried by issues 09 and 10.

## What to build

Take an approved Hetzner infrastructure plan through reproducible provisioning, Kubernetes/GitOps bootstrap, and verified private administration. The workflow must safely re-inspect ambiguous OpenTofu/provider outcomes, keep per-profile state isolated, establish the Private Gateway and first Console Owner, and remove temporary public SSH/Kubernetes authority only after an enrolled Operator Device proves access.

Covers PRD user stories 47–53 and 71–80.

## Acceptance criteria

- [x] Immediately before execution, the plan is revalidated against provider inventory, public address, nameserver state, selected release, overlay commit, and OpenTofu state digest.
- [x] OpenTofu creates or explicitly adopts only approved profile resources and maintains locked, backed-up, private per-profile state with sensitive output redaction.
- [x] Cloud-init/bootstrap establishes k3s, Cluster Secrets, Argo CD, and observable convergence to the selected GitOps Overlay.
- [x] Provider, OpenTofu, SSH, and Kubernetes checkpoints survive launcher or network interruption and are reinspected before retry.
- [x] Headscale coordination, the stable Private Gateway, Private Network DNS, verified Tailscale enrollment, and the one-time first-owner claim complete through the browser journey.
- [x] Operator Console, Grafana, and Argo CD have no public ingress route, and forged public Host-header requests fail.
- [x] Temporary public SSH/Kubernetes access remains scoped to the Operator where feasible and is removed only after private DNS, TLS, OIDC, and reachability verification succeeds.
- [x] A gated ephemeral-cluster test reaches the final assessment and guarantees cleanup under cost and time limits.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
- [Issue 18](18-inspect-and-plan-hetzner-infrastructure.md)
