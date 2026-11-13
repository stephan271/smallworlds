---
status: superseded
---

# Require inspect, plan, approve, execute, and verify for mutations

> **Superseded** by `docs/adr/0062`: the Operator Console is deleted. The text
> below is kept as the record of the console-era decision.

Every mutating Journey Task and future operation will inspect current state, produce an immutable Change Plan, require risk-appropriate approval, execute with durable redacted events and checkpoints, and verify the result from external evidence. Changed preconditions invalidate approval, interruption causes safe resume or reinspection, and destructive, cost-bearing, downtime, exposure, or lockout-risk plans receive stronger confirmation. In particular, public firewall lockdown cannot proceed before verified access from an Operator Device.
