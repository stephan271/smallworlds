# Complete the Local LAN-only private administration handoff

Status: ready-for-agent

## What to build

Complete a Local LAN-only setup by establishing trusted HTTPS and private-only access to operator interfaces, enrolling the Launcher Host, and handing routine administration to the first Console Owner. The flow must preserve LAN-only semantics: no router port is silently opened and no promise of remote administration is made.

Covers PRD user stories 63, 66–75, and 78–80.

## Acceptance criteria

- [ ] The Lifecycle Authority creates and protects the Cluster CA root, issues only an intermediate to the cluster, and can explicitly install trust on the current Operator Device.
- [ ] Headscale coordination and Private Network DNS are reachable only in the LAN-only shape and resolve stable operator hostnames without permanent hosts-file entries.
- [ ] The launcher detects the official Tailscale client, offers pinned verified acquisition with explicit elevation, and retains a manual fallback when automation is unavailable.
- [ ] The Launcher Host enrolls with a short-lived single-use credential while the Private Gateway uses a separate stable identity that survives pod restart or reschedule.
- [ ] Operator Console, Grafana, and Argo CD are reachable through standard HTTPS only via the Private Gateway and cannot be reached through LAN/public ingress or forged Host headers.
- [ ] Private reachability, DNS, TLS, and gateway identity are verified before any temporary SSH or Kubernetes administration path is closed.
- [ ] The launcher displays a short-lived first-owner claim, and successful passkey registration permanently disables the bootstrap grant.
- [ ] The final Setup Journey assessment explains LAN-only limitations and provides the in-cluster console handoff URL.

## Blocked by

- [Issue 09](09-bootstrap-kubernetes-and-gitops-on-a-local-cluster-node.md)
