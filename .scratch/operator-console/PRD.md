# SmallWorlds Operator Console

Status: ready-for-agent

## Problem Statement

An Operator currently has to understand and coordinate multiple shell scripts, local files, credentials, infrastructure tools, Git operations, Kubernetes commands, and external dashboards to establish and understand a SmallWorlds cluster. The current flow exposes implementation details, assumes several tools are installed, persists important values insecurely, is difficult to resume safely after interruption, and provides no single explanation of whether the cluster is configured, delivered, running, reachable, and protected.

After setup, the existing Member Dashboard is useful to community members but is not a privileged administration product. Operator information remains spread across Argo CD, Grafana, Kubernetes, backup Jobs, documentation, and manual Private Network steps. Those interfaces are not consistently restricted to the Private Network, do not share one identity and role model, and do not guide an Operator from an observed problem to the appropriate next action.

The Operator needs one coherent product that establishes a new cluster without globally installed prerequisites, guides every required decision, resumes interrupted work, explains every Cluster Capability's state, configures and verifies backup protection, and safely hands off to a private in-cluster console. It must preserve GitOps as the source of Desired Configuration, avoid becoming an arbitrary cluster-admin shell, and leave infrastructure and break-glass authority available when the cluster itself is unavailable.

## Solution

Create the SmallWorlds Operator Console as one browser-based product with two execution surfaces.

Before a cluster exists, a native cross-platform Bootstrap Launcher serves a Svelte 5 interface locally. It maintains multiple Cluster Profiles, protects credentials in a Launcher Vault, acquires managed tools, prepares the GitOps Overlay, provisions Hetzner or a Local Cluster Node, observes GitOps convergence, establishes the Private Network, enrolls the first Operator Device and Console Owner, configures offsite protection, and supports safe recovery and decommissioning.

After bootstrap, an in-cluster Operator Console presents the same concepts through a Private Gateway. It uses Keycloak OIDC and Console Roles, derives explainable Capability Assessments from configuration, delivery, runtime, access, and protection evidence, and links contextually to read-only Grafana and Argo CD details. Durable changes are proposed through the GitOps Overlay. Direct Runtime Actions remain typed, planned, attributable, bounded, and limited to the first-release allowlist.

The Member Dashboard remains a separate low-privilege product. Operator interfaces have no public ingress route. The first release supports all three Deployment Modes: Hetzner-hosted, Local LAN-only, and Local internet-exposed.

## User Stories

