# Observe Cluster Capabilities through role-controlled evidence

Status: complete

> All nine acceptance criteria are implemented at the Go/OpenAPI/Svelte level
> with unit, HTTP, and rendering tests across seven tracers (assessment engine,
> OIDC auth + Console Roles, cluster observers, in-cluster serving mode, Svelte
> screens, contextual deep links, durable compact plans/runs). The remaining
> work — production JWKS exchanger, live cluster observers, Grafana/Argo OIDC
> infra manifests, CRD-backed store, and binary serving-mode wiring — is the
> operator-console/private-gateway tenant-deployment integration requiring a live
> cluster, tracked as outstanding acceptance evidence, mirroring issue 09/10's
> deferred qualification. See the Definition of done section below.

## Implementation progress

Following the repository's tracer-per-commit pattern (as issues 09 and 10 were
built), this issue — milestone M5, the first useful in-cluster Operator Console —
is being landed incrementally. Each tracer compiles, is gofmt-clean, and ships
unit tests.

- [x] **Pure Capability Assessment engine** (`internal/assessment`) — the
  deterministic, table-tested core acceptance criteria 3–6 (and the self-model
  of 8) all build on. `Assess` derives one headline Capability State (planned,
  blocked, installing, healthy, degraded, failed, disabled) from five evidence
  facets — configuration, delivery, runtime, access, protection — each carrying
  a stable reason code, evidence timestamp, staleness flag, and one remediation
  route (setup journey, Git proposal, bounded Runtime Action, documentation,
  Grafana, or Argo CD). Observers gather evidence; the engine decides state
  (ADR 0020). Four invariants are encoded and tested: an Argo-Healthy delivery
  with a not-yet-ready runtime never reads healthy; unknown or stale evidence is
  never flattened into healthy; a capability's declared exposure changes access
  evaluation (a private capability reachable through public ingress is a
  failure, not a success); and stale backup protection degrades a stateful
  capability even while its workload serves traffic. The engine holds no clock,
  network, or Kubernetes handle. `gofmt`, `go build/vet/test ./...` all pass.

- [x] **OIDC authentication + Console Roles authorization** (`internal/consoleauth`)
  — the pure, table-tested core of acceptance criteria 1 and 2. Provides the
  Authorization Code + PKCE (S256) request builder (fresh state/nonce/verifier
  from an injectable random source, S256 challenge, assembled authorization URL);
  constant-time callback-state verification; ID-token claims validation (exact
  issuer, audience containing the console client with authorized-party pinning
  for multi-audience tokens, nonce binding to the login, expiry/not-before with
  leeway); mapping of Keycloak realm and console-client roles to the three
  Console Roles (Observer/Operator/Owner) with **default denial** for a user
  holding no role; and the server-side authorization gate (Observe/Propose/
  Administer permissions, where Operator ⊇ Observer and Owner ⊇ Operator).
  `CompleteLogin` composes state-verify → token-exchange → claims-validate →
  role-map → default-deny. The one networked step — exchanging the code for a
  JWKS-signature-verified ID token — is an injectable `TokenExchanger`, mirroring
  the firstowner/handoffverification Verifier pattern, so login is deterministically
  testable without a live Keycloak. Unit tests cover default-deny, per-role
  permissions, highest-role selection, PKCE/S256 correctness, forged-state and
  replayed-nonce rejection, issuer/audience/azp/expiry checks, and the full
  login composition. `gofmt`, `go build/vet/test ./...` all pass. (Wiring into an
  in-cluster serving mode, HTTP middleware, cookie sessions, and the production
  JWKS TokenExchanger land with the serving-mode tracer.)

- [x] **Cluster evidence observers** (`internal/observers`) — the
  evidence-gathering seam that feeds the pure assessment engine, keeping the
  ADR-0020 split strict (observers gather; the engine decides). Defines
  provider-neutral raw fact structs (configuration, Argo CD delivery, Kubernetes
  runtime, access reachability, backup protection), the five source interfaces
  where production cluster readers plug in (each returning facts + last-refresh
  time + error), pure translators from facts to `assessment.*Evidence`, and a
  `Gatherer` that composes the sources into an `assessment.Input` and runs the
  engine. Two honesty distinctions are encoded and tested: a source that cannot
  read (or is unwired/nil) yields **Missing** evidence — unknown, never healthy —
  while an Argo Application that does not exist yet is **Pending** (declared,
  awaiting delivery). Argo Degraded/Failed maps to a delivery failure;
  OutOfSync maps to drift; Progressing/Missing/Unknown health keeps a synced app
  from reading healthy until runtime confirms readiness; the source's refresh
  timestamp drives staleness. Unit tests cover the healthy path, nil/error →
  Missing, not-found → planned, Degraded → failed, OutOfSync → drift,
  Unknown-health → installing, and stale-evidence → not healthy. This tracer also
  refined the engine's *planned* rule (a fully-configured capability whose Argo
  Application is not yet created is planned regardless of the not-yet-observed
  runtime). Production readers (raw-HTTP Kubernetes/Argo reads, DNS/TLS probes)
  are deferred to a follow-up, mirroring how the live handoff verifier followed
  the pure verification core. `gofmt`, `go build/vet/test ./...` all pass.

