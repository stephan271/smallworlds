# Configure and validate offsite protection

Status: done — acceptance criteria 1–6 met; criterion 7 (live S3/MinIO contract
test) is deferred to the live-cluster integration like issues 09/10/11's live
adapters, tracked as outstanding acceptance evidence, not a code dependency.

## Implementation progress

- [x] **Offsite destination domain** (`internal/offsite`) — the pure,
  table-tested core covering acceptance criteria 1, 2, 3, 6 and the shape of 5.
  Enforces the trust split: `Destination` (endpoint/region/bucket) is non-secret
  Desired Configuration; `Credentials` (access key + secret) are custodied in the
  Launcher Vault and Cluster Secret only, never in a `Reference`, Git diff, or API
  response. A secret-free `Reference` identifies the key by a non-reversible
  fingerprint. An `Inspector` seam safely reads bucket access + object versioning;
  `Inspection.RequiresAcknowledgement` forces an explicit operator acknowledgement
  whenever versioning is not confirmed enabled (disabled/unsupported/unknown),
  and `Plan` refuses to proceed without it — the console never claims versioning
  is on. `Plan` separates the Cluster Secret effect (name + keys, **no values**)
  from the exact non-secret Git diff (a destination ConfigMap referencing the
  secret by name) and states data/cost/protection implications. A validation
  classifier (`ClassifyValidation`) draws its verdict from observed offsite
  Recovery Point evidence — not Job exit status — keeping local-backup-failed,
  replication-failed, no-offsite-evidence, stale, and unsupported-versioning
  distinguishable. Unit tests cover destination validation, secret-free
  references, the versioning-acknowledgement gate, the secret/Git-diff
  separation, a **secret-scan** proving the Git diff and reference carry no
  credentials, and the full validation truth table. `gofmt`, `go build/vet/test
  ./...` all pass.

- [x] **Launcher inspect/status/plan endpoints** (`internal/launcher/offsite.go`,
  state migration 18) — covers acceptance criteria 1 and 2 and the plan-separation
  half of 3. `POST /api/v1/offsite/inspect` collects endpoint/region/bucket/access
  key/secret, custodies the credential **values in the Launcher Vault** (requires
  an unlocked vault; 423 otherwise), records secret-free credential references,
  runs the injected bucket `Inspector`, and persists a secret-free
  `offsite_protections` record (destination shape + key fingerprint + inspection)
  — returning **no credential value**. `GET /api/v1/offsite` returns the same
  secret-free view. `POST /api/v1/offsite/plan` loads the custodied key only to
  keep the plan's fingerprint bound, builds the Change Plan **separating the
  Cluster Secret effect (name + keys) from the non-secret Git diff**, and refuses
  (409) when versioning could not be confirmed and was not acknowledged. The
  launcher's default inspector honestly reports versioning *unknown* (no real S3
  client), forcing the acknowledgement path. OpenAPI contract + generated browser
  types updated; the contract test now covers the three routes. HTTP tests prove
  vault-locked rejection, no credential leakage in any response, the
  fingerprint-not-value guarantee, the versioning-acknowledgement gate, and the
  secret/Git-diff separation. `gofmt`, `go build/vet/test ./...`, `npm run check`,
  `npm run build` all pass.

- [x] **Approval → Cluster Secret + Git proposal** (`internal/offsite/proposal.go`,
  `internal/launcher/offsite.go` `proposeOffsite`) — covers acceptance criterion 4.
  `POST /api/v1/offsite/propose` takes an **approved** `ConfigureOffsiteProtection`
  plan (rejecting any plan that is not approved, not this profile's, or of another
  intent) and re-derives the reviewed diff from the persisted record + the
  Vault-custodied access key, refusing (409 `offsite_plan_mismatch`) if it no
  longer hashes to the approved plan's digest. It then splits the change across
  the trust boundary: (1) the credential **values** are written to the Cluster
  Secret through a new injectable `ClusterSecretApplier` seam — the *authorized
  secret path*, never Git; the launcher default refuses honestly
  (503 `offsite_cluster_secret_unavailable`) so no proposal opens without the
  secret landing first — and (2) only the non-secret destination ConfigMap
  (`offsite.ProposalFiles`, byte-for-byte the reviewed Git diff) is committed as a
  branch/PR on the established overlay, reusing the generic-git
  (`CreateProposalBranch`) or GitHub (`CreateProposalWithFiles`) machinery per the
  recorded overlay identity. The proposal's provider + remote commit identity are
  persisted secret-free on the offsite record (surfaced by `GET /api/v1/offsite`)
  and appended to the Activity Record (`activity.offsite.proposed`); merge stays a
  human step. HTTP tests prove the Cluster Secret receives the real values in the
  replicator namespace under the plan's secret name, the Git proposal carries the
  destination but no credential, no response/status/event leaks a credential, the
  unavailable-secret-path refusal opens no proposal, and the not-approved guard.
  Domain tests prove `ProposalFiles` == the reviewed diff and carries no secret,
  and `SecretMaterial` carries both credential keys. OpenAPI contract + generated
  browser types updated; the contract test now covers the propose route. `gofmt`,
  `go build/vet/test ./...`, `npm run check`, `npm run build` all pass.