1. As an Operator, I want to download one native Bootstrap Launcher, so that I do not have to install Git, GitHub CLI, OpenTofu, Terraform, Kubernetes tools, or a JavaScript runtime first.
2. As an Operator, I want to run the Bootstrap Launcher on Linux, macOS, or Windows, so that I can administer SmallWorlds from my normal computer.
3. As an Operator, I want the launcher to open a secure local browser session automatically, so that setup feels like a web application without exposing it to my network.
4. As an Operator, I want the launcher to continue active work when I close the browser, so that long provisioning runs are not accidentally interrupted.
5. As an Operator, I want reopening the launcher to reconnect to the existing process, so that I do not create conflicting setup processes.
6. As an Operator, I want to create and name multiple Cluster Profiles, so that production, development, and local installations remain separate.
7. As an Operator, I want each Cluster Profile to retain its progress and external identities, so that I can leave and resume later.
8. As an Operator, I want one Launcher Host to be the Lifecycle Authority for a Cluster Profile, so that infrastructure mutations cannot race from multiple computers.
9. As an Operator, I want to export a Recovery Bundle, so that I can recover or transfer Lifecycle Authority if my computer is lost or replaced.
10. As an Operator, I want Recovery Bundles encrypted with a standard format, so that a copied archive does not expose control of my cloud, cluster, or certificate authority.
11. As an Operator, I want to import a Recovery Bundle on another supported operating system, so that recovery is not tied to one computer platform.
12. As an Operator, I want the Setup Journey to recommend one next action, so that I can progress without understanding the entire architecture first.
13. As an Operator, I want to revisit earlier setup decisions, so that correcting a value does not require restarting from scratch.
14. As an Operator, I want completed Journey Tasks to be revalidated, so that stale credentials or changed external state are not mistaken for success.
15. As an Operator, I want external waiting steps to remain resumable, so that token creation, nameserver delegation, and router configuration can happen at my pace.
16. As an Operator, I want advanced configuration available without cluttering the normal path, so that both new and experienced Operators can use the same product.
17. As an Operator, I want every mutation to be inspected and planned before execution, so that I understand what will change.
18. As an Operator, I want plans to disclose cost, downtime, exposure, data, and lockout effects, so that approval is informed.
19. As an Operator, I want changed preconditions to invalidate an old approval, so that the launcher never applies a stale plan.
20. As an Operator, I want execution progress persisted as a Workflow Run, so that interruption and reconnection are safe.
21. As an Operator, I want verification based on observed evidence rather than a successful command exit, so that completion is trustworthy.
22. As an Operator, I want cancellation to stop at a safe checkpoint, so that a cancel button does not imply an unsafe rollback.
23. As an Operator, I want structured and redacted Activity Records, so that I can understand what happened without leaking secrets.
24. As an Operator, I want to select GitHub as my Git Provider without installing `gh`, so that repository setup is handled inside the product.
25. As an Operator, I want the launcher to guide creation of a fine-grained GitHub token, so that I know which permissions are required.
26. As an Operator, I want the launcher to validate GitHub token permissions and expiry, so that setup fails early with a useful explanation.
27. As an Operator, I want the launcher to create a private GitOps Overlay repository, so that I do not have to prepare it manually.
28. As an Operator, I want to rotate temporary repository-creation authority to repository-scoped ongoing authority, so that SmallWorlds does not retain excessive GitHub permissions.
29. As an Operator, I want to use an existing generic HTTPS Git repository, so that GitHub is not mandatory.
30. As an Operator using generic Git, I want proposal branches and clear manual merge guidance, so that provider-specific pull-request support is not required.
31. As an Operator, I want the initial GitOps Overlay commit created automatically, so that Argo CD has a valid source of Desired Configuration.
32. As an Operator, I want later durable changes presented as Git diffs and proposals, so that the live cluster never becomes a hidden source of truth.
33. As an Operator, I want the upstream SmallWorlds base pinned to an exact release, so that installation and upgrades are reproducible.
34. As an Operator, I want release compatibility checked before mutation, so that an incompatible launcher cannot damage a Cluster Profile.
35. As an Operator, I want update proposals surfaced without automatic merging, so that I retain control over version adoption.
36. As an Operator, I want a clear distinction between Platform Services and Community Applications, so that infrastructure and member-facing functionality are not conflated.
37. As an Operator, I want required Platform Services selected automatically, so that I cannot accidentally produce an invalid cluster.
38. As an Operator, I want Community Applications to be opt-in, so that the launcher does not install every resource-heavy workload by default.
39. As an Operator, I want Minimal, Collaboration, Full, and Custom selections, so that I can start quickly or choose precisely.
40. As an Operator, I want capacity estimates for selected capabilities, so that I can avoid an obviously undersized node.
41. As an Operator, I want exposure and backup implications shown during selection, so that application choices include their operational consequences.
42. As an Operator, I want to create a Platform-Service-only cluster, so that Community Applications can be introduced later.
43. As an Operator, I want to add a Community Application after bootstrap through a Git proposal, so that the cluster can grow safely.
44. As an Operator, I want dependency, capacity, exposure, and protection checks before adding an application, so that enabling it is not merely a manifest toggle.
45. As an Operator, I want Hetzner Small, Recommended, and High-capacity presets, so that I can choose infrastructure without studying every server type.
46. As an experienced Operator, I want advanced Hetzner location, server, and volume overrides, so that I can tailor cost and capacity.
47. As an Operator, I want current provider availability and estimated recurring costs in the plan, so that cloud resources are not a financial surprise.
48. As an Operator, I want the launcher to validate my Hetzner project token, so that missing authority is discovered before provisioning.
49. As an Operator, I want the launcher to create or explicitly adopt the Primary IP, DNS zone, SSH public key, firewall, volume, and server, so that manual project preparation is minimized.
50. As an Operator, I want ownership conflicts shown as adoption plans, so that similarly named existing resources are never silently taken over.
51. As an Operator, I want nameserver delegation checked before a public installation, so that DNS and certificate failures are caught early.
52. As an Operator, I want the launcher to acquire and verify OpenTofu and provider tooling, so that infrastructure reconciliation remains reproducible without global prerequisites.
53. As an Operator, I want OpenTofu state isolated per Cluster Profile, so that production and development infrastructure cannot collide.
54. As an Operator, I want to install on another Linux server in my network over SSH, so that “Local” does not mean “this computer only.”
55. As a Linux Operator, I want an explicit same-host option when supported, so that a dedicated local server can also run its own launcher.
56. As an Operator, I want to authenticate to a Local Cluster Node with an SSH agent, key, or password, so that common environments are supported.
57. As an Operator, I want separate sudo credentials supported, so that direct root SSH is not required.
58. As an Operator, I want to confirm and pin the Cluster Node's host key, so that the launcher does not disable SSH identity checking.
59. As an Operator, I want a read-only node preflight, so that unsupported Linux, insufficient capacity, occupied ports, or missing privileges are explained before mutation.
60. As an Operator, I want foreign Kubernetes installations and unidentified SmallWorlds data to block ordinary setup, so that existing workloads are not overwritten.
61. As an Operator, I want interrupted installations belonging to the same Cluster Profile to resume, so that partial progress is recoverable.
62. As an Operator, I want the launcher to offer a dedicated per-profile SSH key, so that retained login passwords are unnecessary.
63. As an Operator, I want Local LAN-only mode to remain LAN-only, so that setup never silently exposes a router port.
64. As an Operator, I want Local internet-exposed mode to show required router forwarding rules, so that I know what must be configured manually.
65. As an Operator, I want router configuration accepted by acknowledgement for now, so that setup does not depend on unreliable automatic probing.
66. As an Operator, I want a managed Cluster CA for LAN-only mode, so that operator interfaces can use trusted HTTPS without a registered public domain.
67. As an Operator, I want the Cluster CA root protected by the Lifecycle Authority, so that the cluster alone cannot impersonate the trust root permanently.
68. As an Operator, I want trust installation offered on my Operator Device, so that browser warnings are removed through an explicit privileged action.
69. As an Operator, I want Private Network DNS for operator hostnames, so that I do not maintain hosts-file entries on enrolled devices.
70. As an Operator, I want member-facing LAN DNS guidance kept separate, so that Private Network enrollment is not required for every community member.
71. As an Operator, I want the official Tailscale client detected and installed through a guided, verified flow, so that it is not an undocumented prerequisite.
72. As an Operator, I want the Launcher Host enrolled automatically with a short-lived credential, so that first private access does not require SSH command sequences.
73. As an Operator, I want the Private Gateway to have a stable identity, so that operator URLs survive pod restarts.
74. As an Operator, I want the Operator Console, Grafana, and Argo CD reachable only through the Private Gateway, so that forged public Host headers cannot bypass privacy.
75. As an Operator, I want public Headscale coordination available only where the Deployment Mode requires it, so that LAN-only semantics are preserved.
76. As a Console Owner, I want to issue short-lived Enrollment Invitations, so that additional Operator Devices can join without cluster-shell access.
77. As a Console Owner, I want to revoke an Operator Device through a planned action, so that lost devices can be removed accountably.
78. As an Operator, I want Private Network access verified before public SSH or Kubernetes access is closed, so that the launcher does not lock me out.
79. As an Operator, I want a one-time first-owner claim displayed by the launcher, so that setup does not depend on working mail delivery.
80. As an Operator, I want to register a passkey for routine console access, so that I do not use the Keycloak realm administrator daily.
81. As a Console Owner, I want Observer, Operator, and Owner roles, so that routine access follows least privilege.
82. As a Console Observer, I want to see cluster state without mutation controls, so that observation can be delegated safely.
83. As a Console Operator, I want to create GitOps proposals and bounded Runtime Actions, so that routine work does not require Owner authority.
84. As a Console Owner, I want to manage operator access and sensitive in-cluster actions, so that authority remains explicit.
85. As an Operator acting as Lifecycle Authority, I want to recover Console Owner access, so that lost identities or broken OIDC do not make the cluster permanently unmanageable.
86. As an Operator, I want Grafana and Argo CD to use Keycloak OIDC, so that linked tools do not require unrelated everyday credentials.
87. As an Operator, I want Grafana and Argo CD read-only during normal use, so that they cannot bypass the GitOps proposal path.
88. As an Operator, I want contextual links to the relevant Argo application or Grafana dashboard, so that detailed investigation starts in the right place.
89. As an Operator, I want every Cluster Capability to show a headline Capability State, so that I can scan the cluster quickly.
90. As an Operator, I want configuration, delivery, runtime, access, and protection facets behind that state, so that the headline is explainable.
91. As an Operator, I want evidence timestamps and staleness shown, so that unknown old data is not presented as healthy.
92. As an Operator, I want Argo CD health treated as one source of evidence, so that a synced manifest is not mistaken for a working application.
93. As an Operator, I want access assessment to respect each capability's declared exposure, so that public and private reachability are not evaluated identically.
94. As an Operator, I want stateful capabilities degraded when backup protection is stale, so that availability does not hide data-loss risk.
95. As an Operator, I want each unhealthy facet to offer a relevant next route, so that I can open setup, a Git proposal, a bounded action, documentation, or an external detail view.
96. As an Operator, I want the console to assess itself and the Private Gateway, so that loss of the administration product is visible through normal monitoring.
97. As an Operator, I want an inventory of every protected dataset, so that backup coverage is understandable per capability.
98. As an Operator, I want local and offsite Recovery Points distinguished, so that a same-disk Garage copy is not described as disaster protection.
99. As an Operator, I want backup freshness and retention shown, so that a successful old Job does not imply current recoverability.
100. As an Operator, I want to configure an offsite S3 destination through the Setup Journey, so that the documented protection gap can be closed intuitively.
101. As an Operator, I want offsite credentials kept out of Git, so that backup setup does not leak secrets into Desired Configuration.
102. As an Operator, I want bucket access and versioning inspected where supported, so that the destination is validated before it is trusted.
103. As an Operator, I want an explicit acknowledgement when versioning cannot be inspected, so that unknown protection is not silently marked complete.
104. As an Operator, I want a bounded validation backup and replication run, so that configuration is proven by evidence.
105. As an Operator, I want the last Restore Drill date and result displayed, so that operational confidence includes restore experience.
106. As an Operator, I want planned restore and retention capabilities shown honestly as future work, so that I understand the roadmap without seeing fake controls.
107. As an Operator, I want normal logs, metrics, and Activity Records to stay on my systems, so that SmallWorlds sends no default telemetry.
108. As an Operator, I want to preview a redacted diagnostics bundle before sharing it, so that support material remains under my control.
109. As an Operator, I want secrets absent from browser payloads, logs, plans, custom resources, Git, diagnostics, and CI artifacts, so that observability does not weaken security.
110. As an Operator, I want credentials represented by presence, source, expiry, and rotation status rather than their value, so that the UI remains useful without revealing them.
111. As an Operator, I want preserve-data decommissioning, so that I can stop workloads or compute while retaining declared data.
112. As an Operator, I want retained provider resources and continuing costs shown during decommissioning, so that “stopped” does not imply “free.”
113. As an Operator, I want full decommissioning to inventory backup and Recovery Bundle status, so that irreversible deletion is informed.
114. As an Operator, I want full decommissioning to require typed confirmation, so that a casual click cannot delete cluster data.
115. As an Operator, I want shared DNS zones and the GitOps Overlay retained automatically, so that one Cluster Profile cannot destroy shared configuration.
116. As an Operator, I want only proven profile-owned resources deleted, so that naming similarities cannot cause collateral deletion.
117. As an Operator, I want forgetting a Cluster Profile separate from decommissioning, so that local cleanup never mutates external infrastructure accidentally.
118. As an Operator, I want the interface available in English and German from the first release, so that I can operate in my preferred language.
119. As an Operator, I want dates, durations, sizes, and currencies localized, so that operational values are easy to interpret.
120. As an Operator using assistive technology, I want WCAG 2.2 AA behavior, so that setup and observation are accessible.
121. As a keyboard user, I want every workflow and dialog usable without a pointer, so that I can operate efficiently and accessibly.
122. As a screen-reader user, I want meaningful progress summaries rather than every log line announced, so that long Workflow Runs remain understandable.
123. As an Operator, I want states communicated with text and icons as well as color, so that status remains clear under different visual conditions.
124. As an Operator, I want reduced-motion and high-contrast support, so that the console respects my display needs.
125. As an Operator, I want status and diagnostics usable on a phone, so that I can inspect the cluster from an enrolled mobile device.
126. As an Operator, I want setup and plans to remain functional on mobile while optimized for larger screens, so that emergency access is possible without compromising normal usability.
127. As an Operator, I want signed, checksummed release artifacts and managed downloads, so that prerequisite-free installation does not weaken supply-chain integrity.
128. As an Operator, I want interrupted downloads cached and resumed, so that temporary network failures do not restart setup unnecessarily.
129. As an Operator, I want internet requirements diagnosed clearly, so that prerequisite-free is not confused with offline installation.
130. As a future air-gapped Operator, I want the Offline Bundle retained on the roadmap, so that offline bootstrap is not forgotten.

