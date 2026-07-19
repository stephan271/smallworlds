---
status: accepted
---

# Manage a private certificate authority for LAN-only clusters

For Local LAN-only mode, the Lifecycle Authority will generate and retain the Cluster CA root in the Launcher Vault and issue an intermediate CA to cert-manager as a Cluster Secret. With explicit elevation, the launcher installs trust in the current Operator Device, and later enrollment includes trust setup; the public root may also be exported for member devices. The root private key never enters the cluster and is preserved through the Recovery Bundle, eliminating routine browser warnings without requiring a registered domain or public CA.
