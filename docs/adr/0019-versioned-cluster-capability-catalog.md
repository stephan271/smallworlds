---
status: accepted
---

# Define Cluster Capabilities in a versioned declarative catalog

A versioned catalog will be the shared source of metadata for Platform Services and Community Applications, including stable identity, category, optionality, dependencies, conflicts, supported Deployment Modes, exposure policy, configuration schema, documentation, health evidence, and expected backup coverage. The Bootstrap Launcher, GitOps Overlay renderer, and in-cluster overview consume this catalog, while runtime health continues to come from live Argo CD and Kubernetes observations rather than being stored in catalog files.
