# Configure and validate offsite protection

Status: in-progress

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

## Remaining

- **Launcher Setup Journey wiring** (criterion 4): endpoints to collect
  credentials into the Launcher Vault, run the bucket `Inspector`, produce the
  Change Plan, and on approval create/update the Cluster Secret through the
  authorized secret path and open the required Git proposal — reusing the
  existing Vault-custody and Git-proposal machinery, without logging credentials.
- **Bounded validation Workflow Run** (criterion 5): start only the declared
  backup/replication work, persist checkpoints/events (via `consoleworkflow`),
  and classify the outcome from observed offsite evidence.
- **Setup Journey UI** for the offsite step.
- **Live S3 contract test** (criterion 7): the production `Inspector` against
  compatible local object storage (MinIO/localstack), covering auth errors,
  unsupported versioning APIs, interruption, and secret scanning — a
  live-integration step, deferred like issue 11's live adapters.

## What to build

Guide an Operator from an identified offsite-protection gap to a verified S3 destination. Credentials must move through the Launcher Vault and Cluster Secret path, non-secret replication configuration must move through the GitOps proposal path, and completion must be supported by a bounded backup/replication Workflow Run and observed Recovery Point evidence.

Covers PRD user stories 100–104 and 109–110.

## Acceptance criteria

- [ ] The Setup Journey collects endpoint, region, bucket, access key, and secret without returning stored values or placing them in Desired Configuration.
- [ ] Bucket access is inspected safely, and versioning is verified where supported or requires an explicit recorded acknowledgement when it cannot be inspected.
- [ ] The Change Plan separates Cluster Secret effects from the exact non-secret Git diff and explains data, cost, and protection implications.
- [ ] Approval produces or updates the Cluster Secret through the authorized secret path and opens the required Git proposal without logging credentials.
- [ ] A bounded validation run starts only the declared backup/replication work, persists checkpoints/events, and verifies the resulting offsite evidence rather than trusting Job exit status.
- [ ] Failed local backup, failed replication, stale observation, and unsupported versioning remain distinguishable with relevant remediation.
- [ ] Contract tests use compatible local object storage and cover authentication errors, unsupported versioning APIs, interruption, and secret scanning.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 12](12-inspect-dataset-protection-and-recovery-evidence.md)
