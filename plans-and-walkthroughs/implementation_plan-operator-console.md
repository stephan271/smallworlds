# SmallWorlds Operator Console — Implementation Plan

**Status:** ready for implementation; OD-001 remains a scheduled decision gate

This plan was sharpened through a `grill-with-docs` session. Accepted architectural decisions live in `docs/adr/`; domain language lives in `CONTEXT.md`.

## Accepted product requirements

- The first release ships complete English and German interfaces. English is the canonical source locale, browser locale selects the initial language, and Operators can override it. Backend errors use stable codes and parameters rather than translated prose as their interface contract.
- WCAG 2.2 AA is a release criterion. The interface supports complete keyboard navigation, visible focus, screen-reader labels and live-progress announcements, non-color state cues, reduced motion, high contrast, and responsive status/diagnostics down to phone width. Setup and Change Plan workflows are optimized for desktop/tablet but remain functional on mobile. Both languages receive equivalent accessibility coverage.
- Existing Cluster Import is deferred; the first-release acceptance matrix covers newly created Cluster Profiles. The future capability remains defined in `CONTEXT.md`.
- The first in-cluster release is observe/configure/propose oriented, with only the bounded Runtime Actions recorded in ADR-0030.
- The future Offline Bundle remains an explicit roadmap capability rather than an initial-release requirement.
- Stable releases require current evidence for all three Deployment Modes. Pull requests run deterministic unit, contract, rendering, browser, and local Kubernetes tests without cloud mutation. A manually triggered credential-protected workflow provisions and destroys an ephemeral Hetzner cluster under cost/time limits; a dedicated Linux runner tests Local LAN-only over SSH; Local internet-exposed mode uses a recorded manual release test because router behavior is not reproducible in ordinary CI.

## Open decision gates

### OD-001 — Distribution of release-specific bootstrap assets

**Decision deadline:** before implementing the first real Hetzner or Local provisioning adapter and release pipeline.

The Bootstrap Launcher needs OpenTofu configuration, cloud-init and Local-node payloads, GitOps Overlay templates, the Cluster Capability catalog, and bootstrap manifests compatible with the SmallWorlds release it installs.

Options still under consideration:

1. Embed the assets in the signed launcher. This is operationally simple and self-contained, but couples new-cluster creation to the launcher's bundled SmallWorlds release.
2. Fetch a separately signed, versioned asset archive. This lets one compatible launcher create multiple SmallWorlds releases, but adds an artifact format, download/cache lifecycle, and signature chain.

Early implementation must consume an internal read-only asset-source interface and a filesystem test adapter. Before this gate closes, prototype both release adapters and compare reproducibility, binary/artifact size, release automation, offline-cache behavior, downgrade support, and failure modes. Do not expose the asset-source choice in the browser interface.

## 1. Outcome

Build one coherent SmallWorlds administration product with two execution surfaces:

1. A cross-platform Bootstrap Launcher runs on the Operator's computer, serves a Svelte 5 interface on loopback, and establishes or decommissions clusters without globally installed tooling.
2. An in-cluster Operator Console serves the same visual language through the Private Gateway and provides observation, setup completion, GitOps proposals, backup setup/status, enrollment management, and a deliberately bounded set of Runtime Actions.

The existing Homepage deployment remains the Member Dashboard. It is not renamed, extended with privileged features, or granted additional write authority.

### First-release success statement

An Operator can download one native launcher, create a new Cluster Profile, and establish any of the three Deployment Modes without first installing Git, `gh`, Terraform/OpenTofu, `kubectl`, or a JavaScript runtime. The Setup Journey reaches an evidence-backed operational state, enrolls the first Console Owner, makes operator interfaces reachable only through the Private Gateway, verifies offsite protection, and can later decommission the installation safely.

## 2. Scope

### Included

- Native Launcher builds for Linux x86-64/ARM64, macOS Intel/Apple Silicon, and Windows x86-64.
- SvelteKit with Svelte 5 and `adapter-static`, embedded in a Go executable.
- Multiple Cluster Profiles on one Launcher Host; one cluster per in-cluster console.
- New-cluster setup for Hetzner-hosted, Local LAN-only, and Local internet-exposed modes.
- Remote Local Cluster Node installation over SSH from every Launcher platform; same-host installation only on supported Linux.
- First-class GitHub plus generic existing HTTPS Git repositories.
- Managed OpenTofu tooling for Hetzner.
- GitOps Overlay creation, initial commit, later branches/proposals, and Argo CD observation.
- Declarative Cluster Capability catalog and capacity-aware selection.
- Private Gateway, Headscale/Tailscale device enrollment, Private Network DNS, and private-only operator tools.
- Keycloak OIDC with Observer, Operator, and Owner Console Roles.
- Explainable Capability Assessments.
- Backup configuration, coverage/freshness status, and bounded validation.
- Recovery Bundle export/import and Console Owner recovery.
- Preserve-data and full decommission journeys.
- English and German with equal accessibility coverage.

### Explicitly deferred

- Existing Cluster Import.
- Offline Bundle.
- Full headless CLI parity.
- GitLab/Forgejo first-class provider adapters and SSH Git remotes.
- Community Application removal.
- Member-application privatization.
- Automated router changes or a dedicated router-forward verification task.
- Direct repair, restart, restore, resize, forced sync, arbitrary Kubernetes mutation, and autonomous remediation.
- Concurrent lifecycle authority or synchronized Cluster Profiles.
- Default outbound telemetry.

## 3. System shape

