---
status: accepted
---

# Keep secret values out of Git

Launcher-side credentials and recovery material will live in a Launcher Vault whose wrapping key uses the operating-system credential store when available and a passphrase-unlocked fallback otherwise; Cluster Profiles retain only references. Values required in-cluster are written as Cluster Secrets during bootstrap or explicit secret-management work, while generated application credentials remain cluster-owned and backup-protected. Recovery Bundles carry required launcher secrets under independent encryption, and the first release will not add SOPS, a Git decryption controller, or secret values to the GitOps Overlay.
