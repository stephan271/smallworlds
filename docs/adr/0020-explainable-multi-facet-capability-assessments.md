---
status: accepted
---

# Derive Capability State from explainable multi-facet assessments

Each Cluster Capability will expose a Capability Assessment covering configuration completeness, Argo CD delivery, runtime readiness and probes, expected access through DNS/TLS/networking, and backup protection where data is owned. The Operator Console derives a headline Capability State from these facets but retains the underlying evidence and remediation hint. An Argo Healthy value or ready pod is therefore evidence, not sufficient proof that a capability is operational and protected.
