# Provision a Hetzner cluster and complete private handoff

Status: ready-for-agent

## What to build

Take an approved Hetzner infrastructure plan through reproducible provisioning, Kubernetes/GitOps bootstrap, and verified private administration. The workflow must safely re-inspect ambiguous OpenTofu/provider outcomes, keep per-profile state isolated, establish the Private Gateway and first Console Owner, and remove temporary public SSH/Kubernetes authority only after an enrolled Operator Device proves access.

Covers PRD user stories 47–53 and 71–80.

## Acceptance criteria

- [ ] Immediately before execution, the plan is revalidated against provider inventory, public address, nameserver state, selected release, overlay commit, and OpenTofu state digest.
- [ ] OpenTofu creates or explicitly adopts only approved profile resources and maintains locked, backed-up, private per-profile state with sensitive output redaction.
- [ ] Cloud-init/bootstrap establishes k3s, Cluster Secrets, Argo CD, and observable convergence to the selected GitOps Overlay.
- [ ] Provider, OpenTofu, SSH, and Kubernetes checkpoints survive launcher or network interruption and are reinspected before retry.
- [ ] Headscale coordination, the stable Private Gateway, Private Network DNS, verified Tailscale enrollment, and the one-time first-owner claim complete through the browser journey.
- [ ] Operator Console, Grafana, and Argo CD have no public ingress route, and forged public Host-header requests fail.
- [ ] Temporary public SSH/Kubernetes access remains scoped to the Operator where feasible and is removed only after private DNS, TLS, OIDC, and reachability verification succeeds.
- [ ] A gated ephemeral-cluster test reaches the final assessment and guarantees cleanup under cost and time limits.

## Blocked by

- [Issue 05](05-establish-a-github-hosted-gitops-overlay.md)
- [Issue 10](10-complete-the-local-lan-only-private-administration-handoff.md)
- [Issue 18](18-inspect-and-plan-hetzner-infrastructure.md)