## Implementation Decisions

- The product has two execution surfaces: a native Bootstrap Launcher before and outside the cluster, and an in-cluster Operator Console after bootstrap.
- The existing Member Dashboard remains separate and low privilege.
- The browser client uses SvelteKit with Svelte 5 and a static adapter. Go exclusively owns backend endpoints, workflows, persistence, credentials, infrastructure, and cluster access.
- The same compiled browser client is embedded in both execution surfaces, with server-advertised capability differences instead of duplicated frontend applications.
- The launcher ships natively for Linux x86-64/ARM64, macOS Intel/Apple Silicon, and Windows x86-64. Every platform supports remote Linux installation; same-host installation is Linux-only.
- One launcher manages multiple Cluster Profiles. Each in-cluster console is single-cluster.
- One Launcher Host is the Lifecycle Authority for a Cluster Profile. Concurrent multi-launcher mutation is not supported.
- Recovery Bundles use age-compatible encryption with passphrase/scrypt by default and optional recipient keys.
- The launcher runs as a single unprivileged per-user background process. It does not install a privileged service or auto-start in the first release.
- Local launcher authentication uses a one-time loopback URL exchanged for a secure session. In-cluster authentication uses Keycloak OIDC.
- Console Roles are Observer, Operator, and Owner. Infrastructure lifecycle authority is not granted through an in-cluster role.
- The first Owner is established through a one-time claim and passkey registration. The realm administrator remains break-glass.
- Launcher-based Owner recovery is a first-release journey.
- Durable non-secret cluster configuration belongs exclusively to the GitOps Overlay and is reconciled by Argo CD.
- Initial repository creation may push directly. Subsequent durable changes use a proposal branch and pull request where the Git Provider supports it.
- Secret values never enter Git. Launcher credentials live in the Launcher Vault; cluster-required values live as Cluster Secrets.
- GitHub is the first full Git Provider. Generic HTTPS Git supports existing repositories and proposal branches. SSH remotes and other first-class providers are deferred.
- GitHub uses guided fine-grained personal access tokens. Temporary repository-creation authority is rotated to repository-scoped ongoing authority.
- Hetzner infrastructure continues to use HCL reconciled by a launcher-managed pinned OpenTofu toolchain.
- Hetzner project resources are discovered, explicitly adopted, or created after token validation. Shared resources and profile-owned resources are distinguished.
- Local Cluster Nodes support agent, private-key, and password SSH with root or sudo. Host-key pinning is mandatory.
- Ordinary Local setup resumes only recognized same-profile installations and blocks foreign clusters or unidentified data.
- The Setup Journey is dependency-driven and resumable rather than a rigid page sequence.
- Every mutation follows Inspect, Plan, Approve, Execute, and Verify.
- Change Plans are immutable, risk-labeled, precondition-bound, and secret-free.
- Workflow Runs persist checkpoints, structured redacted events, cancellation state, and verification evidence.
- Launcher workflows persist in SQLite. In-cluster compact plans/runs persist as Kubernetes custom resources, with detailed events in Loki.
- The static client communicates through versioned JSON HTTP endpoints described by OpenAPI 3.1. Server-Sent Events deliver live updates.
- There is no GraphQL, general WebSocket protocol, or generic arbitrary-command endpoint in the first release.
- Cluster Capabilities are defined in a versioned declarative catalog and classified as Platform Services or Community Applications.
- The catalog declares stable identity, optionality, dependencies, conflicts, supported Deployment Modes, exposure, resource estimates, evidence, protection expectations, and localized display keys.
- Platform Services required for a valid cluster are always selected. Community Applications are opt-in through capacity-aware presets or Custom selection.
- Capability Assessment is a dedicated module that derives a headline state from configuration, delivery, runtime, access, and protection evidence.
- The Private Gateway is the only web entrypoint for the Operator Console, Grafana, Argo CD, and future operator interfaces.
- Operator services have no public ingress route and are protected by NetworkPolicies as defense in depth.
- Headscale coordination remains reachable only as required by the selected Deployment Mode.
- Tailscale client detection, verified acquisition, elevation, and enrollment are guided launcher responsibilities.
- Private Network DNS resolves operator hostnames. Member-facing Local LAN DNS remains separate guidance.
- LAN-only mode does not promise remote administration.
- LAN-only HTTPS uses a Cluster CA whose root stays with the Lifecycle Authority and whose intermediate is available to cluster certificate issuance.
- Local internet-exposed router configuration remains manual and acknowledged; automatic router changes and a dedicated forwarding verifier are excluded.
- Grafana and Argo CD use Keycloak OIDC and normal read-only mappings. Their local administrator credentials are break-glass only.
- The first backup surface covers protection inventory, offsite S3 configuration, destination validation, bounded validation runs, and manual Restore Drill records.
- Restore execution, retention mutation, and Recovery Point deletion are deferred.
- The first in-cluster mutation allowlist covers Git proposals, backup configuration/validation, Operator Device enrollment management, observation refresh, credential rotation, and planned retry of idempotent setup work.
- Community Applications can be added after bootstrap but not removed in the first release.
- Safe preserve-data and full decommissioning are first-release Launcher responsibilities.
- English and German are complete initial languages. Backend error contracts use stable codes and parameters rather than translated prose.
- WCAG 2.2 AA is a release criterion.
- No outbound analytics or crash reporting occurs by default. Diagnostics are local and previewed before export.
- Cluster and launcher updates are explicit, signed, pinned, and compatibility-checked. There are no silent infrastructure or capability upgrades.
- Initial bootstrap requires internet access. An Offline Bundle remains explicit future work.
- Existing Cluster Import is deferred. Legacy scripts stay operational for critical fixes until import has been stable for at least one release.
- Full non-interactive CLI parity is deferred. The binary initially exposes only operational launcher/version/diagnostic/recovery commands.
- Release-specific bootstrap asset distribution remains one scheduled decision gate. Early modules consume an internal read-only asset source so packaging can be decided before real provisioning adapters.