```text
Launcher Host
┌─────────────────────────────────────────────────────────────┐
│ smallworlds-admin                                           │
│  ├─ loopback HTTP + one-time session                        │
│  ├─ embedded static Svelte client                           │
│  ├─ workflow/domain modules                                 │
│  ├─ SQLite profile/run store + Launcher Vault               │
│  └─ Git / SSH / OpenTofu / provider adapters                │
└───────────────┬──────────────────────────────┬──────────────┘
                │                              │
          Git Provider                  Provider or SSH
                │                              │
                └─────────────┬────────────────┘
                              ▼
Cluster
┌─────────────────────────────────────────────────────────────┐
│ Argo CD reconciles the GitOps Overlay                       │
│                                                             │
│ Public ingress                 Private Network              │
│  ├─ member applications        ┌─────────────────────────┐  │
│  └─ Headscale coordination ───▶│ Private Gateway         │  │
│                                │  ├─ Operator Console     │  │
│                                │  ├─ Grafana              │  │
│                                │  └─ Argo CD               │  │
│                                └─────────────────────────┘  │
│                                                             │
│ Operator Console API     Executor       Kubernetes/Argo     │
│  ├─ read assessments     ├─ approved bounded actions        │
│  └─ plans/approvals      └─ Git proposals / backup checks   │
└─────────────────────────────────────────────────────────────┘
```

### Trust zones

- **Browser client:** untrusted presentation tier; never receives reusable provider credentials or secret values.
- **Bootstrap Launcher:** Lifecycle Authority after Launcher Vault unlock; can reach provider APIs, the Cluster Node, Git, and Kubernetes.
- **In-cluster API:** read-mostly and OIDC-authenticated; owns no cloud lifecycle credential.
- **In-cluster executor:** separate process/service account that accepts only approved typed work, not arbitrary commands.
- **Private Gateway:** only network route to privileged HTTP interfaces; public Traefik has no routes for those hosts.
- **GitOps Overlay:** durable non-secret Desired Configuration.
- **Kubernetes Secrets:** runtime secret values, excluded from Git.

## 4. Proposed repository layout

```text
operator-console/
├── go.mod
├── cmd/
│   └── smallworlds-admin/          # launcher/in-cluster executable entrypoint
├── api/
│   ├── openapi.yaml                # /api/v1 contract
│   └── generated/                  # generated Go types/handlers
├── catalog/
│   ├── schema.json
│   └── capabilities/*.yaml
├── internal/
│   ├── administration/             # top-level use cases
│   ├── assessment/                 # Capability Assessment rules
│   ├── catalog/                    # catalog validation/query
│   ├── changes/                    # plan/approval/run contract
│   ├── journey/                    # Setup Journey dependency model
│   ├── profiles/                   # Cluster Profile lifecycle
│   ├── protection/                 # backup coverage/recovery evidence
│   ├── secrets/                    # Launcher Vault and secret references
│   └── adapters/
│       ├── assets/                 # OD-001 seam
│       ├── github/
│       ├── git/
│       ├── headscale/
│       ├── hetzner/
│       ├── keycloak/
│       ├── kubernetes/
│       ├── opentofu/
│       ├── sqlite/
│       └── ssh/
├── migrations/                     # SQLite schema migrations
├── web/
│   ├── src/
│   ├── messages/{en,de}.json
│   ├── static/
│   ├── svelte.config.js
│   └── package.json
└── test/
    ├── fixtures/
    ├── golden/
    └── integration/

infrastructure/kubernetes/
├── apps/operator-console.yaml
└── tenants/operator-console/
    ├── api-deployment.yaml
    ├── executor-deployment.yaml
    ├── service.yaml
    ├── rbac.yaml
    ├── network-policy.yaml
    ├── oidc-config.yaml
    ├── crds/
    └── kustomization.yaml

infrastructure/kubernetes/tenants/private-gateway/
├── deployment.yaml
├── service.yaml
├── certificates.yaml
├── network-policy.yaml
└── kustomization.yaml
```

The exact private-gateway manifests may use a Tailscale sidecar, a dedicated proxy, or a small gateway binary after the networking spike. The product contract—not the mechanism—is fixed.

## 5. Deep module interfaces

Browser handlers, CLI commands, controllers, and tests cross the same small interfaces. Provider-specific details remain internal adapters.

```go
type Journey interface {
    Inspect(ctx context.Context, profileID ProfileID) (JourneySnapshot, error)
    Plan(ctx context.Context, intent Intent) (ChangePlan, error)
}

type Changes interface {
    Approve(ctx context.Context, planID PlanID, approval Approval) (WorkflowRun, error)
    Cancel(ctx context.Context, runID RunID) (CancellationResult, error)
    GetRun(ctx context.Context, runID RunID) (WorkflowRun, error)
}

type Assessments interface {
    Snapshot(ctx context.Context) (AdministrationSnapshot, error)
    Watch(ctx context.Context, since Cursor) (<-chan AssessmentEvent, error)
}
```

Important interface invariants:

- Plans contain no secret values.
- Approval names one immutable plan digest and expires when preconditions change.
- Execution consumes typed intents; there is no `runCommand(string)` interface.
- Verification evidence is distinct from executor exit status.
- Adapters return structured errors with stable codes, redacted parameters, retryability, and remediation categories.
- Cancellation is cooperative. A run either stops at a declared safe checkpoint or reports that the current atomic operation must finish; cancellation never implies rollback.

## 6. Core data model

### Cluster Profile

Launcher-owned fields include:

- Stable UUID, name, Deployment Mode, selected SmallWorlds release.
- Launcher/cluster compatibility range.
- Desired inputs before the GitOps Overlay exists; afterward, Git repository/ref identity.
- Infrastructure-state location and digest.
- Kubeconfig and Cluster CA references.
- Trusted SSH host key and Local Cluster Node identity.
- Launcher Vault references, never values.
- Setup Journey and Workflow Run references.
- Lifecycle Authority identity and Recovery Bundle status.

### Change Plan

