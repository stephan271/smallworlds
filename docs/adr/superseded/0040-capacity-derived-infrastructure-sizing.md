---
status: superseded
---

# Derive infrastructure sizing from selected capabilities

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

Hetzner setup will offer Small, Recommended, and High-capacity presets derived from the selected Cluster Capabilities, while advanced Operators may choose current locations, server types, and volume sizes. Change Plans show availability and estimated recurring cost and warn that volumes can grow but not shrink; a matching golden image is an automatically detected optimization rather than a prerequisite. Local-node preflight evaluates the same capability requirements against observed hardware instead of cloud presets.