## Testing Decisions

- The primary acceptance seam is the versioned browser/backend interface driving the shared Inspect, Plan, Approve, Execute, and Verify workflow. Tests should assert observable plans, events, external effects, assessments, and recovery behavior rather than internal function calls.
- Svelte browser journeys are exercised through Playwright, following the repository's existing end-to-end precedent for real user flows.
- The existing ephemeral staging workflow is prior art for real Hetzner provisioning, guaranteed cleanup, Argo convergence, and live browser verification.
- Deterministic domain rules receive focused tests beneath the primary seam where combinatorial coverage is valuable: journey dependencies, plan invalidation, catalog validation, assessment aggregation, redaction, ownership classification, and Recovery Bundle integrity.
- Every adapter has contract tests for its actual variable behavior: GitHub/generic Git, Hetzner, OpenTofu, SSH/sudo, Kubernetes/Argo, Headscale, Keycloak, S3, SQLite, and cluster custom resources.
- Provider contract tests cover pagination, permission failures, conflicts, rate limits, transient failures, retries, and ambiguous partial completion.
- SSH tests cover password, key, agent, sudo, host-key mismatch, interruption, and foreign-installation detection.
- OpenTofu tests cover pinning, initialization, planning, state locking, backup, interruption, reinspection, and sensitive-output redaction.
- GitOps rendering uses golden behavior fixtures for all Deployment Modes, capability presets, custom domains, and development suffixes, followed by Kustomize and schema validation.
- Secret scanning is required across generated Git, frontend payloads, logs, custom resources, diagnostics, CI artifacts, and cleartext Recovery Bundle metadata.
- Capability Assessment observers are tested separately from pure aggregation rules, ensuring unknown or stale evidence never becomes healthy by default.
- SSE tests cover reconnect cursors, duplicate events, interrupted streams, browser closure, and process restart.
- Authorization tests verify every Console Role server-side and confirm that hiding a control is never the only access restriction.
- Private Gateway integration tests attempt public-IP access with forged operator host headers and must fail.
- Private networking tests prove stable gateway identity, DNS, TLS, OIDC callbacks, and NetworkPolicy behavior in all three network shapes.
- Backup tests distinguish Job success from local Recovery Point, offsite Recovery Point, retention, versioning confidence, and Restore Drill evidence.
- Decommission tests inventory shared/profile-owned/unknown resources and inject interruption at each deletion stage.
- Recovery Bundle round-trip tests run across supported operating-system families and cover wrong credentials, corruption, duplicate authority, and identity mismatch.
- Accessibility tests use automated axe checks, keyboard-only Playwright journeys, focus management checks, screen-reader-friendly progress behavior, reduced motion, high contrast, and mobile reflow in both languages.
- Translation CI requires English/German key parity and validates parameter formatting for backend error codes.
- Pull requests run deterministic tests without paid cloud mutation.
- A gated workflow provisions and cleans up an ephemeral Hetzner cluster under strict cost and time limits.
- A dedicated Linux node validates Local LAN-only setup over SSH.
- Local internet-exposed mode requires a recorded manual stable-release test because router behavior is not reproducible in ordinary CI.
- A stable release requires current passing evidence for all three Deployment Modes.

