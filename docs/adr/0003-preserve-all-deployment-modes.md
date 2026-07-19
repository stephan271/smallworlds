---
status: accepted
---

# Preserve all three deployment modes in the first usable release

The first usable Bootstrap Launcher must support Hetzner-hosted, Local LAN-only, and Local internet-exposed clusters. Although implementation may land as smaller vertical slices, the launcher is not a complete replacement for the existing setup scripts until all three modes work end to end; this prevents the new workflow model from embedding Hetzner-only assumptions that would later be costly to remove.