- Stable ID and content digest.
- Typed intent and actor.
- Observed preconditions with resource versions/digests.
- Proposed Git, provider, node, cluster, exposure, cost, downtime, and data changes.
- Risk labels: reversible, destructive, cost-bearing, lockout-risk, secret rotation, downtime.
- Verification and recovery strategy.
- Required approval strength and expiry.
- Redacted human summary in English/German parameters.

### Workflow Run

- Plan digest, actor, timestamps, state, current checkpoint.
- Structured redacted events.
- Adapter operation IDs for safe reinspection.
- Cancellation state.
- Verification evidence and final outcome.
- Loki query/reference for detailed in-cluster logs.

### Kubernetes custom resources

Use `admin.smallworlds.network/v1alpha1` initially:

- `ChangePlan`: compact proposal, digest, risk, approval and expiry; no secrets.
- `WorkflowRun`: typed operation, phase, checkpoints, evidence summary and Loki reference.

Set size budgets and pruning before CRDs ship. Do not store full command output or large diffs in status fields.

### Launcher persistence

SQLite tables should cover profiles, journey tasks, plans, approvals, runs, events, trusted hosts, tool metadata, asset metadata, and secret references. Use migrations from the first schema. Set restrictive filesystem permissions/ACLs and use atomic backups before migration.

The Launcher Vault is separate from ordinary SQLite fields. Use an OS credential-store wrapping key where available and a passphrase-unlocked encrypted fallback for headless Linux. Recovery Bundles use age encryption as recorded in ADR-0036.

## 7. Cluster Capability catalog

Each YAML entry validates against one JSON Schema and contains only declarative metadata:

```yaml
id: nextcloud
category: community-application
display:
  nameKey: capability.nextcloud.name
  descriptionKey: capability.nextcloud.description
selection:
  optionality: optional
  presets: [collaboration, full]
dependencies: [keycloak, garage, cloudnative-pg]
conflicts: []
deploymentModes: [hetzner, local-lan, local-public]
exposure: public
resources:
  cpuMillicores: 500
  memoryMiB: 1024
  storageGiB: 48
delivery:
  argoApplication: nextcloud
runtimeEvidence:
  workloads: [Deployment/nextcloud]
  endpoint: files
protection:
  datasets: [nextcloud-db, nextcloud-files, nextcloud-config]
links:
  docs: doc/tenant-nextcloud.md
  grafanaDashboard: nextcloud
```

Catalog rules:

- Stable IDs never depend on display names.
- Text uses localization keys.
- Dependencies must be acyclic and refer to existing IDs.
- Required Platform Services cannot be disabled.
- Exposure is explicit; absence is validation failure.
- Resource values are estimates and clearly labeled as such.
- Expected protection datasets must map to a protection observer.
- Catalog rendering does not contain executable shell snippets.

Generate the Setup Journey selection screen, presets, root overlay resources, assessment registration, and documentation links from this catalog. Add a CI linter that catches dependency cycles, missing translations, missing Argo manifests, unknown protection observers, and unsupported Deployment Modes.

## 8. Capability Assessment model

Every assessment returns one headline Capability State plus evidence facets:

1. **Configuration:** selected/disabled, required values, dependency blockers, Git declaration.
2. **Delivery:** Argo sync, health, operation phase, drift, last reconciliation.
3. **Runtime:** controller readiness, failed Jobs, PVC binding, endpoint/application probes.
4. **Access:** exposure policy, DNS resolution, certificate readiness, expected Private Gateway/public reachability.
5. **Protection:** dataset coverage, local Recovery Point, offsite Recovery Point, retention and Restore Drill freshness.

Assessment rules are pure and table-tested. Observers gather evidence; they do not decide presentation state. Each non-healthy facet produces a stable reason code, evidence timestamps, staleness indicator, and one remediation route: Setup Journey task, Git proposal, bounded Runtime Action, documentation, or external-tool deep link.

Do not flatten unknown/stale evidence into healthy. Protection failures may degrade a stateful capability even when its workload is serving traffic.

## 9. Setup Journey

Journey Tasks form a dependency graph, but the UI recommends one next task at a time. Every completed task is revalidatable.

### Common tasks

1. **Create Cluster Profile** — name, language, Deployment Mode, target release.
2. **Unlock Launcher Vault** — establish credential-store/fallback behavior.
3. **Select capabilities** — Minimal/Collaboration/Full/Custom, capacity and exposure review.
4. **Configure Git Provider** — validate credentials, create/select private repository, render/push initial GitOps Overlay.
5. **Rotate GitHub creation credential** — replace temporary Administration authority with repo-scoped ongoing authority.
6. **Configure identity/domain** — admin email, onboarding policy, domain or LAN naming, CA/public certificate path.
7. **Inspect target** — provider project or SSH node read-only preflight.
8. **Size installation** — capability-derived provider preset or Local capacity verdict.
9. **Plan infrastructure/node changes** — immutable Change Plan and approval.
10. **Bootstrap Kubernetes and Argo CD** — deploy bootstrap manifests and Cluster Secrets.
11. **Observe GitOps convergence** — retry only through typed/idempotent logic; retain Argo evidence.
12. **Establish Private Network** — deploy Headscale/Private Gateway, acquire verified Tailscale client, enroll Launcher Host.
13. **Verify private administration** — prove the Launcher Host reaches the Private Gateway before closing public SSH/Kubernetes access.
14. **Claim first Console Owner** — one-time Keycloak claim and passkey registration.
15. **Configure offsite protection** — destination credentials, bucket/versioning inspection, validation run.
16. **Final assessment** — surface incomplete acknowledgements/warnings and hand off to the in-cluster console.

### Hetzner-specific tasks

- Validate a read/write project token and enumerate project identity.
- Verify domain nameserver delegation.
- Discover/adopt or create Primary IP, DNS zone, SSH public key, firewall, volume, and related resources.
- Keep shared DNS zones outside per-profile destructive ownership.
- Run OpenTofu init/plan/apply from a private profile workspace with pinned tools/providers.
- Initially scope SSH and Kubernetes API firewall access to the Operator's observed public address where feasible; remove those paths only after tailnet verification.
- Show estimated current provider cost and retained-resource cost on every plan/decommission.

