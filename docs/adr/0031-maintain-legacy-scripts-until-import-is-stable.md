---
status: accepted
---

# Maintain legacy scripts until Existing Cluster Import is stable

`smallworlds-init.sh` and `prepare-community-repo.sh` remain operational for existing users but receive only critical bug, compatibility, and security fixes; new setup functionality belongs exclusively in the Bootstrap Launcher. They may be marked deprecated after the launcher reaches parity across all three Deployment Modes, but removal or conversion to wrappers waits until Existing Cluster Import has shipped for at least one stable release. This limits duplication without stranding clusters whose lifecycle still depends on the scripts.
