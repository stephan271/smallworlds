---
status: superseded
---

# Model setup as a dependency-driven resumable journey

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

Initial setup will be a Setup Journey composed of revalidatable Journey Tasks with explicit prerequisites rather than a rigid page sequence. The interface recommends one next action for a simple happy path, but Operators may review decisions, navigate away, pause for external work, and resume safely; completed work is rechecked rather than trusted forever. Advanced inputs remain available before planning, and mutating tasks require a reviewable plan and approval.
