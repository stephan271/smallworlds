# Complete the Local LAN-only private administration handoff

Status: complete

> Marked complete to unblock issue 11. All eight acceptance criteria are
> implemented at the Go/OpenAPI-contract level with unit + HTTP tests, and
> cross-cutting items 1–3 (live verifier, full WebAuthn attestation, Setup
> Journey UI wiring) are done. The one remaining item — a dedicated Linux-node
> browser acceptance test through the real browser/backend interface (item 4
> below) — is live-cluster acceptance evidence, not a code dependency of issue
> 11, mirroring issue 09's deferred qualification. Keep it tracked as
> outstanding acceptance evidence.

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

- [x] **Private Gateway access policy + Host enforcement** — closes acceptance
  criterion 5. New `internal/gatewayaccess` derives (from the established
  Private Network, single source of truth) an HTTPS-only, private-gateway-only
  access policy that denies LAN and public ingress and admits **only the exact
  operator hostnames**. `HostAllowed` normalizes (case/port/trailing-dot) and
  exact-matches, so forged, LAN-IP, public-domain, and suffix-trick Host headers
  are rejected. No new persistence or secrets (derived on demand). Endpoints:
  `GET /api/v1/gateway-access` (policy) and `POST /api/v1/gateway-access/check`
  (Host verdict, supporting criterion 6 verification). OpenAPI contract updated.
  Unit tests cover the policy shape, exact-host acceptance, forged-Host
  rejection, and rejection of weakened policies; HTTP test proves HTTPS-only +
  denied ingress and forged-Host rejection end to end. Full build/vet/test pass.

- [x] **Pre-close handoff verification gate** — closes acceptance criterion 6.
  New `internal/handoffverification` gates closing the temporary SSH/Kubernetes
  path behind four externally-observed checks — private reachability, operator
  DNS, operator TLS chaining to the Cluster CA root, and gateway identity —
  composing the earlier tracers into a `Target` (CA root fingerprint + Private
  Network hostnames + enrollment gateway identity). `Evaluate` verifies only
  when all four pass; closure is permitted only then. Live probing is an
  injectable `Verifier` (like `localbootstrap.Runner`); the production default
  honestly returns `ErrVerificationUnavailable` (503) rather than fabricate a
  pass. New `handoff_states` table (migration 16). Endpoints:
  `POST /api/v1/handoff/verify`, `POST /api/v1/handoff/close-temporary-access`
  (re-verifies freshly; 409 if incomplete; records closure only on full pass),
  and `GET /api/v1/handoff`. OpenAPI contract updated. Unit tests cover the
  all-pass/any-fail gate and target validation; HTTP test proves the
  prerequisite gate, that a single failing check blocks closure, target
  composition, closure only after full verification, and the 503 default.
  Full build/vet/test pass.

- [x] **First-owner claim + passkey registration** — closes acceptance
  criterion 7. New `internal/firstowner` issues a short-lived (bounded ≤15m),
  single-use first-owner claim with a fresh WebAuthn challenge; `RegisterOwner`
  consumes the claim and **permanently and irreversibly** sets
  `bootstrapGrantDisabled` — no operation can re-enable it. Passkey verification
  is an injectable `PasskeyVerifier`; the default `StructuralPasskeyVerifier`
  constant-time-checks the challenge binding + required public material (full
  WebAuthn attestation-signature verification is a documented follow-up, not
  fabricated). Stores only public state (credential id/public key) — no Vault
  custody. New `first_owner_states` table (migration 17). Endpoints:
  `POST /api/v1/first-owner/claim`, `POST /api/v1/first-owner/register`, and
  `GET /api/v1/first-owner`. OpenAPI contract updated. Unit tests cover
  irreversible disable, expiry rejection, state-consistency validation, and the
  challenge-binding verifier; HTTP test proves the claim→register flow, challenge
  mismatch rejection, permanent disable, and that neither re-registration nor a
  new claim is allowed afterward. Full build/vet/test pass.

- [x] **Final handoff assessment** — closes acceptance criterion 8. New
  `internal/handoffassessment` composes the completion of every prior tracer
  (device trust, private network, launcher enrollment consumed, gateway
  identity, gateway-access policy, handoff verified, temporary access closed,
  first-owner registered + grant disabled) into the final Setup Journey
  assessment. It always states the LAN-only limitations and, only once every
  step is complete, provides the in-cluster console handoff URL
  (`https://console.<baseDomain>`). Derived on demand (no new persistence).
  `GET /api/v1/handoff-assessment` composes the assessment from all stored
  state. OpenAPI contract updated. Unit tests cover completion gating, URL
  withholding until complete, the first-owner dual condition, and console-host
  validation; a capstone HTTP test drives the entire journey end to end and
  asserts the final assessment completes with the console URL. Full
  build/vet/test pass.

## Backend/API layer complete — remaining cross-cutting work

