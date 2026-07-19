---
status: accepted
---

# Keep the GitOps Overlay as the durable source of truth

The Bootstrap Launcher may create and push the GitOps Overlay's initial commit directly, but subsequent Desired Configuration changes will normally be presented as diffs and proposed through branches and pull requests. The Operator Console will not edit the upstream SmallWorlds base or maintain an independent durable configuration store. Direct Kubernetes writes are limited to Runtime Actions and explicit break-glass procedures, leaving Argo CD responsible for applying and reconciling accepted configuration.
