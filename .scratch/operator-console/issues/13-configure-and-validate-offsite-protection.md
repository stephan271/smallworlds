# Configure and validate offsite protection

Status: ready-for-agent

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