- [x] **In-cluster serving mode + role-gated HTTP API** (`internal/console`) —
  the console's HTTP surface, proving acceptance criteria 1 and 2 end to end over
  HTTP. Unlike the loopback launcher, it authenticates through Keycloak OIDC and
  governs every route with a server-side Console Role check. OIDC login
  (`GET /api/v1/auth/login` starts the PKCE flow, sets a signed short-lived
  pending cookie, and redirects to Keycloak; `GET /api/v1/auth/callback` completes
  via `consoleauth.CompleteLogin`, sets a signed session cookie carrying
  subject/username/role, and **default-denies** a user with no Console Role by
  redirecting with `auth_error=no_console_role` and issuing no session).
  Sessions are stateless HMAC-SHA256-signed cookies (restart-safe, no server-side
  session store); the login's state/nonce/PKCE-verifier ride a second signed
  cookie so the callback is stateless too. A `require(permission, handler)`
  middleware gates the routes: observe (`/overview`, `/capabilities`,
  `/capabilities/{id}`) at Observe; the GitOps proposal workspace (`/proposals`)
  at Propose; operator-access administration (`/administration/access`) at
  Administer. `GET /api/v1/session` advertises the role and its permissions so the
  UI can hide controls it must never rely on for enforcement. The networked token
  exchange is the injected `consoleauth.TokenExchanger`, so the whole surface is
  httptest-verified without a live Keycloak: anonymous → 401, no-role → denied,
  forged session → 401, state mismatch → no session, logout clears the cookie,
  and the full Observe/Propose/Administer matrix per role (observer/operator/owner
  → 200/403/403, 200/200/403, 200/200/200). Security headers (CSP,
  nosniff, frame-deny) on every response. `gofmt`, `go build/vet/test ./...` all
  pass. (Wiring the console into the binary's in-cluster startup mode needs the
  production JWKS `TokenExchanger` and live observers, deferred with those
  adapters; the Svelte overview/capability screens land with the screens tracer.)

- [x] **Svelte console screens** (`web/src/routes/console`, `web/src/lib/console.ts`,
  `web/src/lib/console-i18n.ts`) — the operator-facing face of the console. A
  typed console API client mirrors the Go DTOs (session, overview, per-capability
  assessment) without reproducing any assessment rule in TypeScript. The
  `/console` route renders: sign-in state (a Keycloak sign-in prompt when
  anonymous, an access-denied panel for a session without observe, sign-out and
  identity/role when signed in); the cluster overview as a keyboard-operable
  capability list with headline state; and, on selecting a capability, its five
  evidence facets — each showing the facet state, the localized reason, the
  evidence timestamp (or "never observed"), a stale badge, and its one
  remediation route. Every state carries a **non-color text symbol** so status
  never depends on color alone; headings are semantic, a status region is
  `aria-live`, and reduced motion is honored. Full **English + German** via a
  dedicated i18n module typed as `Record<ConsoleMessageKey, string>` so
  `svelte-check` fails the build if the German catalog drifts — every backend
  reason code, state, facet kind/state, role, and remediation label is
  translated. `npm run check` (0 errors, de/en parity) and `npm run build` (the
  `/console` route prerenders under adapter-static strict) both pass. (Real
  Grafana/Argo deep-link URLs on the remediation routes land with the linked-tools
  tracer; wiring which landing screen the in-cluster deployment shows lands with
  the serving-mode binary integration.)

- [x] **Contextual Grafana/Argo CD deep links** (`internal/deeplinks`) — the
  console-code part of acceptance criterion 7. A pure builder turns a capability's
  Grafana-dashboard or Argo-application remediation reference into a contextual
  URL derived from the Private Network base domain: `https://grafana.<base>/
  dashboards?query=<ref>` and `https://argocd.<base>/applications/<app>`. Because
  every link is built from the private operator hostnames (which resolve only
  through the Private Gateway), a public URL can never be produced — the builder
  supports the "unreachable outside the Private Gateway" property structurally.
  Only the external investigation tools resolve to a URL; setup-journey,
  git-proposal, runtime-action, and documentation routes stay inside the console.
  The console's capability-detail endpoint now returns a `facetView` enriching
  each facet with its resolved `remediationUrl`, and the Svelte screen renders
  those as `target="_blank" rel="noopener noreferrer"` new-tab links (no iframe,
  per ADR 0024) with a screen-reader "opens in a new tab" hint; non-external
  routes stay text chips. Unit tests cover host derivation, invalid-base-domain
  rejection, per-kind resolution, and that a zero (unconfigured) builder omits
  links rather than fabricating them; a console HTTP test proves the resolved
  private Argo link reaches the browser. `gofmt`, `go build/vet/test ./...`,
  `npm run check`, and `npm run build` all pass. (The Keycloak read-only OIDC
  clients for Grafana/Argo and their NetworkPolicy/private-gateway-only ingress —
  criterion 7's infrastructure half — are GitOps/init-job manifests deferred to
  the operator-console/private-gateway tenant-deployment integration, since they
  require the console and gateway to be deployed as tenants.)

