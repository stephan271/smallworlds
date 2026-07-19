---
status: accepted
---

# Pin Local Cluster Node identity and support common SSH credentials

The Bootstrap Launcher will connect to Local Cluster Nodes through an SSH agent, an existing passphrase-protected private key, or username/password authentication, with direct root or separate sudo credentials. First contact requires Operator confirmation of the host-key fingerprint, which is then pinned to the Cluster Profile; host-key checking will never be disabled. After initial access the launcher may offer a dedicated per-profile Ed25519 key, while all private material remains in the Launcher Vault and a read-only preflight precedes privileged changes.
