# Inspect and plan Hetzner infrastructure

Status: ready-for-agent

## What to build

Give an Operator a read-only Hetzner inspection and infrastructure planning journey. The launcher validates a project token, discovers relevant resources and ownership conflicts, verifies public-domain prerequisites, acquires the pinned OpenTofu toolchain, and creates a capacity-aware cost-bearing Change Plan without mutating the project.

Covers PRD user stories 45–53 and 129.

## Acceptance criteria

- [ ] A Hetzner project token is stored through the Launcher Vault and validated for project identity and required read/write authority before planning.
- [ ] Inspection inventories Primary IP, DNS zone, SSH public key, firewall, volume, server, DNS records, and reverse DNS with stable provider identities.
- [ ] Existing resources are classified as shared, profile-owned, adoptable, conflicting, or unknown; similarly named resources are never silently adopted.
- [ ] Nameserver delegation is checked before a public installation can proceed.
- [ ] Small, Recommended, and High-capacity presets derive from selected Cluster Capabilities, while advanced current location, server, and volume choices remain available.
- [ ] Live availability and estimated recurring costs appear in the plan, including volume growth limitations and resources that may remain billable.
- [ ] The launcher obtains pinned verified OpenTofu/provider artifacts and prepares an isolated per-profile state workspace with locking and backup behavior.
- [ ] No provider resource changes occur until the Operator approves a still-current immutable plan.
- [ ] Contract tests cover pagination, permissions, conflicts, rate limits, state sensitivity, provider acquisition failure, and redaction.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 04](04-select-cluster-capabilities-and-preview-a-gitops-overlay.md)
- [Issue 07](07-acquire-and-resume-verified-bootstrap-assets.md)