### Local-specific tasks

- Accept SSH agent, key/passphrase, or username/password; support root or sudo.
- Confirm and pin the SSH host key.
- Read-only preflight: supported Linux/systemd, architecture, memory, disk, required kernel/network behavior, existing k3s, ports, paths, firewall and sudo.
- Block foreign Kubernetes installations and unidentified data/path collisions.
- Offer a per-profile Ed25519 key after initial trust.
- For same-host Linux, use a narrowly scoped elevation helper rather than assuming root.
- LAN-only: generate Cluster CA root/intermediate, install Operator Device trust with consent, establish Private Network DNS, and do not promise remote access.
- Internet-exposed: show router rules and accept acknowledgement without UPnP/NAT-PMP or a dedicated verifier.

### Resume semantics

- A process/browser restart reloads the current Workflow Run and reinspects its last external operation.
- Do not repeat provider create calls without idempotency/adoption checks.
- OpenTofu state is locked per profile and backed up before mutation.
- SSH steps use markers and verification, not only a local “done” flag.
- Git pushes compare remote refs and avoid force-push.
- Kubernetes apply waits for observed resource identity/readiness.
- If preconditions diverge, invalidate the old plan and explain the replan.

## 10. GitOps Overlay rendering and Git behavior

Move overlay generation out of Bash heredocs and mutation of the base checkout into a deterministic renderer. Inputs are the selected release, catalog entries, domain/environment naming, exposure, onboarding policy, and provider-specific repository identity.

Renderer outputs include:

- Root `kustomization.yaml` referencing one exact SmallWorlds tag.
- Per-capability directories for selected Platform Services and Community Applications.
- Argo `Application` repo/path patches.
- Domain/environment patches.
- Renovate configuration and repository target.
- Operator Console and Private Gateway selection/configuration.
- Non-secret references to expected Cluster Secrets.

Verification:

- Characterization/golden fixtures for all three modes, empty/minimal/full selections, custom domains and `.dev` naming.
- `kustomize build`/schema validation for every fixture.
- Tests comparing required behavior from `prepare-community-repo.sh`, without requiring byte-for-byte reproduction of accidental formatting.
- Secret scanners assert generated Git contains no token, password, kubeconfig, private key or CA private material.

Git behavior:

- The initial empty repository may receive a direct first commit.
- Subsequent durable changes create a named branch and provider proposal.
- GitHub opens a pull request; generic HTTPS Git pushes the branch and returns clear manual merge instructions.
- No force pushes or automatic merges.
- The UI displays the exact diff and remote commit IDs.
- Argo CD receives repository access via Cluster Secret; token expiry is a Capability Assessment reason.

## 11. Private Gateway and identity

### Networking implementation spike

Before production manifests, prove one gateway design on each Deployment Mode:

- A stable Headscale node identity survives pod restart/reschedule.
- Tailnet clients can reach three hostnames on standard HTTPS.
- Public-IP requests with forged operator host headers cannot route to the services.
- DNS-01/public certs work in public modes and the Cluster CA works in LAN-only.
- Source identity and OIDC callback URLs remain correct through the proxy.
- NetworkPolicies allow only gateway-to-service traffic.
- Headscale coordination remains reachable according to mode.

Prefer a dedicated Tailscale sidecar/gateway proxy with persisted node state and internal upstreams. Do not put private hosts on public Traefik merely because their public DNS records point at 100.64.0.0/10.

### Operator Device enrollment

- Acquire official Tailscale packages from pinned/verified upstream sources.
- OS adapters request elevation explicitly and never silently install.
- Use short-lived single-use Headscale credentials for devices.
- Keep the gateway machine identity separate and stable.
- Owner-generated Enrollment Invitations expire and are attributable.
- Revocation is a typed Runtime Action with a Change Plan because it may lock out a device.
- LAN-only enrollment includes Cluster CA trust and Private Network DNS.

### OIDC and roles

- Create a dedicated Keycloak client for the Operator Console.
- Map Observer, Operator, Owner roles explicitly; default deny when no role exists.
- Keep sessions short enough for a privileged tool and support passkeys.
- Validate issuer/audience/nonce/PKCE and rotate client credentials through a typed task.
- First Owner claim is one-time and self-disabling.
- Launcher-based Owner recovery is independent of normal Private Gateway/OIDC availability.

### Linked tools

- Configure Grafana and Argo CD OIDC clients through GitOps/init jobs.
- Map normal console roles to read-only access.
- Open contextual links in a new tab; do not iframe.
- Retain local admin credentials as Cluster Secrets/break-glass only.

## 12. Backup and protection implementation

Build a protection inventory from the catalog plus live resources. It must represent the repository's current two-hop chain: application/CNPG/Velero/PV data to Garage, then whole-Garage replication offsite.

### Read model

For every dataset show:

- Owning capability and data type.
- Expected producer and schedule.
- Latest successful local Recovery Point and age.
- Latest offsite Recovery Point/replication and age.
- Declared retention.
- Coverage gaps and stale/unknown evidence.
- Last recorded Restore Drill and result.

### First-release mutations

- Collect offsite S3 endpoint, region/bucket, access key and secret into the Launcher Vault/Cluster Secret path.
- Validate endpoint and bucket access without logging credentials.
- Inspect versioning when the S3 implementation exposes a compatible API; otherwise require an explicit acknowledgement rather than claiming it is enabled.
- Render/propose non-secret replication configuration.
- Trigger one declared backup/replication validation Job and observe completion/evidence.
- Permit recording a manual Restore Drill result.

Do not implement restore, backup deletion, retention mutation, or arbitrary `velero`/`rclone` command entry.

