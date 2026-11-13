---
status: accepted
---

# Preserve all three deployment modes in the first usable release

> **Still holds; the console parts do not.** All three Deployment Modes
> (Hetzner, local LAN-only, local internet-exposed) are supported by
> `smallworlds-init.sh` and the Setup Wizard. The launcher this asked to support
> them is gone (`docs/adr/0051`, `docs/adr/0062`).

The first usable Bootstrap Launcher must support Hetzner-hosted, Local LAN-only, and Local internet-exposed clusters. Although implementation may land as smaller vertical slices, the launcher is not a complete replacement for the existing setup scripts until all three modes work end to end; this prevents the new workflow model from embedding Hetzner-only assumptions that would later be costly to remove.
