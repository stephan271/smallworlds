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