## 13. Decommission and recovery

### Recovery Bundle

- Export a versioned age-encrypted archive with a minimal cleartext format header.
- Include Cluster Profile, SQLite snapshot, OpenTofu state, kubeconfig, Cluster CA root, and required Launcher Vault material.
- Exclude caches, full Loki logs and downloaded tools/assets.
- Verify integrity and identity on import before assigning Lifecycle Authority.
- Support passphrase/scrypt by default and age recipients as an advanced path.
- Test round-trip import across supported operating systems.

### Preserve-data decommission

- Inspect backup/Recovery Bundle status and all profile-owned/shared resources.
- Hetzner: delete compute/workload resources selected by the plan while preserving declared persistent data; list ongoing volume/IP costs precisely.
- Local: uninstall SmallWorlds/k3s resources while retaining the data directory.
- Remove only profile-owned DNS records; retain shared zones and the GitOps Overlay.

### Full decommission

- Require a fresh inspection, stronger typed confirmation and explicit retained/shared-resource list.
- Warn when offsite protection or Recovery Bundle evidence is absent/stale but allow an explicit Owner override so costs can still be stopped.
- Delete only resources with proven profile ownership.
- Never combine decommission with “forget profile.”
- Produce a final Activity Record and optionally export it before local profile deletion.

## 14. HTTP interface

Initial endpoint groups under `/api/v1`:

```text
GET    /session
POST   /session/exchange

GET    /profiles
POST   /profiles
GET    /profiles/{id}
GET    /profiles/{id}/journey
GET    /profiles/{id}/snapshot

GET    /capabilities
GET    /capabilities/{id}
GET    /protection
GET    /network
GET    /activity

POST   /plans
GET    /plans/{id}
POST   /plans/{id}/approve
POST   /plans/{id}/reject
GET    /runs/{id}
POST   /runs/{id}/cancel

GET    /events?cursor=...
POST   /recovery-bundles/export
POST   /recovery-bundles/import
```

Use discriminated typed intents for `/plans`, such as `CreateGitOpsOverlay`, `ProvisionInfrastructure`, `BootstrapNode`, `EnrollDevice`, `ConfigureBackup`, `AddCapability`, `RotateCredential`, `RecoverOwner`, and `DecommissionCluster`.

Generate TypeScript types/client helpers from OpenAPI. CI fails on uncommitted generated changes or breaking changes without an API version decision. SSE events carry IDs/cursors and clients reconnect with `Last-Event-ID`. Apply request limits, same-origin checks, CSRF protection, secure cookies and strict CSP. Never accept shell, YAML, HCL, `kubectl` arguments, or arbitrary URLs in a generic execution endpoint.

## 15. Svelte 5 application

### Routes and information architecture

```text
/profiles                         Launcher profile chooser
/profiles/[id]/setup              Setup Journey
/overview                         operational summary and next actions
/capabilities                     Platform Services and Community Applications
/capabilities/[id]                facets, evidence, history and deep links
/backups                          protection inventory and setup
/network                          gateway, devices, DNS, exposure
/activity                         plans, approvals and Workflow Runs
/settings                         identity, language, release, diagnostics
```

The in-cluster build hides launcher-only profile/lifecycle routes based on server-advertised capabilities, not compile-time forks scattered through components.

### Frontend rules

- Use Svelte 5 runes and SvelteKit client-side routing; no SvelteKit server actions.
- Keep domain data in generated API types and small feature stores; do not reproduce assessment or plan rules in TypeScript.
- Centralize SSE reconnect/cursor behavior.
- English is the canonical message catalog; CI requires German key parity.
- Backend error codes map to translated messages with safely formatted parameters.
- Localize timestamps, durations, byte sizes and provider currency.
- No external fonts, analytics, CDN scripts or runtime translation service.
- Design tokens cover light/dark/high-contrast themes and semantic state colors/icons/text.
- Destructive/cost/lockout plans use consistent risk presentation and confirmation patterns.
- Never display stored secret values after submission; show presence, source, expiry and rotation status.

### Accessibility checks

- Automated axe checks for primary routes in both languages.
- Keyboard-only Playwright journeys.
- Screen-reader-friendly progress using throttled `aria-live` summaries rather than every log line.
- Focus restoration after navigation/dialogs and after async plan completion.
- Charts/timelines have table/text alternatives.
- Touch targets and reflow pass at phone width; no hover-only evidence.

## 16. Launcher packaging and lifecycle

- One Go binary embeds the compiled Svelte assets.
- Enforce a single per-user process with a lock and authenticated rendezvous metadata stored under OS-appropriate application data.
- Bind a random loopback port only; exchange a one-time URL token for an HttpOnly SameSite cookie and scrub the URL.
- Reopening the launcher reconnects to the process and opens the browser.
- Continue active Workflow Runs without a browser.
- Refuse normal exit during mutation; offer cooperative cancellation/stop-after-checkpoint.
- Do not install a system service or auto-start in the first release.
- Package native artifacts appropriately: checksummed archives on Linux, signed/notarized app packaging on macOS, and signed Windows artifact/installer strategy before stable release.
- Build reproducibly where possible and publish checksums, signatures, SBOMs and third-party notices under the repository's MIT license.
- Managed OpenTofu/providers/Tailscale downloads are pinned, checksummed/signature-verified, cached, and never searched from ambient `PATH` unless an explicit developer override is active.

## 17. Delivery plan: tracer-bullet vertical slices

Each milestone must leave a demonstrable end-to-end behavior through the real browser/backend interface. Avoid building all adapters horizontally before any journey works.

### M0 — Foundations and decision spikes

Deliver:

