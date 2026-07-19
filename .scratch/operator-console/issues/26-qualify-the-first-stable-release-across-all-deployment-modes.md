# Qualify the first stable release across all Deployment Modes

Status: ready-for-agent

## What to build

Run the complete first-release qualification and assemble current evidence that the Bootstrap Launcher and in-cluster Operator Console satisfy the approved product contract in Hetzner-hosted, Local LAN-only, and Local internet-exposed Deployment Modes. This is a release qualification slice, not a place to weaken or silently defer failed requirements; defects found here return to the owning implementation issue.

Verifies the release-level completion of PRD user stories 1–130.

## Acceptance criteria

- [ ] A non-developer can complete each new-cluster Setup Journey from one native launcher with only documented external account, domain, or router actions.
- [ ] Current evidence exists for gated ephemeral Hetzner provisioning/cleanup, dedicated-node Local LAN-only setup, and a recorded real-router Local internet-exposed test.
- [ ] All modes establish verified Private Gateway-only access for Operator Console, Grafana, and Argo CD with correct OIDC, DNS, TLS, and no forged public Host-header bypass.
- [ ] Resume, cancellation, stale-plan invalidation, recovery transfer, Owner recovery, device access, offsite protection, updates, application addition, and both decommission paths pass their acceptance journeys.
- [ ] English/German, WCAG 2.2 AA, browser/mobile, cross-platform launcher, performance/resource, schema compatibility, and long-run soak evidence is current.
- [ ] Secret scans pass across Git, browser payloads, logs, plans, custom resources, diagnostics, bundles' cleartext metadata, and CI artifacts, with no unresolved critical/high security finding.
- [ ] Signed/checksummed artifacts, SBOMs, notices, release notes, operator documentation, Recovery Bundle custody guidance, break-glass guidance, and known limitations are ready.
- [ ] The Offline Bundle and Existing Cluster Import remain explicitly documented future work, and legacy scripts remain available under the accepted compatibility policy.
- [ ] Every failed qualification item links back to an implementation issue; stable release is declared only after all first-release Definition of Done items pass.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 06](06-establish-a-generic-https-gitops-overlay.md)
- [Issues 12–25](12-inspect-dataset-protection-and-recovery-evidence.md)
