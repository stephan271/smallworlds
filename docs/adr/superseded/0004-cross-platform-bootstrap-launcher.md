---
status: superseded
---

# Ship the Bootstrap Launcher for Linux, macOS, and Windows

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

The first usable Bootstrap Launcher will run natively on Linux x86-64/ARM64, macOS Intel/Apple Silicon, and Windows x86-64. Every platform can provision Hetzner or a separate Linux Cluster Node over SSH; only a supported Linux Launcher Host may additionally install the cluster on itself. This matrix supports prerequisite-free onboarding while forcing orchestration code to avoid assumptions inherited from the current Bash scripts.