- Go module, SvelteKit/Svelte 5 workspace, reproducible build command, embedded hello screen.
- OpenAPI generation loop for Go and TypeScript.
- ADR/glossary/plan links in contributor documentation.
- CI jobs for Go, Svelte, schema, translations and generated-code cleanliness.
- Spike reports for:
  - OD-001 asset distribution.
  - Private Gateway on all three networking shapes.
  - Cross-platform OS credential stores, elevation, background-process rendezvous and browser opening.
  - OpenTofu state sensitivity/redaction and provider acquisition.
  - Cluster CA trust installation on Linux/macOS/Windows.

Exit criteria:

- OD-001 is resolved before M3 begins.
- Gateway proof shows no public Host-header bypass.
- Unsupported OS operations have documented fallbacks before UI work promises automation.

### M1 — Walking launcher journey with fake adapters

Deliver:

- Single-instance background launcher and loopback token exchange.
- SQLite migrations, Cluster Profile CRUD, Launcher Vault abstraction.
- Svelte shell, profile chooser, Setup Journey, plan review, activity stream.
- English/German and baseline accessibility.
- Fake target intent that runs Inspect → Plan → Approve → Execute → Verify, survives browser close/process restart, and streams SSE events.
- Age-encrypted Recovery Bundle round trip for fake profile secrets.

Exit criteria:

- A Playwright test creates a profile, approves a fake run, closes/reopens the browser, sees verification, exports/imports the profile, and passes in both languages.

### M2 — Catalog, assessment and GitOps renderer

Deliver:

- Catalog schema and entries for every currently declared Platform Service and Community Application.
- Dependency/cycle/resource/exposure/protection validators.
- Pure Capability Assessment engine with fixture observers.
- Capacity-aware presets.
- Deterministic overlay renderer replacing the behavior currently hardcoded in `prepare-community-repo.sh`.
- Golden and Kustomize render tests across modes/selections/domains.

Exit criteria:

- Every app currently listed in `OPTIONAL_APPS` is represented once in the catalog.
- Minimal and Full overlays render without secrets and reference the selected immutable tag.

### M3 — Git provider vertical slice

Deliver:

- Generic HTTPS Git adapter using a Go library/controlled executable-free implementation.
- GitHub API adapter: token validation, private repository creation, initial push, branches and pull requests.
- PAT creation guidance, permission/expiry assessment and post-creation rotation task.
- Real Git diff in Change Plans.

Exit criteria:

- A test GitHub repository can be created, initialized, rotated to ongoing credentials, changed through a PR, and observed without `git` or `gh` installed.
- Generic Git can push a proposal branch and return manual merge guidance.

### M4 — Local LAN-only bootstrap tracer

This is the first real provisioning tracer because the repository has a verified Local bootstrap path and no current production Hetzner node.

Deliver:

- SSH credentials, host-key pinning and sudo/root preflight.
- Dedicated-node collision policy and profile-specific key option.
- Shared bootstrap renderer/contract used by the new adapter for a remote Linux Cluster Node.
- Cluster CA root/intermediate lifecycle and current-device trust installation.
- k3s/Argo bootstrap, Cluster Secret injection and GitOps convergence assessment.
- Headscale, Private Gateway, Private Network DNS and Launcher Host enrollment.
- First Console Owner claim.
- Local preserve-data uninstall path.

Exit criteria:

- From each launcher OS family, a remote supported Linux node can be inspected; at least the dedicated Linux release runner completes the full installation.
- Operator Console/Grafana/Argo are reachable through the Private Network and absent from public/LAN ingress routes.
- Browser TLS is trusted on the enrolled test Operator Device.

### M5 — In-cluster observation console

Deliver:

- Operator Console API and executor deployments, CRDs, least-privilege RBAC and NetworkPolicies.
- Keycloak OIDC and role enforcement.
- Argo/Kubernetes/certificate/access observers.
- Capability, overview, activity and contextual deep-link screens.
- Grafana/Argo read-only OIDC mappings.
- WorkflowRun CRD + Loki event references.

Exit criteria:

- Observer cannot mutate; Operator can create an allowed proposal; Owner can manage enrollment.
- Unauthorized/no-role users are denied.
- Restarting console/executor loses no compact run state.

### M6 — Hetzner/OpenTofu bootstrap

Deliver:

- Pinned launcher-managed OpenTofu/provider acquisition.
- Private per-profile workspace/state/locking/backups.
- Hetzner token/project validation and shared/profile-owned resource inventory.
- Primary IP/DNS/SSH/firewall/volume discovery, adoption plan and creation.
- Live locations/server offerings/cost plan and capability-derived sizing.
- Cloud-init/bootstrap integration, temporary admin access restriction and verified tailnet lockdown.
- Preserve-data and full Hetzner decommission.

Exit criteria:

- A gated workflow creates a new cluster from an empty Hetzner project, reaches all final assessments, and destroys profile-owned resources under a strict cleanup trap.
- Shared zone and GitOps repository survive full cluster decommission.

### M7 — Local internet-exposed bootstrap

Deliver:

- Public-domain/nameserver/token inputs and DNS-01 path.
- Manual router-forward instructions and acknowledgement.
- DDNS/public-IP behavior from the existing bootstrap contract.
- Public Headscale coordination plus private-only operator services.
- Mode-specific mail/Jitsi warnings retained and localized.

Exit criteria:

- Recorded release test covers a real router/public IP, certificates, Private Gateway access and expected public member application access.

### M8 — Backup and protection vertical slice

Deliver:

- Protection observers for CNPG, Velero, PV backup Jobs, Garage and offsite replicator.
- Dataset coverage screen and Capability Assessment integration.
- Offsite S3 secret/setup, versioning inspection/acknowledgement and validation Workflow Run.
- Manual Restore Drill record.

Exit criteria:

- The UI distinguishes “Job succeeded,” local Recovery Point, offsite Recovery Point and unknown/stale evidence.
- The repository's documented unconfigured-offsite gap becomes a specific Journey Task rather than a generic failed Job.

