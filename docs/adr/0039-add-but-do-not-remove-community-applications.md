---
status: superseded
---

# Add but do not remove Community Applications in the first release

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

After bootstrap, an Operator may select a disabled Community Application and receive dependency, capacity, exposure, backup, GitOps diff, and Change Plan analysis before the console opens a pull request. Once merged, Argo CD delivery and runtime evidence become part of its Capability Assessment. Disabling or removing installed stateful applications remains future work because the first release does not yet define destructive data-retention and deletion semantics.
