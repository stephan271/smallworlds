---
status: superseded
---

# Use a loopback bootstrap session and Keycloak Console Roles

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

The Bootstrap Launcher will bind only to loopback and exchange a one-time high-entropy launch URL for a secure HTTP-only session, avoiding an identity dependency before the cluster exists. The in-cluster Operator Console will use Keycloak OIDC and three Console Roles—Observer, Operator, and Owner—while also requiring Private Network reachability. Infrastructure lifecycle and break-glass authority remain with the Lifecycle Authority rather than being granted through an in-cluster role.
