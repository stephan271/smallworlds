---
status: superseded
---

# Maintain legacy scripts until Existing Cluster Import is stable

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

`smallworlds-init.sh` and `prepare-community-repo.sh` remain operational for existing users but receive only critical bug, compatibility, and security fixes; new setup functionality belongs exclusively in the Bootstrap Launcher. They may be marked deprecated after the launcher reaches parity across all three Deployment Modes, but removal or conversion to wrappers waits until Existing Cluster Import has shipped for at least one stable release. This limits duplication without stranding clusters whose lifecycle still depends on the scripts.
