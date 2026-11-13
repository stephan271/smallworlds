---
status: superseded
---

# Pin Local Cluster Node identity and support common SSH credentials

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

The Bootstrap Launcher will connect to Local Cluster Nodes through an SSH agent, an existing passphrase-protected private key, or username/password authentication, with direct root or separate sudo credentials. First contact requires Operator confirmation of the host-key fingerprint, which is then pinned to the Cluster Profile; host-key checking will never be disabled. After initial access the launcher may offer a dedicated per-profile Ed25519 key, while all private material remains in the Launcher Vault and a read-only preflight precedes privileged changes.
