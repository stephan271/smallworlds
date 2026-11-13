---
status: accepted
---

# Privatize only operator interfaces in the first release

> **Still holds; the console parts do not.** Still holds: member applications
> are public, operator interfaces are tailnet-only (`doc/hardening.md`). The
> Operator Console is gone (`docs/adr/0062`).

The first Operator Console release guarantees Private Gateway-only access for the console, Grafana, and Argo CD, while Headscale coordination remains reachable as required and Community Applications retain their existing exposure behavior. The capability catalog and UI still make each exposure policy explicit. Privatizing member applications remains a separate initiative because identity callbacks, sharing links, mail protocols, and guest access require capability-specific design rather than a blanket ingress change.