## Out of Scope

- Importing an existing script-created SmallWorlds cluster into a Cluster Profile.
- Air-gapped or offline bootstrap; the future Offline Bundle remains planned.
- Full headless/non-interactive CLI parity.
- Concurrent lifecycle mutation or synchronization across multiple Launcher Hosts.
- First-class GitLab, Forgejo, or other Git Provider integrations.
- SSH Git remotes.
- Secret values encrypted into the GitOps Overlay through SOPS or another Git decryption controller.
- Removing or disabling an installed stateful Community Application.
- Privatizing all Community Applications.
- Automatically configuring routers through UPnP, NAT-PMP, or vendor APIs.
- A dedicated external router-forward verification task.
- Promise of remote administration for Local LAN-only mode.
- Direct workload restart, forced Argo sync, storage resize, database repair, restore execution, Recovery Point deletion, or retention mutation.
- Arbitrary Kubernetes, shell, OpenTofu, YAML, or HCL execution supplied through the browser.
- Autonomous AI remediation or generic self-healing operations.
- Embedding Grafana or Argo CD with iframes.
- Normal write access through Grafana or Argo CD.
- Silent launcher, cluster, capability, or infrastructure upgrades.
- Default analytics, usage telemetry, or automatic crash reporting.
- Runtime machine translation; English and German are authored release content.
- Replacing or removing legacy installation scripts before launcher parity and stable Existing Cluster Import.

