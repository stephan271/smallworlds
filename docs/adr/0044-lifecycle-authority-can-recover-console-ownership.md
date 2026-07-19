---
status: accepted
---

# Let the Lifecycle Authority recover Console ownership

The first-release Bootstrap Launcher will recover lost Console Owner access only after the Launcher Vault is unlocked and cluster identity and current ownership are inspected. An approved Change Plan uses break-glass Kubernetes or Keycloak authority to create one short-lived replacement Owner claim without automatically removing existing Owners. The recovery is attributable in the Activity Record, prompts rotation of any break-glass credential used, and remains available when normal OIDC or Private Gateway access is broken.