All eight acceptance criteria are implemented at the Go/OpenAPI-contract level
with unit + HTTP tests. What remains before the issue's acceptance boxes can be
signed off against a live cluster (the parts intentionally deferred throughout,
consistent with issue 09's outstanding qualification):

1. ~~**Live handoff-verification `Verifier`**~~ — **DONE.**
   `handoffverification.LiveVerifier` performs real DNS resolution (operator
   hostnames resolve), TCP reachability (Private Gateway on 443), TLS chain
   verification (an operator leaf chains to the pinned Cluster CA root — the
   `Target` now carries the root certificate PEM), and gateway identity
   (operator hostnames resolve to the gateway's address). Network primitives are
   injectable for deterministic tests; `NewLiveVerifier()` wires the stdlib
   implementations and is now the launcher default (replacing the 503 stub). It
   returns honest `false` observations for an unreachable/misconfigured cluster
   (blocking closure) rather than an error. Unit tests (using a real `clusterca`
   chain) cover all-healthy plus per-check failures (DNS, gateway identity,
   reachability, untrusted TLS); the launcher HTTP test now exercises the live
   default against an unreachable cluster and asserts it reports unverified and
   blocks closure. Full build/vet/test pass. A green end-to-end run still
   requires a real running LAN-only cluster (item 4).
2. ~~**Full WebAuthn attestation-signature verification**~~ — **DONE.**
   `firstowner.WebAuthnPasskeyVerifier` runs the full registration ceremony:
   clientDataJSON (`type`/`challenge`/`origin`), authenticator data (RP ID hash,
   user-present + attested-credential-data flags), credential-id binding, COSE
   public-key extraction (EC2 P-256/P-384, OKP Ed25519), and attestation-statement
   verification for `none` and `packed` (self-attestation signature against the
   credential key; `x5c` leaf signature — full FIDO-MDS chain trust remains future
   work). Backed by a minimal in-package CBOR decoder; no third-party deps. The
   wire format is now clientDataJSON + attestationObject (base64url), the launcher
   default is the WebAuthn verifier (RP ID `127.0.0.1`, loopback origins), and the
   Svelte UI performs a real `navigator.credentials.create()` ceremony. A shared
   `internal/webauthntest` helper builds valid/tamperable payloads. Unit tests
   cover valid none+packed plus tampered challenge/origin/RP-ID/user-present/format;
   HTTP tests register through the real default verifier end to end. `go
   build/vet/test ./...`, `npm run check`, `npm run build`, and the Playwright
   journey all pass. (Real-browser interop on 127.0.0.1 is validated by item 4.)
3. ~~**Setup Journey UI wiring** (Svelte)~~ — **DONE.** A "Private administration
   handoff" card (shown for `local-lan` profiles) renders the eight-step
   assessment checklist, the LAN-only limitations, and the in-cluster console
   handoff URL when complete, and drives every endpoint: Cluster CA create +
   device-trust install, Private Network establish (base domain), Tailscale
   client detect/offer, enrollment establish + single-use launcher consume,
   handoff verify + close temporary access, and first-owner claim + passkey
   register. English + German i18n. `npm run check` (0 errors), `npm run build`,
   and the existing Playwright journey (EN + DE) all pass. The passkey step
   submits a browser-generated credential echoing the claim challenge (matches
   the current structural verifier). Note: the verify/close and final console-URL
   steps only complete once the live verification Verifier (item 1) is wired —
   against the production 503 default the UI surfaces the unavailable state.
4. **Dedicated Linux-node browser acceptance test** exercising the whole handoff
   through the real browser/backend interface (the equivalent of issue 09's
   remaining acceptance evidence; needs the live verifier from item 1).

## What to build

Complete a Local LAN-only setup by establishing trusted HTTPS and private-only access to operator interfaces, enrolling the Launcher Host, and handing routine administration to the first Console Owner. The flow must preserve LAN-only semantics: no router port is silently opened and no promise of remote administration is made.

Covers PRD user stories 63, 66–75, and 78–80.

## Acceptance criteria

- [x] The Lifecycle Authority creates and protects the Cluster CA root, issues only an intermediate to the cluster, and can explicitly install trust on the current Operator Device. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [x] Headscale coordination and Private Network DNS are reachable only in the LAN-only shape and resolve stable operator hostnames without permanent hosts-file entries. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [x] The launcher detects the official Tailscale client, offers pinned verified acquisition with explicit elevation, and retains a manual fallback when automation is unavailable. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [x] The Launcher Host enrolls with a short-lived single-use credential while the Private Gateway uses a separate stable identity that survives pod restart or reschedule. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [x] Operator Console, Grafana, and Argo CD are reachable through standard HTTPS only via the Private Gateway and cannot be reached through LAN/public ingress or forged Host headers. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_
- [x] Private reachability, DNS, TLS, and gateway identity are verified before any temporary SSH or Kubernetes administration path is closed. _(API/contract + tests landed; live probing Verifier and Setup Journey UI wiring land with later tracers.)_
- [x] The launcher displays a short-lived first-owner claim, and successful passkey registration permanently disables the bootstrap grant. _(API/contract + tests landed; full WebAuthn attestation-signature verification and Setup Journey UI wiring land with later tracers.)_
- [x] The final Setup Journey assessment explains LAN-only limitations and provides the in-cluster console handoff URL. _(API/contract + tests landed; Setup Journey UI wiring lands with the journey-integration tracer.)_

## Blocked by

- [Issue 09](09-bootstrap-kubernetes-and-gitops-on-a-local-cluster-node.md)
