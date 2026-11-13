---
status: superseded
---

# Persist launcher workflows in SQLite and in-cluster workflows in Kubernetes

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

The Bootstrap Launcher will persist Setup Journey state, Change Plans, Workflow Runs, and compact redacted events in per-profile SQLite storage. The in-cluster console will persist compact ChangePlan and WorkflowRun custom resources, while detailed structured events flow to Loki and are referenced from the run. Neither adapter stores secret values or Desired Configuration, and the Kubernetes resources inherit Velero protection without introducing another application database.
