---
status: superseded
---

# Require internet for the first release and retain an Offline Bundle roadmap

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

Prerequisite-free bootstrap does not mean air-gapped bootstrap in the first release: setup may require Git hosting, provider APIs, signed SmallWorlds releases, OpenTofu providers, package sources, certificate authorities, and container registries. The launcher will pin, verify, cache, and diagnose these downloads so interrupted work can resume. A future Offline Bundle remains an explicit planned capability with its own version, integrity, size, and update semantics rather than an implicit promise of the initial launcher.