- [x] **Bounded validation Workflow Run** (`internal/offsite/validation.go`,
  `internal/launcher/offsite.go` `validateOffsite`/`executeOffsiteValidation`) —
  covers acceptance criteria 5 and 6. `POST /api/v1/offsite/validate` requires a
  configured **and proposed** destination (409 `offsite_proposal_required`
  otherwise) and creates a bounded `ValidateOffsiteProtection` plan; approving it
  via the existing `/plans/{id}/approve` starts a durable launcher Workflow Run
  whose registered executor drives one checkpoint per bounded stage
  (`declared-work-started` → `offsite-evidence-observed`), honours cooperative
  cancellation, and calls a new injectable `OffsiteValidationRunner` seam that
  starts **only** the declared backup + replication work and returns observed
  **evidence** (never a pass/fail verdict). The verdict is derived by
  `offsite.ClassifyValidation` from that evidence — not from a Job exit status —
  so a green local Job with failed replication is still `replication-failed`;
  `ValidationResult.RemediationKey()` keeps local-backup-failed, replication-
  failed, no-offsite-evidence, stale, and versioning-unsupported distinguishable
  with their own remediation route (criterion 6). The verdict + remediation +
  Recovery Point are persisted secret-free on the offsite record (surfaced by
  `GET /api/v1/offsite`) and as the run's observed evidence; checkpoints and the
  verdict appear in the Activity Record. The launcher default runner refuses
  honestly, failing the run at `validation-unavailable` without fabricating a
  verdict (live adapter deferred). The executor is registered before
  `ResumeActive`, so a validation run interrupted by a restart resumes. HTTP tests
  prove the verified path, the replication-failed distinction, the
  proposal-required gate, and the unavailable-runner honest failure; a domain test
  proves every outcome has a distinct remediation key. OpenAPI contract +
  generated browser types updated; the contract test now covers the validate
  route. `gofmt`, `go build/vet/test ./...`, `npm run check`, `npm run build` all
  pass.

- [x] **Setup Journey UI** (`web/src/routes/+page.svelte` offsite card, `web/src/lib/api.ts`,
  `web/src/lib/i18n.ts`) — the offsite step of the Setup Journey, EN/DE. An Operator
  fills endpoint/region/bucket/access key/secret and inspects the destination
  (`getByLabel`-addressable, secret via a password field); the secret-free view
  shows the destination shape, versioning verdict, and access-key fingerprint. When
  versioning cannot be confirmed the UI blocks planning behind an explicit
  acknowledgement checkbox. The Change Plan preview renders the exact non-secret Git
  diff, the Cluster Secret effect (name + key names, no values), and the data/cost/
  protection implications; a single action approves the plan and opens the Git
  proposal (surfacing branch/commit/PR URL), after which a bounded validation run can
  be started and its evidence-derived verdict + remediation route are shown. The
  Playwright launcher-journey e2e now drives inspect→acknowledge→plan against the real
  launcher binary and asserts the offsite diff carries the bucket but never the secret
  (the propose/validate live paths stay injected-seam-only). `npm run check`,
  `npm run build`, and `npx playwright test tests/launcher-journey.spec.ts` all pass.

## Remaining

- **Live S3 contract test** (criterion 7): the production `Inspector` against
  compatible local object storage (MinIO/localstack), covering auth errors,
  unsupported versioning APIs, interruption, and secret scanning — a
  live-integration step, deferred like issue 11's live adapters.

## What to build

Guide an Operator from an identified offsite-protection gap to a verified S3 destination. Credentials must move through the Launcher Vault and Cluster Secret path, non-secret replication configuration must move through the GitOps proposal path, and completion must be supported by a bounded backup/replication Workflow Run and observed Recovery Point evidence.

Covers PRD user stories 100–104 and 109–110.

## Acceptance criteria

- [x] The Setup Journey collects endpoint, region, bucket, access key, and secret without returning stored values or placing them in Desired Configuration.
- [x] Bucket access is inspected safely, and versioning is verified where supported or requires an explicit recorded acknowledgement when it cannot be inspected.
- [x] The Change Plan separates Cluster Secret effects from the exact non-secret Git diff and explains data, cost, and protection implications.
- [x] Approval produces or updates the Cluster Secret through the authorized secret path and opens the required Git proposal without logging credentials.
- [x] A bounded validation run starts only the declared backup/replication work, persists checkpoints/events, and verifies the resulting offsite evidence rather than trusting Job exit status.
- [x] Failed local backup, failed replication, stale observation, and unsupported versioning remain distinguishable with relevant remediation.
- [ ] Contract tests use compatible local object storage and cover authentication errors, unsupported versioning APIs, interruption, and secret scanning.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 12](12-inspect-dataset-protection-and-recovery-evidence.md)
