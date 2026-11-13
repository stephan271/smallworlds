---
status: accepted
---

# Route operator interfaces exclusively through a Private Gateway

> **Still holds; the console parts do not.** Operator interfaces are still
> reachable only from the Private Network, but there is no Private Gateway: they
> carry an `ipAllowList` for the Headscale tailnet (`doc/hardening.md`). The
> Operator Console is gone (`docs/adr/0062`).

The cluster will expose the Operator Console, Grafana, Argo CD, and future operator interfaces through a dedicated, stable Private Gateway joined to Headscale. Their hostnames resolve to its tailnet address, the gateway proxies to internal ClusterIP services, and those services have no route on the public Traefik entrypoint; NetworkPolicies additionally restrict access to the gateway. Headscale coordination remains public for enrollment, while public member applications retain their own explicit exposure policy. This prevents public-IP Host-header bypasses that DNS-only privacy or a shared public ingress could permit.