## Further Notes

- The implementation companion is the completed Operator Console implementation plan, which defines repository structure, milestone slices, risks, release gates, and Definition of Done.
- The project glossary is authoritative for terms such as Operator Console, Bootstrap Launcher, Cluster Profile, Setup Journey, Cluster Capability, Capability Assessment, Private Gateway, Recovery Point, and Workflow Run.
- Accepted architectural decisions are recorded as ADRs and should not be silently reopened during implementation.
- The ideal test surface is the highest shared workflow seam. Provider and persistence adapters exist because behavior genuinely varies, not to expose their implementation to callers.
- Release-specific bootstrap asset packaging is intentionally unresolved. The choice between embedding assets and fetching a separately signed archive must be decided before real Hetzner or Local provisioning implementation, after a bounded experiment compares reproducibility, release automation, size, caching, downgrade support, and failure modes.
- Svelte 5 MCP support is not a prerequisite. Version-sensitive implementation details should be checked against official Svelte documentation when work begins.
- The first implementation tracer should establish the launcher/Svelte/OpenAPI/session/workflow seam with fake adapters before any live cloud mutation.
- Local LAN-only is the first real provisioning tracer because the repository already has a verified Local bootstrap path and no current production Hetzner node.
- The existing unrelated working-tree modification to the Bulwark deployment is outside this PRD and must remain untouched.

## Comments
