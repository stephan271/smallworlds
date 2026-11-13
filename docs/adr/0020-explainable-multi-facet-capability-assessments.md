---
status: superseded
---

# Derive Capability State from explainable multi-facet assessments

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

Each Cluster Capability will expose a Capability Assessment covering configuration completeness, Argo CD delivery, runtime readiness and probes, expected access through DNS/TLS/networking, and backup protection where data is owned. The Operator Console derives a headline Capability State from these facets but retains the underlying evidence and remediation hint. An Argo Healthy value or ready pod is therefore evidence, not sufficient proof that a capability is operational and protected.