### M9 — Post-bootstrap proposals, enrollment and recovery

Deliver:

- Add Community Application proposal flow with capacity/dependency/protection review.
- Operator Device invitations/revocation.
- Credential rotation tasks.
- Console Owner recovery from the Launcher.
- Diagnostics bundle with Operator preview and redaction report.

Exit criteria:

- Adding an app changes Git only through a PR and is tracked until Argo/runtime/protection assessment.
- Owner recovery works when normal OIDC login is deliberately unavailable.

### M10 — Cross-platform hardening and stable release

Deliver:

- Native packaging/signing/notarization, SBOM and third-party notices.
- Complete platform matrix for remote SSH/Hetzner; Linux-only same-host guard.
- Accessibility audit, German copy review and browser matrix.
- Performance/resource budgets and long-run soak tests.
- Stable-release evidence for three Deployment Modes.
- Documentation for download, first run, Recovery Bundle custody, break-glass, decommission and known limitations.

Exit criteria:

- All first-release Definition of Done items below pass with artifacts/evidence attached to the release.

## 18. Testing strategy

### Unit and property tests

- Journey dependency resolution, invalidation and next-action selection.
- Plan digest/precondition/approval expiry.
- Capability Assessment truth tables, stale/unknown evidence and headline aggregation.
- Catalog dependency cycles and preset/resource calculations.
- Secret redaction across structured errors/events/diffs.
- Recovery Bundle encryption/decryption/corruption/wrong-recipient behavior.
- Git URL/domain/environment normalization.
- Decommission ownership classification.

### Contract tests

- OpenAPI request/response and generated-client compatibility.
- Provider adapters against recorded/mock HTTP servers, including pagination/rate limits/403/409/5xx.
- SSH adapter against disposable SSH servers for password/key/agent/sudo/host-key mismatch.
- OpenTofu adapter against fixture modules with interrupted/partial operations and state locking.
- Kubernetes observers/controllers with `envtest` or disposable clusters.
- S3 behavior with compatible local object storage and unsupported versioning responses.

### Rendering and supply-chain tests

- Golden GitOps overlays for the full mode/preset matrix.
- `kustomize build` and Kubernetes schema validation.
- OpenTofu formatting/validation and lock-file enforcement.
- Image/tool/provider pin and checksum policy.
- Secret scans over generated overlays, logs, bundles' cleartext headers, frontend bundles and CI artifacts.
- Dependency/license/SBOM generation.

### Browser tests

- Vitest/component tests for pure UI state and translation formatting.
- Playwright for English and German primary journeys.
- Keyboard-only and axe accessibility tests.
- SSE disconnect/reconnect, duplicate event IDs and stale cursors.
- Browser close/reopen during active Workflow Run.
- Role authorization and forbidden controls/routes.
- Mobile reflow, reduced motion and high contrast.

### Fault-injection scenarios

- Launcher killed during provider apply, SSH bootstrap, Git push and convergence watch.
- DNS delegation delayed; certificates pending.
- Git token expires between plan and apply.
- Operator IP changes before firewall apply.
- Headscale healthy but gateway not enrolled.
- Private access works, then public lockdown apply fails halfway.
- Argo retries exhausted for one application.
- Backup validation succeeds locally but offsite replication fails.
- Disk full/low memory on Local preflight and after installation.
- Recovery Bundle imported on another OS.
- Full decommission interrupted after compute deletion but before DNS cleanup.

### Real environment matrix

| Mode | PR | Gated automation | Stable-release evidence |
|---|---|---|---|
| Hetzner | mocks/rendering | ephemeral paid cluster with forced cleanup | required |
| Local LAN-only | SSH/K8s fixtures | dedicated Linux node | required |
| Local internet-exposed | rendering/fixtures | not ordinary CI | recorded manual test required |

## 19. Security controls

- Threat-model the Launcher, browser session, Private Gateway, executor, Git token, OpenTofu state, Recovery Bundle and decommission flows before their milestone implementation.
- Bind launcher HTTP only to loopback; random port, one-time token, session rotation, SameSite/HttpOnly cookies, CSRF and CSP.
- Use strict file permissions/ACLs and avoid inherited broad directories.
- Never put credentials on command lines or environment variables visible to unrelated processes when an input file/stdin/provider credential channel exists.
- Redact by type at creation time; do not rely only on regex after logging.
- Pin SSH host keys; no `StrictHostKeyChecking=no` in new code.
- Verify downloaded executable signatures/checksums and reject downgrade/substitution.
- Separate read-mostly API and executor service accounts. Executor RBAC enumerates allowed resource names/verbs where Kubernetes permits.
- Do not mount a general cluster-admin token into the web/API pod.
- Plans and CRDs contain references, never raw secrets.
- NetworkPolicies deny public/tenant access to operator services.
- OIDC role checks occur server-side for every endpoint; hiding controls is not authorization.
- Activity Records include actor, plan digest, approval, executor and verification outcome.
- Diagnostics exports show a redaction preview and never include Launcher Vault values, Kubernetes Secret data or raw OpenTofu sensitive outputs.
- Decommission ownership uses provider IDs/tags/state, not only names.

## 20. Observability and support

### Launcher

- Structured local logs with rotation and per-run correlation IDs.
- Health endpoint available only to the authenticated loopback session.
- Activity view separates human summaries from detailed redacted events.
- No telemetry export by default.
- Diagnostics bundle includes versions, OS/architecture, catalog/bundle digests, redacted plan/run summaries, validation results and selected logs.

### In-cluster

- Prometheus metrics for request/result counts, observer freshness, assessment states, run duration, executor queue and SSE connections.
- Kubernetes readiness/liveness endpoints that do not depend on every external provider.
- Structured stdout logs to Loki with actor/run/capability IDs but no secrets.
- Grafana dashboard and alerts for console/executor unavailable, stuck runs, stale observers and gateway loss.
- The Operator Console must report its own degraded assessment without depending on itself as the sole alert route.

