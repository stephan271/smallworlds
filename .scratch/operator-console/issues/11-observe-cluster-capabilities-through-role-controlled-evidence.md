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
