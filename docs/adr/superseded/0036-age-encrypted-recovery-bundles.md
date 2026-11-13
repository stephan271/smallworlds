---
status: superseded
---

# Encrypt Recovery Bundles with age

> **Superseded** by `docs/adr/0051` (the Setup Wizard replaced the Bootstrap
> Launcher) and `docs/adr/0062` (the Operator Console is deleted). The text below
> is kept as the record of the launcher-era decision.

Recovery Bundles will use the established age format rather than custom cryptography. Passphrase encryption through age's scrypt recipient is the default, while advanced custody may target one or more age public recipients. A minimal format/version header may remain visible, but Cluster Profile state, infrastructure state, kubeconfig, Cluster CA material, and Launcher Vault secrets are encrypted together; import verifies integrity and previews cluster identity before transferring Lifecycle Authority.