## 21. Legacy compatibility and rollout

- Keep Homepage and its existing low-privilege role unchanged until a separate Member Dashboard decision.
- Add `operator-console` and `private-gateway` as new Platform Services and Argo applications; do not overload `dashboard`.
- Keep both legacy bootstrap paths maintained for critical fixes while the launcher is built.
- New renderers should share templates/contracts internally, but do not rewrite legacy scripts before characterization coverage exists.
- Mark legacy scripts deprecated only after M7 proves three-mode parity.
- Do not remove/wrap them until Existing Cluster Import has been stable for at least one release.
- Existing healthy clusters may be used as development targets manually, but product acceptance does not claim import/migration.
- Update README/admin-tool documentation incrementally at the milestone that changes an Operator path.

## 22. Risks and mitigations

| Risk | Impact | Mitigation / gate |
|---|---|---|
| Scope expands into a full cluster manager | Delivery stalls and unsafe generic actions appear | Typed intents, first-release action allowlist, vertical slices |
| Asset/version packaging remains ambiguous | Non-reproducible bootstrap | OD-001 must close before M3/M4 real provisioning |
| Private Gateway works only on one network shape | Operator lockout or public bypass | Three-mode networking spike and forged-Host test |
| Cross-platform elevation/keychain/client install varies | “No prerequisites” promise breaks | M0 OS spike, capability detection, explicit fallbacks |
| OpenTofu state leaks sensitive values | Cloud/cluster compromise | private workspace, redaction tests, encrypted Recovery Bundle |
| Bootstrap cycle: cluster needs Headscale before private access | Setup deadlock | temporary scoped admin path; verify tailnet before lockdown |
| GitHub token is broad or expires | GitOps/updates stop | creation-token rotation, expiry assessment, typed rotation task |
| Local CA root is lost | LAN trust cannot be recovered cleanly | Launcher Vault + Recovery Bundle; root never cluster-only |
| Existing script/local/cloud bootstrap contracts drift | Mode-specific regressions | shared fixtures, characterization tests, all-mode release evidence |
| CRDs/Loki accumulate unbounded history | API/storage pressure | compact schemas, size budgets, retention/pruning policy |
| Catalog metadata diverges from manifests/docs | Incorrect status/setup | CI cross-checks catalog IDs, Argo apps, docs, translations, observers |
| Decommission deletes shared/unowned resources | Irrecoverable loss | ownership inventory, shared-resource classification, typed confirmation |
| German UI lags English or errors are untranslatable | Broken initial requirement | canonical message keys, parity CI, stable backend error codes |
| No Existing Cluster Import in v1 | Current users cannot migrate | keep legacy scripts; explicit roadmap and no rebuild claim |

## 23. Definition of Done for the first stable release

### Product

- A non-developer can complete each new-cluster Setup Journey with one downloaded launcher and documented external account/domain/router actions only.
- All three Deployment Modes meet their acceptance record.
- Remote Local installation works from Linux, macOS and Windows launchers; same-host option appears only on supported Linux.
- Setup resumes safely after browser closure and tested process interruption.
- Operator Console, Grafana and Argo CD have no public ingress route and are reachable through the Private Gateway with Keycloak OIDC.
- First Owner claim, Owner recovery, device enrollment/revocation and Git token rotation work.
- Capability selection is capacity-aware and all installed capabilities have explainable assessments.
- Backup screen distinguishes coverage/local/offsite/restore-drill evidence and completes an offsite validation.
- Community Applications can be added through a GitHub PR or generic-Git proposal branch, but not removed.
- Preserve-data and full decommission plans prove ownership and behave as documented.
- Recovery Bundle round trip succeeds cross-platform.

### Quality

- English and German are complete and reviewed.
- WCAG 2.2 AA automated/manual evidence exists for primary workflows.
- No known critical/high security issue; threat-model actions are closed or explicitly accepted.
- No secret appears in generated Git, browser payloads, logs, CRDs, diagnostics or CI artifacts.
- API, catalog and database schemas are versioned with migration/compatibility tests.
- Signed/checksummed platform artifacts, SBOMs and third-party notices are published.
- Resource budgets are measured for launcher idle/active and in-cluster API/executor/gateway.
- Operator and contributor documentation matches the shipped paths.

### Architectural deletion tests

- Removing the workflow module would force plan/approval/resume/verification complexity into every adapter and handler.
- Removing the catalog would force selection/dependency/exposure/protection metadata back into scripts, UI and observers.
- Removing the assessment module would force health aggregation rules into every screen.
- No module exists only to pass data through unchanged; adapters are introduced only for real environment variation or replaceable test implementations.

## 24. Immediate implementation backlog

Start with these independently reviewable issues:

1. Scaffold `operator-console/` Go + SvelteKit/Svelte 5 + embedded static build.
2. Add OpenAPI generation and a single `/api/v1/session` endpoint/client.
3. Implement single-instance loopback launcher/session exchange.
4. Add SQLite migration runner and Cluster Profile repository.
5. Implement Launcher Vault interface with in-memory test adapter and OS-spike prototypes.
6. Implement fake typed workflow with plan digest, approval, checkpoints, verification and SSE.
7. Build profile/setup/activity Svelte routes in English/German with accessibility tests.
8. Define catalog JSON Schema and convert current app/service inventory.
9. Implement catalog validation and dependency/resource/preset engine.
10. Characterize `prepare-community-repo.sh` outputs and create renderer fixtures.
11. Implement deterministic GitOps Overlay renderer without secrets.
12. Complete OD-001 experiment and record the decision before real provisioning adapters.

Do not begin with live Hetzner mutations, a generic command runner, or a visual-only dashboard. The first mergeable tracer must already exercise the durable plan/approval/run/verification seam through the Svelte client.
