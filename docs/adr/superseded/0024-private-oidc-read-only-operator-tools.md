---
status: superseded
---

# Integrate operator tools through private OIDC read-only deep links

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

Grafana and Argo CD will be reachable only through the Private Gateway, use Keycloak OIDC, and receive read-only mappings for normal Console Roles. Capability Assessments open contextual links to the relevant dashboard or Argo application in a new tab rather than embedding iframes, while durable changes continue through the GitOps Overlay and the Operator Console's planning contract. Local administrator credentials remain separate audited break-glass credentials rather than normal navigation credentials.
