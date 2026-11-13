---
status: superseded
---

# Define Cluster Capabilities in a versioned declarative catalog

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

A versioned catalog will be the shared source of metadata for Platform Services and Community Applications, including stable identity, category, optionality, dependencies, conflicts, supported Deployment Modes, exposure policy, configuration schema, documentation, health evidence, and expected backup coverage. The Bootstrap Launcher, GitOps Overlay renderer, and in-cluster overview consume this catalog, while runtime health continues to come from live Argo CD and Kubernetes observations rather than being stored in catalog files.
