# Observe Cluster Capabilities through role-controlled evidence

Status: in-progress

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

## What to build

Deliver the first useful in-cluster Operator Console. Authenticated Operators see an overview and per-capability explanations derived from configuration, Argo delivery, Kubernetes runtime, access, and protection evidence. Server-side Console Roles govern every route and action, while Grafana and Argo CD remain contextual, private, OIDC-authenticated, read-only investigation tools.

Covers PRD user stories 81–96.

## Acceptance criteria

- [ ] Keycloak OIDC validates issuer, audience, nonce, and PKCE and maps Observer, Operator, and Owner roles with default denial for users without a Console Role.
- [ ] Server-side authorization proves Observers cannot mutate, Operators can access allowed proposals/actions, and Owners can access sensitive in-cluster administration.
- [ ] Every cataloged Cluster Capability displays a headline Capability State backed by configuration, delivery, runtime, access, and protection facets.
- [ ] Facets retain reason codes, timestamps, staleness, and unknown evidence; Argo Healthy or a ready workload is never sufficient by itself.
- [ ] Exposure policy changes how access evidence is evaluated, and stale protection can degrade a serving stateful capability.
- [ ] Each unhealthy facet offers one relevant next route to setup, a proposal, a bounded Runtime Action, documentation, Grafana, or Argo CD.
- [ ] Grafana and Argo CD use Keycloak OIDC with normal read-only mappings, open contextually in new tabs, and remain unreachable outside the Private Gateway.
- [ ] The console and Private Gateway appear in the capability model and can be assessed as degraded without relying on the console as the sole alert path.
- [ ] Compact in-cluster plans/runs persist through restart while detailed events remain redacted and referenced from Loki.

## Blocked by

- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