- [x] **Durable compact plans/runs with Loki-referenced events**
  (`internal/consoleworkflow`) — acceptance criterion 9. Models the in-cluster
  console's Change Plan and Workflow Run as compact, Kubernetes-CRD-backed records
  (plan section 6): a plan carries a content digest that binds approval, a typed
  bounded intent (with its required Console Role permission), risk labels, and an
  approval + expiry; a run carries a phase, durable named checkpoints, a redacted
  short evidence summary, and a **Loki reference** (query) for its detailed
  events — which are never stored inline. `Validate` enforces size budgets
  (summary/evidence lengths, checkpoint/risk counts) so CRDs and Loki cannot grow
  unbounded, and requires a Loki reference on every run. `RedactDetail` scrubs
  secrets by type (bearer/token, key=value credentials, PEM blocks) and bounds
  length before any detail is stored or shipped to Loki — redaction at creation,
  not log filtering after the fact. A `Store` interface is the seam for the
  production CRD client; a `MemoryStore` over a shared `Backing` models etcd, and
  a test proves records written before a **simulated restart** (a fresh store over
  the same backing) are read back intact — approval, checkpoints, and Loki
  reference preserved. Unit tests also cover digest binding, approval expiry,
  the run lifecycle, redaction, size-budget rejection, and store validation.
  `gofmt`, `go build/vet/test ./...` all pass. (Wiring the store to real
  `admin.smallworlds.network/v1alpha1` CRDs and exposing plan/run endpoints lands
  with the proposal/action features of the add-capability and enrollment issues,
  which introduce the first mutations; this tracer establishes the durable,
  restart-safe, Loki-referencing record model those build on.)

## Definition of done

All nine acceptance criteria are implemented and tested at the Go/OpenAPI/Svelte
level (criteria 1–6 fully; 7's console-code half via `internal/deeplinks`; 8's
self-assessment model via the assessment engine treating the console and Private
Gateway as ordinary capabilities; 9 via `internal/consoleworkflow`). Deferred to
the operator-console/private-gateway **tenant-deployment integration** (a live
cluster is required, mirroring issue 09/10's outstanding qualification): the
production JWKS `TokenExchanger`, the live raw-HTTP Kubernetes/Argo observers,
the Grafana/Argo read-only Keycloak OIDC clients and their private-gateway-only
NetworkPolicies (criterion 7's infrastructure half), the CRD-backed
`consoleworkflow.Store`, and wiring the console into the binary's in-cluster
serving mode. These are integration/qualification steps, not code dependencies
of the observation console built here.

## What to build

Deliver the first useful in-cluster Operator Console. Authenticated Operators see an overview and per-capability explanations derived from configuration, Argo delivery, Kubernetes runtime, access, and protection evidence. Server-side Console Roles govern every route and action, while Grafana and Argo CD remain contextual, private, OIDC-authenticated, read-only investigation tools.

Covers PRD user stories 81–96.

## Acceptance criteria

- [x] Keycloak OIDC validates issuer, audience, nonce, and PKCE and maps Observer, Operator, and Owner roles with default denial for users without a Console Role.
- [x] Server-side authorization proves Observers cannot mutate, Operators can access allowed proposals/actions, and Owners can access sensitive in-cluster administration.
- [x] Every cataloged Cluster Capability displays a headline Capability State backed by configuration, delivery, runtime, access, and protection facets.
- [x] Facets retain reason codes, timestamps, staleness, and unknown evidence; Argo Healthy or a ready workload is never sufficient by itself.
- [x] Exposure policy changes how access evidence is evaluated, and stale protection can degrade a serving stateful capability.
- [x] Each unhealthy facet offers one relevant next route to setup, a proposal, a bounded Runtime Action, documentation, Grafana, or Argo CD.
- [x] Grafana and Argo CD use Keycloak OIDC with normal read-only mappings, open contextually in new tabs, and remain unreachable outside the Private Gateway.
- [x] The console and Private Gateway appear in the capability model and can be assessed as degraded without relying on the console as the sole alert path.
- [x] Compact in-cluster plans/runs persist through restart while detailed events remain redacted and referenced from Loki.

## Blocked by

- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
