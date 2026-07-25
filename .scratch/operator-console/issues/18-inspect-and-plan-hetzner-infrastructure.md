# Inspect and plan Hetzner infrastructure

Status: done — acceptance criteria 1–6, 8, and 9 met; criterion 7 is implemented
end to end but its *published* artifacts are outstanding release engineering:
no signed OpenTofu/hcloud-provider descriptor exists in
`bootstrapassets.DefaultCatalog()` yet, so the launcher refuses honestly (503
`hetzner_toolchain_unavailable`) rather than falling back to an ambient `tofu`
binary. Applying an approved plan to the project is issue 19: the
`launcher.HetznerProvisioner` seam is defined and exercised, and its default
refuses, so approval can never quietly become provisioning.

## Implementation progress

- [x] **hetzner domain** (`internal/hetzner`, minus `client.go`) — the pure core.
  `AssessToken` classifies a probe into valid/malformed/unauthorized/read-only/
  inconclusive/project-mismatch and identifies the token by fingerprint only; a
  malformed token is refused locally, and a token addressing a different project
  than the profile is bound to is refused rather than silently re-pointing the
  profile (criterion 1). `Expectations`/`Classify` derive every expected
  resource name from `infrastructure/terraform/main.tf`'s naming and classify
  the inventory as shared / profile-owned / adoptable / conflicting / unknown
  from provider labels and *exact* names — a similarly named resource is
  reported under `Similar` and is never adoptable (criteria 2, 3).
  `CheckDelegation` treats an unanswered lookup as unknown, never as confirmed,
  so a public installation stays blocked (criterion 4). `Requirements`/`Presets`
  derive Small/Recommended/High from a `capability.Assessment` plus node
  overhead and headroom, and `ResolveChoice` validates advanced location/type/
  volume overrides (criterion 5). `EstimateCost` prices server + volume +
  Primary IP from the *observed* catalog and always carries the notes about
  one-way volume growth and resources that stay billable (criterion 6).
  `BuildPlan` produces the immutable, cost-bearing `ChangePlan` bound to the
  inventory digest (`StillCurrent`), with blockers for unresolved adoptions,
  conflicts, similar names, missing delegation, unavailable types, and
  undersized presets. `gofmt`/`go vet`/`go test` clean.
- [x] **pinned toolchain + isolated state workspace** (`internal/tofu`) —
  criterion 7. `Release()` binds the pinned OpenTofu (1.10.6) and hcloud
  provider (1.54.0) versions into the asset-catalog release id, and
  `Inspect`/`Acquire` resolve *only* signed, digest-verified descriptors through
  the existing `bootstrapassets` boundary (`AssetSource`), keeping "no global
  prerequisites" from turning into "whatever binary is on PATH": an unpublished
  release returns `ErrToolchainUnavailable`, distinct from an integrity failure.
  `Workspace` gives each Cluster Profile its own state directory with an
  exclusive `O_EXCL` lock (never broken, holder named), atomic owner-only state
  writes that back up the previous generation (10 kept, pruned), and a `Status`
  projection that exposes no paths and no state contents — only a digest.
- [x] **read-only Hetzner API client** (`internal/hetzner/client.go`) — every
  call is a GET except one deliberately invalid `POST /ssh_keys` write-authority
  probe that the provider rejects before it can create anything (403 read-only
  vs 422 read-write). All listings follow pagination to the end (a partial
  listing is how a resource gets silently duplicated), 429 and 401/403 stay
  distinguishable from an empty project, prices/availability come from live
  `/pricing` and `/server_types`, and delegation comes from the zone's
  *delegated* nameservers. httptest contract tests cover pagination,
  permissions, rate limits, and that no error echoes the token.
- [x] **Launcher endpoints** (`internal/launcher/hetzner.go`, `server.go`,
  state migration 19 `hetzner_projects`) — `POST /api/v1/hetzner/token/validate`
  custodies only a *usable* token in the Launcher Vault and returns the verdict
  without the value; `/inspect` classifies the inventory and checks delegation;
  `/presets` and `/plan` derive capacity from the selected Cluster Capabilities
  and the live catalog; `/toolchain/acquire` verifies the pinned artifacts and
  prepares the workspace; `GET /api/v1/hetzner` is the secret-free status view.
  The plan's workflow digest binds the infrastructure plan's own digest, and the
  registered `ProvisionHetznerInfrastructure` executor re-inspects the project
  on approval and fails `plan-stale` before touching anything (criterion 8).
  OpenAPI contract and generated web types updated.
- [x] **Setup Journey UI** (`web/src/routes/+page.svelte`, `lib/api.ts`,
  `lib/i18n.ts`) — a Hetzner card shown only for the Hetzner Deployment Mode,
  driving token → inspect → presets/advanced choice → toolchain → plan →
  approve. Ownership is labelled per resource with an explicit per-resource
  adoption checkbox, similar names are shown but never adoptable, delegation and
  the cost notes are stated in full, and blockers replace the approve button
  until resolved. EN/DE parity; `npm run check` (0 errors) and `npm run build`
  pass.

## What to build

Give an Operator a read-only Hetzner inspection and infrastructure planning journey. The launcher validates a project token, discovers relevant resources and ownership conflicts, verifies public-domain prerequisites, acquires the pinned OpenTofu toolchain, and creates a capacity-aware cost-bearing Change Plan without mutating the project.

Covers PRD user stories 45–53 and 129.

## Acceptance criteria

- [ ] A Hetzner project token is stored through the Launcher Vault and validated for project identity and required read/write authority before planning.
- [ ] Inspection inventories Primary IP, DNS zone, SSH public key, firewall, volume, server, DNS records, and reverse DNS with stable provider identities.
- [ ] Existing resources are classified as shared, profile-owned, adoptable, conflicting, or unknown; similarly named resources are never silently adopted.
- [ ] Nameserver delegation is checked before a public installation can proceed.
- [ ] Small, Recommended, and High-capacity presets derive from selected Cluster Capabilities, while advanced current location, server, and volume choices remain available.
- [ ] Live availability and estimated recurring costs appear in the plan, including volume growth limitations and resources that may remain billable.
- [ ] The launcher obtains pinned verified OpenTofu/provider artifacts and prepares an isolated per-profile state workspace with locking and backup behavior.
- [ ] No provider resource changes occur until the Operator approves a still-current immutable plan.
- [ ] Contract tests cover pagination, permissions, conflicts, rate limits, state sensitivity, provider acquisition failure, and redaction.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 04](04-select-cluster-capabilities-and-preview-a-gitops-overlay.md)
- [Issue 07](07-acquire-and-resume-verified-bootstrap-assets.md)
