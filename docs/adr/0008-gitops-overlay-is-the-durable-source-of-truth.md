---
status: accepted
---

# Keep the GitOps Overlay as the durable source of truth

> **Still holds; the console parts do not.** The GitOps Overlay is still the
> durable source of truth. For updates, `docs/adr/0065` replaces "proposed
> through branches and pull requests" with direct commits by the Update Desk.
> The console and launcher named here are gone (`docs/adr/0062`).

The Bootstrap Launcher may create and push the GitOps Overlay's initial commit directly, but subsequent Desired Configuration changes will normally be presented as diffs and proposed through branches and pull requests. The Operator Console will not edit the upstream SmallWorlds base or maintain an independent durable configuration store. Direct Kubernetes writes are limited to Runtime Actions and explicit break-glass procedures, leaving Argo CD responsible for applying and reconciling accepted configuration.
