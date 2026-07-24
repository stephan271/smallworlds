# Complete the Local LAN-only private administration handoff

Status: in-progress

## Implementation progress

Following the repository's tracer-per-commit pattern (as issue 09 was built),
this issue is being landed incrementally. Each tracer compiles, is gofmt-clean,
and ships unit tests.

- [x] **Cluster CA hierarchy** (`internal/clusterca`) — foundation for acceptance
  criterion 1. Creates an offline P-384 root whose private key stays with the
  Lifecycle Authority (`RootPrivateKeyPEM`, intended for Launcher Vault custody);
  issues a path-length-constrained intermediate (`MaxPathLenZero`) that the
  cluster can use to mint leaves but never further sub-CAs; produces key-free
  `DeviceTrust` for explicit trust install on the current Operator Device; and a
  secret-free `Reference`/`Digest` for SQLite persistence and plan binding.
  `LoadAuthority` supports resume. Tests assert the chain verifies end to end,
  the root key never reaches cluster/device material, and references are
  secret-free. `go build ./...` and `go test ./internal/clusterca/` pass.
- [x] **Cluster CA custody + endpoints** — closes acceptance criterion 1 at the
  API/contract level. New `cluster_ca_references` state table (migration 13,
  opaque public-material JSON, device-trust flag) with Record/Get/MarkInstalled
  methods; three authenticated endpoints — `POST /api/v1/cluster-ca/establish`
  (creates the root, custodies the root **and** intermediate private keys in the
  Launcher Vault, records secret-free metadata + credential references,
  idempotent/resumable), `GET /api/v1/cluster-ca` (secret-free status), and
  `POST /api/v1/cluster-ca/device-trust` (returns the key-free root certificate
  to install and marks it installed). OpenAPI contract + browser types updated.
  Endpoint enforces LAN-only (`local-lan`) mode, since internet-exposed/Hetzner
  use publicly trusted ACME certificates. Tests prove locked-vault rejection,
  no key/certificate leakage to the browser, idempotent identity, credential
  visibility without values, and device-trust install. Also fixed a pre-existing
  issue-09 test-compile break (`successfulBootstrapRunner` missing `Observe`)
  that blocked the launcher test package. Full `go build/vet/test ./...` pass.

- [x] **Private Network shape + custody + endpoints** — closes acceptance
  criterion 2. New `internal/privatenetwork` deterministically derives the
  LAN-only shape: private (non-public) Headscale coordination host, stable
  operator hostnames (`console`/`grafana`/`argocd`) under a private base domain,
  all resolving via MagicDNS onto one stable Private Gateway hostname — no
  permanent hosts-file entries. Secret-free `Reference`/`Digest`;
  `GenerateCoordinationSecret` kept out of the reference. New `private_networks`
  state table (migration 14); `POST /api/v1/private-network/establish` (custodies
  the Headscale coordination secret in the Vault, idempotent/resumable, LAN-only)
  and `GET /api/v1/private-network`. OpenAPI contract updated. Tests prove
  locked-vault rejection, no secret leakage, hostnames resolving to the gateway,
  idempotency, credential visibility, and the LAN-only guard. Full build/vet/test
  pass.

- [x] **Tailscale client detection + acquisition offer** — closes acceptance
  criterion 3. New `internal/tailscaleclient` detects an installed official
  client (injectable probe; API never leaks the host path), resolves a pinned,
  integrity-verified acquisition descriptor from a trusted-host catalog
  (`pkgs.tailscale.com`/`github.com`, HTTPS + SHA-256), always surfaces the
  explicit-elevation requirement, and always retains the manual-install
  fallback. `DefaultCatalog` ships empty — no fabricated/unverified pins — so it
  honestly offers only manual fallback until release engineering pins reviewed
  digests (mirroring how bootstrap asset digests are provided). Host-level
  `GET /api/v1/tailscale-client` endpoint; OpenAPI contract updated. Unit tests
  cover the verified-acquisition path, unsupported-platform fallback, descriptor
  rejection (untrusted host/http/missing digest/bad version/format), and
  path-free detection; HTTP test asserts platform reporting, retained fallback,
  no automated acquisition from the empty default, and no path leakage. Full
  build/vet/test pass.

- [x] **Enrollment identities + custody + endpoints** — closes acceptance
  criterion 4. New `internal/enrollment` derives two distinct tailnet
  identities under the Private Network's base domain: a **short-lived
  (bounded ≤15m TTL), single-use** Launcher Host credential and a **separate,
  stable, non-expiring** Private Gateway identity that survives pod
  restart/reschedule. Secret-free `Reference` with `ConsumeLauncher`
  single-use semantics; two secrets generated for Vault custody, never in the
  reference. New `enrollments` state table (migration 15). Endpoints:
  `POST /api/v1/enrollment/establish` (requires an established Private Network,
  custodies both secrets, idempotent/resumable), `GET /api/v1/enrollment`, and
  `POST /api/v1/enrollment/launcher/consume` (marks used, **destroys** the
  single-use secret + its custody reference; rejects reuse/expiry). OpenAPI
  contract updated. Unit tests cover derivation, single-use consumption,
  expiry rejection, and lifetime validation; HTTP test proves the
  private-network precondition, distinct lifetimes, no secret leakage, custody
  visibility, one-time consumption, and that the stable gateway survives
  launcher consumption. Full build/vet/test pass.

### Remaining tracers (each still to build)

1. HTTPS-only-via-Private-Gateway access with LAN/public-ingress and forged
   Host-header rejection (criterion 5).
2. Pre-close verification of private reachability, DNS, TLS, and gateway identity
   before any temporary SSH/Kubernetes path is removed (criterion 6).
3. Short-lived first-owner claim; successful passkey registration permanently
   disables the bootstrap grant (criterion 7).
4. Final Setup Journey assessment explaining LAN-only limitations + in-cluster
   console handoff URL (criterion 8), plus the browser acceptance test and the
   Setup Journey UI wiring for the Cluster CA, Private Network, and enrollment
   steps.

## What to build

Complete a Local LAN-only setup by establishing trusted HTTPS and private-only access to operator interfaces, enrolling the Launcher Host, and handing routine administration to the first Console Owner. The flow must preserve LAN-only semantics: no router port is silently opened and no promise of remote administration is made.

Covers PRD user stories 63, 66–75, and 78–80.

## Acceptance criteria

- [x] The Lifecycle Authority creates and protects the Cluster CA root, issues only an intermediate to the cluster, and can explicitly install trust on the current Operator Device. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [x] Headscale coordination and Private Network DNS are reachable only in the LAN-only shape and resolve stable operator hostnames without permanent hosts-file entries. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [x] The launcher detects the official Tailscale client, offers pinned verified acquisition with explicit elevation, and retains a manual fallback when automation is unavailable. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [x] The Launcher Host enrolls with a short-lived single-use credential while the Private Gateway uses a separate stable identity that survives pod restart or reschedule. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [ ] Operator Console, Grafana, and Argo CD are reachable through standard HTTPS only via the Private Gateway and cannot be reached through LAN/public ingress or forged Host headers.
- [ ] Private reachability, DNS, TLS, and gateway identity are verified before any temporary SSH or Kubernetes administration path is closed.
- [ ] The launcher displays a short-lived first-owner claim, and successful passkey registration permanently disables the bootstrap grant.
- [ ] The final Setup Journey assessment explains LAN-only limitations and provides the in-cluster console handoff URL.

## Blocked by

- [Issue 09](09-bootstrap-kubernetes-and-gitops-on-a-local-cluster-node.md)
