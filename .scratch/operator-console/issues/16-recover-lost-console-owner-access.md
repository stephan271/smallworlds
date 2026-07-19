# Recover lost Console Owner access

Status: ready-for-agent

## What to build

Let the Lifecycle Authority recover routine Console Owner access when normal OIDC or Private Gateway access is unavailable. The Bootstrap Launcher unlocks the Launcher Vault, proves the Cluster Profile and live cluster identity, inspects existing ownership, presents a sensitive Change Plan, and creates one short-lived replacement Owner claim without silently removing current Owners.

Covers PRD user stories 23, 79–85, and 109.

## Acceptance criteria

- [ ] Recovery is available only from the authoritative Cluster Profile after the Launcher Vault is unlocked and cluster identity is verified.
- [ ] Inspection shows existing Owner evidence when obtainable and explains whether Kubernetes or Keycloak break-glass authority will be used.
- [ ] The Change Plan is secret-free, risk-labeled, attributable, and requires stronger approval appropriate to access recovery.
- [ ] Execution creates one expiring single-use Owner claim and does not delete, demote, or reset any existing Owner automatically.
- [ ] The replacement identity can claim Owner and register a passkey even when the normal console login path was deliberately unavailable.
- [ ] The Activity Record identifies the recovery path and prompts rotation of any break-glass credential used without disclosing it.
- [ ] Mismatched cluster identity, missing Lifecycle Authority, stale approval, and reused claims are rejected safely.

## Blocked by

- [Issue 02](02-store-credentials-safely-in-the-launcher-vault.md)
- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
- [Issue 11](11-observe-cluster-capabilities-through-role-controlled-evidence.md)
